package memory

// Live smoke test for the two-tier memory store against a real Qdrant and a
// real OpenAI-compatible embedding API. Skipped unless EZYAPPER_LIVE_QDRANT=1.
//
//	EZYAPPER_LIVE_QDRANT=1
//	EZYAPPER_LIVE_QDRANT_HOST=qdrant.example.com   (gRPC host)
//	EZYAPPER_LIVE_QDRANT_PORT=443                  (gRPC port, TLS when an API key is set)
//	EZYAPPER_LIVE_QDRANT_API_KEY=...
//	EZYAPPER_LIVE_EMBED_URL=https://api.example.com/v1
//	EZYAPPER_LIVE_EMBED_MODEL=BAAI/bge-m3
//	EZYAPPER_LIVE_EMBED_DIM=1024
//	EZYAPPER_LIVE_EMBED_KEY_FILE=/path/to/config.yaml  (reads memory_pipeline.embedding.api_key)
//
// Every collection it creates is prefixed with liveTestPrefix and deleted at
// the end; no other collection is touched.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"ezyapper/internal/config"

	"github.com/qdrant/go-client/qdrant"
	"gopkg.in/yaml.v3"
)

const liveTestPrefix = "ezyapper_test_tiers_"

type liveEmbedder struct {
	url, model, key string
	http            *http.Client
}

func (e *liveEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	body, _ := json.Marshal(map[string]any{"model": e.model, "input": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(e.url, "/")+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.key)
	// Some gateways (Cloudflare) reject Go's default User-Agent.
	req.Header.Set("User-Agent", "EZyapper/1.0")
	resp, err := e.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding API status %d", resp.StatusCode)
	}
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, fmt.Errorf("empty embedding response")
	}
	return out.Data[0].Embedding, nil
}

func (e *liveEmbedder) Stop() {}

func liveEnv(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Fatalf("%s is required for the live test", key)
	}
	return v
}

