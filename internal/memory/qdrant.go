package memory

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/retry"

	"github.com/google/uuid"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// discordIDToUint64 converts a Discord ID string to uint64 for Qdrant.
// Returns an error if the ID cannot be parsed (instead of silently returning 0).
func discordIDToUint64(discordID string) (uint64, error) {
	id, err := strconv.ParseUint(discordID, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid Discord ID %q: %w", discordID, err)
	}
	return id, nil
}

// QdrantClient wraps the Qdrant client.
//
// Long-term memories are stored in two tiers (see tiers.go): a payload-only
// source collection that is the source of truth, and a per-embedding-model
// vector collection used for search. Profiles and relationships are plain
// payload stores (they are only ever fetched by ID or payload filter).
type QdrantClient struct {
	client      *qdrant.Client
	store       pointStore
	host        string
	port        int
	vectorSize  int
	maxRetries  int
	baseBackoff time.Duration
	maxBackoff  time.Duration

	embeddingModel string
	// collPrefix is prepended to every collection name. It is empty in
	// production and only set by the live smoke test to isolate test data.
	collPrefix string

	sourceCollection        string
	vectorCollection        string
	legacyCollection        string
	profilesCollection      string
	relationshipsCollection string

	// profilesDim / relationshipsDim are the dense vector sizes of the
	// existing profiles / relationships collections (0 when payload-only).
	// Collections created by older versions require a placeholder vector of
	// their own size, independent of the current embedding model.
	profilesDim      int
	relationshipsDim int
}

const (
	CollectionProfiles      = "profiles"
	CollectionRelationships = "relationships"
)

var ErrProfileNotFound = errors.New("profile not found")

func (qc *QdrantClient) retryWithBackoff(ctx context.Context, operation string, fn func() error) error {
	_, err := retry.Retry(ctx, qc.maxRetries, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, fn()
	}, retry.WithErrorClassifier(isRetryableGrpc),
		retry.WithBaseDelay(qc.baseBackoff),
		retry.WithMaxDelay(qc.maxBackoff))
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}

func isRetryableGrpc(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
		return true
	default:
		return false
	}
}

// NewQdrantClient creates a new Qdrant client and makes sure all collections
// exist. embeddingModel and cfg.VectorSize select the vector-tier collection.
func NewQdrantClient(ctx context.Context, cfg *config.QdrantConfig, embeddingModel string) (*QdrantClient, error) {
	return newQdrantClient(ctx, cfg, embeddingModel, "")
}

func newQdrantClient(ctx context.Context, cfg *config.QdrantConfig, embeddingModel, collPrefix string) (*QdrantClient, error) {
	if strings.TrimSpace(embeddingModel) == "" {
		return nil, fmt.Errorf("embedding model is required to select the vector collection")
	}
	if cfg.VectorSize <= 0 {
		return nil, fmt.Errorf("vector size must be greater than 0, got %d", cfg.VectorSize)
	}

	qdrantCfg := &qdrant.Config{
		Host: cfg.Host,
		Port: cfg.Port,
	}

	// Add API key and enable TLS if provided
	if cfg.APIKey != "" {
		qdrantCfg.APIKey = cfg.APIKey
		qdrantCfg.UseTLS = true
	}

	client, err := qdrant.NewClient(qdrantCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create qdrant client: %w", err)
	}

	qc := newQdrantClientWithStore(&grpcPointStore{client: client}, cfg, embeddingModel, collPrefix)
	qc.client = client
	qc.host = cfg.Host
	qc.port = cfg.Port

	// Initialize collections
	if err := qc.initializeCollections(ctx); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed to initialize collections: %w", err)
	}

	return qc, nil
}

// newQdrantClientWithStore builds a client around an arbitrary pointStore
// (used by tests with an in-memory fake). It does not create collections.
func newQdrantClientWithStore(store pointStore, cfg *config.QdrantConfig, embeddingModel, collPrefix string) *QdrantClient {
	return &QdrantClient{
		store:                   store,
		vectorSize:              cfg.VectorSize,
		maxRetries:              cfg.MaxRetries,
		baseBackoff:             time.Duration(cfg.RetryBaseDelayMs) * time.Millisecond,
		maxBackoff:              time.Duration(cfg.RetryMaxDelayMs) * time.Millisecond,
		embeddingModel:          embeddingModel,
		collPrefix:              collPrefix,
		sourceCollection:        collPrefix + CollectionMemorySource,
		vectorCollection:        collPrefix + vectorCollectionName(embeddingModel, cfg.VectorSize),
		legacyCollection:        collPrefix + CollectionLegacyMemories,
		profilesCollection:      collPrefix + CollectionProfiles,
		relationshipsCollection: collPrefix + CollectionRelationships,
	}
}

// Close closes the connection
func (qc *QdrantClient) Close() error {
	if qc.client != nil {
		return qc.client.Close()
	}
	return nil
}

// VectorCollection returns the name of the vector collection for the
// configured embedding model and dimension.
func (qc *QdrantClient) VectorCollection() string { return qc.vectorCollection }

// SourceCollection returns the name of the payload-only source collection.
func (qc *QdrantClient) SourceCollection() string { return qc.sourceCollection }

// initializeCollections creates collections if they don't exist.
func (qc *QdrantClient) initializeCollections(ctx context.Context) error {
	if err := qc.ensureMemoryTiers(ctx); err != nil {
		return err
	}

	dim, err := qc.ensurePayloadCollection(ctx, qc.profilesCollection, nil)
	if err != nil {
		return fmt.Errorf("failed to create profiles collection: %w", err)
	}
	qc.profilesDim = dim

	dim, err = qc.ensurePayloadCollection(ctx, qc.relationshipsCollection, qc.createRelationshipsPayloadIndexes)
	if err != nil {
		return fmt.Errorf("failed to create relationships collection: %w", err)
	}
	qc.relationshipsDim = dim

	return nil
}

