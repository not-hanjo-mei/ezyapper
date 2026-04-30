package memory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ezyapper/internal/ai"
)

// mockAIClient implements aiChatCompleter for testing.
type mockAIClient struct {
	createChatCompletionFn func(context.Context, ai.ChatCompletionRequest) (*ai.ChatCompletionResponse, error)
}

func (m *mockAIClient) CreateChatCompletion(ctx context.Context, req ai.ChatCompletionRequest) (*ai.ChatCompletionResponse, error) {
	if m.createChatCompletionFn != nil {
		return m.createChatCompletionFn(ctx, req)
	}
	return &ai.ChatCompletionResponse{Content: "[]"}, nil
}

// TestAnalyzeConversation_AIError verifies that an LLM error is propagated
// as an error (not silently replaced with empty slice).
func TestAnalyzeConversation_AIError(t *testing.T) {
	mock := &mockAIClient{
		createChatCompletionFn: func(ctx context.Context, req ai.ChatCompletionRequest) (*ai.ChatCompletionResponse, error) {
			return nil, errors.New("LLM HTTP 500")
		},
	}
	c := &Consolidator{
		aiClient: mock,
		prompt:   "test prompt",
	}
	ctx := context.Background()
	_, err := c.analyzeConversation(ctx, "some conversation", []string{"user-1"})
	if err == nil {
		t.Fatal("expected error from LLM failure, got nil")
	}
	if !strings.Contains(err.Error(), "LLM") {
		t.Errorf("expected error message to reference LLM failure, got: %v", err)
	}
}

// TestAnalyzeConversationBatch_AIError verifies that an LLM error propagates
// in the batch path, not silently swallowed.
func TestAnalyzeConversationBatch_AIError(t *testing.T) {
	mock := &mockAIClient{
		createChatCompletionFn: func(ctx context.Context, req ai.ChatCompletionRequest) (*ai.ChatCompletionResponse, error) {
			return nil, errors.New("LLM batch HTTP 503")
		},
	}
	c := &Consolidator{
		aiClient: mock,
		prompt:   "test prompt",
	}
	ctx := context.Background()
	_, err := c.analyzeConversationBatch(ctx, "some conversation", []string{"user-1"})
	if err == nil {
		t.Fatal("expected error from LLM batch failure, got nil")
	}
	if !strings.Contains(err.Error(), "LLM") {
		t.Errorf("expected error message to reference LLM failure, got: %v", err)
	}
}

// TestAnalyzeConversation_ValidJSON_EmptyExtracts verifies that a valid
// empty JSON array "[]" from the LLM returns (nil extracts, nil error).
// This is NOT an error — the LLM found no memories to extract.
func TestAnalyzeConversation_ValidJSON_EmptyExtracts(t *testing.T) {
	mock := &mockAIClient{
		createChatCompletionFn: func(ctx context.Context, req ai.ChatCompletionRequest) (*ai.ChatCompletionResponse, error) {
			return &ai.ChatCompletionResponse{Content: "[]"}, nil
		},
	}
	c := &Consolidator{
		aiClient: mock,
		prompt:   "test prompt",
	}
	ctx := context.Background()
	extracts, err := c.analyzeConversation(ctx, "some conversation", []string{"user-1"})
	if err != nil {
		t.Fatalf("expected nil error for valid empty JSON, got: %v", err)
	}
	if len(extracts) != 0 {
		t.Errorf("expected 0 extracts for empty JSON array, got %d", len(extracts))
	}
}

// TestProcessWithMessages_AIError verifies that ProcessWithMessages
// propagates analyzeConversation errors upward instead of silently
// returning nil.
func TestProcessWithMessages_AIError(t *testing.T) {
	mock := &mockAIClient{
		createChatCompletionFn: func(ctx context.Context, req ai.ChatCompletionRequest) (*ai.ChatCompletionResponse, error) {
			return nil, errors.New("LLM HTTP 500")
		},
	}
	qdrant := newMockQdrantStore()
	c := &Consolidator{
		qdrant:      qdrant,
		aiClient:    mock,
		prompt:      "test prompt",
		maxMessages: 100,
	}
	ctx := context.Background()
	msg := &DiscordMessage{
		AuthorID:  "user-1",
		Username:  "testuser",
		Content:   "hello world",
		Timestamp: time.Now(),
	}
	err := c.ProcessWithMessages(ctx, "user-1", []*DiscordMessage{msg})
	if err == nil {
		t.Fatal("expected error from ProcessWithMessages when LLM fails, got nil")
	}
}
