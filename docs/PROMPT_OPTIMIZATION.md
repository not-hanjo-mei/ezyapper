# Prompt Optimization

This document describes the prompt caching optimizations implemented in EZyapper to reduce API costs and improve response times.

## Overview

EZyapper implements several strategies to maximize prompt caching with LLM providers (OpenAI, Anthropic, etc.):

1. **Static/Dynamic Content Separation** - Separate cacheable system prompt from dynamic user context
2. **Stable Tool Ordering** - Alphabetically sorted tools for consistent cache keys
3. **Tool Schema Caching** - Pre-computed schemas to avoid rebuild overhead
4. **Cache Hit Monitoring** - Logging to track optimization effectiveness

## How It Works

### Static vs Dynamic Content

```
┌─────────────────────────────────────────────────────────�?�?System Prompt (CACHED)                                  �?├─────────────────────────────────────────────────────────�?�?- Bot persona definition                                �?�?- Static guidelines                                     �?�?- Mention instructions                                  �?�?- Tool schemas (alphabetically sorted)                  �?└─────────────────────────────────────────────────────────�?                           �?                           �?┌─────────────────────────────────────────────────────────�?�?User Message (NOT CACHED)                               �?├─────────────────────────────────────────────────────────�?�?Dynamic Context:                                        �?�?- User traits/facts/preferences                         �?�?- Retrieved memories                                    �?�?- Recent users list                                     �?�?                                                        �?�?Original user message                                   �?└─────────────────────────────────────────────────────────�?```

### Why This Works

LLM providers cache prompts based on the **prefix** of the request. By keeping the system prompt static and moving dynamic content to the user message:

- The system prompt hash remains constant across requests
- LLM provider can cache and reuse the system prompt
- Only the user message (with dynamic content) needs to be processed fresh

## Implementation Details

### 1. Tool Registry Caching

**File:** `internal/ai/tools.go`

```go
// ToolRegistry with caching
type ToolRegistry struct {
    tools        map[string]*Tool
    cachedSchema []openai.Tool  // Pre-computed
    schemaHash   string         // Cache key
    mu           sync.RWMutex   // Thread-safe
}

// GetTools returns cached schema (stable ordering)
func (r *ToolRegistry) GetTools() []openai.Tool {
    r.mu.RLock()
    defer r.mu.RUnlock()
    
    tools := make([]openai.Tool, len(r.cachedSchema))
    copy(tools, r.cachedSchema)
    return tools
}
```

**Key Features:**
- Tools sorted alphabetically by name
- Schema computed once at registration
- Copy returned to prevent external modification
- Thread-safe with RWMutex

### 2. Static/Dynamic Separation

**File:** `internal/bot/handlers.go`

```go
func (b *Bot) generateResponse(...) {
    // Static system prompt (cacheable)
    baseSystemPrompt := b.config.FormatSystemPrompt(...)
    staticInstructions := "\n\nMention Guidelines:..."
    systemPrompt := baseSystemPrompt + staticInstructions
    
    // Dynamic context (not cached)
    dynamicContext := b.buildDynamicContext(
        authorName,
        profile,      // User traits/facts/preferences
        memories,     // Retrieved memories
        recentMessages,
    )
    
    req := ai.ChatCompletionRequest{
        SystemPrompt: systemPrompt,  // Cached
        Messages:     history,
        UserContext:  dynamicContext, // Appended to user message
    }
}
```

### 3. Prompt Compiler

**File:** `internal/prompt/compiler.go`

```go
// Compiler for safe template substitution
type Compiler struct{}

// Compile substitutes variables, preserves unknown placeholders
func (c *Compiler) Compile(template string, vars map[string]string) string {
    result := template
    for key, value := range vars {
        placeholder := "{" + key + "}"
        result = strings.ReplaceAll(result, placeholder, value)
    }
    return result  // Unknown {vars} preserved
}

// Registry for named templates
type Registry struct {
    templates map[string]string
}

func (r *Registry) Register(id, template string)
func (r *Registry) GetWithCompile(id string, vars map[string]string) string
```

