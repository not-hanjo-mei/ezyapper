package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"ezyapper/internal/ai/tools"
	"ezyapper/internal/config"

	"github.com/sashabaranov/go-openai"
)

func TestMediaPartJSON(t *testing.T) {
	cases := []struct {
		part MediaPart
		want string
	}{
		{MediaPart{Kind: PartAudioURL, URL: "data:audio/ogg;base64,AA=="}, `{"audio_url":{"url":"data:audio/ogg;base64,AA=="},"type":"audio_url"}`},
		{MediaPart{Kind: PartVideoURL, URL: "https://x/v.mp4"}, `{"type":"video_url","video_url":{"url":"https://x/v.mp4"}}`},
		{MediaPart{Kind: PartImageURL, URL: "data:image/webp;base64,AA=="}, `{"image_url":{"detail":"auto","url":"data:image/webp;base64,AA=="},"type":"image_url"}`},
		{MediaPart{Kind: PartInputAudio, Data: "AA==", Format: "mp3"}, `{"input_audio":{"data":"AA==","format":"mp3"},"type":"input_audio"}`},
	}
	for _, c := range cases {
		got, _ := json.Marshal(c.part.JSON())
		if string(got) != c.want {
			t.Errorf("%s: got %s want %s", c.part.Kind, got, c.want)
		}
	}
}

func TestDataURIDropsParameters(t *testing.T) {
	if got := DataURI("audio/ogg; codecs=opus", []byte{0}); got != "data:audio/ogg;base64,AA==" {
		t.Fatalf("got %s", got)
	}
}

// The media parts go on the user message that carried the prompt, even
// after the tool loop appended assistant/tool turns and a feedback message.
func TestSpliceMediaPartsTargetsPromptMessage(t *testing.T) {
	req := openai.ChatCompletionRequest{Model: "m", Messages: []openai.ChatCompletionMessage{
		{Role: "system", Content: "static"},
		{Role: "user", Content: "prompt"},
		{Role: "assistant", ToolCalls: []openai.ToolCall{{ID: "1", Type: "function", Function: openai.FunctionCall{Name: "f", Arguments: "{}"}}}},
		{Role: "tool", Content: "r", ToolCallID: "1"},
		{Role: "user", Content: "feedback"},
	}}
	body, err := spliceMediaParts(req, []MediaPart{{Kind: PartAudioURL, URL: "data:audio/ogg;base64,AA=="}})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if got := string(doc.Messages[1].Content); got != `[{"text":"prompt","type":"text"},{"audio_url":{"url":"data:audio/ogg;base64,AA=="},"type":"audio_url"}]` {
		t.Fatalf("prompt message content = %s", got)
	}
	if got := string(doc.Messages[4].Content); got != `"feedback"` {
		t.Fatalf("feedback message must stay plain, got %s", got)
	}
	if got := string(doc.Messages[0].Content); got != `"static"` {
		t.Fatalf("system prompt must stay a plain string, got %s", got)
	}
}

// fakeChat answers chat completions and records request bodies.
type fakeChat struct {
	mu     sync.Mutex
	bodies []map[string]any
	reply  func(n int) string
}

func (f *fakeChat) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing auth header")
		}
		if ua := r.Header.Get("User-Agent"); ua == "" || strings.HasPrefix(ua, "Go-http-client") {
			t.Errorf("default User-Agent sent: %q", ua)
		}
		raw, _ := io.ReadAll(r.Body)
		var doc map[string]any
		_ = json.Unmarshal(raw, &doc)
		f.mu.Lock()
		f.bodies = append(f.bodies, doc)
		n := len(f.bodies)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, f.reply(n))
	}
}

func mediaTestClient(url string) *Client {
	cfg := &config.AIConfig{LLMConfig: config.LLMConfig{APIKey: "test-key", APIBaseURL: url, Model: "omni", MaxTokens: 100, RetryCount: 1, Timeout: 5}, MaxToolIterations: 3}
	reg := tools.NewToolRegistry()
	reg.Register(&tools.Tool{Name: "lookup", Description: "d", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}})
	return NewClient(cfg, reg)
}

func lastUserContent(t *testing.T, doc map[string]any) []any {
	t.Helper()
	msgs := doc["messages"].([]any)
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i].(map[string]any)
		if m["role"] == "user" {
			c, ok := m["content"].([]any)
			if !ok {
				t.Fatalf("user content is not a part list: %v", m["content"])
			}
			return c
		}
	}
	t.Fatal("no user message")
	return nil
}

