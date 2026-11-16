package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"ezyapper/internal/ai/tools"

	openai "github.com/sashabaranov/go-openai"
)

// scriptedReply is one mocked chat completion: text plus the names of the
// tools it calls (empty = a final answer).
type scriptedReply struct {
	content string
	calls   []string
}

// budgetHarness serves scripted replies in order (the last one repeats) and
// records every request body.
type budgetHarness struct {
	mu       sync.Mutex
	replies  []scriptedReply
	requests []openai.ChatCompletionRequest
	executed []string
}

func (h *budgetHarness) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req openai.ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	n := len(h.requests)
	h.requests = append(h.requests, req)
	reply := h.replies[min(n, len(h.replies)-1)]
	h.mu.Unlock()

	msg := map[string]any{"role": "assistant", "content": reply.content}
	finish := "stop"
	if len(reply.calls) > 0 {
		calls := make([]map[string]any, len(reply.calls))
		for i, name := range reply.calls {
			calls[i] = map[string]any{
				"id":       fmt.Sprintf("call_%d_%d", n, i),
				"type":     "function",
				"function": map[string]any{"name": name, "arguments": "{}"},
			}
		}
		msg["tool_calls"] = calls
		finish = "tool_calls"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "chatcmpl-1", "object": "chat.completion", "created": 1, "model": "m",
		"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
		"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	})
}

const (
	testBudgetDescription = "No tool calls remain. Reply now."
	testBudgetResult      = "Not executed: no tool calls remain."
)

// runBudget runs one CreateChatCompletionWithTools turn with
// max_tool_iterations = maxIter and the tools echo and react registered.
func runBudget(t *testing.T, maxIter int, replies ...scriptedReply) (*ChatCompletionResponse, *budgetHarness) {
	t.Helper()
	h := &budgetHarness{replies: replies}
	server := httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(server.Close)

	cfg := defaultAIConfig()
	cfg.APIBaseURL = server.URL
	cfg.MaxToolIterations = maxIter
	cfg.ToolBudget.Description = testBudgetDescription
	cfg.ToolBudget.Result = testBudgetResult
	reg := tools.NewToolRegistry()
	for _, name := range []string{"echo", "react"} {
		reg.Register(&tools.Tool{Name: name, Description: name, Handler: func(context.Context, map[string]any) (string, error) { return "ok", nil }})
	}
	c := NewClient(cfg, reg)

	handler := func(_ context.Context, tc openai.ToolCall) (string, error) {
		h.mu.Lock()
		h.executed = append(h.executed, tc.Function.Name)
		h.mu.Unlock()
		return tc.Function.Name + " done", nil
	}
	resp, err := c.CreateChatCompletionWithTools(context.Background(), ChatCompletionRequest{
		SystemPrompt: "system",
		Messages:     []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "go"}},
	}, handler)
	if err != nil {
		t.Fatalf("CreateChatCompletionWithTools: %v", err)
	}
	return resp, h
}

func toolNames(req openai.ChatCompletionRequest) []string {
	names := make([]string, len(req.Tools))
	for i, tool := range req.Tools {
		names[i] = tool.Function.Name
	}
	return names
}

func assertTools(t *testing.T, req openai.ChatCompletionRequest, want ...string) {
	t.Helper()
	got := toolNames(req)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
}

// toolResults returns the content of every tool message in req.
func toolResults(req openai.ChatCompletionRequest) []string {
	var out []string
	for _, m := range req.Messages {
		if m.Role == openai.ChatMessageRoleTool {
			out = append(out, m.Content)
		}
	}
	return out
}

// After the last round, the leftover calls get the fixed result (not
// executed) and the closing request offers only tool_budget_exhausted.
func TestToolBudget_ClosingRequestOffersOnlyFixedTool(t *testing.T) {
	resp, h := runBudget(t, 1,
		scriptedReply{calls: []string{"react"}},
		scriptedReply{calls: []string{"react", "echo"}},
		scriptedReply{content: "final"},
	)
	if resp.Content != "final" || len(resp.ToolCalls) != 0 {
		t.Fatalf("reply = %q calls %v, want final and no calls", resp.Content, resp.ToolCalls)
	}
	if len(h.requests) != 3 {
		t.Fatalf("requests = %d, want 3 (initial, after round 1, closing 1)", len(h.requests))
	}
	assertTools(t, h.requests[0], "echo", "react")
	assertTools(t, h.requests[1], "echo", "react")
	assertTools(t, h.requests[2], ToolBudgetToolName)
	fixed := h.requests[2].Tools[0].Function
	if fixed.Description != testBudgetDescription {
		t.Fatalf("fixed tool description = %q", fixed.Description)
	}
	if fmt.Sprint(h.executed) != "[react]" {
		t.Fatalf("executed = %v, want only round 1's react", h.executed)
	}
	got := toolResults(h.requests[2])
	want := []string{"react done", testBudgetResult, testBudgetResult}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("tool results = %q, want %q", got, want)
	}
}