// ensureMemoryTiers creates the source and the current vector collection when
// missing and logs which other vector collections are left in place.
func (qc *QdrantClient) ensureMemoryTiers(ctx context.Context) error {
	srcInfo, err := qc.store.CollectionInfo(ctx, qc.sourceCollection)
	if err != nil {
		return err
	}
	if srcInfo == nil {
		if err := qc.store.CreateSourceCollection(ctx, qc.sourceCollection); err != nil {
			return err
		}
		logger.Infof("[qdrant] Created payload-only memory source collection: %s", qc.sourceCollection)
	} else if srcInfo.DenseSize > 0 {
		return fmt.Errorf("collection %s exists but has a dense vector (size %d); expected a payload-only source collection", qc.sourceCollection, srcInfo.DenseSize)
	}

	vecInfo, err := qc.store.CollectionInfo(ctx, qc.vectorCollection)
	if err != nil {
		return err
	}
	if vecInfo == nil {
		if err := qc.store.CreateVectorCollection(ctx, qc.vectorCollection, qc.vectorSize); err != nil {
			return err
		}
		logger.Infof("[qdrant] Created memory vector collection %s (model=%q dim=%d); it is filled from %s in the background",
			qc.vectorCollection, qc.embeddingModel, qc.vectorSize, qc.sourceCollection)
	} else if vecInfo.DenseSize != qc.vectorSize {
		return fmt.Errorf("vector collection %s has dimension %d but vector_size is %d", qc.vectorCollection, vecInfo.DenseSize, qc.vectorSize)
	}

	names, err := qc.store.ListCollections(ctx)
	if err != nil {
		logger.Warnf("[qdrant] could not list collections: %v", err)
		return nil
	}
	for _, name := range names {
		if name != qc.vectorCollection && isVectorCollection(name, qc.collPrefix) {
			logger.Infof("[qdrant] Vector collection %s belongs to another embedding model/dimension and is not used; it is kept so switching back needs no re-embedding. Delete it manually if no longer needed.", name)
		}
		if name == qc.legacyCollection {
			logger.Infof("[qdrant] Legacy collection %s is present; it is only read for migration and never modified. Delete it manually once the migration is verified.", name)
		}
	}
	return nil
}

// ensurePayloadCollection makes sure a profiles/relationships collection
// exists and returns the size of its default dense vector (0 for a
// payload-only collection). New collections are created payload-only because
// neither profiles nor relationships are ever searched by vector.
func (qc *QdrantClient) ensurePayloadCollection(ctx context.Context, name string, indexFn func(context.Context, string) error) (int, error) {
	info, err := qc.store.CollectionInfo(ctx, name)
	if err != nil {
		return 0, err
	}
	if info != nil {
		logger.Infof("[qdrant] Collection %s already exists", name)
		return info.DenseSize, nil
	}
	if err := qc.client.CreateCollection(ctx, &qdrant.CreateCollection{
		CollectionName: name,
		VectorsConfig:  qdrant.NewVectorsConfigMap(map[string]*qdrant.VectorParams{}),
	}); err != nil {
		return 0, fmt.Errorf("failed to create collection: %w", err)
	}
	logger.Infof("[qdrant] Created payload-only collection: %s", name)
	if indexFn != nil {
		if err := indexFn(ctx, name); err != nil {
			logger.Warnf("[qdrant] Failed to create payload indexes for %s: %v", name, err)
		}
	}
	return 0, nil
}

// placeholderVectors returns the vectors to write for a point in a collection
// that is never searched by vector. For a payload-only collection that is an
// empty named-vector map; collections created by older versions have a dense
// vector of their own size, which gets a zero vector (or emb when it fits).
func placeholderVectors(dim int, emb []float32) *qdrant.Vectors {
	if dim <= 0 {
		return qdrant.NewVectorsMap(map[string]*qdrant.Vector{})
	}
	if len(emb) == dim {
		return qdrant.NewVectors(emb...)
	}
	return qdrant.NewVectors(make([]float32, dim)...)
}

func (qc *QdrantClient) createRelationshipsPayloadIndexes(ctx context.Context, collectionName string) error {
	if _, err := qc.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
		CollectionName: collectionName,
		FieldName:      "user_a",
		FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
	}); err != nil {
		return fmt.Errorf("failed to create user_a index: %w", err)
	}

	if _, err := qc.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
		CollectionName: collectionName,
		FieldName:      "user_b",
		FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
	}); err != nil {
		return fmt.Errorf("failed to create user_b index: %w", err)
	}

	if _, err := qc.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
		CollectionName: collectionName,
		FieldName:      "type",
		FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
	}); err != nil {
		return fmt.Errorf("failed to create type index: %w", err)
	}

	if _, err := qc.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
		CollectionName: collectionName,
		FieldName:      "weight",
		FieldType:      qdrant.FieldType_FieldTypeFloat.Enum(),
	}); err != nil {
		return fmt.Errorf("failed to create weight index: %w", err)
	}

	logger.Infof("[qdrant] Created payload indexes for collection: %s", collectionName)
	return nil
}

// vectorPointFor builds the vector-tier point for a record from its
// source-tier payload and embedding.
func vectorPointFor(id string, sourcePayload map[string]*qdrant.Value, embedding []float32, content string, keywords []string) *tierPoint {
	idx, vals := computeBM25SparseVector(content, keywords)
	return &tierPoint{
		ID:            id,
		Payload:       vectorPayloadFromSource(sourcePayload),
		Dense:         embedding,
		SparseIndices: idx,
		SparseValues:  vals,
	}
}

