package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func newRewriterTestPlugin(t *testing.T, retryCount int, replies []string) (*openAITTSPlugin, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(atomic.AddInt32(&calls, 1)) - 1
		content := replies[len(replies)-1]
		if n < len(replies) {
			content = replies[n]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": content}}},
		})
	}))
	t.Cleanup(srv.Close)

	p := &openAITTSPlugin{
		cfg: ttsConfig{Rewriter: ttsRewriterConfig{
			Enabled:        true,
			BaseURL:        srv.URL,
			APIKey:         "test",
			Model:          "m",
			TimeoutSeconds: 5,
			RetryCount:     retryCount,
			MaxTokens:      100,
			Prompt:         "p",
		}},
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
	return p, &calls
}

// Malformed JSON from the rewriter must not crash the plugin: it is retried,
// and each malformed reply consumes exactly one retry.
func TestRewriterRetriesOnMalformedJSON(t *testing.T) {
	p, calls := newRewriterTestPlugin(t, 2, []string{`{"input": "unterminated`, `{"input":"fixed text"}`})

	text, _, rewritten, _, err := p.maybeRewriteInputAndInstructions("original", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "fixed text" || !rewritten {
		t.Fatalf("expected rewritten text after one retry, got %q rewritten=%v", text, rewritten)
	}
	if got := atomic.LoadInt32(calls); got != 2 {
		t.Fatalf("expected 2 rewriter calls (1 malformed + 1 good), got %d", got)
	}
}

func TestRewriterFallsBackAfterRetriesExhausted(t *testing.T) {
	p, calls := newRewriterTestPlugin(t, 1, []string{`not json at all`})

	text, _, rewritten, _, err := p.maybeRewriteInputAndInstructions("original", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "original" || rewritten {
		t.Fatalf("expected fallback to original text, got %q rewritten=%v", text, rewritten)
	}
	if got := atomic.LoadInt32(calls); got != 2 {
		t.Fatalf("expected retry_count+1 = 2 calls, got %d", got)
	}
}
