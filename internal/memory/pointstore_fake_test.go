package memory

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"ezyapper/internal/config"

	"github.com/qdrant/go-client/qdrant"
)

// fakeCollection is one in-memory collection of fakePointStore.
type fakeCollection struct {
	dim      int // 0 = payload-only
	points   map[string]*tierPoint
	metadata map[string]*qdrant.Value
}

// fakePointStore is an in-memory pointStore used to test the tier logic
// without a live Qdrant. It implements keyword match filters (Must, Should,
// MustNot, nested filters), cosine dense search and ID-ordered scrolling.
type fakePointStore struct {
	mu          sync.Mutex
	collections map[string]*fakeCollection

	// failUpsert makes Upsert into the named collection fail.
	failUpsert map[string]error
	// failDelete makes Delete into the named collection fail.
	failDelete map[string]error
}

func newFakePointStore() *fakePointStore {
	return &fakePointStore{
		collections: map[string]*fakeCollection{},
		failUpsert:  map[string]error{},
		failDelete:  map[string]error{},
	}
}

func clonePoint(p *tierPoint) *tierPoint {
	cp := &tierPoint{ID: p.ID, Payload: map[string]*qdrant.Value{}}
	for k, v := range p.Payload {
		cp.Payload[k] = v
	}
	cp.Dense = slices.Clone(p.Dense)
	cp.SparseIndices = slices.Clone(p.SparseIndices)
	cp.SparseValues = slices.Clone(p.SparseValues)
	return cp
}

func (f *fakePointStore) coll(name string) (*fakeCollection, error) {
	c, ok := f.collections[name]
	if !ok {
		return nil, fmt.Errorf("collection %s not found", name)
	}
	return c, nil
}

func (f *fakePointStore) addCollection(name string, dim int) *fakeCollection {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := &fakeCollection{dim: dim, points: map[string]*tierPoint{}, metadata: map[string]*qdrant.Value{}}
	f.collections[name] = c
	return c
}

func (f *fakePointStore) ListCollections(ctx context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	names := make([]string, 0, len(f.collections))
	for n := range f.collections {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

func (f *fakePointStore) CollectionInfo(ctx context.Context, name string) (*collectionInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.collections[name]
	if !ok {
		return nil, nil
	}
	md := map[string]*qdrant.Value{}
	for k, v := range c.metadata {
		md[k] = v
	}
	return &collectionInfo{DenseSize: c.dim, PointCount: uint64(len(c.points)), Metadata: md}, nil
}

func (f *fakePointStore) CreateSourceCollection(ctx context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.collections[name]; ok {
		return fmt.Errorf("collection %s already exists", name)
	}
	f.collections[name] = &fakeCollection{points: map[string]*tierPoint{}, metadata: map[string]*qdrant.Value{}}
	return nil
}

func (f *fakePointStore) CreateVectorCollection(ctx context.Context, name string, dim int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.collections[name]; ok {
		return fmt.Errorf("collection %s already exists", name)
	}
	f.collections[name] = &fakeCollection{dim: dim, points: map[string]*tierPoint{}, metadata: map[string]*qdrant.Value{}}
	return nil
}

func (f *fakePointStore) SetCollectionMetadata(ctx context.Context, name string, md map[string]*qdrant.Value) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, err := f.coll(name)
	if err != nil {
		return err
	}
	for k, v := range md {
		c.metadata[k] = v
	}
	return nil
}

func (f *fakePointStore) Upsert(ctx context.Context, collection string, points []*tierPoint) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failUpsert[collection]; err != nil {
		return err
	}
	c, err := f.coll(collection)
	if err != nil {
		return err
	}
	for _, p := range points {
		if c.dim == 0 && len(p.Dense) > 0 {
			return fmt.Errorf("payload-only collection %s got a dense vector", collection)
		}
		if c.dim > 0 && len(p.Dense) != c.dim {
			return fmt.Errorf("collection %s expects dim %d, got %d", collection, c.dim, len(p.Dense))
		}
		c.points[p.ID] = clonePoint(p)
	}
	return nil
}