// UpsertMemory stores or updates a memory in both tiers. The source tier is
// written first so a record is never only searchable but not readable; if the
// vector write fails the record stays in the source tier and the startup
// vector sync embeds it again.
func (qc *QdrantClient) UpsertMemory(ctx context.Context, memory *Record) error {
	if memory.ID == "" {
		memory.ID = uuid.New().String()
	}

	if memory.CreatedAt.IsZero() {
		memory.CreatedAt = time.Now()
	}

	if len(memory.Embedding) > 0 && len(memory.Embedding) != qc.vectorSize {
		return fmt.Errorf("upsert memory for userID=%s: embedding has %d dimensions, vector collection %s expects %d",
			memory.UserID, len(memory.Embedding), qc.vectorCollection, qc.vectorSize)
	}

	logger.Debugf("[qdrant] userID=%s type=%s content=%.50s", memory.UserID, memory.MemoryType, memory.Content)

	payload, err := qc.memoryToPayload(memory)
	if err != nil {
		return fmt.Errorf("failed to prepare memory payload: %w", err)
	}
	memID := memory.ID

	srcPoint := &tierPoint{ID: memID, Payload: payload}
	if err := qc.retryWithBackoff(ctx, "upsert source record", func() error {
		return qc.store.Upsert(ctx, qc.sourceCollection, []*tierPoint{srcPoint})
	}); err != nil {
		return fmt.Errorf("upsert memory for userID=%s: %w", memory.UserID, err)
	}

	if len(memory.Embedding) == 0 {
		logger.Warnf("[qdrant] memoryID=%s stored without embedding; it will be embedded by the next vector sync", memID)
		return nil
	}

	vecPoint := vectorPointFor(memID, payload, memory.Embedding, memory.Content, memory.Keywords)
	if err := qc.retryWithBackoff(ctx, "upsert vector", func() error {
		return qc.store.Upsert(ctx, qc.vectorCollection, []*tierPoint{vecPoint})
	}); err != nil {
		return fmt.Errorf("upsert memory vector for userID=%s (record kept in source tier): %w", memory.UserID, err)
	}
	logger.Debugf("[qdrant] successfully stored memoryID=%s for userID=%s", memID, memory.UserID)
	return nil
}

// IncrementAccessCount sets the access_count payload field to the given
// absolute values and updates updated_at and last_accessed_at for the given
// memory IDs. Access statistics only live in the source tier.
func (qc *QdrantClient) IncrementAccessCount(ctx context.Context, memoryIDs []string, increments map[string]int) error {
	if len(memoryIDs) == 0 {
		return nil
	}

	now := float64(time.Now().UnixMilli()) / 1000.0

	// Build payload: updated_at and last_accessed_at are common across all
	// points; access_count differs per point. All use SetPayload (merge
	// semantics) to avoid wiping existing payload fields.
	commonPayload := map[string]*qdrant.Value{
		"updated_at":       {Kind: &qdrant.Value_DoubleValue{DoubleValue: now}},
		"last_accessed_at": {Kind: &qdrant.Value_DoubleValue{DoubleValue: now}},
	}

	if err := qc.store.SetPayload(ctx, qc.sourceCollection, memoryIDs, commonPayload); err != nil {
		return fmt.Errorf("increment access count: %w", err)
	}

	var errs []error
	for _, id := range memoryIDs {
		inc, ok := increments[id]
		if !ok {
			continue
		}
		perPointPayload := map[string]*qdrant.Value{
			"access_count": {Kind: &qdrant.Value_IntegerValue{IntegerValue: int64(inc)}},
		}
		if err := qc.store.SetPayload(ctx, qc.sourceCollection, []string{id}, perPointPayload); err != nil {
			logger.Warnf("[qdrant] failed to set access_count for memoryID=%s: %v", id, err)
			errs = append(errs, fmt.Errorf("set access_count for memoryID=%s: %w", id, err))
		}
	}

	return errors.Join(errs...)
}

// GetMemoryPayloads retrieves lightweight payload data for multiple memory IDs.
// Returns a map of memoryID -> access_count.
func (qc *QdrantClient) GetMemoryPayloads(ctx context.Context, memoryIDs []string) (map[string]int, error) {
	if len(memoryIDs) == 0 {
		return map[string]int{}, nil
	}

	points, err := qc.store.Get(ctx, qc.sourceCollection, memoryIDs, true, false)
	if err != nil {
		return nil, fmt.Errorf("get memory payloads: %w", err)
	}

	result := make(map[string]int, len(points))
	for _, pt := range points {
		ac, err := getRequiredInt(pt.Payload, "access_count")
		if err != nil {
			logger.Warnf("[qdrant] missing access_count for memoryID=%s: %v", pt.ID, err)
			result[pt.ID] = 0
			continue
		}
		result[pt.ID] = int(ac)
	}
	return result, nil
}

// memoryTypeShould returns the Should clause for a memory type filter.
func memoryTypeShould(types []string) []*qdrant.Condition {
	if len(types) == 0 {
		return nil
	}
	conditions := make([]*qdrant.Condition, 0, len(types))
	for _, mt := range types {
		conditions = append(conditions, qdrant.NewMatch("memory_type", mt))
	}
	return conditions
}

// buildUserSearchFilter builds the vector-tier filter for SearchMemories.
func buildUserSearchFilter(userID string, opts *SearchOptions) *qdrant.Filter {
	filter := &qdrant.Filter{
		Must: []*qdrant.Condition{
			qdrant.NewMatch("user_id", userID),
		},
	}
	if opts.ChannelID != "" {
		filter.Must = append(filter.Must, qdrant.NewMatch("channel_id", opts.ChannelID))
	}
	filter.Should = memoryTypeShould(opts.MemoryTypes)
	if len(opts.MentionedUserIDs) > 0 {
		filter.Must = append(filter.Must, qdrant.NewMatchKeywords("mentioned_user_ids", opts.MentionedUserIDs...))
	}
	if len(opts.MentionedChannelIDs) > 0 {
		filter.Must = append(filter.Must, qdrant.NewMatchKeywords("mentioned_channel_ids", opts.MentionedChannelIDs...))
	}
	return filter
}

