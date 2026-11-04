package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"ezyapper/internal/config"
)

// ---------- migration decision ----------

func TestPlanLegacyMigration(t *testing.T) {
	cases := []struct {
		name string
		st   migrationState
		want migrationAction
	}{
		{"no legacy", migrationState{}, migrationNone},
		{"no legacy, marker ignored", migrationState{Marker: legacyMigrationInProgress}, migrationNone},
		{"legacy empty", migrationState{LegacyExists: true}, migrationNone},
		{"fresh upgrade", migrationState{LegacyExists: true, LegacyCount: 10}, migrationStart},
		{"interrupted", migrationState{LegacyExists: true, LegacyCount: 10, SourceCount: 4, Marker: legacyMigrationInProgress}, migrationResume},
		{"done", migrationState{LegacyExists: true, LegacyCount: 10, SourceCount: 12, Marker: legacyMigrationDone}, migrationNone},
		{"foreign data", migrationState{LegacyExists: true, LegacyCount: 10, SourceCount: 3}, migrationSkipForeignData},
	}
	for _, tc := range cases {
		if got := planLegacyMigration(tc.st); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestDecideVectorReuse(t *testing.T) {
	cases := []struct {
		name      string
		legacy    int
		current   int
		sims      []float32
		wantReuse bool
	}{
		{"dim mismatch", 1536, 1024, nil, false},
		{"unknown legacy dim", 0, 1024, nil, false},
		{"dim match, no probe", 1024, 1024, nil, true},
		{"dim match, probes agree", 1024, 1024, []float32{0.999, 0.998}, true},
		{"dim match, one probe disagrees", 1024, 1024, []float32{0.999, 0.2}, false},
	}
	for _, tc := range cases {
		if got := decideVectorReuse(tc.legacy, tc.current, tc.sims); got != tc.wantReuse {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.wantReuse)
		}
	}
}

// ---------- legacy migration ----------

// seedLegacy creates a pre-split "memories" collection with n records whose
// vectors come from emb.
func seedLegacy(t *testing.T, store *fakePointStore, emb *hashEmbedder, n int) {
	t.Helper()
	store.addCollection(CollectionLegacyMemories, emb.dim)
	qc := &QdrantClient{}
	var pts []*tierPoint
	for i := 1; i <= n; i++ {
		r := newTestRecord(i, fmt.Sprintf("%d", i%3+1), fmt.Sprintf("legacy memory number %d", i))
		pl, err := qc.memoryToPayload(r)
		if err != nil {
			t.Fatal(err)
		}
		idx, vals := computeBM25SparseVector(r.Content, r.Keywords)
		pts = append(pts, &tierPoint{ID: r.ID, Payload: pl, Dense: emb.vector(r.Content), SparseIndices: idx, SparseValues: vals})
	}
	if err := store.Upsert(context.Background(), CollectionLegacyMemories, pts); err != nil {
		t.Fatal(err)
	}
}

func legacySnapshot(store *fakePointStore) string {
	pts := store.points(CollectionLegacyMemories)
	keys := make([]string, 0, len(pts))
	for id := range pts {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, id := range keys {
		p := pts[id]
		fmt.Fprintf(&b, "%s|%d|%s|%v|%v;", id, len(p.Payload), p.Payload["content"].GetStringValue(), p.Dense, p.SparseIndices)
	}
	return b.String()
}

func TestMigrateLegacy_SameModelReusesVectors(t *testing.T) {
	store := newFakePointStore()
	emb := &hashEmbedder{dim: testDim, salt: "bge"}
	seedLegacy(t, store, emb, 300) // > 2 pages
	before := legacySnapshot(store)
	qc := newTestTieredClient(store, testModel, testDim)

	rep, err := qc.MigrateLegacy(context.Background(), emb)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Action != migrationStart || rep.Copied != 300 || !rep.VectorsReused {
		t.Fatalf("unexpected report %+v", rep)
	}
	if got := emb.calls.Load(); got > vectorReuseProbeCount {
		t.Fatalf("only probe embeddings expected, got %d calls", got)
	}
	if n, _ := store.Count(context.Background(), CollectionMemorySource); n != 300 {
		t.Fatalf("source count %d", n)
	}
	if n, _ := store.Count(context.Background(), qc.VectorCollection()); n != 300 {
		t.Fatalf("vector count %d", n)
	}
	if legacySnapshot(store) != before {
		t.Fatal("legacy collection must not be modified")
	}
	info, _ := store.CollectionInfo(context.Background(), CollectionMemorySource)
	if markerValue(info.Metadata, metaLegacyMigration) != legacyMigrationDone {
		t.Fatal("migration must be marked done")
	}

	// Copied records are readable and searchable.
	m, err := qc.GetMemory(context.Background(), testRecordID(7))
	if err != nil || m.Content != "legacy memory number 7" {
		t.Fatalf("GetMemory after migration: %v %+v", err, m)
	}
	hits, err := qc.SearchMemories(context.Background(), m.UserID, emb.vector(m.Content), &SearchOptions{TopK: 1, MinScore: 0.99})
	if err != nil || len(hits) != 1 || hits[0].ID != m.ID {
		t.Fatalf("search after migration: %v %v", err, ids(hits))
	}

	// Idempotent: a second run does nothing.
	rep, err = qc.MigrateLegacy(context.Background(), emb)
	if err != nil || rep.Action != migrationNone {
		t.Fatalf("second run: %+v %v", rep, err)
	}
}

func TestMigrateLegacy_DifferentDimensionReembedsViaSync(t *testing.T) {
	store := newFakePointStore()
	seedLegacy(t, store, &hashEmbedder{dim: 8, salt: "old"}, 20)
	qc := newTestTieredClient(store, testModel, testDim)
	emb := &hashEmbedder{dim: testDim, salt: "bge"}

	rep, err := qc.MigrateLegacy(context.Background(), emb)
	if err != nil {
		t.Fatal(err)
	}
	if rep.VectorsReused || rep.Copied != 20 {
		t.Fatalf("unexpected report %+v", rep)
	}
	if emb.calls.Load() != 0 {
		t.Fatal("no probe expected on dimension mismatch")
	}
	if n, _ := store.Count(context.Background(), qc.VectorCollection()); n != 0 {
		t.Fatalf("no vectors may be copied across dimensions, got %d", n)
	}

	sr, err := qc.SyncVectors(context.Background(), emb)
	if err != nil {
		t.Fatal(err)
	}
	if sr.Embedded != 20 || sr.Failed != 0 {
		t.Fatalf("sync report %+v", sr)
	}
	vec := store.points(qc.VectorCollection())[testRecordID(5)]
	if cosineSimilarity(vec.Dense, emb.vector("legacy memory number 5")) < 0.9999 {
		t.Fatal("vector must come from the current model")
	}
}

func TestMigrateLegacy_SameDimensionDifferentModelIsNotReused(t *testing.T) {
	store := newFakePointStore()
	seedLegacy(t, store, &hashEmbedder{dim: testDim, salt: "other-model"}, 10)
	qc := newTestTieredClient(store, testModel, testDim)

	rep, err := qc.MigrateLegacy(context.Background(), &hashEmbedder{dim: testDim, salt: "bge"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.VectorsReused {
		t.Fatal("vectors from another model must not be reused even with matching dimension")
	}
	if n, _ := store.Count(context.Background(), qc.VectorCollection()); n != 0 {
		t.Fatalf("vector count %d", n)
	}
}

func TestMigrateLegacy_ProbeFailureFallsBackToDimension(t *testing.T) {
	store := newFakePointStore()
	legacyEmb := &hashEmbedder{dim: testDim, salt: "bge"}
	seedLegacy(t, store, legacyEmb, 5)
	qc := newTestTieredClient(store, testModel, testDim)

	failing := &hashEmbedder{dim: testDim, salt: "bge", failOn: map[string]bool{}}
	for i := 1; i <= 5; i++ {
		failing.failOn[fmt.Sprintf("legacy memory number %d", i)] = true
	}
	rep, err := qc.MigrateLegacy(context.Background(), failing)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.VectorsReused {
		t.Fatal("with an unreachable embedder the matching dimension decides")
	}
}

func TestMigrateLegacy_ResumesAfterInterruption(t *testing.T) {
	store := newFakePointStore()
	emb := &hashEmbedder{dim: testDim, salt: "bge"}
	seedLegacy(t, store, emb, 200)
	qc := newTestTieredClient(store, testModel, testDim)

	// Interrupt: the vector upsert fails after the first source page.
	store.failUpsert[qc.VectorCollection()] = errors.New("connection reset")
	if _, err := qc.MigrateLegacy(context.Background(), emb); err == nil {
		t.Fatal("expected interrupted migration")
	}
	info, _ := store.CollectionInfo(context.Background(), CollectionMemorySource)
	if markerValue(info.Metadata, metaLegacyMigration) != legacyMigrationInProgress {
		t.Fatal("interrupted migration must stay marked in progress")
	}
	if n, _ := store.Count(context.Background(), CollectionMemorySource); n == 0 || n == 200 {
		t.Fatalf("expected a partial copy, got %d", n)
	}

	delete(store.failUpsert, qc.VectorCollection())
	rep, err := qc.MigrateLegacy(context.Background(), emb)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Action != migrationResume || rep.Copied != 200 {
		t.Fatalf("unexpected report %+v", rep)
	}
	if n, _ := store.Count(context.Background(), CollectionMemorySource); n != 200 {
		t.Fatalf("source count %d", n)
	}
	if n, _ := store.Count(context.Background(), qc.VectorCollection()); n != 200 {
		t.Fatalf("vector count %d", n)
	}
}

func TestMigrateLegacy_SkipsWhenSourceHasForeignData(t *testing.T) {
	store := newFakePointStore()
	emb := &hashEmbedder{dim: testDim}
	seedLegacy(t, store, emb, 5)
	qc := newTestTieredClient(store, testModel, testDim)
	seedRecords(t, qc, emb, newTestRecord(999, "1", "new data"))

	rep, err := qc.MigrateLegacy(context.Background(), emb)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Action != migrationSkipForeignData {
		t.Fatalf("got %s", rep.Action)
	}
	if n, _ := store.Count(context.Background(), CollectionMemorySource); n != 1 {
		t.Fatalf("source must be untouched, count=%d", n)
	}
}

func TestMigrateLegacy_NoLegacyCollection(t *testing.T) {
	store := newFakePointStore()
	qc := newTestTieredClient(store, testModel, testDim)
	rep, err := qc.MigrateLegacy(context.Background(), &hashEmbedder{dim: testDim})
	if err != nil || rep.Action != migrationNone {
		t.Fatalf("%+v %v", rep, err)
	}
	if info, _ := store.CollectionInfo(context.Background(), CollectionLegacyMemories); info != nil {
		t.Fatal("the legacy collection must never be created")
	}
}

// ---------- vector sync (re-embedding) ----------

func seedSourceOnly(t *testing.T, qc *QdrantClient, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		if err := qc.UpsertMemory(context.Background(), newTestRecord(i, "1", fmt.Sprintf("text %d", i))); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSyncVectors_EmbedsAllMissingInBatches(t *testing.T) {
	store := newFakePointStore()
	qc := newTestTieredClient(store, testModel, testDim)
	n := tierSyncBatchSize*2 + 17
	seedSourceOnly(t, qc, n)
	emb := &hashEmbedder{dim: testDim, delay: time.Millisecond}

	rep, err := qc.SyncVectors(context.Background(), emb)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Scanned != n || rep.Embedded != n || rep.Failed != 0 || rep.Present != 0 {
		t.Fatalf("report %+v", rep)
	}
	if int(emb.calls.Load()) != n {
		t.Fatalf("embed calls %d, want %d", emb.calls.Load(), n)
	}
	if m := emb.maxInFlight.Load(); m > tierSyncConcurrency {
		t.Fatalf("concurrency bound exceeded: %d > %d", m, tierSyncConcurrency)
	}
	if cnt, _ := store.Count(context.Background(), qc.VectorCollection()); int(cnt) != n {
		t.Fatalf("vector count %d", cnt)
	}
	// Filters must work on re-embedded points.
	got, err := qc.SearchMemories(context.Background(), "1", emb.vector("text 42"), &SearchOptions{TopK: 1, MinScore: 0.99})
	if err != nil || len(got) != 1 || got[0].ID != testRecordID(42) {
		t.Fatalf("search after sync: %v %v", err, ids(got))
	}
}

func TestSyncVectors_ResumesOnlyMissing(t *testing.T) {
	store := newFakePointStore()
	qc := newTestTieredClient(store, testModel, testDim)
	seedSourceOnly(t, qc, 50)
	emb := &hashEmbedder{dim: testDim}

	// 20 records already have vectors (e.g. from an interrupted run).
	for i := 1; i <= 20; i++ {
		r, _ := qc.GetMemory(context.Background(), testRecordID(i))
		if err := qc.UpsertMemory(context.Background(), withEmbedding(r, emb.vector(r.Content))); err != nil {
			t.Fatal(err)
		}
	}

	rep, err := qc.SyncVectors(context.Background(), emb)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Present != 20 || rep.Embedded != 30 || emb.calls.Load() != 30 {
		t.Fatalf("report %+v calls %d", rep, emb.calls.Load())
	}

	// Second run is a no-op.
	rep, err = qc.SyncVectors(context.Background(), emb)
	if err != nil || rep.Embedded != 0 || rep.Present != 50 || emb.calls.Load() != 30 {
		t.Fatalf("second run %+v %v calls %d", rep, err, emb.calls.Load())
	}
}

func TestSyncVectors_FailedRecordsRetriedNextRun(t *testing.T) {
	store := newFakePointStore()
	qc := newTestTieredClient(store, testModel, testDim)
	seedSourceOnly(t, qc, 10)
	emb := &hashEmbedder{dim: testDim, failOn: map[string]bool{"text 3": true, "text 7": true}}

	rep, err := qc.SyncVectors(context.Background(), emb)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Embedded != 8 || rep.Failed != 2 {
		t.Fatalf("report %+v", rep)
	}

	emb.mu.Lock()
	emb.failOn = nil
	emb.mu.Unlock()
	rep, err = qc.SyncVectors(context.Background(), emb)
	if err != nil || rep.Embedded != 2 || rep.Failed != 0 {
		t.Fatalf("retry %+v %v", rep, err)
	}
}

func TestSyncVectors_DimensionMismatchAborts(t *testing.T) {
	store := newFakePointStore()
	qc := newTestTieredClient(store, testModel, testDim)
	seedSourceOnly(t, qc, 5)

	_, err := qc.SyncVectors(context.Background(), &hashEmbedder{dim: testDim + 1})
	if err == nil || !errors.Is(err, errEmbeddingDimMismatch) {
		t.Fatalf("expected dimension mismatch, got %v", err)
	}
	if n, _ := store.Count(context.Background(), qc.VectorCollection()); n != 0 {
		t.Fatalf("nothing may be written, got %d", n)
	}
}

func TestSyncVectors_StopsOnCancel(t *testing.T) {
	store := newFakePointStore()
	qc := newTestTieredClient(store, testModel, testDim)
	seedSourceOnly(t, qc, tierSyncBatchSize*3)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := qc.SyncVectors(ctx, &hashEmbedder{dim: testDim}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// ---------- service wiring ----------

func TestNewService_MigratesThenSyncsInBackground(t *testing.T) {
	store := newFakePointStore()
	// Legacy data from a model with another dimension → full re-embed.
	seedLegacy(t, store, &hashEmbedder{dim: 8, salt: "old"}, 40)
	qc := newTestTieredClient(store, testModel, testDim)
	emb := &hashEmbedder{dim: testDim, salt: "bge"}

	cfg := &ServiceConfig{
		ConsolidationInterval: 10,
		ShortTermLimit:        50,
		TopK:                  5,
		MinScore:              0.5,
		Consolidation:         &config.ConsolidationConfig{LLMConfig: config.LLMConfig{Model: "gpt-4"}, SystemPrompt: "test"},
	}
	svc, err := NewService(cfg, testQdrantCfgDim(testDim), qc, emb, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	// Migration is synchronous: records are readable right away.
	if n, err := svc.CountMemories(context.Background()); err != nil || n != 40 {
		t.Fatalf("CountMemories after NewService: %d %v", n, err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		n, _ := store.Count(context.Background(), qc.VectorCollection())
		if n == 40 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background vector sync did not finish, have %d/40", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