func readEmbeddingKey(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	var doc struct {
		MemoryPipeline struct {
			Embedding struct {
				APIKey string `yaml:"api_key"`
			} `yaml:"embedding"`
		} `yaml:"memory_pipeline"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse key file: %v", err)
	}
	if doc.MemoryPipeline.Embedding.APIKey == "" {
		t.Fatal("memory_pipeline.embedding.api_key missing in key file")
	}
	return doc.MemoryPipeline.Embedding.APIKey
}

func dropTestCollections(t *testing.T, c *qdrant.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	names, err := c.ListCollections(ctx)
	if err != nil {
		t.Logf("cleanup: list collections: %v", err)
		return
	}
	for _, n := range names {
		if !strings.HasPrefix(n, liveTestPrefix) {
			continue // never touch anything else
		}
		if err := c.DeleteCollection(ctx, n); err != nil {
			t.Logf("cleanup: delete %s: %v", n, err)
		}
	}
}

func TestLiveTwoTierMemory(t *testing.T) {
	if os.Getenv("EZYAPPER_LIVE_QDRANT") != "1" {
		t.Skip("set EZYAPPER_LIVE_QDRANT=1 to run the live two-tier smoke test")
	}
	port, _ := strconv.Atoi(liveEnv(t, "EZYAPPER_LIVE_QDRANT_PORT"))
	dim, _ := strconv.Atoi(liveEnv(t, "EZYAPPER_LIVE_EMBED_DIM"))
	cfg := &config.QdrantConfig{
		Host:             liveEnv(t, "EZYAPPER_LIVE_QDRANT_HOST"),
		Port:             port,
		APIKey:           liveEnv(t, "EZYAPPER_LIVE_QDRANT_API_KEY"),
		VectorSize:       dim,
		MaxRetries:       3,
		RetryBaseDelayMs: 200,
		RetryMaxDelayMs:  2000,
	}
	model := liveEnv(t, "EZYAPPER_LIVE_EMBED_MODEL")
	emb := &liveEmbedder{
		url:   liveEnv(t, "EZYAPPER_LIVE_EMBED_URL"),
		model: model,
		key:   readEmbeddingKey(t, liveEnv(t, "EZYAPPER_LIVE_EMBED_KEY_FILE")),
		http:  &http.Client{Timeout: 30 * time.Second},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	raw, err := qdrant.NewClient(&qdrant.Config{Host: cfg.Host, Port: cfg.Port, APIKey: cfg.APIKey, UseTLS: true, SkipCompatibilityCheck: true})
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	dropTestCollections(t, raw)
	defer dropTestCollections(t, raw)

	// 1. A pre-split legacy collection written with the current model.
	legacy := &grpcPointStore{client: raw}
	legacyName := liveTestPrefix + CollectionLegacyMemories
	if err := raw.CreateCollection(ctx, &qdrant.CreateCollection{
		CollectionName:      legacyName,
		VectorsConfig:       qdrant.NewVectorsConfig(&qdrant.VectorParams{Size: uint64(dim), Distance: qdrant.Distance_Cosine}),
		SparseVectorsConfig: qdrant.NewSparseVectorsConfig(map[string]*qdrant.SparseVectorParams{sparseVectorName: {}}),
	}); err != nil {
		t.Fatal(err)
	}
	texts := []string{
		"Alice loves green tea and drinks it every morning",
		"Alice plays chess on Sundays at the local club",
		"Bob is learning to play the violin",
		"Bob's favourite food is ramen",
		"Alice is allergic to peanuts",
	}
	conv := &QdrantClient{}
	var legacyPts []*tierPoint
	for i, text := range texts {
		user := "111"
		if strings.HasPrefix(text, "Bob") {
			user = "222"
		}
		r := newTestRecord(i+1, user, text)
		if i == 0 {
			r.MentionedUserIDs = []string{"222"}
		}
		v, err := emb.Embed(ctx, text)
		if err != nil {
			t.Fatalf("embed: %v", err)
		}
		if len(v) != dim {
			t.Fatalf("model returned %d dims, EZYAPPER_LIVE_EMBED_DIM=%d", len(v), dim)
		}
		pl, _ := conv.memoryToPayload(r)
		idx, vals := computeBM25SparseVector(r.Content, r.Keywords)
		legacyPts = append(legacyPts, &tierPoint{ID: r.ID, Payload: pl, Dense: v, SparseIndices: idx, SparseValues: vals})
	}
	if err := legacy.Upsert(ctx, legacyName, legacyPts); err != nil {
		t.Fatal(err)
	}

	// 2. Startup: collections + migration with vector reuse.
	qc, err := newQdrantClient(ctx, cfg, model, liveTestPrefix)
	if err != nil {
		t.Fatal(err)
	}
	defer qc.Close()
	rep, err := qc.MigrateLegacy(ctx, emb)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("migration: %+v", rep)
	if rep.Copied != len(texts) || !rep.VectorsReused {
		t.Fatalf("expected %d copied with reused vectors, got %+v", len(texts), rep)
	}
	if rep2, err := qc.MigrateLegacy(ctx, emb); err != nil || rep2.Action != migrationNone {
		t.Fatalf("second migration must be a no-op: %+v %v", rep2, err)
	}
	if n, _ := legacy.Count(ctx, legacyName); n != uint64(len(texts)) {
		t.Fatalf("legacy collection changed: %d points", n)
	}

	// 3. Search + hydrate.
	q, _ := emb.Embed(ctx, "what does Alice drink?")
	hits, err := qc.SearchMemories(ctx, "111", q, &SearchOptions{TopK: 3, MinScore: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || !strings.Contains(hits[0].Content, "green tea") {
		t.Fatalf("unexpected search result: %+v", hits)
	}
	for _, h := range hits {
		if h.UserID != "111" {
			t.Fatalf("user filter leaked %s", h.UserID)
		}
	}
	t.Logf("search top hit: %q", hits[0].Content)

	// 4. Store, mention cleanup, delete.
	nr := newTestRecord(100, "111", "Alice moved to Berlin last year")
	nr.Embedding, _ = emb.Embed(ctx, nr.Content)
	if err := qc.UpsertMemory(ctx, nr); err != nil {
		t.Fatal(err)
	}
	qc.RemoveUserFromMentions(ctx, "222")
	m1, err := qc.GetMemory(ctx, testRecordID(1))
	if err != nil || len(m1.MentionedUserIDs) != 0 {
		t.Fatalf("mention not removed: %+v %v", m1, err)
	}
	if err := qc.DeleteMemory(ctx, testRecordID(4)); err != nil {
		t.Fatal(err)
	}
	if n, _ := qc.CountMemories(ctx); n != uint64(len(texts)) { // 5 + 1 - 1
		t.Fatalf("source count %d", n)
	}
	if n, _ := qc.store.Count(ctx, qc.VectorCollection()); n != uint64(len(texts)) {
		t.Fatalf("vector count %d", n)
	}

	// 5. Profiles / relationships on payload-only collections.
	if err := qc.UpsertProfile(ctx, &Profile{UserID: "111", Traits: []string{"curious"}, Facts: map[string]string{}, Preferences: map[string]string{}, FirstSeenAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if p, err := qc.GetProfile(ctx, "111"); err != nil || len(p.Traits) != 1 {
		t.Fatalf("profile: %+v %v", p, err)
	}
	if err := qc.UpsertRelationship(ctx, &Relationship{UserA: "111", UserB: "222", Type: RelMention, InteractionCount: 1, LastInteractionAt: time.Now(), Weight: 1}); err != nil {
		t.Fatal(err)
	}
	if rels, err := qc.GetRelationshipBetween(ctx, "111", "222", RelMention); err != nil || len(rels) != 1 {
		t.Fatalf("relationships: %d %v", len(rels), err)
	}

	// 6. "Model change": a new vector collection is built by re-embedding
	// the source tier; the previous one is left in place.
	alt, err := newQdrantClient(ctx, cfg, model+"-alt", liveTestPrefix)
	if err != nil {
		t.Fatal(err)
	}
	defer alt.Close()
	sr, err := alt.SyncVectors(ctx, emb)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("re-embed: %+v into %s", sr, alt.VectorCollection())
	if sr.Embedded != len(texts) || sr.Failed != 0 {
		t.Fatalf("unexpected sync report %+v", sr)
	}
	if sr2, err := alt.SyncVectors(ctx, emb); err != nil || sr2.Embedded != 0 {
		t.Fatalf("second sync must be a no-op: %+v %v", sr2, err)
	}
	if n, _ := qc.store.Count(ctx, qc.VectorCollection()); n != uint64(len(texts)) {
		t.Fatalf("old vector collection changed: %d", n)
	}
	hits, err = alt.SearchMemories(ctx, "111", q, &SearchOptions{TopK: 1, MinScore: 0.1})
	if err != nil || len(hits) != 1 || !strings.Contains(hits[0].Content, "green tea") {
		t.Fatalf("search on re-embedded collection: %+v %v", hits, err)
	}
}