// searchAndHydrate runs a vector-tier query, drops hits below minScore and
// loads the full records from the source tier, preserving the ranking.
func (qc *QdrantClient) searchAndHydrate(ctx context.Context, q *vectorQuery, minScore float64) ([]*Record, error) {
	hits, err := qc.store.Query(ctx, qc.vectorCollection, q)
	if err != nil {
		return nil, err
	}
	logger.Debugf("[qdrant] got %d results, min_score=%.4f", len(hits), minScore)

	kept := make([]scoredID, 0, len(hits))
	for i, h := range hits {
		logger.Debugf("[qdrant] result %d: score=%.4f", i+1, h.Score)
		if h.Score < float32(minScore) {
			continue
		}
		kept = append(kept, h)
	}
	return qc.hydrate(ctx, kept)
}

// hydrate loads records for the given hits from the source tier in hit
// order. Vector points whose record no longer exists in the source tier
// (e.g. a delete whose vector half failed) are removed best-effort.
func (qc *QdrantClient) hydrate(ctx context.Context, hits []scoredID) ([]*Record, error) {
	if len(hits) == 0 {
		return []*Record{}, nil
	}
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
	}
	points, err := qc.store.Get(ctx, qc.sourceCollection, ids, true, false)
	if err != nil {
		return nil, fmt.Errorf("hydrate %d records: %w", len(ids), err)
	}
	byID := make(map[string]*tierPoint, len(points))
	for _, pt := range points {
		byID[pt.ID] = pt
	}

	memories := make([]*Record, 0, len(hits))
	var orphans []string
	failed := 0
	for _, h := range hits {
		pt, ok := byID[h.ID]
		if !ok {
			orphans = append(orphans, h.ID)
			continue
		}
		memory, err := qc.payloadToMemory(pt.Payload, h.ID)
		if err != nil {
			logger.Warnf("[qdrant] Failed to convert payload to memory (id=%s): %v", h.ID, err)
			failed++
			continue
		}
		logger.Debugf("[qdrant] hit score=%.4f type=%s content=%q", h.Score, memory.MemoryType, memory.Content)
		memories = append(memories, memory)
	}
	if failed > 0 {
		logger.Warnf("[qdrant] %d payloads failed to convert", failed)
	}
	if len(orphans) > 0 {
		logger.Warnf("[qdrant] %d vector points have no source record; removing them from %s", len(orphans), qc.vectorCollection)
		if err := qc.store.Delete(ctx, qc.vectorCollection, orphans); err != nil {
			logger.Warnf("[qdrant] failed to remove orphan vectors: %v", err)
		}
	}
	return memories, nil
}

// SearchMemories searches for similar memories. opts must be non-nil.
func (qc *QdrantClient) SearchMemories(ctx context.Context, userID string, embedding []float32, opts *SearchOptions) ([]*Record, error) {
	if opts == nil {
		return nil, fmt.Errorf("search options are required")
	}

	memories, err := qc.searchAndHydrate(ctx, &vectorQuery{
		Dense:  embedding,
		Filter: buildUserSearchFilter(userID, opts),
		Limit:  uint64(opts.TopK),
	}, opts.MinScore)
	if err != nil {
		return nil, fmt.Errorf("failed to search memories: %w", err)
	}
	return memories, nil
}

// SearchMemoriesByChannel searches for memories in a specific channel using semantic similarity.
// Only filters by channel_id (no user_id restriction), allowing cross-user memory retrieval.
func (qc *QdrantClient) SearchMemoriesByChannel(ctx context.Context, channelID string, embedding []float32, opts *SearchOptions) ([]*Record, error) {
	if channelID == "" {
		return nil, fmt.Errorf("channel ID is required")
	}
	if opts == nil {
		return nil, fmt.Errorf("search options are required")
	}

	filter := &qdrant.Filter{
		Must: []*qdrant.Condition{
			qdrant.NewMatch("channel_id", channelID),
		},
		Should: memoryTypeShould(opts.MemoryTypes),
	}

	memories, err := qc.searchAndHydrate(ctx, &vectorQuery{
		Dense:  embedding,
		Filter: filter,
		Limit:  uint64(opts.TopK),
	}, opts.MinScore)
	if err != nil {
		return nil, fmt.Errorf("failed to search memories by channel: %w", err)
	}
	return memories, nil
}

// scrollSourceRecords reads one page of source records matching filter.
func (qc *QdrantClient) scrollSourceRecords(ctx context.Context, filter *qdrant.Filter, limit uint32) ([]*Record, error) {
	points, _, err := qc.store.Scroll(ctx, qc.sourceCollection, filter, "", limit, true, false)
	if err != nil {
		return nil, err
	}
	memories := []*Record{}
	for _, point := range points {
		memory, err := qc.payloadToMemory(point.Payload, point.ID)
		if err != nil {
			logger.Warnf("[qdrant] failed to convert payload to memory: %v", err)
			continue
		}
		memories = append(memories, memory)
	}
	return memories, nil
}

// GetMemoriesByUser retrieves all memories for a user
func (qc *QdrantClient) GetMemoriesByUser(ctx context.Context, userID string, limit int) ([]*Record, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be greater than 0, got: %d", limit)
	}

	logger.Debugf("[qdrant] retrieving memories for userID=%s limit=%d", userID, limit)

	memories, err := qc.scrollSourceRecords(ctx, &qdrant.Filter{
		Must: []*qdrant.Condition{qdrant.NewMatch("user_id", userID)},
	}, uint32(limit))
	if err != nil {
		return nil, fmt.Errorf("query memories for userID=%s: %w", userID, err)
	}

	logger.Debugf("[qdrant] retrieved %d memories for userID=%s", len(memories), userID)
	return memories, nil
}

