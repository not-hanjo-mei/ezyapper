# Architecture Overview

EZyapper is designed with a modular, layered architecture optimized for high concurrency and extensibility. It uses **Qdrant vector database** for long-term memory storage, making it completely stateless.

## System Architecture

```
┌─────────────────────────────────────────────────────────────────�?�?                        Discord Gateway                          �?└─────────────────────────────────────────────────────────────────�?                                 �?                                 �?┌─────────────────────────────────────────────────────────────────�?�?                       Bot Layer (internal/bot)                  �?�? ┌──────────────�? ┌──────────────�? ┌──────────────────────�? �?�? �?  Session    �? �?  Handlers   �? �?  Rate Limiter       �? �?�? �? Management  �? �? (Events)    �? �?  & Cooldown         �? �?�? └──────────────�? └──────────────�? └──────────────────────�? �?└─────────────────────────────────────────────────────────────────�?                                 �?                     ┌───────────┼───────────�?                     �?          �?          �?┌────────────────�?┌────────────────�?┌────────────────────────�?�? AI Layer      �?�? Memory Layer  �?�? Plugin Layer          �?�?(internal/ai)  �?�?(internal/memory) �?�?(internal/plugin)   �?�?               �?�?               �?�?                       �?�?┌────────────�?�?�?┌────────────�?�?�?┌────────────────────�?�?�?�? Client    �?�?�?�? Service   �?�?�?�? Plugin Registry   �?�?�?�? (OpenAI)  �?�?�?�? Interface �?�?�?�? & Manager         �?�?�?└────────────�?�?�?└────────────�?�?�?└────────────────────�?�?�?┌────────────�?�?�?┌────────────�?�?�?┌────────────────────�?�?�?�? Tools     �?�?�?�? Qdrant    �?�?�?�? Built-in Plugins  �?�?�?�? (MCP)     �?�?�?�? Client    �?�?�?�? - AntiSpam        �?�?�?└────────────�?�?�?└────────────�?�?�?└────────────────────�?�?└────────────────�?└────────────────�?└────────────────────────�?                     �?                     �?┌─────────────────────────────────────────────────────────────────�?�?                   Qdrant Vector Database                        �?�? ┌──────────────�? ┌──────────────�? ┌──────────────────────�? �?�? �? Memories    �? �?  Profiles   �? �?  Collections        �? �?�? �? Collection  �? �?  Collection �? �?  (Vector Search)    �? �?�? └──────────────�? └──────────────�? └──────────────────────�? �?└─────────────────────────────────────────────────────────────────�?                                 �?                                 �?┌─────────────────────────────────────────────────────────────────�?�?                     WebUI Layer (internal/web)                  �?�? ┌──────────────�? ┌──────────────�? ┌──────────────────────�? �?�? �? Gin Server  �? �? REST API    �? �? Static Files        �? �?�? �?             �? �? Endpoints   �? �? (Dashboard)         �? �?�? └──────────────�? └──────────────�? └──────────────────────�? �?└─────────────────────────────────────────────────────────────────�?```

## Core Components

### 1. Configuration Layer (`internal/config`)

Uses Viper for flexible configuration management:
- YAML configuration files
- Environment variable overrides (prefix: `EZYAPPER_`)
- Validation at startup
- No hot-reload (requires restart)

```go
type Config struct {
    Discord   DiscordConfig
    AI        AIConfig
    Memory    MemoryConfig
    Web       WebConfig
    Qdrant    QdrantConfig
    Blacklist BlacklistConfig
    Plugins   PluginsConfig
    MCP       MCPConfig
}
```

### 2. Logging Layer (`internal/logger`)

Uses Zap for high-performance structured logging:
- JSON and console output formats
- Log rotation with lumberjack
- Configurable log levels
- Global and contextual loggers

### 3. Memory Layer (`internal/memory`)

**NEW**: Qdrant-based memory system replacing SQLite.

#### Memory Service (`service.go`)
- Interface for memory operations
- Handles both short-term and long-term memory
- Async consolidation triggers

#### Qdrant Client (`qdrant.go`)
- gRPC connection to Qdrant
- Collection management (memories, profiles)
- Vector search operations
- Point upserts and deletions

#### Models (`models.go`)
- User profiles are **auto-created** on first message/access if not present in Qdrant.