func TestChatCompletionWithToolsAndMediaParts(t *testing.T) {
	fake := &fakeChat{reply: func(n int) string {
		if n == 1 {
			return `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`
		}
		return `{"choices":[{"message":{"role":"assistant","content":"heard it","reasoning_content":"thinking"},"finish_reason":"stop"}]}`
	}}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	c := mediaTestClient(srv.URL)

	parts := []MediaPart{{Kind: PartAudioURL, URL: "data:audio/ogg;base64,T2dnUw=="}}
	req := ChatCompletionRequest{SystemPrompt: "sys", Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "what did I say?"}}, MediaParts: parts}
	resp, err := c.CreateChatCompletionWithTools(context.Background(), req, func(ctx context.Context, tc openai.ToolCall) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "heard it" || resp.ReasoningContent != "thinking" {
		t.Fatalf("resp = %+v", resp)
	}
	if len(fake.bodies) != 2 {
		t.Fatalf("expected 2 requests (tool loop), got %d", len(fake.bodies))
	}
	for i, doc := range fake.bodies {
		if doc["tools"] == nil {
			t.Errorf("request %d lost its tools", i)
		}
		msgs := doc["messages"].([]any)
		user := msgs[1].(map[string]any)["content"].([]any)
		if len(user) != 2 || user[1].(map[string]any)["type"] != "audio_url" {
			t.Errorf("request %d: audio part missing from the prompt message: %v", i, user)
		}
	}
}

func TestDescribeMedia(t *testing.T) {
	fake := &fakeChat{reply: func(int) string {
		return `{"choices":[{"message":{"role":"assistant","content":" a cat dancing "},"finish_reason":"stop"}]}`
	}}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	c := mediaTestClient(srv.URL)
	got, err := c.DescribeMedia(context.Background(), "describe", MediaPart{Kind: PartVideoURL, URL: "data:video/mp4;base64,AA=="})
	if err != nil || got != "a cat dancing" {
		t.Fatalf("got %q err %v", got, err)
	}
	doc := fake.bodies[0]
	if doc["model"] != "omni" || doc["max_tokens"].(float64) != 100 {
		t.Fatalf("request = %v", doc)
	}
	content := lastUserContent(t, doc)
	if content[0].(map[string]any)["text"] != "describe" || content[1].(map[string]any)["type"] != "video_url" {
		t.Fatalf("content = %v", content)
	}
}

// Reasoning models that run out of max_tokens answer with reasoning only;
// that must surface as an error, not an empty description.
func TestDescribeMediaReasoningOnlyIsError(t *testing.T) {
	fake := &fakeChat{reply: func(int) string {
		return `{"choices":[{"message":{"role":"assistant","content":"","reasoning_content":"hmm"},"finish_reason":"length"}]}`
	}}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	_, err := mediaTestClient(srv.URL).DescribeMedia(context.Background(), "d", MediaPart{Kind: PartVideoURL, URL: "x"})
	if err == nil || !strings.Contains(err.Error(), "max_tokens") {
		t.Fatalf("expected max_tokens hint, got %v", err)
	}
}

func TestMediaRequestErrorKeepsStatusForRetry(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, `{"error":"bad format"}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	_, err := mediaTestClient(srv.URL).DescribeMedia(context.Background(), "d", MediaPart{Kind: PartAudioURL, URL: "x"})
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected 400 error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("400 must not be retried, got %d calls", calls)
	}
}

func TestFetchMediaLimits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/ogg")
		_, _ = w.Write(make([]byte, 100))
	}))
	defer srv.Close()
	c := mediaTestClient(srv.URL)
	data, ct, err := c.FetchMedia(context.Background(), srv.URL+"/a.ogg", 100, "audio/")
	if err != nil || len(data) != 100 || ct != "audio/ogg" {
		t.Fatalf("got %d bytes %q %v", len(data), ct, err)
	}
	if _, _, err := c.FetchMedia(context.Background(), srv.URL+"/a.ogg", 99, "audio/"); err == nil {
		t.Fatal("expected size limit error")
	}
	if _, _, err := c.FetchMedia(context.Background(), srv.URL+"/a.ogg", 0, "video/"); err == nil {
		t.Fatal("expected content type error")
	}
}