// ListMemoriesByType retrieves all memories for a user filtered by memory_type
func (qc *QdrantClient) ListMemoriesByType(ctx context.Context, userID string, memoryType string) ([]*Record, error) {
	logger.Debugf("[qdrant] listing memories for userID=%s type=%s", userID, memoryType)

	memories, err := qc.scrollSourceRecords(ctx, &qdrant.Filter{
		Must: []*qdrant.Condition{
			qdrant.NewMatch("user_id", userID),
			qdrant.NewMatch("memory_type", memoryType),
		},
	}, 1000)
	if err != nil {
		return nil, fmt.Errorf("list memories for userID=%s type=%s: %w", userID, memoryType, err)
	}

	logger.Debugf("[qdrant] listed %d memories for userID=%s type=%s", len(memories), userID, memoryType)
	return memories, nil
}

// ScrollMemories reads up to `limit` records from the source tier and
// attaches their embeddings from the current vector tier. Records without a
// vector yet (e.g. while re-embedding) are returned with an empty Embedding.
func (qc *QdrantClient) ScrollMemories(ctx context.Context, limit uint32) ([]*scrollPoint, error) {
	results, _, err := qc.store.Scroll(ctx, qc.sourceCollection, nil, "", limit, true, false)
	if err != nil {
		return nil, fmt.Errorf("scroll memories: %w", err)
	}

	ids := make([]string, 0, len(results))
	for _, pt := range results {
		ids = append(ids, pt.ID)
	}
	vectors := map[string][]float32{}
	if len(ids) > 0 {
		vecPoints, err := qc.store.Get(ctx, qc.vectorCollection, ids, false, true)
		if err != nil {
			return nil, fmt.Errorf("scroll memories: load vectors: %w", err)
		}
		for _, vp := range vecPoints {
			vectors[vp.ID] = vp.Dense
		}
	}

	points := make([]*scrollPoint, 0, len(results))
	for _, pt := range results {
		points = append(points, &scrollPoint{
			ID:        pt.ID,
			Payload:   pt.Payload,
			Embedding: vectors[pt.ID],
		})
	}
	return points, nil
}

// DetectDamagedMemories scans memory points for payload damage.
// A point is damaged when its payload is empty or missing the schema_version key.
// Returns the list of damaged memory UUIDs.
func (qc *QdrantClient) DetectDamagedMemories(ctx context.Context, limit uint32) ([]string, error) {
	points, err := qc.ScrollMemories(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("detect damaged memories: %w", err)
	}

	damaged := detectDamagedPoints(points)
	logger.Infof("[qdrant] detect damaged memories: scanned %d, found %d damaged", len(points), len(damaged))
	return damaged, nil
}

// detectDamagedPoints checks each scrollPoint and returns UUIDs of points
// whose payload is empty or missing schema_version.
func detectDamagedPoints(points []*scrollPoint) []string {
	var damaged []string
	for _, pt := range points {
		if len(pt.Payload) == 0 {
			damaged = append(damaged, pt.ID)
			continue
		}
		if _, ok := pt.Payload["schema_version"]; !ok {
			damaged = append(damaged, pt.ID)
		}
	}
	return damaged
}

// repairOrDeleteDamagedPoints deletes damaged points and returns counts + affected user IDs.
// It iterates point payloads, deletes those detected as damaged, extracts user_id
// from raw payload, and accumulates unique user IDs.
func repairOrDeleteDamagedPoints(ctx context.Context, points []*scrollPoint, deleteFn func(ctx context.Context, id string) error) (deleted int, userIDs []string) {
	damaged := detectDamagedPoints(points)
	if len(damaged) == 0 {
		return 0, nil
	}

	damagedSet := make(map[string]bool, len(damaged))
	for _, id := range damaged {
		damagedSet[id] = true
	}

	seenUsers := make(map[string]struct{})

	for _, pt := range points {
		if !damagedSet[pt.ID] {
			continue
		}

		// Extract user_id from raw payload before deletion.
		uid := ""
		if v, ok := pt.Payload["user_id"]; ok && v != nil {
			uid = v.GetStringValue()
		}

		if err := deleteFn(ctx, pt.ID); err != nil {
			logger.Errorf("[qdrant] repair: failed to delete damaged memoryID=%s: %v", pt.ID, err)
			continue
		}

		deleted++

		if uid != "" {
			if _, exists := seenUsers[uid]; !exists {
				seenUsers[uid] = struct{}{}
				userIDs = append(userIDs, uid)
			}
			logger.Infof("[qdrant] repair: deleted damaged memoryID=%s userID=%s", pt.ID, uid)
		} else {
			logger.Infof("[qdrant] repair: deleted damaged memoryID=%s (no user_id)", pt.ID)
		}
	}

	return deleted, userIDs
}

// RepairOrDeleteDamagedMemories scans memory points for payload damage,
// deletes damaged points, and returns the count of deleted points plus
// the deduplicated list of affected user IDs.
func (qc *QdrantClient) RepairOrDeleteDamagedMemories(ctx context.Context, limit uint32) (deleted int, userIDs []string, err error) {
	points, err := qc.ScrollMemories(ctx, limit)
	if err != nil {
		return 0, nil, fmt.Errorf("repair damaged memories: %w", err)
	}

	deleted, userIDs = repairOrDeleteDamagedPoints(ctx, points, qc.DeletePoint)
	logger.Infof("[qdrant] repair: scanned %d points, deleted %d damaged, affected %d users", len(points), deleted, len(userIDs))
	return deleted, userIDs, nil
}

// ScrollRelationships scrolls all relationship points with payload, limited to `limit` points.
func (qc *QdrantClient) ScrollRelationships(ctx context.Context, limit uint32) ([]*scrollRelationshipPoint, error) {
	results, _, err := qc.store.Scroll(ctx, qc.relationshipsCollection, nil, "", limit, true, false)
	if err != nil {
		return nil, fmt.Errorf("scroll relationships: %w", err)
	}

	points := make([]*scrollRelationshipPoint, 0, len(results))
	for _, pt := range results {
		points = append(points, &scrollRelationshipPoint{
			ID:      pt.ID,
			Payload: pt.Payload,
		})
	}
	return points, nil
}

