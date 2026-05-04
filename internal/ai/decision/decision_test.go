package decision

import (
	"os"
	"strings"
	"testing"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/logger"
)

func TestMain(m *testing.M) {
	logger.Init(logger.Config{Level: "info", File: os.DevNull})
	os.Exit(m.Run())
}

func TestNewDecisionServiceRequiresExplicitCredentials(t *testing.T) {
	cfg := &config.DecisionConfig{
		APIKey:         "decision-key",
		APIBaseURL:     "https://example.com/v1",
		Timeout:        1,
		HTTPTimeoutSec: 60,
	}

	svc, err := NewDecisionService(cfg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if svc == nil || svc.client == nil || svc.httpClient == nil {
		t.Fatal("expected initialized decision service")
	}

	if svc.httpClient.Timeout != 60*time.Second {
		t.Fatalf("expected http timeout 60s, got %s", svc.httpClient.Timeout)
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
	if err == nil {
		t.Fatalf("expected error for out-of-range confidence 1.5, got result=%+v", result)
	}
	if !strings.Contains(err.Error(), "above 1") {
		t.Fatalf("expected 'above 1' error, got: %v", err)
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

// --- retryableError ---

func TestRetryableError_NilError(t *testing.T) {
	if retryableError(nil) {
		t.Fatal("expected nil error to not be retryable")
	}
}

func TestRetryableError_RateLimit429(t *testing.T) {
	// 429 in error string
	err := fakeError("HTTP 429 Too Many Requests")
	if !retryableError(err) {
		t.Fatal("expected 429 error to be retryable")
	}
	// "too many requests" in error string
	err = fakeError("too many requests, try again later")
	if !retryableError(err) {
		t.Fatal("expected 'too many requests' error to be retryable")
	}
}

func TestRetryableError_Server5xx(t *testing.T) {
	for _, code := range []string{"500", "502", "503", "504"} {
		err := fakeError("HTTP " + code + " Internal Server Error")
		if !retryableError(err) {
			t.Fatalf("expected %s error to be retryable", code)
		}
	}
}

func TestRetryableError_ConnectionErrors(t *testing.T) {
	tests := []string{
		"connection refused",
		"dial tcp: i/o timeout",
		"context deadline exceeded",
		"unexpected EOF",
	}
	for _, msg := range tests {
		err := fakeError(msg)
		if !retryableError(err) {
			t.Fatalf("expected %q error to be retryable", msg)
		}
	}
}

func TestRetryableError_Client4xxNotRetryable(t *testing.T) {
	for _, code := range []string{"400", "401", "403", "404"} {
		err := fakeError("HTTP " + code + " Bad Request")
		if retryableError(err) {
			t.Fatalf("expected %s error to NOT be retryable", code)
		}
	}
}

func TestRetryableError_GenericErrorNotRetryable(t *testing.T) {
	err := fakeError("something went wrong")
	if retryableError(err) {
		t.Fatal("expected generic error to not be retryable")
	}
}

// fakeError is a simple error type for testing error classifiers.
type fakeError string

func (e fakeError) Error() string { return string(e) }