func (f *fakePointStore) Get(ctx context.Context, collection string, ids []string, withPayload, withVectors bool) ([]*tierPoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, err := f.coll(collection)
	if err != nil {
		return nil, err
	}
	var out []*tierPoint
	for _, id := range ids {
		p, ok := c.points[id]
		if !ok {
			continue
		}
		out = append(out, project(p, withPayload, withVectors))
	}
	return out, nil
}

func project(p *tierPoint, withPayload, withVectors bool) *tierPoint {
	cp := clonePoint(p)
	if !withPayload {
		cp.Payload = nil
	}
	if !withVectors {
		cp.Dense, cp.SparseIndices, cp.SparseValues = nil, nil, nil
	}
	return cp
}

func (f *fakePointStore) sortedIDs(c *fakeCollection) []string {
	ids := make([]string, 0, len(c.points))
	for id := range c.points {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (f *fakePointStore) Scroll(ctx context.Context, collection string, filter *qdrant.Filter, offset string, limit uint32, withPayload, withVectors bool) ([]*tierPoint, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, err := f.coll(collection)
	if err != nil {
		return nil, "", err
	}
	var out []*tierPoint
	for _, id := range f.sortedIDs(c) {
		if offset != "" && id < offset {
			continue
		}
		p := c.points[id]
		if !matchFilter(filter, p.Payload) {
			continue
		}
		if uint32(len(out)) == limit {
			return out, id, nil
		}
		out = append(out, project(p, withPayload, withVectors))
	}
	return out, "", nil
}

func (f *fakePointStore) Query(ctx context.Context, collection string, q *vectorQuery) ([]scoredID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, err := f.coll(collection)
	if err != nil {
		return nil, err
	}
	if c.dim == 0 {
		return nil, fmt.Errorf("collection %s has no vectors", collection)
	}
	var hits []scoredID
	for id, p := range c.points {
		if !matchFilter(q.Filter, p.Payload) {
			continue
		}
		hits = append(hits, scoredID{ID: id, Score: cosineSimilarity(q.Dense, p.Dense)})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].ID < hits[j].ID
		}
		return hits[i].Score > hits[j].Score
	})
	if uint64(len(hits)) > q.Limit {
		hits = hits[:q.Limit]
	}
	return hits, nil
}

func (f *fakePointStore) SetPayload(ctx context.Context, collection string, ids []string, payload map[string]*qdrant.Value) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, err := f.coll(collection)
	if err != nil {
		return err
	}
	for _, id := range ids {
		p, ok := c.points[id]
		if !ok {
			return fmt.Errorf("no point with id %s in %s", id, collection)
		}
		for k, v := range payload {
			p.Payload[k] = v
		}
	}
	return nil
}

func (f *fakePointStore) Delete(ctx context.Context, collection string, ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failDelete[collection]; err != nil {
		return err
	}
	c, err := f.coll(collection)
	if err != nil {
		return err
	}
	for _, id := range ids {
		delete(c.points, id)
	}
	return nil
}

func (f *fakePointStore) DeleteByFilter(ctx context.Context, collection string, filter *qdrant.Filter) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, err := f.coll(collection)
	if err != nil {
		return err
	}
	for id, p := range c.points {
		if matchFilter(filter, p.Payload) {
			delete(c.points, id)
		}
	}
	return nil
}

func (f *fakePointStore) Count(ctx context.Context, collection string) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, err := f.coll(collection)
	if err != nil {
		return 0, err
	}
	return uint64(len(c.points)), nil
}

// points returns a snapshot of a collection's points keyed by ID.
func (f *fakePointStore) points(collection string) map[string]*tierPoint {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]*tierPoint{}
	if c, ok := f.collections[collection]; ok {
		for id, p := range c.points {
			out[id] = clonePoint(p)
		}
	}
	return out
}

func payloadStrings(v *qdrant.Value) []string {
	if v == nil {
		return nil
	}
	if l := v.GetListValue(); l != nil {
		out := make([]string, 0, len(l.GetValues()))
		for _, x := range l.GetValues() {
			out = append(out, x.GetStringValue())
		}
		return out
	}
	return []string{v.GetStringValue()}
}