// deleteRecords removes records from both tiers. The source tier (truth) is
// deleted first: once that succeeds the record is gone for every reader. A
// failed vector delete only leaves an orphan vector, which search hydration
// detects and removes.
func (qc *QdrantClient) deleteRecords(ctx context.Context, ids []string) error {
	if err := qc.store.Delete(ctx, qc.sourceCollection, ids); err != nil {
		return err
	}
	if err := qc.store.Delete(ctx, qc.vectorCollection, ids); err != nil {
		logger.Warnf("[qdrant] deleted %d records from %s but not their vectors from %s: %v", len(ids), qc.sourceCollection, qc.vectorCollection, err)
	}
	return nil
}

// DeletePoint deletes a single memory by ID from both tiers.
func (qc *QdrantClient) DeletePoint(ctx context.Context, id string) error {
	if err := qc.deleteRecords(ctx, []string{id}); err != nil {
		return fmt.Errorf("delete point %s: %w", id, err)
	}
	return nil
}

// DeleteRelationshipPoint deletes a single point by ID from the relationships collection.
func (qc *QdrantClient) DeleteRelationshipPoint(ctx context.Context, id string) error {
	if err := qc.store.Delete(ctx, qc.relationshipsCollection, []string{id}); err != nil {
		return fmt.Errorf("delete relationship point %s: %w", id, err)
	}
	return nil
}

// GetMemory retrieves a single memory by ID from the source tier.
func (qc *QdrantClient) GetMemory(ctx context.Context, memoryID string) (*Record, error) {
	logger.Debugf("[qdrant] retrieving memoryID=%s", memoryID)

	points, err := qc.store.Get(ctx, qc.sourceCollection, []string{memoryID}, true, false)
	if err != nil {
		return nil, fmt.Errorf("get memory %s: %w", memoryID, err)
	}

	if len(points) == 0 {
		logger.Warnf("[qdrant] memory not found: memoryID=%s", memoryID)
		return nil, fmt.Errorf("memory not found")
	}

	memory, err := qc.payloadToMemory(points[0].Payload, memoryID)
	if err != nil {
		return nil, fmt.Errorf("convert payload for memoryID=%s: %w", memoryID, err)
	}

	logger.Debugf("[qdrant] successfully retrieved memoryID=%s type=%s", memoryID, memory.MemoryType)
	return memory, nil
}

// DeleteMemory deletes a single memory from both tiers.
func (qc *QdrantClient) DeleteMemory(ctx context.Context, memoryID string) error {
	logger.Warnf("[qdrant] deleting memoryID=%s", memoryID)

	if err := qc.deleteRecords(ctx, []string{memoryID}); err != nil {
		return fmt.Errorf("delete memory %s: %w", memoryID, err)
	}

	logger.Infof("[qdrant] successfully deleted memoryID=%s", memoryID)
	return nil
}

// DeleteUserMemories deletes all memories for a user from both tiers.
func (qc *QdrantClient) DeleteUserMemories(ctx context.Context, userID string) error {
	logger.Warnf("[qdrant] deleting all memories for userID=%s", userID)

	filter := &qdrant.Filter{
		Must: []*qdrant.Condition{
			qdrant.NewMatch("user_id", userID),
		},
	}
	if err := qc.store.DeleteByFilter(ctx, qc.sourceCollection, filter); err != nil {
		return fmt.Errorf("delete user memories for userID=%s: %w", userID, err)
	}
	if err := qc.store.DeleteByFilter(ctx, qc.vectorCollection, filter); err != nil {
		return fmt.Errorf("delete user memory vectors for userID=%s: %w", userID, err)
	}

	logger.Infof("[qdrant] successfully deleted all memories for userID=%s", userID)
	return nil
}

// CountMemories returns the exact number of memory records (source tier).
func (qc *QdrantClient) CountMemories(ctx context.Context) (uint64, error) {
	return qc.store.Count(ctx, qc.sourceCollection)
}

// CountProfiles returns the exact number of stored profiles.
func (qc *QdrantClient) CountProfiles(ctx context.Context) (uint64, error) {
	return qc.store.Count(ctx, qc.profilesCollection)
}

// UpsertProfile stores or updates a user profile
func (qc *QdrantClient) UpsertProfile(ctx context.Context, profile *Profile) error {
	profile.LastActiveAt = time.Now()

	logger.Debugf("[qdrant] storing profile for userID=%s messageCount=%d memoryCount=%d",
		profile.UserID, profile.MessageCount, profile.MemoryCount)

	// Prepare all data before retry loop
	payload, err := qc.profileToPayload(profile)
	if err != nil {
		return fmt.Errorf("failed to prepare profile payload: %w", err)
	}

	// Profiles are never searched by vector; the collection is payload-only
	// unless it was created by an older version with a dense vector.
	vectors := placeholderVectors(qc.profilesDim, profile.Embedding)

	numID, err := discordIDToUint64(profile.UserID)
	if err != nil {
		return fmt.Errorf("upsert profile: %w", err)
	}

	_, err = retry.Retry(ctx, qc.maxRetries, func(ctx context.Context) (*qdrant.UpdateResult, error) {
		return qc.client.Upsert(ctx, &qdrant.UpsertPoints{
			CollectionName: qc.profilesCollection,
			Points: []*qdrant.PointStruct{
				{
					Id:      qdrant.NewIDNum(numID),
					Vectors: vectors,
					Payload: payload,
				},
			},
		})
	}, retry.WithErrorClassifier(isRetryableGrpc), retry.WithBaseDelay(qc.baseBackoff), retry.WithMaxDelay(qc.maxBackoff))
	if err != nil {
		return fmt.Errorf("upsert profile for userID=%s: %w", profile.UserID, err)
	}
	logger.Debugf("[qdrant] successfully stored profile for userID=%s", profile.UserID)
	return nil
}

