package decision

import (
	"strings"
	"testing"
	"time"

	"ezyapper/internal/config"
)

func TestNewDecisionServiceRequiresExplicitCredentials(t *testing.T) {
	cfg := &config.DecisionConfig{
		APIKey:     "decision-key",
		APIBaseURL: "https://example.com/v1",
		Timeout:    1,
	}

	svc, err := NewDecisionService(cfg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if svc == nil || svc.client == nil || svc.httpClient == nil {
		t.Fatal("expected initialized decision service")
	}

	if svc.httpClient.Timeout != 30*time.Second {
		t.Fatalf("expected minimum http timeout 30s, got %s", svc.httpClient.Timeout)
	}
}

func TestNewDecisionServiceRequiresCredentials(t *testing.T) {
	cfg := &config.DecisionConfig{APIBaseURL: "https://example.com/v1"}

	_, err := NewDecisionService(cfg)
	if err == nil {
		t.Fatal("expected error when decision api key is missing")
	}
	if !strings.Contains(err.Error(), "decision.api_key is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewDecisionServiceRequiresBaseURL(t *testing.T) {
	cfg := &config.DecisionConfig{APIKey: "decision-key"}

	_, err := NewDecisionService(cfg)
	if err == nil {
		t.Fatal("expected error when decision api base url is missing")
	}
	if !strings.Contains(err.Error(), "decision.api_base_url is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDecisionParseResponse(t *testing.T) {
	d := &DecisionService{}

	result, err := d.parseResponse("prefix {\"should_respond\":true,\"reason\":\"mention\",\"confidence\":1.5} suffix")
	if err != nil {
		t.Fatalf("expected parse success, got %v", err)
	}
	if !result.ShouldRespond {
		t.Fatal("expected should_respond=true")
	}
	if result.Reason != "mention" {
		t.Fatalf("unexpected reason: %s", result.Reason)
	}
	if result.Confidence != 1 {
		t.Fatalf("expected confidence clamped to 1, got %v", result.Confidence)
	}
}

func TestDecisionParseResponseInvalidJSON(t *testing.T) {
	d := &DecisionService{}

	_, err := d.parseResponse("not json")
	if err == nil {
		t.Fatal("expected error for invalid response")
	}
	if !strings.Contains(err.Error(), "no valid json") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildPromptsWithInfo(t *testing.T) {
	d := &DecisionService{
		config: &config.DecisionConfig{SystemPrompt: "You are {BotName}."},
	}

	systemPrompt, userPrompt := d.buildPromptsWithInfo(
		"EZyapper",
		MessageInfo{
			AuthorName: "alice",
			AuthorID:   "100",
			Content:    "hello",
			ReplyTo:    "bob",
			ReplyToID:  "200",
		},
		"hello",
		[]ContextMessage{
			{
				AuthorName: "bob",
				AuthorID:   "200",
				Content:    "hey there",
				Timestamp:  time.Unix(1700000000, 0),
			},
		},
	)

	if !strings.Contains(systemPrompt, "EZyapper") {
		t.Fatalf("expected bot name in system prompt, got %q", systemPrompt)
	}
	if !strings.Contains(userPrompt, "<context>") {
		t.Fatalf("expected context block in user prompt, got %q", userPrompt)
	}
	if !strings.Contains(userPrompt, "<currentMessage>") {
		t.Fatalf("expected currentMessage block in user prompt, got %q", userPrompt)
	}
	if !strings.Contains(userPrompt, "Reply to: \"bob\"{UserID=200}") {
		t.Fatalf("expected reply metadata in user prompt, got %q", userPrompt)
	}
}