**Memory Struct:**
```go
type Memory struct {
    ID          string
    UserID      string
    MemoryType  MemoryType  // summary, fact, episode
    Content     string
    Summary     string
    Embedding   []float32   // 1536 dimensions
    Keywords    []string
    Confidence  float64
    CreatedAt   time.Time
}
```

**Profile Struct:**
```go
type Profile struct {
    UserID       string
    Traits       []string
    Facts        map[string]string
    Preferences  map[string]string
    Interests    []string
    MessageCount int
    MemoryCount  int
}
```

#### Short-term Context (`shortterm.go`)
- Fetches recent messages from Discord API
- Configurable limit (example: 20 messages)
- Converts Discord messages to internal format

#### Consolidation (`consolidation.go`)
- Async worker for memory processing
- Triggered every N messages (configurable)
- Extracts facts, traits, preferences
- Updates user profiles

#### Embedder (`embedder.go`)
- Generates vector embeddings via AI
- Uses text-embedding-3-small model
- 1536-dimensional vectors

### 4. AI Layer (`internal/ai`)

#### Client (`client.go`)
- OpenAI-compatible API client
- Custom base URL support (Qwen, DeepSeek, etc.)
- Vision (image analysis) support
- Streaming responses
- Tool calling integration
- **NEW**: Embedding generation

#### Tools (`tools.go`)
MCP (Model Context Protocol) tools for Discord operations:
- Server info retrieval
- Channel management
- User lookups
- Message operations
- Reaction handling
- **Tool schema caching with stable alphabetical ordering**
- **Schema hash generation for cache key tracking**

#### Prompt Optimization (`internal/prompt`)
Template compilation and caching utilities:
- **Compiler**: Safe variable substitution preserving unknown placeholders
- **Registry**: Named template management for different scenarios
- **Static/Dynamic Separation**: Maximizes LLM provider prompt caching
- **Multi-stage Compilation**: Supports partial variable substitution

### 5. Bot Layer (`internal/bot`)

#### Session (`session.go`)
- Discordgo session management
- Intent configuration
- State caching
- Plugin integration
- **Memory service injection**

#### Handlers (`handlers.go`)
- MessageCreate event handling
- Reply probability logic using `crypto/rand` for secure randomness
- Anti-spam checks with per-user/channel rate limits
  - Key format: `channelID:userID`
- AI invocation
- Response formatting
- **Memory search and storage**

### 6. Plugin Layer (`internal/plugin`)

External plugin binaries currently use two runtime modes:

- Persistent JSON-RPC over stdio (cross-platform lifecycle plugins)
- Command-manifest runtime (`plugin.json`) for stateless per-tool execution

Runtime rule:

- If `plugin.json` exists, `runtime` must be explicitly set to `jsonrpc` or `command`.

```go
type PluginInterface interface {
      Info() (PluginInfo, error)
      OnMessage(msg DiscordMessage) (bool, error)
      OnResponse(msg DiscordMessage, response string) error
      Shutdown() error
}
```

Optional tool provider interface:

```go
type ToolProvider interface {
      ListTools() ([]ToolSpec, error)
      ExecuteTool(name string, args map[string]interface{}) (string, error)
}
```

Optional pre-send provider interface:

```go
type BeforeSendProvider interface {
      BeforeSend(msg DiscordMessage, response string) (BeforeSendResult, error)
}
```

Reference implementations are available under `examples/plugins/`.

### 7. WebUI Layer (`internal/web`)

Gin-based web server:
- Basic authentication
- RESTful API endpoints
- Static file serving
- CORS support
- Security headers
- **Memory management endpoints**
- **Profile management endpoints**

> [!NOTE]
> **WebUI Behavior Notes:**
> - **Static Asset Discovery**: Web assets are searched from multiple candidate paths (`./web`, parent paths, and executable-relative paths) before fallback.
> - **Persistent Config Updates**: Configuration updates via API are validated, applied at runtime, and persisted using the config save path.
> - **Plugin Toggle Endpoints Active**: Plugin enable/disable endpoints call `PluginManager` and refresh plugin tools when runtime supports it.
> - **Uptime Source**: Stats uptime is derived from server start time (`time.Since(startTime)`).

## Data Flow

### Message Processing Flow

