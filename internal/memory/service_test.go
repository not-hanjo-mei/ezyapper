package memory

import (
	"context"
	"strings"
	"sync"
	"testing"

	"ezyapper/internal/config"
)

type svcEmbedder struct {
	vectors map[string][]float32
	err     error
}

func (m *svcEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if m.err != nil {
		return nil, m.err
	}
	if v, ok := m.vectors[text]; ok {
		return v, nil
	}
	return []float32{1.0, 2.0, 3.0}, nil
}

func TestNewService_Valid(t *testing.T) {
	emb := &svcEmbedder{}
	cfg := &ServiceConfig{
		ConsolidationInterval: 10,
		ShortTermLimit:        50,
		TopK:                  5,
		MinScore:              0.5,
		Consolidation: &config.ConsolidationConfig{
			Model:        "gpt-4",
			SystemPrompt: "test",
		},
		WorkerQueueSize: 100,
	}
	svc, err := NewService(cfg, &QdrantClient{}, emb, nil, nil)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	svc.Close()
}

func TestNewService_NilQdrant(t *testing.T) {
	cfg := &ServiceConfig{
		ConsolidationInterval: 10,
		ShortTermLimit:        50,
		TopK:                  5,
		MinScore:              0.5,
		Consolidation:         &config.ConsolidationConfig{Model: "gpt-4", SystemPrompt: "test"},
		WorkerQueueSize:       100,
	}
	_, err := NewService(cfg, nil, &svcEmbedder{}, nil, nil)
	if err == nil {
		t.Fatal("expected error for nil qdrant")
	}
	if !strings.Contains(err.Error(), "qdrant client is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewService_NilEmbedder(t *testing.T) {
	cfg := &ServiceConfig{
		ConsolidationInterval: 10,
		ShortTermLimit:        50,
		TopK:                  5,
		MinScore:              0.5,
		Consolidation:         &config.ConsolidationConfig{Model: "gpt-4", SystemPrompt: "test"},
		WorkerQueueSize:       100,
	}
	_, err := NewService(cfg, &QdrantClient{}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for nil embedder")
	}
}

func TestNewService_InvalidConsolidationInterval(t *testing.T) {
	cfg := &ServiceConfig{
		ConsolidationInterval: 0,
		ShortTermLimit:        50,
		TopK:                  5,
		MinScore:              0.5,
		Consolidation:         &config.ConsolidationConfig{Model: "gpt-4", SystemPrompt: "test"},
		WorkerQueueSize:       100,
	}
	_, err := NewService(cfg, &QdrantClient{}, &svcEmbedder{}, nil, nil)
	if err == nil {
		t.Fatal("expected error for invalid consolidation interval")
	}
}

func TestNewService_InvalidShortTermLimit(t *testing.T) {
	cfg := &ServiceConfig{
		ConsolidationInterval: 10,
		ShortTermLimit:        0,
		TopK:                  5,
		MinScore:              0.5,
		Consolidation:         &config.ConsolidationConfig{Model: "gpt-4", SystemPrompt: "test"},
		WorkerQueueSize:       100,
	}
	_, err := NewService(cfg, &QdrantClient{}, &svcEmbedder{}, nil, nil)
	if err == nil {
		t.Fatal("expected error for invalid short term limit")
	}
}

func TestNewService_InvalidTopK(t *testing.T) {
	cfg := &ServiceConfig{
		ConsolidationInterval: 10,
		ShortTermLimit:        50,
		TopK:                  -1,
		MinScore:              0.5,
		Consolidation:         &config.ConsolidationConfig{Model: "gpt-4", SystemPrompt: "test"},
		WorkerQueueSize:       100,
	}
	_, err := NewService(cfg, &QdrantClient{}, &svcEmbedder{}, nil, nil)
	if err == nil {
		t.Fatal("expected error for negative top_k")
	}
}

func TestNewService_InvalidMinScore(t *testing.T) {
	cfg := &ServiceConfig{
		ConsolidationInterval: 10,
		ShortTermLimit:        50,
		TopK:                  5,
		MinScore:              2.0,
		Consolidation:         &config.ConsolidationConfig{Model: "gpt-4", SystemPrompt: "test"},
		WorkerQueueSize:       100,
	}
	_, err := NewService(cfg, &QdrantClient{}, &svcEmbedder{}, nil, nil)
	if err == nil {
		t.Fatal("expected error for min_score > 1")
	}
}

func TestNewService_NilConsolidationConfig(t *testing.T) {
	cfg := &ServiceConfig{
		ConsolidationInterval: 10,
		ShortTermLimit:        50,
		TopK:                  5,
		MinScore:              0.5,
		Consolidation:         nil,
		WorkerQueueSize:       100,
	}
	_, err := NewService(cfg, &QdrantClient{}, &svcEmbedder{}, nil, nil)
	if err == nil {
		t.Fatal("expected error for nil consolidation config")
	}
}

func TestFilterByKeywords_NilKeywords(t *testing.T) {
	s := &MemoryService{}
	memories := []*Record{
		{Content: "hello world", Keywords: []string{"greeting"}},
	}
	result := s.filterByKeywords(memories, nil)
	if len(result) != 0 {
		t.Fatalf("expected 0 results for nil keywords, got %d", len(result))
	}
}

func TestFilterByKeywords_EmptyKeywords(t *testing.T) {
	s := &MemoryService{}
	memories := []*Record{
		{Content: "hello world", Keywords: []string{"greeting"}},
	}
	result := s.filterByKeywords(memories, []string{})
	if len(result) != 0 {
		t.Fatalf("expected 0 results for empty keywords, got %d", len(result))
	}
}

func TestFilterByKeywords_Match(t *testing.T) {
	s := &MemoryService{}
	memories := []*Record{
		{Content: "hello world", Keywords: []string{"greeting"}},
		{Content: "pizza recipe", Keywords: []string{"food"}},
		{Content: "car maintenance", Keywords: []string{"vehicle"}},
	}
	result := s.filterByKeywords(memories, []string{"food"})
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if result[0].Content != "pizza recipe" {
		t.Fatalf("expected 'pizza recipe', got %q", result[0].Content)
	}
}

func TestFilterByKeywords_ContentMatch(t *testing.T) {
	s := &MemoryService{}
	memories := []*Record{
		{Content: "I love pizza", Keywords: []string{}},
	}
	result := s.filterByKeywords(memories, []string{"pizza"})
	if len(result) != 1 {
		t.Fatalf("expected 1 result for content match, got %d", len(result))
	}
}

func TestShouldConsolidate_NoCounter(t *testing.T) {
	s := &MemoryService{
		messageCounters:       make(map[string]int),
		consolidationInterval: 10,
	}
	if s.ShouldConsolidate("user-1") {
		t.Fatal("expected false for user with no counter")
	}
}

func TestShouldConsolidate_BelowThreshold(t *testing.T) {
	s := &MemoryService{
		messageCounters:       map[string]int{"user-1": 5},
		consolidationInterval: 10,
	}
	if s.ShouldConsolidate("user-1") {
		t.Fatal("expected false when below threshold")
	}
}

func TestShouldConsolidate_AtThreshold(t *testing.T) {
	s := &MemoryService{
		messageCounters:       map[string]int{"user-1": 10},
		consolidationInterval: 10,
	}
	if !s.ShouldConsolidate("user-1") {
		t.Fatal("expected true when at threshold")
	}
}

func TestResetMessageCount(t *testing.T) {
	s := &MemoryService{
		messageCounters:       map[string]int{"user-1": 15},
		consolidationInterval: 10,
	}
	s.ResetMessageCount("user-1")
	if s.messageCounters["user-1"] != 0 {
		t.Fatalf("expected 0 after reset, got %d", s.messageCounters["user-1"])
	}
}

func TestResetChannelMessageCount(t *testing.T) {
	s := &MemoryService{
		channelCounters:       map[string]int{"ch-1": 5},
		consolidationInterval: 10,
	}
	s.ResetChannelMessageCount("ch-1")
	if _, exists := s.channelCounters["ch-1"]; exists {
		t.Fatal("expected channel counter to be deleted")
	}
}

func TestConsumeChannelMessageCount_Positive(t *testing.T) {
	s := &MemoryService{
		channelCounters:       map[string]int{"ch-1": 10},
		consolidationInterval: 10,
	}
	remaining := s.ConsumeChannelMessageCount("ch-1", 3)
	if remaining != 7 {
		t.Fatalf("expected 7 remaining, got %d", remaining)
	}
}

func TestConsumeChannelMessageCount_Exhausted(t *testing.T) {
	s := &MemoryService{
		channelCounters:       map[string]int{"ch-1": 5},
		consolidationInterval: 10,
	}
	remaining := s.ConsumeChannelMessageCount("ch-1", 10)
	if remaining != 0 {
		t.Fatalf("expected 0 remaining, got %d", remaining)
	}
	if _, exists := s.channelCounters["ch-1"]; exists {
		t.Fatal("expected counter to be deleted when exhausted")
	}
}

func TestConsumeChannelMessageCount_Negative(t *testing.T) {
	s := &MemoryService{
		channelCounters:       map[string]int{"ch-1": 5},
		consolidationInterval: 10,
	}
	remaining := s.ConsumeChannelMessageCount("ch-1", -1)
	if remaining != 5 {
		t.Fatalf("expected 5 remaining for negative consumption, got %d", remaining)
	}
}

func TestStore_NoEmbedding(t *testing.T) {
	s := &MemoryService{}
	err := s.Store(context.Background(), &Record{})
	if err == nil {
		t.Fatal("expected error for missing embedding")
	}
	if !strings.Contains(err.Error(), "embedding is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServiceConcurrentCounters(t *testing.T) {
	s := &MemoryService{
		messageCounters:       make(map[string]int),
		channelCounters:       make(map[string]int),
		consolidationInterval: 10,
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.counterMu.Lock()
			s.messageCounters["user-1"]++
			s.counterMu.Unlock()
		}()
	}
	wg.Wait()

	s.counterMu.RLock()
	count := s.messageCounters["user-1"]
	s.counterMu.RUnlock()

	if count != 100 {
		t.Fatalf("expected 100 concurrent increments, got %d", count)
	}
}

func TestMemoryStore_Interface(t *testing.T) {
	var _ MemoryStore = (*MemoryService)(nil)
}

func TestProfileStore_Interface(t *testing.T) {
	var _ ProfileStore = (*MemoryService)(nil)
}

func TestService_Interface(t *testing.T) {
	var _ Service = (*MemoryService)(nil)
}
