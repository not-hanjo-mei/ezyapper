package tools

import (
	"context"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestToolRegistryStableOrderingAndHash(t *testing.T) {
	r1 := NewToolRegistry()
	r1.Register(&Tool{
		Name:        "b_tool",
		Description: "b",
		Parameters:  map[string]any{"type": "object"},
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			return "b", nil
		},
	})
	r1.Register(&Tool{
		Name:        "a_tool",
		Description: "a",
		Parameters:  map[string]any{"type": "object"},
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			return "a", nil
		},
	})

	tools := r1.GetTools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Function == nil || tools[0].Function.Name != "a_tool" {
		t.Fatalf("expected first tool to be a_tool, got %+v", tools[0].Function)
	}
	if tools[1].Function == nil || tools[1].Function.Name != "b_tool" {
		t.Fatalf("expected second tool to be b_tool, got %+v", tools[1].Function)
	}

	hash1 := r1.GetSchemaHash()
	if hash1 == "" {
		t.Fatal("expected non-empty schema hash")
	}

	r2 := NewToolRegistry()
	r2.Register(&Tool{
		Name:        "a_tool",
		Description: "a",
		Parameters:  map[string]any{"type": "object"},
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			return "a", nil
		},
	})
	r2.Register(&Tool{
		Name:        "b_tool",
		Description: "b",
		Parameters:  map[string]any{"type": "object"},
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			return "b", nil
		},
	})

	hash2 := r2.GetSchemaHash()
	if hash1 != hash2 {
		t.Fatalf("expected identical hash for same schema, got %s and %s", hash1, hash2)
	}
}

func TestToolRegistryExecuteToolNotFound(t *testing.T) {
	r := NewToolRegistry()
	_, err := r.ExecuteTool(context.Background(), "missing", map[string]any{})
	if err == nil {
		t.Fatal("expected error when tool is not found")
	}
	if !strings.Contains(err.Error(), "tool not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestToolRegistryUnregisterMissingNoop(t *testing.T) {
	r := NewToolRegistry()
	r.Unregister("missing")
	if len(r.GetTools()) != 0 {
		t.Fatalf("expected empty tool list after unregister missing tool, got %d", len(r.GetTools()))
	}
}

func TestHandleToolCall(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&Tool{
		Name:        "echo",
		Description: "echo tool",
		Parameters:  map[string]any{"type": "object"},
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			v, _ := args["message"].(string)
			return "echo:" + v, nil
		},
	})

	out, err := HandleToolCall(context.Background(), r, openai.ToolCall{
		Function: openai.FunctionCall{
			Name:      "echo",
			Arguments: `{"message":"hello"}`,
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if out != "echo:hello" {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestHandleToolCallInvalidArguments(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&Tool{
		Name:        "echo",
		Description: "echo tool",
		Parameters:  map[string]any{"type": "object"},
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			return "ok", nil
		},
	})

	_, err := HandleToolCall(context.Background(), r, openai.ToolCall{
		Function: openai.FunctionCall{
			Name:      "echo",
			Arguments: "{",
		},
	})
	if err == nil {
		t.Fatal("expected parse error for invalid json")
	}
	if !strings.Contains(err.Error(), "failed to parse tool arguments") {
		t.Fatalf("unexpected error: %v", err)
	}
}
