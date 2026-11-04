package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"ezyapper/internal/ai/tools"
	"ezyapper/internal/config"
)

// chatServer answers chat completions and records each request body.
func chatServer(t *testing.T) (*httptest.Server, func() []map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var doc map[string]any
		_ = json.Unmarshal(b, &doc)
		mu.Lock()
		bodies = append(bodies, doc)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any(nil), bodies...)
	}
}

// A temperature key set to 0 in the config reaches the API as 0 on both
// request paths (go-openai and the spliced media request); with the key
// absent (core.ai temperature 0 and nothing set) nothing is added.
func TestExplicitZeroTemperatureIsSent(t *testing.T) {
	srv, bodies := chatServer(t)
	base := config.AIConfig{LLMConfig: config.LLMConfig{APIBaseURL: srv.URL, Model: "m", RetryCount: 0, Timeout: 5, Temperature: 0.8}}

	img := config.ModalityModel{Temperature: new(float32(0))}
	resolved := img.Resolve(base)
	if !resolved.ExplicitZeroTemperature || resolved.Temperature != 0 {
		t.Fatalf("Resolve must mark an explicit temperature 0, got %+v", resolved.LLMConfig)
	}
	c := newClientWithHTTP(&resolved, tools.NewToolRegistry(), &http.Client{})
	if _, err := c.DescribeMedia(context.Background(), "describe", MediaPart{Kind: PartImageURL, URL: "data:image/png;base64,AA=="}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateChatCompletion(context.Background(), ChatCompletionRequest{Messages: nil, SystemPrompt: "s"}); err != nil {
		t.Fatal(err)
	}

	inherited := config.ModalityModel{}.Resolve(base)
	c2 := newClientWithHTTP(&inherited, tools.NewToolRegistry(), &http.Client{})
	if _, err := c2.DescribeMedia(context.Background(), "describe"); err != nil {
		t.Fatal(err)
	}
	zeroUnset := base
	zeroUnset.Temperature = 0
	c3 := newClientWithHTTP(&zeroUnset, tools.NewToolRegistry(), &http.Client{})
	if _, err := c3.DescribeMedia(context.Background(), "describe"); err != nil {
		t.Fatal(err)
	}

	got := bodies()
	if len(got) != 4 {
		t.Fatalf("expected 4 requests, got %d", len(got))
	}
	for i := 0; i < 2; i++ {
		if v, ok := got[i]["temperature"]; !ok || v.(float64) != 0 {
			t.Fatalf("request %d: explicit temperature 0 not sent: %v", i, got[i])
		}
	}
	if v := got[2]["temperature"]; v == nil || v.(float64) < 0.79 || v.(float64) > 0.81 {
		t.Fatalf("absent modality temperature must inherit core.ai's 0.8, got %v", v)
	}
	if _, ok := got[3]["temperature"]; ok {
		t.Fatalf("a zero temperature that is not an explicit key must not be added, got %v", got[3])
	}
}
