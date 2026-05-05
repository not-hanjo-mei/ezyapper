# Prompt Optimization

This document describes the prompt caching optimizations implemented in EZyapper to reduce API costs and improve response times.

## Overview

EZyapper maximizes provider-side caching by:

1. **Static/Dynamic Content Separation** - Keep the system prompt stable and move dynamic context to the user message
2. **Stable Tool Ordering** - Alphabetically sorted tools for consistent cache keys
3. **Tool Schema Caching** - Pre-computed schemas to avoid rebuild overhead
4. **Prompt Length Logging** - Debug logs to validate static vs dynamic sizing

## How It Works

### Static vs Dynamic Content

System prompt (cached):
- Bot persona and rules
- Static guidelines
- Tool schemas (alphabetically sorted)

User message (not cached):
- Dynamic context (profile, memories, recent messages)
- Original user message

### Tool Registry Caching

**File:** `internal/ai/tools/tools.go`

Tool schemas are sorted and cached once at registration. `GetTools()` returns a copy to keep ordering stable across requests.

### Static/Dynamic Separation

**Files:** `internal/bot/handlers_response.go`, `internal/bot/handlers_context.go`

```go
systemPrompt := b.cfg().FormatSystemPrompt(...)
dynamicContext := b.buildDynamicContext(...)

req := ai.ChatCompletionRequest{
    SystemPrompt: systemPrompt,
    Messages:     history,
    UserContext:  dynamicContext,
}
```

### System Prompt Formatting

**File:** `internal/config/config.go`

`FormatSystemPrompt` replaces `{BotName}`, `{AuthorName}`, `{ServerName}`, `{GuildID}`, and `{ChannelID}` in the template.

## Configuration

No additional configuration required. Optimizations are automatic.

Enable debug logs:

```yaml
operations:
  logging:
    level: "debug"
```

Relevant logs:
- `[prompt] system prompt length` (static)
- `[prompt] dynamic context length` (per request)
- `[prompt] full context length` (when user context is inlined)

## Best Practices

1. **Keep system prompt stable** - Avoid adding timestamps or random content
2. **Use tool descriptions consistently** - Don't change tool schemas between requests
3. **Keep dynamic content in `UserContext`** - Avoid putting it in the system prompt

## Troubleshooting

If cache benefits seem low:

1. Ensure the system prompt is stable across requests
2. Verify tool schemas are registered once and not mutated
3. Confirm dynamic context is appended via `UserContext`

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

### Config.FormatSystemPrompt

```go
func (c *Config) FormatSystemPrompt(authorName, serverName, guildID, channelID string) string
```

## See Also

- [Architecture Overview](ARCHITECTURE.md)
- [Configuration Reference](CONFIGURATION.md)
- [AGENTS.md](../AGENTS.md) - Internal code structure
