// Package tools provides the tool registry and Discord tool implementations
// for the AI subsystem.
package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	openai "github.com/sashabaranov/go-openai"
)

// ToolRegistry manages tools with caching for optimal prompt caching performance.
// It maintains stable tool ordering and caches the generated schema to ensure
// consistent API requests that can benefit from provider-side prompt caching.
type ToolRegistry struct {
	tools        map[string]*Tool
	cachedSchema []openai.Tool
	schemaHash   string
	mu           sync.RWMutex
}

// Tool represents a callable tool with its schema definition
type Tool struct {
	Name        string
	Description string
	Parameters  any
	Handler     ToolExecutor
}

// ToolExecutor is the function signature for tool implementations
type ToolExecutor func(ctx context.Context, args map[string]any) (string, error)

// NewToolRegistry creates a new tool registry with caching enabled
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]*Tool),
	}
}

// Register adds a tool to the registry and rebuilds the cached schema.
// The schema is rebuilt to maintain alphabetical ordering for cache stability.
func (r *ToolRegistry) Register(tool *Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.tools[tool.Name] = tool
	r.rebuildSchemaLocked()
}

// Unregister removes a tool from the registry if it exists.
func (r *ToolRegistry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[name]; !exists {
		return
	}

	delete(r.tools, name)
	r.rebuildSchemaLocked()
}

// GetTools returns the cached tool schema with stable ordering.
// This method is safe for concurrent use and returns a pre-computed
// schema to avoid rebuilding on every request, improving performance
// and enabling prompt caching.
func (r *ToolRegistry) GetTools() []openai.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Return a copy to prevent external modification
	tools := make([]openai.Tool, len(r.cachedSchema))
	copy(tools, r.cachedSchema)
	return tools
}

// GetSchemaHash returns a hash of the current tool schema.
// This can be used as a prompt_cache_key to help LLM providers
// route requests with identical tool schemas to the same cache.
func (r *ToolRegistry) GetSchemaHash() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.schemaHash
}

// rebuildSchemaLocked rebuilds the cached schema with alphabetically sorted tools.
// Must be called with lock held.
func (r *ToolRegistry) rebuildSchemaLocked() {
	// Sort tool names alphabetically for stable ordering
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	// Build ordered schema
	tools := make([]openai.Tool, 0, len(names))
	for _, name := range names {
		tool := r.tools[name]
		tools = append(tools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}

	r.cachedSchema = tools
	r.schemaHash = computeToolSchemaHash(tools)
}

// computeToolSchemaHash generates a SHA-256 hash of the tool schema
// for use as a cache key identifier.
func computeToolSchemaHash(tools []openai.Tool) string {
	h := sha256.New()
	for _, tool := range tools {
		if tool.Function != nil {
			h.Write([]byte(tool.Function.Name))
			h.Write([]byte(tool.Function.Description))
			if params, err := json.Marshal(tool.Function.Parameters); err == nil {
				h.Write(params)
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// HandleToolCall processes a tool call from the LLM and executes the corresponding tool
func HandleToolCall(ctx context.Context, registry *ToolRegistry, toolCall openai.ToolCall) (string, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
		return "", fmt.Errorf("failed to parse tool arguments: %w", err)
	}

	return registry.ExecuteTool(ctx, toolCall.Function.Name, args)
}

// ExecuteTool executes a tool by name with the provided arguments
func (r *ToolRegistry) ExecuteTool(ctx context.Context, name string, args map[string]any) (string, error) {
	r.mu.RLock()
	tool, exists := r.tools[name]
	r.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("tool not found: %s", name)
	}

	return tool.Handler(ctx, args)
}