// GetProfile retrieves a user profile
func (qc *QdrantClient) GetProfile(ctx context.Context, userID string) (*Profile, error) {
	logger.Debugf("[qdrant] getting profile for userID=%s", userID)

	numID, err := discordIDToUint64(userID)
	if err != nil {
		return nil, fmt.Errorf("get profile: %w", err)
	}

	points, err := qc.client.Get(ctx, &qdrant.GetPoints{
		CollectionName: qc.profilesCollection,
		Ids: []*qdrant.PointId{
			qdrant.NewIDNum(numID),
		},
		WithPayload: qdrant.NewWithPayload(true),
	})
	if err != nil {
		logger.Debugf("[qdrant] get error: %v", err)
		return nil, fmt.Errorf("failed to get profile: %w", err)
	}

	logger.Debugf("[qdrant] got %d points", len(points))

	if len(points) == 0 {
		return nil, ErrProfileNotFound
	}

	point := points[0]
	logger.Debugf("[qdrant] point ID: %v, payload keys: %v", point.Id, getPayloadKeys(point.Payload))
	return qc.payloadToProfile(point.Payload, userID)
}

// getPayloadKeys returns the keys from a payload map for debugging
func getPayloadKeys(payload map[string]*qdrant.Value) []string {
	keys := make([]string, 0, len(payload))
	for k := range payload {
		keys = append(keys, k)
	}
	return keys
}

// DeleteProfile deletes a user profile
func (qc *QdrantClient) DeleteProfile(ctx context.Context, userID string) error {
	logger.Warnf("[qdrant] deleting profile for userID=%s", userID)

	numID, err := discordIDToUint64(userID)
	if err != nil {
		return fmt.Errorf("delete profile: %w", err)
	}

	_, err = qc.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: qc.profilesCollection,
		Points:         qdrant.NewPointsSelector(qdrant.NewIDNum(numID)),
	})
	if err != nil {
		return fmt.Errorf("delete profile for userID=%s: %w", userID, err)
	}

	logger.Infof("[qdrant] successfully deleted profile for userID=%s", userID)
	return nil
}

// UpsertRelationship stores or updates a relationship record.
// If rel.ID is empty, a new UUID is generated. Uses retry with backoff for transient failures.
func (qc *QdrantClient) UpsertRelationship(ctx context.Context, rel *Relationship) error {
	if rel.ID == "" {
		rel.ID = uuid.New().String()
	}

	logger.Debugf("[qdrant] upserting relationship user_a=%s user_b=%s type=%s",
		rel.UserA, rel.UserB, rel.Type)

	payload, err := qc.relationshipToPayload(rel)
	if err != nil {
		return fmt.Errorf("failed to prepare relationship payload: %w", err)
	}

	relID := rel.ID
	vectors := placeholderVectors(qc.relationshipsDim, nil)

	_, err = retry.Retry(ctx, qc.maxRetries, func(ctx context.Context) (*qdrant.UpdateResult, error) {
		return qc.client.Upsert(ctx, &qdrant.UpsertPoints{
			CollectionName: qc.relationshipsCollection,
			Points: []*qdrant.PointStruct{
				{
					Id:      qdrant.NewID(relID),
					Vectors: vectors,
					Payload: payload,
				},
			},
		})
	}, retry.WithErrorClassifier(isRetryableGrpc), retry.WithBaseDelay(qc.baseBackoff), retry.WithMaxDelay(qc.maxBackoff))
	if err != nil {
		return fmt.Errorf("upsert relationship for user_a=%s user_b=%s: %w", rel.UserA, rel.UserB, err)
	}

	logger.Debugf("[qdrant] successfully stored relationship id=%s", relID)
	return nil
}

// GetRelationships retrieves all relationships involving the given user.
// Optionally filters by relationship type.
func (qc *QdrantClient) GetRelationships(ctx context.Context, userID string, relType RelationshipType) ([]*Relationship, error) {
	logger.Debugf("[qdrant] getting relationships for userID=%s type=%s", userID, relType)

	if relType != "" {
		// Must match: (user_a = userID OR user_b = userID) AND type = relType
		filter := &qdrant.Filter{
			Must: []*qdrant.Condition{
				qdrant.NewFilterAsCondition(&qdrant.Filter{
					Should: []*qdrant.Condition{
						qdrant.NewMatch("user_a", userID),
						qdrant.NewMatch("user_b", userID),
					},
				}),
				qdrant.NewMatch("type", string(relType)),
			},
		}

		return qc.scrollRelationships(ctx, filter, userID)
	}

	// Should with user_a OR user_b — acts as OR when Must is empty
	filter := &qdrant.Filter{
		Should: []*qdrant.Condition{
			qdrant.NewMatch("user_a", userID),
			qdrant.NewMatch("user_b", userID),
		},
	}

	return qc.scrollRelationships(ctx, filter, userID)
}

// scrollRelationships executes a scroll query and converts results to Relationship records.
func (qc *QdrantClient) scrollRelationships(ctx context.Context, filter *qdrant.Filter, userID string) ([]*Relationship, error) {
	results, err := qc.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: qc.relationshipsCollection,
		Filter:         filter,
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get relationships for userID=%s: %w", userID, err)
	}

	relationships := []*Relationship{}
	for _, point := range results {
		rel, err := qc.payloadToRelationship(point.Payload, point.Id.GetUuid())
		if err != nil {
			logger.Warnf("[qdrant] failed to convert payload to relationship: %v", err)
			continue
		}
		relationships = append(relationships, rel)
	}

	logger.Debugf("[qdrant] retrieved %d relationships for userID=%s", len(relationships), userID)
	return relationships, nil
}