func matchCondition(c *qdrant.Condition, payload map[string]*qdrant.Value) bool {
	if nested := c.GetFilter(); nested != nil {
		return matchFilter(nested, payload)
	}
	field := c.GetField()
	if field == nil {
		panic(fmt.Sprintf("fakePointStore: unsupported condition %v", c))
	}
	values := payloadStrings(payload[field.GetKey()])
	m := field.GetMatch()
	var wanted []string
	switch {
	case m.GetKeywords() != nil:
		wanted = m.GetKeywords().GetStrings()
	default:
		wanted = []string{m.GetKeyword()}
	}
	for _, v := range values {
		if slices.Contains(wanted, v) {
			return true
		}
	}
	return false
}

func matchFilter(f *qdrant.Filter, payload map[string]*qdrant.Value) bool {
	if f == nil {
		return true
	}
	for _, c := range f.GetMust() {
		if !matchCondition(c, payload) {
			return false
		}
	}
	for _, c := range f.GetMustNot() {
		if matchCondition(c, payload) {
			return false
		}
	}
	if len(f.GetShould()) > 0 {
		for _, c := range f.GetShould() {
			if matchCondition(c, payload) {
				return true
			}
		}
		return false
	}
	return true
}

// hashEmbedder is a deterministic fake embedder: the vector for a text is
// derived from FNV hashes seeded by salt, so two embedders with different
// salts behave like two different models of the same dimension.
type hashEmbedder struct {
	dim  int
	salt string

	calls       atomic.Int64
	inFlight    atomic.Int64
	maxInFlight atomic.Int64
	delay       time.Duration
	// failOn makes Embed fail for these texts.
	failOn map[string]bool
	mu     sync.Mutex
}

func (e *hashEmbedder) vector(text string) []float32 {
	v := make([]float32, e.dim)
	var norm float64
	for i := range v {
		h := fnv.New32a()
		fmt.Fprintf(h, "%s|%d|%s", e.salt, i, text)
		x := float64(h.Sum32())/float64(math.MaxUint32)*2 - 1
		v[i] = float32(x)
		norm += x * x
	}
	n := float32(math.Sqrt(norm))
	for i := range v {
		v[i] /= n
	}
	return v
}

func (e *hashEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	e.calls.Add(1)
	cur := e.inFlight.Add(1)
	defer e.inFlight.Add(-1)
	for {
		m := e.maxInFlight.Load()
		if cur <= m || e.maxInFlight.CompareAndSwap(m, cur) {
			break
		}
	}
	if e.delay > 0 {
		time.Sleep(e.delay)
	}
	e.mu.Lock()
	fail := e.failOn[text]
	e.mu.Unlock()
	if fail {
		return nil, fmt.Errorf("embedding failed for %q", text)
	}
	return e.vector(text), nil
}

func (e *hashEmbedder) Stop() {}

// newTestTieredClient returns a QdrantClient on a fake store with the memory
// tiers created for the given model/dimension.
func newTestTieredClient(store *fakePointStore, model string, dim int) *QdrantClient {
	qc := newQdrantClientWithStore(store, &config.QdrantConfig{
		VectorSize:       dim,
		MaxRetries:       1,
		RetryBaseDelayMs: 1,
		RetryMaxDelayMs:  1,
	}, model, "")
	if err := qc.ensureMemoryTiers(context.Background()); err != nil {
		panic(err)
	}
	return qc
}

// testRecordID returns a deterministic UUID-shaped ID for test records.
func testRecordID(i int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
}

// newTestRecord builds a fully populated record.
func newTestRecord(i int, userID, content string) *Record {
	return &Record{
		ID:             testRecordID(i),
		UserID:         userID,
		GuildID:        "100",
		ChannelID:      "200",
		MemoryType:     TypeFact,
		Content:        content,
		Keywords:       []string{"kw"},
		Confidence:     0.9,
		CreatedAt:      time.Now().Add(-time.Hour),
		LastAccessedAt: time.Now().Add(-time.Hour),
	}
}

// configQdrant shortens config.QdrantConfig in tests.
type configQdrant = config.QdrantConfig
