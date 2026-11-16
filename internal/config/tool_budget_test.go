package config

import (
	"strings"
	"testing"
)

// testToolBudget fills core.ai.tool_budget in the Go-built test configs.
var testToolBudget = ToolBudgetConfig{Description: "no tools left", Result: "not executed"}

// core.ai.tool_budget has no built-in text: with tools offered
// (max_tool_iterations > 0) a missing or blank key fails validation.
func TestToolBudget_RequiredKeys(t *testing.T) {
	for _, key := range []string{"description", "result"} {
		for name, val := range map[string]any{"absent": nil, "blank": "  "} {
			tree := exampleTree(t)
			setPath(t, tree, "core.ai.tool_budget."+key, val)
			_, err := loadTree(t, tree)
			want := "core.ai.tool_budget." + key + " is required when core.ai.max_tool_iterations > 0"
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("%s %s: want %q, got %v", name, key, want, err)
			}
		}
	}
}

func TestToolBudget_LoadsFromExample(t *testing.T) {
	cfg := mustLoad(t, exampleTree(t))
	if strings.TrimSpace(cfg.AI.ToolBudget.Description) == "" || strings.TrimSpace(cfg.AI.ToolBudget.Result) == "" {
		t.Fatalf("example must set both tool_budget keys, got %+v", cfg.AI.ToolBudget)
	}
}
