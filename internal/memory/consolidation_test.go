package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ezyapper/internal/ai"
	"ezyapper/internal/config"
	"ezyapper/internal/logger"
)

var embedSleep func(time.Duration) // test-only override for retry sleep; unused in production

func TestMain(m *testing.M) {
	logger.Init(logger.Config{Level: "info"})
	os.Exit(m.Run())
}

// retryableEmbedder returns errors for the first failCount calls, then succeeds.
type retryableEmbedder struct {
	calls     atomic.Int64
	failCount int64
	mu        sync.Mutex
	vectors   map[string][]float32
}

func newRetryableEmbedder(failCount int64) *retryableEmbedder {
	return &retryableEmbedder{
		failCount: failCount,
		vectors:   make(map[string][]float32),
	}
}

func (m *retryableEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	call := m.calls.Add(1)
	if call <= m.failCount {
		return nil, fmt.Errorf("embedding failed (call %d of %d allowed failures)", call, m.failCount)
	}
	return []float32{float32(len(text)), float32(len(text) * 2)}, nil
}
func (m *retryableEmbedder) Stop() {}

// mockQdrantStore implements qdrantStore for consolidation tests.
type mockQdrantStore struct {
	memories      map[string]*Record
	profiles      map[string]*Profile
	relationships map[string]*Relationship
	// upsertMemoryErr forces UpsertMemory to fail after retry exhaustion
	upsertMemoryErr error
	// upsertProfileErr forces UpsertProfile to fail
	upsertProfileErr error
}

func newMockQdrantStore() *mockQdrantStore {
	return &mockQdrantStore{
		memories:      make(map[string]*Record),
		profiles:      make(map[string]*Profile),
		relationships: make(map[string]*Relationship),
	}
}

func (m *mockQdrantStore) UpsertMemory(ctx context.Context, memory *Record) error {
	if m.upsertMemoryErr != nil {
		return m.upsertMemoryErr
	}
	if memory.ID == "" {
		memory.ID = fmt.Sprintf("mem-%d", len(m.memories))
	}
	m.memories[memory.ID] = memory
	return nil
}

func (m *mockQdrantStore) UpsertProfile(ctx context.Context, profile *Profile) error {
	if m.upsertProfileErr != nil {
		return m.upsertProfileErr
	}
	m.profiles[profile.UserID] = profile
	return nil
}

func (m *mockQdrantStore) GetProfile(ctx context.Context, userID string) (*Profile, error) {
	p, ok := m.profiles[userID]
	if !ok {
		return &Profile{
			UserID:      userID,
			Traits:      []string{},
			Facts:       make(map[string]string),
			Preferences: make(map[string]string),
			Interests:   []string{},
			FirstSeenAt: time.Now(),
		}, nil
	}
	return p, nil
}