```
Discord Message
      �?      �?┌──────────────�?�?Rate Limit   �?──�?Reject if exceeded
�?Check        �?└──────────────�?      �?      �?┌──────────────�?�?Blacklist    �?──�?Reject if blacklisted
�?Check        �?    (config-based)
└──────────────�?      �?      �?┌──────────────�?�?Should       �?──�?Skip if not mentioned
�?Respond?     �?    and random check fails
└──────────────�?      �?      �?┌──────────────�?�?Fetch Short  �?�?Term Context �?──�?From Discord API
└──────────────�?      �?      �?┌──────────────�?�?Search Long  �?�?Term Memory  �?──�?Semantic search in Qdrant
└──────────────�?      �?      �?┌──────────────�?�?Get User     �?�?Profile      �?──�?From Qdrant
└──────────────�?      �?      �?┌──────────────�?�?Build        �?�?Context      �?──�?Short-term + memories + profile
└──────────────�?      �?      �?┌──────────────────────────────�?�?Prompt Optimization          �?�?                             �?�?┌──────────────────────────�?�?�?�?Static System Prompt     �?�?──�?Cacheable (persona + guidelines)
�?�?                         �?�?�?�?Dynamic User Context     �?�?──�?Appended to user message
�?�?(facts, memories, users) �?�?�?└──────────────────────────�?�?└──────────────────────────────�?      �?      �?┌──────────────�?�?AI Request   �?◀── With MCP tools
�?(with tools) �?└──────────────�?      �?      �?┌──────────────�?�?Tool Call?   �?──�?Execute tool ──�?New AI request
�?             �?          �?└──────────────�?          �?      �?                   �?      �?                   �?┌──────────────────────────────�?�?Send Response to Discord     �?└──────────────────────────────�?      �?      �?┌──────────────�?�?Increment    �?�?Msg Counter  �?└──────────────�?      �?      �?┌──────────────�?�?Trigger      �?──�?If threshold reached
�?Consolidate? �?└──────────────�?```

## Concurrency Model

### Goroutine Usage

| Component | Goroutines | Purpose |
|-----------|------------|---------|
| Discord Gateway | 1+ | WebSocket event handling |
| Message Processing | Per-message | Parallel message handling |
| Consolidation | Async | Memory processing |
| Web Server | Handler pool | HTTP request handling |
| Scheduled Tasks | Per-plugin | Plugin scheduled tasks |
| Bot Cache Cleanup | 1 | Rate limit cache cleanup |

### Synchronization

- `sync.RWMutex` for configuration access
- `sync.Mutex` for rate limit and cooldown maps
- Context-based cancellation for graceful shutdown
- Channel-based signaling for shutdown coordination

## Performance Considerations

### Memory Management

- **Short-term**: Only keeps last N messages (configurable)
- **Long-term**: Vector search in Qdrant (external service)
- **No local state**: Bot is completely stateless
- **Embeddings**: Cached when possible

### Qdrant Optimization

- **Vector dimensions**: 1536 (text-embedding-3-small)
- **Distance metric**: Cosine similarity
- **Collection sharding**: Automatic
- **Payload indexes**: For filtering (user_id, memory_type)

### AI API Efficiency

- **Prompt caching**: Static system prompt separated from dynamic context
  - Reduces token costs by up to 90% for repeated contexts
  - Tool schemas sorted alphabetically for stable cache keys
  - Cache hit rate logged for monitoring
- **Context windowing**: Only relevant messages + memories sent
- **Tool batching**: Multiple tools in single response
- **Streaming support**: For long responses
- **Vision on demand**: Images processed separately
- **Embeddings**: Batched when possible

## Error Handling

### Error Propagation

1. **Qdrant errors**: Logged, graceful degradation (no memories)
2. **AI API errors**: Retried with backoff, fallback responses
3. **Discord API errors**: Rate limit handling, reconnection
4. **Plugin errors**: Logged, doesn't crash bot

### Graceful Degradation

- WebUI failure doesn't affect bot
- Plugin failure doesn't affect core
- Qdrant failure falls back to short-term only
- AI failure returns error message

## Security

### Authentication

- HTTP Basic Auth for WebUI
- Token-based Discord authentication
- API key for AI provider

### Input Validation

- Message content sanitization
- Configuration validation at startup
- API request validation with Gin binding

### Rate Limiting

- Per-user cooldown
- Per-channel rate limit
- Global rate limit for AI API

## Extensibility Points

1. **Plugins**: Add new features without modifying core
2. **Tools**: Register new MCP tools for AI
3. **Middleware**: Add Gin middleware for WebUI
4. **Memory Types**: Add new memory types to Qdrant
5. **Handlers**: Register new Discord event handlers
