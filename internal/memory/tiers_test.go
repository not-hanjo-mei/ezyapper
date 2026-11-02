package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const testModel = "BAAI/bge-m3"
const testDim = 16

// ---------- naming ----------

func TestSanitizeModelName(t *testing.T) {
	cases := map[string]string{
		"BAAI/bge-m3":                "baai_bge_m3",
		"text-embedding-3-small":     "text_embedding_3_small",
		"  Qwen/Qwen3-Embedding-8B ": "qwen_qwen3_embedding_8b",
		"///":                        "model",
		"a--b__c":                    "a_b_c",
		"_lead/trail_":               "lead_trail",
		"模型":                         "model",
	}
	for in, want := range cases {
		if got := sanitizeModelName(in); got != want {
			t.Errorf("sanitizeModelName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVectorCollectionName(t *testing.T) {
	if got := vectorCollectionName("BAAI/bge-m3", 1024); got != "memories_vec_baai_bge_m3_1024" {
		t.Fatalf("got %q", got)
	}
	if got := vectorCollectionName("text-embedding-3-small", 1536); got != "memories_vec_text_embedding_3_small_1536" {
		t.Fatalf("got %q", got)
	}
	// Same model, different dimension → different collection.
	if vectorCollectionName("m", 512) == vectorCollectionName("m", 1024) {
		t.Fatal("dimension must be part of the name")
	}
}

func TestVectorCollectionName_LongModelsStayDistinctAndBounded(t *testing.T) {
	base := strings.Repeat("very-long-model-name/", 20)
	a := vectorCollectionName(base+"a", 1024)
	b := vectorCollectionName(base+"b", 1024)
	if a == b {
		t.Fatalf("truncated names collide: %q", a)
	}
	if len(a) > 255 || len(sanitizeModelName(base+"a")) > maxModelNameLen {
		t.Fatalf("name too long: %d", len(a))
	}
	if !isVectorCollection(a, "") {
		t.Fatalf("%q not recognised as vector collection", a)
	}
}

func TestIsVectorCollection(t *testing.T) {
	if !isVectorCollection("memories_vec_x_1", "") {
		t.Fatal("expected match")
	}
	if isVectorCollection("memories", "") || isVectorCollection("memories_source", "") {
		t.Fatal("unexpected match")
	}
	if !isVectorCollection("ezyapper_test_memories_vec_x_1", "ezyapper_test_") {
		t.Fatal("expected prefixed match")
	}
	if isVectorCollection("memories_vec_x_1", "ezyapper_test_") {
		t.Fatal("unprefixed collection must not match a prefixed client")
	}
}

func TestVectorPayloadFromSource(t *testing.T) {
	qc := &QdrantClient{}
	rec := newTestRecord(1, "1", "likes tea")
	rec.MentionedUserIDs = []string{"2"}
	src, err := qc.memoryToPayload(rec)
	if err != nil {
		t.Fatal(err)
	}
	vp := vectorPayloadFromSource(src)
	for _, k := range []string{"user_id", "guild_id", "channel_id", "memory_type", "mentioned_user_ids"} {
		if _, ok := vp[k]; !ok {
			t.Errorf("vector payload missing %s", k)
		}
	}
	for _, k := range []string{"content", "keywords", "access_count", "importance_score", "schema_version"} {
		if _, ok := vp[k]; ok {
			t.Errorf("vector payload must not carry %s", k)
		}
	}
}

// ---------- collection setup ----------

func TestEnsureMemoryTiers_CreatesSourceAndVector(t *testing.T) {
	store := newFakePointStore()
	qc := newTestTieredClient(store, testModel, testDim)

	src, _ := store.CollectionInfo(context.Background(), CollectionMemorySource)
	if src == nil || src.DenseSize != 0 {
		t.Fatalf("source collection must exist and be payload-only, got %+v", src)
	}
	vec, _ := store.CollectionInfo(context.Background(), qc.VectorCollection())
	if vec == nil || vec.DenseSize != testDim {
		t.Fatalf("vector collection must exist with dim %d, got %+v", testDim, vec)
	}
	if qc.VectorCollection() != "memories_vec_baai_bge_m3_16" {
		t.Fatalf("unexpected vector collection %q", qc.VectorCollection())
	}
}

func TestEnsureMemoryTiers_ModelChangeKeepsOldCollections(t *testing.T) {
	store := newFakePointStore()
	old := newTestTieredClient(store, "old-model", 8)
	if err := old.UpsertMemory(context.Background(), withEmbedding(newTestRecord(1, "1", "x"), (&hashEmbedder{dim: 8}).vector("x"))); err != nil {
		t.Fatal(err)
	}

	cur := newTestTieredClient(store, "new-model", testDim)
	if cur.VectorCollection() == old.VectorCollection() {
		t.Fatal("expected a new vector collection")
	}
	if n, _ := store.Count(context.Background(), old.VectorCollection()); n != 1 {
		t.Fatalf("old vector collection must be kept intact, count=%d", n)
	}
	if n, _ := store.Count(context.Background(), CollectionMemorySource); n != 1 {
		t.Fatalf("source tier must be shared, count=%d", n)
	}
}

func TestEnsureMemoryTiers_DimensionMismatchFails(t *testing.T) {
	store := newFakePointStore()
	store.addCollection(vectorCollectionName(testModel, testDim), testDim+1)
	qc := newQdrantClientWithStore(store, testQdrantCfgDim(testDim), testModel, "")
	if err := qc.ensureMemoryTiers(context.Background()); err == nil {
		t.Fatal("expected dimension mismatch error")
	}
}

func TestEnsureMemoryTiers_SourceWithVectorsFails(t *testing.T) {
	store := newFakePointStore()
	store.addCollection(CollectionMemorySource, 4)
	qc := newQdrantClientWithStore(store, testQdrantCfgDim(testDim), testModel, "")
	if err := qc.ensureMemoryTiers(context.Background()); err == nil {
		t.Fatal("expected error for a source collection with vectors")
	}
}

func TestPlaceholderVectors(t *testing.T) {
	if v := placeholderVectors(0, []float32{1}); len(v.GetVectors().GetVectors()) != 0 || v.GetVector() != nil {
		t.Fatalf("payload-only collection must get an empty vector map, got %v", v)
	}
	if v := placeholderVectors(3, nil); len(v.GetVector().GetDense().GetData()) != 3 {
		t.Fatalf("legacy collection must get a zero vector of its own size, got %v", v)
	}
	if v := placeholderVectors(2, []float32{1, 2}); v.GetVector().GetDense().GetData()[1] != 2 {
		t.Fatalf("fitting embedding must be used, got %v", v)
	}
}

// ---------- store / search / hydrate ----------

func withEmbedding(r *Record, emb []float32) *Record {
	r.Embedding = emb
	return r
}

func TestUpsertMemory_WritesBothTiers(t *testing.T) {
	store := newFakePointStore()
	qc := newTestTieredClient(store, testModel, testDim)
	emb := &hashEmbedder{dim: testDim}

	rec := newTestRecord(1, "1", "likes green tea")
	rec.MentionedUserIDs = []string{"7"}
	if err := qc.UpsertMemory(context.Background(), withEmbedding(rec, emb.vector(rec.Content))); err != nil {
		t.Fatal(err)
	}

	src := store.points(CollectionMemorySource)[rec.ID]
	if src == nil || len(src.Dense) != 0 || src.Payload["content"].GetStringValue() != "likes green tea" {
		t.Fatalf("bad source point: %+v", src)
	}
	vec := store.points(qc.VectorCollection())[rec.ID]
	if vec == nil || len(vec.Dense) != testDim {
		t.Fatalf("bad vector point: %+v", vec)
	}
	if _, ok := vec.Payload["content"]; ok {
		t.Fatal("vector tier must not hold content")
	}
	if len(vec.SparseIndices) == 0 {
		t.Fatal("vector tier must hold the BM25 sparse vector")
	}
}

func TestUpsertMemory_WrongDimensionRejectedBeforeWrite(t *testing.T) {
	store := newFakePointStore()
	qc := newTestTieredClient(store, testModel, testDim)
	err := qc.UpsertMemory(context.Background(), withEmbedding(newTestRecord(1, "1", "x"), make([]float32, testDim+1)))
	if err == nil {
		t.Fatal("expected error")
	}
	if n, _ := store.Count(context.Background(), CollectionMemorySource); n != 0 {
		t.Fatalf("nothing must be written, source count=%d", n)
	}
}

func TestUpsertMemory_VectorFailureKeepsSourceRecord(t *testing.T) {
	store := newFakePointStore()
	qc := newTestTieredClient(store, testModel, testDim)
	store.failUpsert[qc.VectorCollection()] = errors.New("boom")

	err := qc.UpsertMemory(context.Background(), withEmbedding(newTestRecord(1, "1", "x"), (&hashEmbedder{dim: testDim}).vector("x")))
	if err == nil {
		t.Fatal("expected error")
	}
	if n, _ := store.Count(context.Background(), CollectionMemorySource); n != 1 {
		t.Fatalf("source record must be kept for the next vector sync, count=%d", n)
	}
}

func seedRecords(t *testing.T, qc *QdrantClient, emb *hashEmbedder, recs ...*Record) {
	t.Helper()
	for _, r := range recs {
		if err := qc.UpsertMemory(context.Background(), withEmbedding(r, emb.vector(r.Content))); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSearchMemories_HydratesFromSourceInRankOrder(t *testing.T) {
	store := newFakePointStore()
	qc := newTestTieredClient(store, testModel, testDim)
	emb := &hashEmbedder{dim: testDim}

	seedRecords(t, qc, emb,
		newTestRecord(1, "1", "likes green tea"),
		newTestRecord(2, "1", "plays chess on sundays"),
		newTestRecord(3, "2", "likes green tea"), // other user
	)

	got, err := qc.SearchMemories(context.Background(), "1", emb.vector("likes green tea"), &SearchOptions{TopK: 5, MinScore: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 results for user 1, got %d", len(got))
	}
	if got[0].ID != testRecordID(1) || got[0].Content != "likes green tea" || got[0].GuildID != "100" {
		t.Fatalf("best hit must be the exact match hydrated with full payload, got %+v", got[0])
	}
	for _, r := range got {
		if r.UserID != "1" {
			t.Fatalf("filter leaked user %s", r.UserID)
		}
	}
}

func TestSearchMemories_MinScoreAndFilters(t *testing.T) {
	store := newFakePointStore()
	qc := newTestTieredClient(store, testModel, testDim)
	emb := &hashEmbedder{dim: testDim}

	a := newTestRecord(1, "1", "alpha")
	a.MentionedUserIDs = []string{"9"}
	b := newTestRecord(2, "1", "beta")
	b.ChannelID = "201"
	b.MemoryType = TypeEpisode
	seedRecords(t, qc, emb, a, b)

	// MinScore 0.999 keeps only the exact match.
	got, _ := qc.SearchMemories(context.Background(), "1", emb.vector("alpha"), &SearchOptions{TopK: 5, MinScore: 0.999})
	if len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("min score: got %v", ids(got))
	}
	// Mentioned-user filter.
	got, _ = qc.SearchMemories(context.Background(), "1", emb.vector("beta"), &SearchOptions{TopK: 5, MinScore: -1, MentionedUserIDs: []string{"9"}})
	if len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("mention filter: got %v", ids(got))
	}
	// Type filter.
	got, _ = qc.SearchMemories(context.Background(), "1", emb.vector("alpha"), &SearchOptions{TopK: 5, MinScore: -1, MemoryTypes: []string{string(TypeEpisode)}})
	if len(got) != 1 || got[0].ID != b.ID {
		t.Fatalf("type filter: got %v", ids(got))
	}
	// Channel search across users.
	got, _ = qc.SearchMemoriesByChannel(context.Background(), "201", emb.vector("alpha"), &SearchOptions{TopK: 5, MinScore: -1})
	if len(got) != 1 || got[0].ID != b.ID {
		t.Fatalf("channel search: got %v", ids(got))
	}
}

func ids(recs []*Record) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.ID
	}
	return out
}

func TestSearchMemories_OrphanVectorsAreSkippedAndRemoved(t *testing.T) {
	store := newFakePointStore()
	qc := newTestTieredClient(store, testModel, testDim)
	emb := &hashEmbedder{dim: testDim}
	seedRecords(t, qc, emb, newTestRecord(1, "1", "alpha"), newTestRecord(2, "1", "beta"))

	// Simulate a delete whose vector half failed.
	_ = store.Delete(context.Background(), CollectionMemorySource, []string{testRecordID(2)})

	got, err := qc.SearchMemories(context.Background(), "1", emb.vector("beta"), &SearchOptions{TopK: 5, MinScore: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != testRecordID(1) {
		t.Fatalf("got %v", ids(got))
	}
	if _, ok := store.points(qc.VectorCollection())[testRecordID(2)]; ok {
		t.Fatal("orphan vector must be removed during hydration")
	}
}

func TestSearchHybrid_UsesVectorTierAndHydrates(t *testing.T) {
	store := newFakePointStore()
	qc := newTestTieredClient(store, testModel, testDim)
	emb := &hashEmbedder{dim: testDim}
	seedRecords(t, qc, emb, newTestRecord(1, "1", "alpha"))

	idx, vals := computeBM25SparseVector("alpha", nil)
	got, err := qc.SearchHybrid(context.Background(), "1", emb.vector("alpha"), idx, vals, &SearchOptions{TopK: 3, MinScore: -1}, 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Content != "alpha" {
		t.Fatalf("got %+v", got)
	}
}

// ---------- reads, deletes, updates ----------

func TestReadsComeFromSourceTier(t *testing.T) {
	store := newFakePointStore()
	qc := newTestTieredClient(store, testModel, testDim)
	emb := &hashEmbedder{dim: testDim}
	seedRecords(t, qc, emb, newTestRecord(1, "1", "alpha"), newTestRecord(2, "1", "beta"))

	// Wipe the vector tier: ID/user reads must be unaffected.
	_ = store.DeleteByFilter(context.Background(), qc.VectorCollection(), nil)

	if m, err := qc.GetMemory(context.Background(), testRecordID(1)); err != nil || m.Content != "alpha" {
		t.Fatalf("GetMemory: %v %+v", err, m)
	}
	if ms, err := qc.GetMemoriesByUser(context.Background(), "1", 10); err != nil || len(ms) != 2 {
		t.Fatalf("GetMemoriesByUser: %v %d", err, len(ms))
	}
	if ms, err := qc.ListMemoriesByType(context.Background(), "1", string(TypeFact)); err != nil || len(ms) != 2 {
		t.Fatalf("ListMemoriesByType: %v %d", err, len(ms))
	}
	if n, err := qc.CountMemories(context.Background()); err != nil || n != 2 {
		t.Fatalf("CountMemories: %v %d", err, n)
	}
}

func TestDeletesHitBothTiers(t *testing.T) {
	store := newFakePointStore()
	qc := newTestTieredClient(store, testModel, testDim)
	emb := &hashEmbedder{dim: testDim}
	seedRecords(t, qc, emb,
		newTestRecord(1, "1", "alpha"),
		newTestRecord(2, "1", "beta"),
		newTestRecord(3, "2", "gamma"),
	)
	ctx := context.Background()

	if err := qc.DeleteMemory(ctx, testRecordID(1)); err != nil {
		t.Fatal(err)
	}
	if err := qc.DeleteUserMemories(ctx, "2"); err != nil {
		t.Fatal(err)
	}
	src := store.points(CollectionMemorySource)
	vec := store.points(qc.VectorCollection())
	if len(src) != 1 || len(vec) != 1 || src[testRecordID(2)] == nil || vec[testRecordID(2)] == nil {
		t.Fatalf("expected only record 2 in both tiers, src=%d vec=%d", len(src), len(vec))
	}

	if err := qc.DeletePoint(ctx, testRecordID(2)); err != nil {
		t.Fatal(err)
	}
	if len(store.points(CollectionMemorySource)) != 0 || len(store.points(qc.VectorCollection())) != 0 {
		t.Fatal("DeletePoint must clear both tiers")
	}
}

func TestDelete_VectorFailureStillDeletesRecord(t *testing.T) {
	store := newFakePointStore()
	qc := newTestTieredClient(store, testModel, testDim)
	seedRecords(t, qc, &hashEmbedder{dim: testDim}, newTestRecord(1, "1", "alpha"))
	store.failDelete[qc.VectorCollection()] = errors.New("boom")

	if err := qc.DeleteMemory(context.Background(), testRecordID(1)); err != nil {
		t.Fatalf("source delete succeeded, expected nil error, got %v", err)
	}
	if len(store.points(CollectionMemorySource)) != 0 {
		t.Fatal("record must be gone from the source tier")
	}
}

func TestRemoveUserFromMentions_UpdatesBothTiers(t *testing.T) {
	store := newFakePointStore()
	qc := newTestTieredClient(store, testModel, testDim)
	var recs []*Record
	for i := 1; i <= 150; i++ { // more than one scroll page
		r := newTestRecord(i, "1", fmt.Sprintf("memory %d", i))
		r.MentionedUserIDs = []string{"9", "8"}
		recs = append(recs, r)
	}
	seedRecords(t, qc, &hashEmbedder{dim: testDim}, recs...)

	qc.RemoveUserFromMentions(context.Background(), "9")

	for _, coll := range []string{CollectionMemorySource, qc.VectorCollection()} {
		for id, p := range store.points(coll) {
			got := payloadStrings(p.Payload["mentioned_user_ids"])
			if len(got) != 1 || got[0] != "8" {
				t.Fatalf("%s %s: mentions = %v", coll, id, got)
			}
		}
	}
}

func TestAccessCountUpdatesSourceOnly(t *testing.T) {
	store := newFakePointStore()
	qc := newTestTieredClient(store, testModel, testDim)
	seedRecords(t, qc, &hashEmbedder{dim: testDim}, newTestRecord(1, "1", "alpha"))
	ctx := context.Background()

	id := testRecordID(1)
	if err := qc.IncrementAccessCount(ctx, []string{id}, map[string]int{id: 5}); err != nil {
		t.Fatal(err)
	}
	counts, err := qc.GetMemoryPayloads(ctx, []string{id})
	if err != nil || counts[id] != 5 {
		t.Fatalf("counts=%v err=%v", counts, err)
	}
	if _, ok := store.points(qc.VectorCollection())[id].Payload["access_count"]; ok {
		t.Fatal("access_count must not be mirrored into the vector tier")
	}
}

// ---------- maintenance on tiers ----------

func TestScrollMemories_JoinsVectorsFromVectorTier(t *testing.T) {
	store := newFakePointStore()
	qc := newTestTieredClient(store, testModel, testDim)
	emb := &hashEmbedder{dim: testDim}
	seedRecords(t, qc, emb, newTestRecord(1, "1", "alpha"))
	// A record that has no vector yet (e.g. pending re-embedding).
	if err := qc.UpsertMemory(context.Background(), newTestRecord(2, "1", "beta")); err != nil {
		t.Fatal(err)
	}

	pts, err := qc.ScrollMemories(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 2 {
		t.Fatalf("expected 2 points, got %d", len(pts))
	}
	for _, p := range pts {
		switch p.ID {
		case testRecordID(1):
			if len(p.Embedding) != testDim || p.Payload["content"].GetStringValue() != "alpha" {
				t.Fatalf("record 1 must carry payload and vector: %+v", p)
			}
		case testRecordID(2):
			if len(p.Embedding) != 0 {
				t.Fatal("record 2 has no vector yet")
			}
		}
	}
}

func TestMaintenanceMerge_OnTieredStoreDeletesBothTiers(t *testing.T) {
	store := newFakePointStore()
	qc := newTestTieredClient(store, testModel, testDim)
	emb := &hashEmbedder{dim: testDim}
	a := newTestRecord(1, "1", "same text")
	a.ImportanceScore = 0.9
	b := newTestRecord(2, "1", "same text")
	b.ImportanceScore = 0.1
	seedRecords(t, qc, emb, a, b)

	w := &MaintenanceWorker{store: qc, config: &ServiceConfig{
		MergeCosineThreshold: 0.95,
		Scoring:              ScoringConfig{Weights: ScoringWeights{Importance: 1}, MemoryStrengthMultiplier: 1},
	}}
	if err := w.mergeDuplicates(context.Background()); err != nil {
		t.Fatal(err)
	}
	src := store.points(CollectionMemorySource)
	vec := store.points(qc.VectorCollection())
	if len(src) != 1 || len(vec) != 1 || src[a.ID] == nil || vec[a.ID] == nil {
		t.Fatalf("expected only the higher-scored record in both tiers; src=%d vec=%d", len(src), len(vec))
	}
}

func testQdrantCfgDim(dim int) *configQdrant {
	return &configQdrant{VectorSize: dim, MaxRetries: 1, RetryBaseDelayMs: 1, RetryMaxDelayMs: 1}
}

// compile-time check that the fake satisfies the interface.
var _ pointStore = (*fakePointStore)(nil)
