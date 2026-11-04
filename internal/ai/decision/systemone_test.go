package decision

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"ezyapper/internal/config"
)

func testSystemOneConfig(baseURL string) *config.SystemOneConfig {
	return &config.SystemOneConfig{
		APIBaseURL:    baseURL,
		APIKey:        "test-key",
		Model:         "jev-latest",
		Timeout:       5,
		RetryCount:    2,
		Threshold:     0.7,
		Instructions:  "Should Ezy reply to current_message?",
		MaxStateBytes: 48000,
	}
}

// fakeSystemOne serves /v1/systemone with the given handler and records
// the last decoded request.
type fakeSystemOne struct {
	srv      *httptest.Server
	calls    atomic.Int32
	lastReq  atomic.Pointer[map[string]any]
	lastAuth atomic.Pointer[string]
}

func newFakeSystemOne(t *testing.T, handler func(call int, w http.ResponseWriter)) *fakeSystemOne {
	t.Helper()
	f := &fakeSystemOne{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		f.lastReq.Store(&m)
		auth := r.Header.Get("Authorization")
		f.lastAuth.Store(&auth)
		handler(int(f.calls.Add(1)), w)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func noulResponse(p float64) func(int, http.ResponseWriter) {
	return func(_ int, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"model":"jev-1.14.0","answers":{"should_respond":{"type":"noul","noul":%v}},"usage":{"input_tokens":10,"output_tokens":2}}`, p)
	}
}

func TestSystemOne_RequestShape(t *testing.T) {
	f := newFakeSystemOne(t, noulResponse(0.9))
	cfg := testSystemOneConfig(f.srv.URL + "/v1/") // trailing slash is trimmed
	cfg.CriteriaTrue = "talks to Ezy"
	cfg.CriteriaFalse = "talks to others"
	svc, err := NewSystemOneService(cfg)
	if err != nil {
		t.Fatal(err)
	}

	t0 := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	recent := []ContextMessage{
		// Newest first, as Discord returns pages.
		{AuthorName: "Ezy", AuthorID: "9", Content: "hi bob", IsBot: true, Timestamp: t0.Add(time.Minute), ReplyTo: "bob"},
		{AuthorName: "bob", AuthorID: "2", Content: "hello", Timestamp: t0},
	}
	msg := MessageInfo{AuthorName: "alice", AuthorID: "1", Content: "Ezy, what's up?", ReplyTo: "bob", ReplyToID: "2", Timestamp: t0.Add(2 * time.Minute)}

	res, err := svc.ShouldRespondWithInfo(context.Background(), "IGNORED_NAME", msg, 2, recent)
	if err != nil {
		t.Fatal(err)
	}
	if !res.ShouldRespond {
		t.Fatalf("expected respond for p=0.9, got %+v", res)
	}

	if got := *f.lastAuth.Load(); got != "Bearer test-key" {
		t.Fatalf("authorization = %q", got)
	}
	req := *f.lastReq.Load()
	if req["model"] != "jev-latest" {
		t.Fatalf("model = %v", req["model"])
	}
	q := req["questions"].(map[string]any)["should_respond"].(map[string]any)
	if q["type"] != "noul" || q["instructions"] != "Should Ezy reply to current_message?" {
		t.Fatalf("question = %v", q)
	}
	crit := q["criteria"].(map[string]any)
	if crit["true"] != "talks to Ezy" || crit["false"] != "talks to others" {
		t.Fatalf("criteria = %v", crit)
	}

	state := req["state"].(map[string]any)
	hist := state["recent_messages"].([]any)
	if len(hist) != 2 {
		t.Fatalf("history len = %d", len(hist))
	}
	first, second := hist[0].(map[string]any), hist[1].(map[string]any)
	if first["author"] != "bob" || second["author"] != "Ezy" {
		t.Fatalf("history must be oldest first, got %v then %v", first["author"], second["author"])
	}
	if second["is_bot"] != true || second["reply_to"] != "bob" || second["timestamp"] != "2026-11-01T08:01:00Z" {
		t.Fatalf("bot entry = %v", second)
	}
	if _, ok := first["reply_to"]; ok {
		t.Fatalf("empty reply_to should be omitted: %v", first)
	}
	if _, ok := state["older_messages_omitted"]; ok {
		t.Fatalf("older_messages_omitted should be absent when nothing was dropped")
	}
	cur := state["current_message"].(map[string]any)
	if cur["author"] != "alice" || cur["content"] != "Ezy, what's up?" || cur["reply_to"] != "bob" || cur["image_count"] != float64(2) || cur["is_bot"] != false {
		t.Fatalf("current_message = %v", cur)
	}
	raw, _ := json.Marshal(req)
	if strings.Contains(string(raw), "IGNORED_NAME") || strings.Contains(string(raw), `"author_id"`) {
		t.Fatalf("request must not contain the botName argument or author ids: %s", raw)
	}
}

func TestSystemOne_NoCriteriaOmitted(t *testing.T) {
	f := newFakeSystemOne(t, noulResponse(0.1))
	svc, _ := NewSystemOneService(testSystemOneConfig(f.srv.URL + "/v1"))
	if _, err := svc.ShouldRespondWithInfo(context.Background(), "", MessageInfo{AuthorName: "a", Content: "x"}, 0, nil); err != nil {
		t.Fatal(err)
	}
	q := (*f.lastReq.Load())["questions"].(map[string]any)["should_respond"].(map[string]any)
	if _, ok := q["criteria"]; ok {
		t.Fatalf("criteria must be omitted when not configured: %v", q)
	}
	if hist := (*f.lastReq.Load())["state"].(map[string]any)["recent_messages"].([]any); len(hist) != 0 {
		t.Fatalf("expected empty history array, got %v", hist)
	}
}

// The bot replies only when Jev says yes (p > 0.5) AND p > threshold, both
// strict. Confidence is p when replying, 1-p otherwise.
func TestSystemOneResult_Threshold(t *testing.T) {
	cases := []struct {
		name                string
		p, threshold        float64
		jevYes, aboveThresh bool
		respond             bool
		wantConfidence      float64
	}{
		{"p == threshold does not reply", 0.70, 0.70, true, false, false, 0.30},
		{"just above threshold replies", 0.7001, 0.70, true, true, true, 0.7001},
		{"below threshold", 0.69, 0.70, true, false, false, 0.31},
		{"confident yes", 0.95, 0.70, true, true, true, 0.95},
		{"confident no", 0.05, 0.70, false, false, false, 0.95},
		{"p == 0.5 is not a yes (threshold 0.3)", 0.50, 0.30, false, true, false, 0.50},
		{"p == 0.5 with threshold 0.5", 0.50, 0.50, false, false, false, 0.50},
		{"just above 0.5 with threshold 0.5", 0.5001, 0.50, true, true, true, 0.5001},
		{"threshold below 0.5: a no above it does not reply", 0.40, 0.30, false, true, false, 0.60},
		{"threshold below 0.5: a yes replies", 0.51, 0.30, true, true, true, 0.51},
		{"threshold below 0.5: p == threshold", 0.30, 0.30, false, false, false, 0.70},
		{"threshold 0: any yes replies", 0.60, 0, true, true, true, 0.60},
		{"threshold 0: p == 0", 0, 0, false, false, false, 1},
		{"high threshold, certain yes", 1.00, 0.99, true, true, true, 1.00},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := systemOneResult(c.p, c.threshold)
			if r.ShouldRespond != c.respond {
				t.Errorf("respond=%v want %v", r.ShouldRespond, c.respond)
			}
			if diff := r.Confidence - c.wantConfidence; diff > 1e-9 || diff < -1e-9 {
				t.Errorf("confidence=%v want %v", r.Confidence, c.wantConfidence)
			}
			want := fmt.Sprintf("p(yes)=%.4f jev_yes=%t (p>0.5) above_threshold=%t (p>%g)", c.p, c.jevYes, c.aboveThresh, c.threshold)
			if r.Reason != want {
				t.Errorf("reason=%q want %q", r.Reason, want)
			}
		})
	}
}

func TestSystemOne_RetriesTransientErrors(t *testing.T) {
	for _, status := range []int{429, 500, 529} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f := newFakeSystemOne(t, func(call int, w http.ResponseWriter) {
				if call == 1 {
					http.Error(w, `{"detail":"busy"}`, status)
					return
				}
				noulResponse(0.8)(call, w)
			})
			svc, _ := NewSystemOneService(testSystemOneConfig(f.srv.URL + "/v1"))
			res, err := svc.ShouldRespondWithInfo(context.Background(), "", MessageInfo{Content: "x"}, 0, nil)
			if err != nil {
				t.Fatalf("expected success after retry, got %v", err)
			}
			if !res.ShouldRespond || f.calls.Load() != 2 {
				t.Fatalf("calls=%d res=%+v", f.calls.Load(), res)
			}
		})
	}
}

func TestSystemOne_DoesNotRetryRequestErrors(t *testing.T) {
	for _, status := range []int{400, 401, 413, 422} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f := newFakeSystemOne(t, func(_ int, w http.ResponseWriter) {
				http.Error(w, `{"detail":{"error_type":"api_usage_error","message":"Invalid request."}}`, status)
			})
			svc, _ := NewSystemOneService(testSystemOneConfig(f.srv.URL + "/v1"))
			_, err := svc.ShouldRespondWithInfo(context.Background(), "", MessageInfo{Content: "x"}, 0, nil)
			if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", status)) {
				t.Fatalf("expected HTTP %d error, got %v", status, err)
			}
			if f.calls.Load() != 1 {
				t.Fatalf("status %d must not be retried, calls=%d", status, f.calls.Load())
			}
		})
	}
}

func TestSystemOne_RetryExhausted(t *testing.T) {
	f := newFakeSystemOne(t, func(_ int, w http.ResponseWriter) {
		http.Error(w, "overloaded", 529)
	})
	cfg := testSystemOneConfig(f.srv.URL + "/v1")
	cfg.RetryCount = 1
	svc, _ := NewSystemOneService(cfg)
	if _, err := svc.ShouldRespondWithInfo(context.Background(), "", MessageInfo{Content: "x"}, 0, nil); err == nil {
		t.Fatal("expected error after retries")
	}
	if f.calls.Load() != 2 {
		t.Fatalf("expected 1 + retry_count attempts, got %d", f.calls.Load())
	}
}

func TestSystemOne_BadResponsesAreErrors(t *testing.T) {
	bodies := map[string]string{
		"not json":       `nope`,
		"missing answer": `{"model":"jev","answers":{}}`,
		"null noul":      `{"model":"jev","answers":{"should_respond":{"type":"noul"}}}`,
		"out of range":   `{"model":"jev","answers":{"should_respond":{"type":"noul","noul":1.5}}}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			f := newFakeSystemOne(t, func(_ int, w http.ResponseWriter) { io.WriteString(w, body) })
			cfg := testSystemOneConfig(f.srv.URL + "/v1")
			cfg.RetryCount = 0
			svc, _ := NewSystemOneService(cfg)
			if _, err := svc.ShouldRespondWithInfo(context.Background(), "", MessageInfo{Content: "x"}, 0, nil); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestSystemOne_PerAttemptTimeoutRetries(t *testing.T) {
	f := newFakeSystemOne(t, func(call int, w http.ResponseWriter) {
		if call == 1 {
			time.Sleep(1500 * time.Millisecond)
		}
		noulResponse(0.2)(call, w)
	})
	cfg := testSystemOneConfig(f.srv.URL + "/v1")
	cfg.Timeout = 1
	svc, _ := NewSystemOneService(cfg)
	res, err := svc.ShouldRespondWithInfo(context.Background(), "", MessageInfo{Content: "x"}, 0, nil)
	if err != nil {
		t.Fatalf("expected success on second attempt, got %v", err)
	}
	if res.ShouldRespond || f.calls.Load() != 2 {
		t.Fatalf("calls=%d res=%+v", f.calls.Load(), res)
	}
}

func TestSystemOne_CancelledContextStops(t *testing.T) {
	f := newFakeSystemOne(t, noulResponse(0.9))
	svc, _ := NewSystemOneService(testSystemOneConfig(f.srv.URL + "/v1"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.ShouldRespondWithInfo(ctx, "", MessageInfo{Content: "x"}, 0, nil); err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if f.calls.Load() != 0 {
		t.Fatalf("no request expected, got %d", f.calls.Load())
	}
}

func TestBuildDecisionState_DropsOldestFirst(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var recent []ContextMessage
	for i := 0; i < 50; i++ {
		recent = append(recent, ContextMessage{
			AuthorName: fmt.Sprintf("user%d", i),
			Content:    strings.Repeat("x", 200),
			Timestamp:  t0.Add(time.Duration(i) * time.Minute),
		})
	}
	msg := MessageInfo{AuthorName: "alice", Content: "current"}

	full, omitted, err := buildDecisionState(msg, 0, recent, 1<<20)
	if err != nil || omitted != 0 {
		t.Fatalf("unbounded: omitted=%d err=%v", omitted, err)
	}

	budget := len(full) / 3
	out, omitted, err := buildDecisionState(msg, 0, recent, budget)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > budget {
		t.Fatalf("state %d bytes exceeds budget %d", len(out), budget)
	}
	var st decisionState
	if err := json.Unmarshal(out, &st); err != nil {
		t.Fatal(err)
	}
	if omitted == 0 || st.OlderMessagesOmitted != omitted || len(st.RecentMessages)+omitted != 50 {
		t.Fatalf("omitted=%d field=%d kept=%d", omitted, st.OlderMessagesOmitted, len(st.RecentMessages))
	}
	if st.RecentMessages[len(st.RecentMessages)-1].Author != "user49" {
		t.Fatalf("newest message must be kept, last=%s", st.RecentMessages[len(st.RecentMessages)-1].Author)
	}
	if st.RecentMessages[0].Author != fmt.Sprintf("user%d", omitted) {
		t.Fatalf("oldest messages must be dropped first, first kept=%s omitted=%d", st.RecentMessages[0].Author, omitted)
	}
	if st.CurrentMessage.Content != "current" {
		t.Fatalf("current message must be intact, got %q", st.CurrentMessage.Content)
	}

	// Dropping is minimal: one more message would not fit.
	st.RecentMessages = append([]stateMessage{{Author: fmt.Sprintf("user%d", omitted-1), Content: strings.Repeat("x", 200), Timestamp: formatStateTime(t0.Add(time.Duration(omitted-1) * time.Minute))}}, st.RecentMessages...)
	st.OlderMessagesOmitted = omitted - 1
	if bigger, _ := encodeJSON(st); len(bigger) <= budget {
		t.Fatalf("dropped more than needed: %d-byte state with one more message fits budget %d", len(bigger), budget)
	}
}

func TestBuildDecisionState_TruncatesHugeCurrentMessage(t *testing.T) {
	recent := []ContextMessage{{AuthorName: "bob", Content: "old"}}
	msg := MessageInfo{AuthorName: "alice", Content: strings.Repeat("日本語", 2000)} // 18000 bytes
	out, omitted, err := buildDecisionState(msg, 1, recent, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > 1000 {
		t.Fatalf("state %d bytes exceeds budget", len(out))
	}
	var st decisionState
	if err := json.Unmarshal(out, &st); err != nil {
		t.Fatal(err)
	}
	if omitted != 1 || len(st.RecentMessages) != 0 {
		t.Fatalf("history must be dropped before cutting the current message: omitted=%d kept=%d", omitted, len(st.RecentMessages))
	}
	c := st.CurrentMessage.Content
	if !strings.HasSuffix(c, truncationMarker) || !utf8.ValidString(c) || !strings.HasPrefix(c, "日本語") {
		t.Fatalf("bad truncated content: %q", c)
	}
	if st.CurrentMessage.ImageCount != 1 {
		t.Fatalf("image_count lost")
	}
}

func TestBuildDecisionState_BudgetTooSmall(t *testing.T) {
	_, _, err := buildDecisionState(MessageInfo{AuthorName: "alice", Content: "hi"}, 0, nil, 10)
	if err == nil {
		t.Fatal("expected budget error")
	}
	if isRetryableSystemOneError(err) {
		t.Fatal("budget error must not be retried")
	}
}

func TestBuildDecisionState_NoHTMLEscaping(t *testing.T) {
	out, _, err := buildDecisionState(MessageInfo{AuthorName: "a", Content: "<b>&</b>"}, 0, nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "<b>&</b>") {
		t.Fatalf("expected raw <>&, got %s", out)
	}
}

func TestNewSelectsSystemOneProvider(t *testing.T) {
	cfg := &config.DecisionConfig{Provider: config.DecisionProviderSystemOne, SystemOne: *testSystemOneConfig("https://api.example.com/v1")}
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	so, ok := d.(*SystemOneService)
	if !ok {
		t.Fatalf("expected *SystemOneService, got %T", d)
	}
	if so.endpoint != "https://api.example.com/v1/systemone" {
		t.Fatalf("endpoint = %q", so.endpoint)
	}
	if so.httpClient.Timeout != 5*time.Second {
		t.Fatalf("http timeout = %s", so.httpClient.Timeout)
	}
}

func TestNewSystemOneServiceRequiresCredentials(t *testing.T) {
	if _, err := NewSystemOneService(&config.SystemOneConfig{APIKey: "k"}); err == nil {
		t.Fatal("expected error without base url")
	}
	if _, err := NewSystemOneService(&config.SystemOneConfig{APIBaseURL: "u"}); err == nil {
		t.Fatal("expected error without api key")
	}
}