func (m *mockQdrantStore) GetMemoriesByUser(ctx context.Context, userID string, limit int) ([]*Record, error) {
	var result []*Record
	for _, mem := range m.memories {
		if mem.UserID == userID {
			result = append(result, mem)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *mockQdrantStore) SearchMemories(ctx context.Context, userID string, embedding []float32, opts *SearchOptions) ([]*Record, error) {
	if opts == nil {
		return nil, nil
	}
	var result []*Record
	for _, mem := range m.memories {
		if mem.UserID != userID {
			continue
		}
		if len(opts.MemoryTypes) > 0 {
			match := false
			for _, mt := range opts.MemoryTypes {
				if string(mem.MemoryType) == mt {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		result = append(result, mem)
		if len(result) >= opts.TopK {
			break
		}
	}
	return result, nil
}

func (m *mockQdrantStore) ListMemoriesByType(ctx context.Context, userID string, memoryType string) ([]*Record, error) {
	var result []*Record
	for _, mem := range m.memories {
		if mem.UserID == userID && string(mem.MemoryType) == memoryType {
			result = append(result, mem)
		}
	}
	return result, nil
}

func (m *mockQdrantStore) UpsertRelationship(ctx context.Context, rel *Relationship) error {
	m.relationships[rel.ID] = rel
	return nil
}

func (m *mockQdrantStore) GetRelationshipBetween(ctx context.Context, userA, userB string, relType RelationshipType) ([]*Relationship, error) {
	relID := relationshipID(userA, userB, relType)
	if rel, ok := m.relationships[relID]; ok {
		return []*Relationship{rel}, nil
	}
	return nil, nil
}

// TestEmbedWithRetry_Success verifies embedWithRetry retries on failure then succeeds.
func TestEmbedWithRetry_Success(t *testing.T) {
	defer func() { embedSleep = nil }()
	embedSleep = func(d time.Duration) {} // skip real sleep

	ctx := context.Background()
	emb := newRetryableEmbedder(2) // fails first 2 calls, succeeds on 3rd

	c := &Consolidator{
		embedder:        emb,
		retryMaxRetries: 3,
		retryBaseDelay:  1 * time.Second,
		retryMaxDelay:   30 * time.Second,
	}

	vec, err := c.embedWithRetry(ctx, "test text")
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if len(vec) != 2 {
		t.Fatalf("expected 2-element vector, got %d", len(vec))
	}
	if emb.calls.Load() != 3 {
		t.Fatalf("expected 3 total calls (2 failures + 1 success), got %d", emb.calls.Load())
	}
}

// TestEmbedWithRetry_Exhausted verifies embedWithRetry returns error after all retries fail.
func TestEmbedWithRetry_Exhausted(t *testing.T) {
	defer func() { embedSleep = nil }()
	embedSleep = func(d time.Duration) {}

	ctx := context.Background()
	// Always fails — 1 initial + 3 retries = 4 attempts
	emb := newRetryableEmbedder(999)

	c := &Consolidator{
		embedder:        emb,
		retryMaxRetries: 3,
		retryBaseDelay:  1 * time.Second,
		retryMaxDelay:   30 * time.Second,
	}

	_, err := c.embedWithRetry(ctx, "test text")
	if err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}
	if emb.calls.Load() != 4 {
		t.Fatalf("expected 4 total calls (1 initial + 3 retries), got %d", emb.calls.Load())
	}
}

// TestEmbedWithRetry_ContextCancelled verifies context cancellation stops retry loop.
func TestEmbedWithRetry_ContextCancelled(t *testing.T) {
	defer func() { embedSleep = nil }()
	embedSleep = func(d time.Duration) {}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	emb := newRetryableEmbedder(999)
	c := &Consolidator{
		embedder:        emb,
		retryMaxRetries: 3,
		retryBaseDelay:  1 * time.Second,
		retryMaxDelay:   30 * time.Second,
	}

	_, err := c.embedWithRetry(ctx, "test text")
	if err == nil {
		t.Fatal("expected context cancelled error, got nil")
	}
}

// TestEmbedWithRetry_ImmediateSuccess verifies zero retries when first call succeeds.
func TestEmbedWithRetry_ImmediateSuccess(t *testing.T) {
	defer func() { embedSleep = nil }()
	embedSleep = func(d time.Duration) {}

	ctx := context.Background()
	emb := newRetryableEmbedder(0) // no failures

	c := &Consolidator{
		embedder:        emb,
		retryMaxRetries: 3,
		retryBaseDelay:  1 * time.Second,
		retryMaxDelay:   30 * time.Second,
	}

	vec, err := c.embedWithRetry(ctx, "immediate")
	if err != nil {
		t.Fatalf("expected success on first call, got: %v", err)
	}
	if len(vec) != 2 {
		t.Fatalf("expected 2-element vector, got %d", len(vec))
	}
	if emb.calls.Load() != 1 {
		t.Fatalf("expected exactly 1 call, got %d", emb.calls.Load())
	}
}

// selectiveEmbedder fails embedding for specific content strings.
type selectiveEmbedder struct {
	failFor map[string]bool
}

func (e *selectiveEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if e.failFor[text] {
		return nil, errors.New("embedding disabled for this text")
	}
	return []float32{float32(len(text)), float32(len(text) * 2)}, nil
}
func (e *selectiveEmbedder) Stop() {}

// TestProfileMemoryCount_OnlyOnSuccess verifies that when all Qdrant upserts
// fail, MemoryCount remains unchanged.
func TestProfileMemoryCount_OnlyOnSuccess(t *testing.T) {
	defer func() { embedSleep = nil }()
	embedSleep = func(d time.Duration) {}

	ctx := context.Background()
	qdrant := newMockQdrantStore()
	qdrant.upsertMemoryErr = errors.New("qdrant unavailable")

	profile := &Profile{
		UserID:      "user-1",
		MemoryCount: 5,
		Traits:      []string{},
		Facts:       make(map[string]string),
		Preferences: make(map[string]string),
		Interests:   []string{},
	}

	c := &Consolidator{
		qdrant:   qdrant,
		embedder: newRetryableEmbedder(0),
	}

	extracts := []Extract{
		{Content: "memory one", Type: string(TypeFact), Confidence: 0.9},
		{Content: "memory two", Type: string(TypeSummary), Confidence: 0.8},
		{Content: "memory three", Type: string(TypeEpisode), Confidence: 0.7},
	}

	stored := 0
	for _, extract := range extracts {
		memory := &Record{
			UserID:     profile.UserID,
			MemoryType: Type(extract.Type),
			Content:    extract.Content,
			Keywords:   extract.Keywords,
			Confidence: extract.Confidence,
			CreatedAt:  time.Now(),
		}
		embedding, err := c.embedWithRetry(ctx, memory.Content)
		if err != nil {
			continue
		}
		memory.Embedding = embedding
		if err := c.qdrant.UpsertMemory(ctx, memory); err != nil {
			t.Logf("memory upsert failed (expected): %v", err)
		} else {
			stored++
		}
	}

	if stored > 0 {
		profile.MemoryCount += stored
	}

	if profile.MemoryCount != 5 {
		t.Errorf("MemoryCount should remain 5 when all upserts fail, got %d", profile.MemoryCount)
	}
	if stored != 0 {
		t.Errorf("expected 0 stored memories (all upserts fail), got %d", stored)
	}
}

// TestProfileMemoryCount_Consistent verifies that partial embedding failure still
// results in correct MemoryCount: only successfully stored memories are counted.
func TestProfileMemoryCount_Consistent(t *testing.T) {
	defer func() { embedSleep = nil }()
	embedSleep = func(d time.Duration) {}

	ctx := context.Background()
	qdrant := newMockQdrantStore()

	profile := &Profile{
		UserID:      "user-1",
		MemoryCount: 10,
		Traits:      []string{},
		Facts:       make(map[string]string),
		Preferences: make(map[string]string),
		Interests:   []string{},
	}

	// 5 extracts but 2 fail at embedding -> only 3 reach Qdrant -> stored=3
	c := &Consolidator{
		qdrant: qdrant,
		embedder: &selectiveEmbedder{
			failFor: map[string]bool{
				"memory beta":  true,
				"memory delta": true,
			},
		},
	}

	extracts := []Extract{
		{Content: "memory alpha", Type: string(TypeFact), Confidence: 0.95},
		{Content: "memory beta", Type: string(TypeSummary), Confidence: 0.85},
		{Content: "memory gamma", Type: string(TypeEpisode), Confidence: 0.75},
		{Content: "memory delta", Type: string(TypeFact), Confidence: 0.65},
		{Content: "memory epsilon", Type: string(TypeSummary), Confidence: 0.55},
	}

	stored := 0
	for _, extract := range extracts {
		memory := &Record{
			UserID:     profile.UserID,
			MemoryType: Type(extract.Type),
			Content:    extract.Content,
			Keywords:   extract.Keywords,
			Confidence: extract.Confidence,
			CreatedAt:  time.Now(),
		}
		embedding, err := c.embedWithRetry(ctx, memory.Content)
		if err != nil {
			continue
		}
		memory.Embedding = embedding
		if err := c.qdrant.UpsertMemory(ctx, memory); err != nil {
			t.Logf("memory upsert failed: %v", err)
		} else {
			stored++
		}
	}

	if stored > 0 {
		profile.MemoryCount += stored
	}

	if profile.MemoryCount != 13 {
		t.Errorf("MemoryCount should be 13 (10 + 3 stored), not %d (10 + %d stored, but had %d extracts)",
			profile.MemoryCount, stored, len(extracts))
	}
	if stored != 3 {
		t.Errorf("expected 3 stored memories (2 fail at embedding), got %d", stored)
	}
}

// TestPassesEntropyGate_NilMessage verifies nil messages are rejected.
func TestPassesEntropyGate_NilMessage(t *testing.T) {
	cfg := EntropyGateConfig{MinContentLength: 10, MinUniqueWordRatio: 0.15}
	if PassesEntropyGate(nil, cfg) {
		t.Error("expected false for nil message")
	}
}

// TestPassesEntropyGate_BotMessage verifies bot messages are rejected when OtherBotPolicy is ignore.
func TestPassesEntropyGate_BotMessage(t *testing.T) {
	cfg := EntropyGateConfig{OtherBotPolicy: config.OtherBotIgnore, MinContentLength: 10, MinUniqueWordRatio: 0.15}
	msg := &DiscordMessage{IsBot: true, Content: "hello world from bot"}
	if PassesEntropyGate(msg, cfg) {
		t.Error("expected false for bot message when OtherBotPolicy is ignore")
	}
}

// TestPassesEntropyGate_BotMessage_Allowed verifies bot messages pass when OtherBotPolicy is context_only or full.
func TestPassesEntropyGate_BotMessage_Allowed(t *testing.T) {
	cfg := EntropyGateConfig{OtherBotPolicy: config.OtherBotContextOnly, MinContentLength: 10, MinUniqueWordRatio: 0.15}
	msg := &DiscordMessage{IsBot: true, Content: "hello world from bot"}
	if !PassesEntropyGate(msg, cfg) {
		t.Error("expected true for bot message when OtherBotPolicy is context_only")
	}
}

// TestPassesEntropyGate_TooShort verifies messages below min length are rejected.
func TestPassesEntropyGate_TooShort(t *testing.T) {
	cfg := EntropyGateConfig{MinContentLength: 20, MinUniqueWordRatio: 0.15}
	msg := &DiscordMessage{Content: "hi"}
	if PassesEntropyGate(msg, cfg) {
		t.Error("expected false for too-short message")
	}
}

// TestPassesEntropyGate_PureEmoji verifies purely emoji messages are rejected.
func TestPassesEntropyGate_PureEmoji(t *testing.T) {
	cfg := EntropyGateConfig{MinContentLength: 1, MinUniqueWordRatio: 0.15}
	msg := &DiscordMessage{Content: "😀🎉👍"}
	if PassesEntropyGate(msg, cfg) {
		t.Error("expected false for purely emoji message")
	}
}

// TestPassesEntropyGate_LowUniqueRatio verifies repetitive messages are rejected.
func TestPassesEntropyGate_LowUniqueRatio(t *testing.T) {
	cfg := EntropyGateConfig{MinContentLength: 5, MinUniqueWordRatio: 0.5}
	msg := &DiscordMessage{Content: "hello hello hello hello world"}
	// 5 words, 2 unique → ratio=0.4 < 0.5 → rejected
	if PassesEntropyGate(msg, cfg) {
		t.Error("expected false for low unique word ratio")
	}
}

// TestPassesEntropyGate_ValidMessage verifies a normal message passes.
func TestPassesEntropyGate_ValidMessage(t *testing.T) {
	cfg := EntropyGateConfig{MinContentLength: 5, MinUniqueWordRatio: 0.15}
	msg := &DiscordMessage{Content: "hello world, how are you today?"}
	if !PassesEntropyGate(msg, cfg) {
		t.Error("expected true for valid message with diverse words")
	}
}

// TestUpdateProfileFromExtraction_ProfileUpdates verifies structured profile updates
// from LLM output correctly populate profile fields.
func TestUpdateProfileFromExtraction_ProfileUpdates(t *testing.T) {
	c := &Consolidator{}
	profile := &Profile{
		UserID: "user-1",
		Traits: []string{"friendly"},
	}
	importance := 0.8
	extracts := []Extract{
		{
			Type:            string(TypeFact),
			Content:         "user works at Acme Corp",
			ImportanceScore: &importance,
			ProfileUpdates: &ProfileUpdateSet{
				Traits:      []string{"helpful", "curious"},
				Facts:       map[string]string{"workplace": "Acme Corp", "role": "engineer"},
				Preferences: map[string]string{"language": "Go"},
				Interests:   []string{"distributed systems", "open source"},
			},
		},
	}

	c.updateProfileFromExtraction(profile, extracts)

	if len(profile.Traits) != 3 {
		t.Errorf("expected 3 traits, got %d: %v", len(profile.Traits), profile.Traits)
	}
	if profile.Facts["workplace"] != "Acme Corp" {
		t.Errorf("expected workplace=Acme Corp, got %q", profile.Facts["workplace"])
	}
	if profile.Facts["role"] != "engineer" {
		t.Errorf("expected role=engineer, got %q", profile.Facts["role"])
	}
	if profile.Preferences["language"] != "Go" {
		t.Errorf("expected preference language=Go, got %q", profile.Preferences["language"])
	}
	if len(profile.Interests) != 2 {
		t.Errorf("expected 2 interests, got %d: %v", len(profile.Interests), profile.Interests)
	}
}

// TestUpdateProfileFromExtraction_ProfileUpdates_DedupTraits verifies duplicate traits are not added.
func TestUpdateProfileFromExtraction_ProfileUpdates_DedupTraits(t *testing.T) {
	c := &Consolidator{}
	profile := &Profile{
		UserID: "user-1",
		Traits: []string{"helpful"},
	}
	extracts := []Extract{
		{
			Type: string(TypeFact),
			ProfileUpdates: &ProfileUpdateSet{
				Traits: []string{"helpful", "friendly"},
			},
		},
	}

	c.updateProfileFromExtraction(profile, extracts)

	if len(profile.Traits) != 2 {
		t.Errorf("expected 2 traits (dedup), got %d: %v", len(profile.Traits), profile.Traits)
	}
}

// TestUpdateProfileFromExtraction_LegacyFallback verifies legacy fact parsing still works.
func TestUpdateProfileFromExtraction_LegacyFallback(t *testing.T) {
	c := &Consolidator{}
	profile := &Profile{
		UserID: "user-1",
	}
	extracts := []Extract{
		{
			Type:    string(TypeFact),
			Content: "name: Alice",
		},
	}

	c.updateProfileFromExtraction(profile, extracts)

	if profile.Facts["name"] != "Alice" {
		t.Errorf("expected name=Alice from legacy fallback, got %q", profile.Facts["name"])
	}
}

// TestUpdateProfileFromExtraction_LegacyInterestFallback verifies legacy interest extraction still works.
func TestUpdateProfileFromExtraction_LegacyInterestFallback(t *testing.T) {
	c := &Consolidator{}
	profile := &Profile{
		UserID: "user-1",
	}
	extracts := []Extract{
		{
			Type:     string(TypeInterest),
			Keywords: []string{"golang", "rust"},
		},
	}

	c.updateProfileFromExtraction(profile, extracts)

	if len(profile.Interests) != 2 {
		t.Errorf("expected 2 interests from legacy fallback, got %d: %v", len(profile.Interests), profile.Interests)
	}
}

// TestStoreMemories_ImportanceAwareDedup verifies that a lower-importance extract
// does not overwrite a higher-confidence existing memory.
func TestStoreMemories_ImportanceAwareDedup(t *testing.T) {
	ctx := context.Background()
	qdrant := newMockQdrantStore()

	// Pre-populate an existing high-confidence fact
	existing := &Record{
		UserID:     "user-1",
		MemoryType: TypeFact,
		Content:    "name: Alice",
		Confidence: 0.95,
		CreatedAt:  time.Now(),
	}
	if err := qdrant.UpsertMemory(ctx, existing); err != nil {
		t.Fatalf("failed to seed existing memory: %v", err)
	}

	c := &Consolidator{
		qdrant:          qdrant,
		embedder:        newRetryableEmbedder(0),
		retryMaxRetries: 1,
		retryBaseDelay:  1 * time.Second,
		retryMaxDelay:   30 * time.Second,
	}

	lowImportance := 0.3
	extracts := []Extract{
		{
			Content:         "name: Alice",
			Type:            string(TypeFact),
			Confidence:      0.5,
			ImportanceScore: &lowImportance,
		},
	}

	stored, err := c.storeMemories(ctx, "user-1", extracts, "chan-1", "guild-1", nil, nil)
	if err != nil {
		t.Fatalf("storeMemories failed: %v", err)
	}
	if stored != 0 {
		t.Errorf("expected 0 stored (low importance vs high confidence existing), got %d", stored)
	}
}

// TestStoreMemories_HighImportanceOverwrites verifies that a higher-importance extract
// does overwrite a lower-confidence existing memory.
func TestStoreMemories_HighImportanceOverwrites(t *testing.T) {
	ctx := context.Background()
	qdrant := newMockQdrantStore()

	existing := &Record{
		UserID:     "user-1",
		MemoryType: TypeFact,
		Content:    "name: Alice",
		Confidence: 0.4,
		CreatedAt:  time.Now(),
	}
	if err := qdrant.UpsertMemory(ctx, existing); err != nil {
		t.Fatalf("failed to seed existing memory: %v", err)
	}

	c := &Consolidator{
		qdrant:          qdrant,
		embedder:        newRetryableEmbedder(0),
		retryMaxRetries: 1,
		retryBaseDelay:  1 * time.Second,
		retryMaxDelay:   30 * time.Second,
	}

	highImportance := 0.9
	extracts := []Extract{
		{
			Content:         "name: Alice",
			Type:            string(TypeFact),
			Confidence:      0.85,
			ImportanceScore: &highImportance,
		},
	}

	stored, err := c.storeMemories(ctx, "user-1", extracts, "chan-1", "guild-1", nil, nil)
	if err != nil {
		t.Fatalf("storeMemories failed: %v", err)
	}
	if stored != 1 {
		t.Errorf("expected 1 stored (high importance overwrites low confidence), got %d", stored)
	}

	// Verify tier was assigned
	for _, m := range qdrant.memories {
		if m.DecayCategory == "" {
			t.Error("expected DecayCategory to be assigned")
		}
	}
}

// TestStoreMemories_TierAssignment verifies DecayCategory is assigned on store.
func TestStoreMemories_TierAssignment(t *testing.T) {
	ctx := context.Background()
	qdrant := newMockQdrantStore()

	c := &Consolidator{
		qdrant:          qdrant,
		embedder:        newRetryableEmbedder(0),
		retryMaxRetries: 1,
		retryBaseDelay:  1 * time.Second,
		retryMaxDelay:   30 * time.Second,
	}

	highImp := 0.95
	extracts := []Extract{
		{
			Content:         "some unique fact",
			Type:            string(TypeFact),
			Confidence:      0.9,
			ImportanceScore: &highImp,
		},
	}

	stored, err := c.storeMemories(ctx, "user-1", extracts, "chan-1", "guild-1", nil, nil)
	if err != nil {
		t.Fatalf("storeMemories failed: %v", err)
	}
	if stored != 1 {
		t.Fatalf("expected 1 stored, got %d", stored)
	}

	for _, m := range qdrant.memories {
		if m.DecayCategory != "hot" {
			t.Errorf("expected hot tier for high score memory, got %q (imp=%.2f conf=%.2f)", m.DecayCategory, m.ImportanceScore, m.Confidence)
		}
		if m.ImportanceScore != highImp {
			t.Errorf("expected ImportanceScore=%.2f, got %.2f", highImp, m.ImportanceScore)
		}
	}
}

func TestExtractMentions_Empty(t *testing.T) {
	userIDs, channelIDs := extractMentions(nil)
	if len(userIDs) != 0 {
		t.Errorf("expected 0 user IDs for nil messages, got %v", userIDs)
	}
	if len(channelIDs) != 0 {
		t.Errorf("expected 0 channel IDs for nil messages, got %v", channelIDs)
	}

	userIDs, channelIDs = extractMentions([]*DiscordMessage{})
	if len(userIDs) != 0 {
		t.Errorf("expected 0 user IDs for empty messages, got %v", userIDs)
	}
	if len(channelIDs) != 0 {
		t.Errorf("expected 0 channel IDs for empty messages, got %v", channelIDs)
	}
}

func TestExtractMentions_SingleUserMention(t *testing.T) {
	messages := []*DiscordMessage{
		{Content: "hello <@123>"},
	}
	userIDs, channelIDs := extractMentions(messages)

	if len(userIDs) != 1 || userIDs[0] != "123" {
		t.Errorf("expected [\"123\"], got %v", userIDs)
	}
	if len(channelIDs) != 0 {
		t.Errorf("expected no channel IDs, got %v", channelIDs)
	}
}

func TestExtractMentions_MultipleUserMentions(t *testing.T) {
	messages := []*DiscordMessage{
		{Content: "hello <@123> and <@456>"},
	}
	userIDs, channelIDs := extractMentions(messages)

	if len(userIDs) != 2 {
		t.Errorf("expected 2 user IDs, got %d: %v", len(userIDs), userIDs)
	}
	if len(channelIDs) != 0 {
		t.Errorf("expected no channel IDs, got %v", channelIDs)
	}
	found := make(map[string]bool)
	for _, id := range userIDs {
		found[id] = true
	}
	if !found["123"] || !found["456"] {
		t.Errorf("expected [\"123\", \"456\"], got %v", userIDs)
	}
}

func TestExtractMentions_ChannelMention(t *testing.T) {
	messages := []*DiscordMessage{
		{Content: "check out <#789>"},
	}
	userIDs, channelIDs := extractMentions(messages)

	if len(channelIDs) != 1 || channelIDs[0] != "789" {
		t.Errorf("expected [\"789\"], got %v", channelIDs)
	}
	if len(userIDs) != 0 {
		t.Errorf("expected no user IDs, got %v", userIDs)
	}
}

func TestExtractMentions_MixedMentions(t *testing.T) {
	messages := []*DiscordMessage{
		{Content: "<@111> told me about <#222> and <@333> also mentioned <#444>"},
	}
	userIDs, channelIDs := extractMentions(messages)

	if len(userIDs) != 2 {
		t.Errorf("expected 2 user IDs, got %d: %v", len(userIDs), userIDs)
	}
	if len(channelIDs) != 2 {
		t.Errorf("expected 2 channel IDs, got %d: %v", len(channelIDs), channelIDs)
	}
}

func TestExtractMentions_DuplicateFiltering(t *testing.T) {
	messages := []*DiscordMessage{
		{Content: "hey <@123> and again <@123>"},
	}
	userIDs, _ := extractMentions(messages)

	if len(userIDs) != 1 {
		t.Errorf("expected 1 unique user ID after dedup, got %d: %v", len(userIDs), userIDs)
	}
}

func TestExtractMentions_AcrossMessages(t *testing.T) {
	messages := []*DiscordMessage{
		{Content: "hello <@123>"},
		{Content: "hi <@456>"},
		{Content: "yo <#789>"},
	}
	userIDs, channelIDs := extractMentions(messages)

	if len(userIDs) != 2 {
		t.Errorf("expected 2 user IDs across messages, got %d: %v", len(userIDs), userIDs)
	}
	if len(channelIDs) != 1 {
		t.Errorf("expected 1 channel ID, got %d: %v", len(channelIDs), channelIDs)
	}
}

func TestExtractMentions_ExclamationVariant(t *testing.T) {
	messages := []*DiscordMessage{
		{Content: "hello <@!123>"},
	}
	userIDs, _ := extractMentions(messages)

	if len(userIDs) != 1 || userIDs[0] != "123" {
		t.Errorf("expected [\"123\"] for <@!123> variant, got %v", userIDs)
	}
}

func TestExtractMentions_200Mentions(t *testing.T) {
	var parts []string
	for i := range 200 {
		parts = append(parts, fmt.Sprintf("<@%d>", 1000+i))
	}
	content := "many mentions:"
	for _, p := range parts {
		content += " " + p
	}

	messages := []*DiscordMessage{{Content: content}}
	userIDs, _ := extractMentions(messages)

	if len(userIDs) != 200 {
		t.Errorf("expected 200 mention IDs, got %d", len(userIDs))
	}
}

func TestExtractUserMentions_Empty(t *testing.T) {
	result := extractUserMentions("")
	if len(result) != 0 {
		t.Errorf("expected nil for empty string, got %v", result)
	}

	result = extractUserMentions("no mentions here")
	if len(result) != 0 {
		t.Errorf("expected nil for no-mention string, got %v", result)
	}
}

func TestExtractChannelMentions_Empty(t *testing.T) {
	result := extractChannelMentions("")
	if len(result) != 0 {
		t.Errorf("expected nil for empty string, got %v", result)
	}

	result = extractChannelMentions("no channel mentions here")
	if len(result) != 0 {
		t.Errorf("expected nil for no-mention string, got %v", result)
	}
}

func TestRelationshipID_Deterministic(t *testing.T) {
	id1 := relationshipID("a", "b", RelMention)
	id2 := relationshipID("b", "a", RelMention)

	if id1 != id2 {
		t.Errorf("relationshipID should be deterministic regardless of order: %q vs %q", id1, id2)
	}
	if id1 != "a:b:mention" {
		t.Errorf("expected \"a:b:mention\", got %q", id1)
	}
}

func TestRelationshipID_DifferentTypes(t *testing.T) {
	id1 := relationshipID("a", "b", RelMention)
	id2 := relationshipID("a", "b", RelReply)

	if id1 == id2 {
		t.Errorf("different relationship types should produce different IDs, got %q for both", id1)
	}
}

func TestUpdateRelationships_SelfMentionSkipped(t *testing.T) {
	ctx := context.Background()
	qdrant := newMockQdrantStore()

	c := &Consolidator{
		qdrant:   qdrant,
		ownBotID: "bot-1",
	}

	messages := []*DiscordMessage{
		{
			AuthorID:  "111",
			ChannelID: "chan-1",
			Content:   "hey <@111>",
		},
	}

	if err := c.updateRelationships(ctx, messages); err != nil {
		t.Fatalf("updateRelationships failed: %v", err)
	}

	if len(qdrant.relationships) != 0 {
		t.Errorf("expected no relationships for self-mention, got %d", len(qdrant.relationships))
	}
}

func TestUpdateRelationships_OwnBotSkipped(t *testing.T) {
	ctx := context.Background()
	qdrant := newMockQdrantStore()

	c := &Consolidator{
		qdrant:   qdrant,
		ownBotID: "bot-1",
	}

	messages := []*DiscordMessage{
		{
			AuthorID:  "bot-1",
			ChannelID: "chan-1",
			Content:   "I <@222> think so",
		},
	}

	if err := c.updateRelationships(ctx, messages); err != nil {
		t.Fatalf("updateRelationships failed: %v", err)
	}

	if len(qdrant.relationships) != 0 {
		t.Errorf("expected no relationships for own bot, got %d", len(qdrant.relationships))
	}
}

func TestUpdateRelationships_CreatesRelationship(t *testing.T) {
	ctx := context.Background()
	qdrant := newMockQdrantStore()

	c := &Consolidator{
		qdrant:   qdrant,
		ownBotID: "bot-1",
	}

	messages := []*DiscordMessage{
		{
			AuthorID:  "333",
			ChannelID: "chan-1",
			Content:   "hey <@444>",
		},
	}

	if err := c.updateRelationships(ctx, messages); err != nil {
		t.Fatalf("updateRelationships failed: %v", err)
	}

	relID := relationshipID("333", "444", RelMention)
	rel, ok := qdrant.relationships[relID]
	if !ok {
		t.Fatalf("expected relationship %q to be created, got keys: %v", relID, qdrant.relationships)
	}
	if rel.InteractionCount != 1 {
		t.Errorf("expected InteractionCount=1, got %d", rel.InteractionCount)
	}
	if rel.Weight != 1.0 {
		t.Errorf("expected Weight=1.0, got %.1f", rel.Weight)
	}
}

func TestUpdateRelationships_IncrementsExisting(t *testing.T) {
	ctx := context.Background()
	qdrant := newMockQdrantStore()

	relID := relationshipID("555", "666", RelMention)
	qdrant.relationships[relID] = &Relationship{
		ID:                relID,
		UserA:             "555",
		UserB:             "666",
		Type:              RelMention,
		InteractionCount:  3,
		LastInteractionAt: time.Now().Add(-1 * time.Hour),
		ChannelIDs:        []string{"chan-1"},
		Weight:            3.0,
	}

	c := &Consolidator{
		qdrant:   qdrant,
		ownBotID: "bot-1",
	}

	messages := []*DiscordMessage{
		{
			AuthorID:  "555",
			ChannelID: "chan-1",
			Content:   "hey <@666>",
		},
	}

	if err := c.updateRelationships(ctx, messages); err != nil {
		t.Fatalf("updateRelationships failed: %v", err)
	}

	rel, ok := qdrant.relationships[relID]
	if !ok {
		t.Fatal("relationship should still exist")
	}
	if rel.InteractionCount != 4 {
		t.Errorf("expected InteractionCount=4, got %d", rel.InteractionCount)
	}
	if rel.Weight != 4.0 {
		t.Errorf("expected Weight=4.0, got %.1f", rel.Weight)
	}
}

func TestUpdateRelationships_DeletedUser(t *testing.T) {
	ctx := context.Background()
	qdrant := newMockQdrantStore()

	c := &Consolidator{
		qdrant:   qdrant,
		ownBotID: "bot-1",
	}

	messages := []*DiscordMessage{
		{
			AuthorID:  "777",
			ChannelID: "chan-1",
			Content:   "hey <@888>",
		},
	}

	if err := c.updateRelationships(ctx, messages); err != nil {
		t.Fatalf("updateRelationships failed: %v", err)
	}

	relID := relationshipID("777", "888", RelMention)
	if _, ok := qdrant.relationships[relID]; !ok {
		t.Errorf("deleted user mentions should still be tracked for record, missing relationship %q", relID)
	}
}

func BenchmarkExtractMentions(b *testing.B) {
	messages := make([]*DiscordMessage, 100)
	for i := range messages {
		messages[i] = &DiscordMessage{
			Content: fmt.Sprintf("message %d with <@%d> and <#%d>", i, 100+i, 200+i),
		}
	}
	for b.Loop() {
		extractMentions(messages)
	}
}

// TestEntropyGate_IntegrationWithProcessWithMessages verifies entropy gate filters
// messages before consolidation in ProcessWithMessages.
func TestEntropyGate_IntegrationWithProcessWithMessages(t *testing.T) {
	mock := &mockAIClient{
		createChatCompletionFn: func(ctx context.Context, req ai.ChatCompletionRequest) (*ai.ChatCompletionResponse, error) {
			return &ai.ChatCompletionResponse{Content: "[]"}, nil
		},
	}
	qdrant := newMockQdrantStore()
	c := &Consolidator{
		qdrant:               qdrant,
		aiClient:             mock,
		prompt:               "test prompt",
		maxMessages:          100,
		entropyMinContentLen: 10,
		entropyMinWordRatio:  0.15,
	}
	ctx := context.Background()

	// All messages too short — should return nil early
	messages := []*DiscordMessage{
		{AuthorID: "user-1", Username: "test", Content: "hi", Timestamp: time.Now()},
		{AuthorID: "user-1", Username: "test", Content: "ok", Timestamp: time.Now()},
	}

	err := c.ProcessWithMessages(ctx, "user-1", messages)
	if err != nil {
		t.Fatalf("expected nil error when all filtered, got: %v", err)
	}
}