// GetRelationshipBetween retrieves relationships between two specific users.
// Optionally filters by relationship type.
func (qc *QdrantClient) GetRelationshipBetween(ctx context.Context, userA, userB string, relType RelationshipType) ([]*Relationship, error) {
	logger.Debugf("[qdrant] getting relationship between user_a=%s user_b=%s type=%s", userA, userB, relType)

	// (user_a = A AND user_b = B) OR (user_a = B AND user_b = A)
	userFilter := &qdrant.Filter{
		Should: []*qdrant.Condition{
			qdrant.NewFilterAsCondition(&qdrant.Filter{
				Must: []*qdrant.Condition{
					qdrant.NewMatch("user_a", userA),
					qdrant.NewMatch("user_b", userB),
				},
			}),
			qdrant.NewFilterAsCondition(&qdrant.Filter{
				Must: []*qdrant.Condition{
					qdrant.NewMatch("user_a", userB),
					qdrant.NewMatch("user_b", userA),
				},
			}),
		},
	}

	var filter *qdrant.Filter
	if relType != "" {
		filter = &qdrant.Filter{
			Must: []*qdrant.Condition{
				qdrant.NewFilterAsCondition(userFilter),
				qdrant.NewMatch("type", string(relType)),
			},
		}
	} else {
		filter = userFilter
	}

	results, err := qc.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: qc.relationshipsCollection,
		Filter:         filter,
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get relationship between user_a=%s user_b=%s: %w", userA, userB, err)
	}

	relationships := []*Relationship{}
	for _, point := range results {
		rel, err := qc.payloadToRelationship(point.Payload, point.Id.GetUuid())
		if err != nil {
			logger.Warnf("[qdrant] failed to convert payload to relationship: %v", err)
			continue
		}
		relationships = append(relationships, rel)
	}

	logger.Debugf("[qdrant] retrieved %d relationships between user_a=%s user_b=%s", len(relationships), userA, userB)
	return relationships, nil
}

// RemoveUserFromMentions finds all memory records where mentioned_user_ids
// contains userID and removes it from the array in both tiers (the vector
// tier mirrors mentioned_user_ids for filtered search). Logs a warning on
// failure but does not return an error to avoid blocking other operations.
func (qc *QdrantClient) RemoveUserFromMentions(ctx context.Context, userID string) {
	logger.Debugf("[qdrant] removing userID=%s from mentioned_user_ids", userID)

	filter := &qdrant.Filter{
		Must: []*qdrant.Condition{
			qdrant.NewMatch("mentioned_user_ids", userID),
		},
	}

	// Collect first, then update: updating the filtered field while paging
	// through the same filter would shift the pages.
	type pending struct {
		id       string
		mentions []string
	}
	var updates []pending
	var totalRemoved, totalFailed int
	offset := ""
	for {
		points, next, err := qc.store.Scroll(ctx, qc.sourceCollection, filter, offset, 100, true, false)
		if err != nil {
			logger.Warnf("[qdrant] failed to scroll memories for mention removal: %v", err)
			return
		}
		for _, point := range points {
			memory, err := qc.payloadToMemory(point.Payload, point.ID)
			if err != nil {
				logger.Warnf("[qdrant] failed to convert payload for mention removal (id=%s): %v", point.ID, err)
				totalFailed++
				continue
			}
			newMentions := make([]string, 0, len(memory.MentionedUserIDs))
			for _, uid := range memory.MentionedUserIDs {
				if uid != userID {
					newMentions = append(newMentions, uid)
				}
			}
			updates = append(updates, pending{id: point.ID, mentions: newMentions})
		}
		if next == "" || len(points) == 0 {
			break
		}
		offset = next
	}

	for _, u := range updates {
		vals := make([]*qdrant.Value, 0, len(u.mentions))
		for _, uid := range u.mentions {
			vals = append(vals, &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: uid}})
		}
		updatedPayload := map[string]*qdrant.Value{
			"mentioned_user_ids": {Kind: &qdrant.Value_ListValue{ListValue: &qdrant.ListValue{Values: vals}}},
		}
		if err := qc.store.SetPayload(ctx, qc.sourceCollection, []string{u.id}, updatedPayload); err != nil {
			logger.Warnf("[qdrant] failed to update mentions for memoryID=%s: %v", u.id, err)
			totalFailed++
			continue
		}
		if err := qc.store.SetPayload(ctx, qc.vectorCollection, []string{u.id}, updatedPayload); err != nil {
			logger.Warnf("[qdrant] failed to update vector-tier mentions for memoryID=%s: %v", u.id, err)
		}
		totalRemoved++
	}

	if totalRemoved > 0 || totalFailed > 0 {
		logger.Infof("[qdrant] mention removal complete: %d updated, %d failed", totalRemoved, totalFailed)
	}
}

func computeBM25SparseVector(content string, keywords []string) ([]uint32, []float32) {
	const k1, b = 1.2, 0.75

	tokens := tokenize(strings.ToLower(content))
	for _, kw := range keywords {
		tokens = append(tokens, strings.ToLower(kw))
	}
	if len(tokens) == 0 {
		return nil, nil
	}

	tf := make(map[string]int)
	for _, t := range tokens {
		tf[t]++
	}

	avgdl := 10.0
	dl := float64(len(tokens))

	type entry struct {
		idx uint32
		w   float32
	}
	entries := make([]entry, 0, len(tf))
	for term, freq := range tf {
		tfNorm := (float64(freq) * (k1 + 1)) / (float64(freq) + k1*(1-b+b*dl/avgdl))
		idx := hashToken(term)
		entries = append(entries, entry{idx: idx, w: float32(tfNorm)})
	}

	slices.SortFunc(entries, func(a, b entry) int {
		if a.idx < b.idx {
			return -1
		}
		if a.idx > b.idx {
			return 1
		}
		return 0
	})

	indices := make([]uint32, len(entries))
	values := make([]float32, len(entries))
	for i, e := range entries {
		indices[i] = e.idx
		values[i] = e.w
	}
	return indices, values
}

func tokenize(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func hashToken(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32() % (1 << 24)
}