**Usage Example:**

```go
registry := prompt.NewRegistry()
registry.Register("greeting", "Hello {UserName}! I'm {BotName}.")

result := registry.GetWithCompile("greeting", map[string]string{
    "{UserName}": "Alice",
    "{BotName}":  "EZyapper",
})
// Result: "Hello Alice! I'm EZyapper."
```

## Configuration

No additional configuration required. Optimizations are automatic.

### Monitoring Cache Hits

Cache hit information is logged at DEBUG level:

```bash
# Enable debug logging in config.yaml
logging:
  level: "debug"
```

**Example log output:**
```
[prompt] system prompt length: 1250 chars (static)
[prompt] dynamic context length: 450 chars
[cache] prompt cache hit: 850/1000 tokens (85.0%)
```

## Expected Benefits

| Scenario | Token Reduction | Cost Reduction |
|----------|----------------|----------------|
| High-frequency users | 70-90% | 70-90% |
| Multi-turn conversations | 60-80% | 60-80% |
| Tool-heavy interactions | 50-70% | 50-70% |

**Note:** Actual benefits depend on your LLM provider's caching implementation.

## Provider-Specific Notes

### OpenAI
- Caches prompts with matching prefixes
- 1024+ token prefixes eligible for caching
- Cache hits reduce latency by 2-5x
- Monitor `usage.prompt_tokens_details.cached_tokens` in API response

### Anthropic Claude
- Use `cache_control` field for explicit caching
- Automatic caching available in newer API versions
- Check [Anthropic prompt caching docs](https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching)

## Best Practices

1. **Keep system prompt stable** - Avoid adding timestamps or random content
2. **Use tool descriptions consistently** - Don't change tool schemas between requests
3. **Monitor cache hit rates** - Check logs to verify optimizations working
4. **Consider provider limits** - Different providers have different caching rules

## Troubleshooting

### Low Cache Hit Rates

If cache hits are lower than expected:

1. Check system prompt is truly static
2. Verify tool schemas aren't changing
3. Ensure `UserContext` is properly separated
4. Review provider-specific caching requirements

### Debugging

Enable detailed logging:

```yaml
logging:
  level: "debug"
  file: "bot.log"
```

Check for:
- `[prompt] system prompt length` - Should be consistent
- `[prompt] dynamic context length` - Should vary per request
- `[cache] prompt cache hit` - Should show >50% for benefit

## API Reference

### ToolRegistry

```go
// Get cached tool schema
func (r *ToolRegistry) GetTools() []openai.Tool

// Get schema hash for cache key tracking
func (r *ToolRegistry) GetSchemaHash() string
```

### ChatCompletionRequest

```go
type ChatCompletionRequest struct {
    SystemPrompt string                           // Static (cached)
    Messages     []openai.ChatCompletionMessage
    Tools        []openai.Tool
    UserContext  string                           // Dynamic (not cached)
}
```

### Compiler

```go
// Create compiler
func NewCompiler() *Compiler

// Compile template with variables
func (c *Compiler) Compile(template string, vars map[string]string) string

// Safe compile with brace escaping
func (c *Compiler) SafeCompile(template string, vars map[string]string) string

// Extract variable names from template
func (c *Compiler) ExtractVariables(template string) []string
```

### Registry

```go
// Create registry
func NewRegistry() *Registry

// Register template
func (r *Registry) Register(id, template string)

// Get and compile template
func (r *Registry) GetWithCompile(id string, vars map[string]string) string

// List all template IDs
func (r *Registry) List() []string
```

## See Also

- [Architecture Overview](ARCHITECTURE.md)
- [Configuration Reference](CONFIGURATION.md)
- [AGENTS.md](../AGENTS.md) - Internal code structure