// Calling the fixed tool gets the fixed result and one more closing request,
// still offering only the fixed tool.
func TestToolBudget_FixedToolCallGetsFixedResult(t *testing.T) {
	resp, h := runBudget(t, 1,
		scriptedReply{calls: []string{"echo"}},
		scriptedReply{calls: []string{"echo"}},
		scriptedReply{calls: []string{ToolBudgetToolName}},
		scriptedReply{content: "done talking"},
	)
	if resp.Content != "done talking" {
		t.Fatalf("reply = %q", resp.Content)
	}
	if len(h.requests) != 4 {
		t.Fatalf("requests = %d, want 4", len(h.requests))
	}
	assertTools(t, h.requests[3], ToolBudgetToolName)
	msgs := h.requests[3].Messages
	last := msgs[len(msgs)-1]
	if last.Role != openai.ChatMessageRoleTool || last.Content != testBudgetResult {
		t.Fatalf("last message = %s %q, want the fixed tool result", last.Role, last.Content)
	}
	if prev := msgs[len(msgs)-2]; len(prev.ToolCalls) != 1 || prev.ToolCalls[0].Function.Name != ToolBudgetToolName || prev.ToolCalls[0].ID != last.ToolCallID {
		t.Fatalf("fixed result must answer the fixed tool call, got %+v / %q", prev.ToolCalls, last.ToolCallID)
	}
	if fmt.Sprint(h.executed) != "[echo]" {
		t.Fatalf("executed = %v, want only round 1's echo", h.executed)
	}
}

// A real tool called in a closing request (ignoring the tools list) is not
// executed; it gets the fixed result.
func TestToolBudget_RealToolInClosingNotExecuted(t *testing.T) {
	resp, h := runBudget(t, 1,
		scriptedReply{calls: []string{"echo"}},
		scriptedReply{calls: []string{"echo"}},
		scriptedReply{calls: []string{"react"}},
		scriptedReply{content: "ok then"},
	)
	if resp.Content != "ok then" {
		t.Fatalf("reply = %q", resp.Content)
	}
	if fmt.Sprint(h.executed) != "[echo]" {
		t.Fatalf("executed = %v, want only round 1's echo", h.executed)
	}
	assertTools(t, h.requests[3], ToolBudgetToolName)
	got := toolResults(h.requests[3])
	want := []string{"echo done", testBudgetResult, testBudgetResult}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("tool results = %q, want %q", got, want)
	}
}

// At most 2 closing requests: a model that keeps calling tools is cut off
// and its text is used.
func TestToolBudget_ClosingCapOfTwo(t *testing.T) {
	resp, h := runBudget(t, 1,
		scriptedReply{calls: []string{"echo"}},
		scriptedReply{calls: []string{"echo"}},
		scriptedReply{content: "partial", calls: []string{ToolBudgetToolName}},
	)
	// initial + after round 1 + 2 closing requests
	if len(h.requests) != 4 {
		t.Fatalf("requests = %d, want 4", len(h.requests))
	}
	assertTools(t, h.requests[2], ToolBudgetToolName)
	assertTools(t, h.requests[3], ToolBudgetToolName)
	if resp.Content != "partial" || len(resp.ToolCalls) != 0 {
		t.Fatalf("reply = %q calls %v, want partial and dropped calls", resp.Content, resp.ToolCalls)
	}
	if fmt.Sprint(h.executed) != "[echo]" {
		t.Fatalf("executed = %v", h.executed)
	}
}

// When the rounds are not used up, nothing changes: every request offers
// the full tool list and no fixed result appears.
func TestToolBudget_NotReachedUnchanged(t *testing.T) {
	resp, h := runBudget(t, 3,
		scriptedReply{calls: []string{"echo"}},
		scriptedReply{calls: []string{"react"}},
		scriptedReply{content: "all good"},
	)
	if resp.Content != "all good" {
		t.Fatalf("reply = %q", resp.Content)
	}
	if len(h.requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(h.requests))
	}
	for i, req := range h.requests {
		assertTools(t, req, "echo", "react")
		for _, r := range toolResults(req) {
			if r == testBudgetResult {
				t.Fatalf("request %d carries the fixed result", i)
			}
		}
	}
	if fmt.Sprint(h.executed) != "[echo react]" {
		t.Fatalf("executed = %v", h.executed)
	}
}
