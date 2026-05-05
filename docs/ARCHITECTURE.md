# Architecture Overview

EZyapper is designed with a modular, layered architecture optimized for high concurrency and extensibility. It uses the **Qdrant vector database** for long-term memory storage, keeping runtime state minimal outside caches.

## System Architecture

```
Discord Gateway
      |
Bot Layer (internal/bot)
      |-> AI Layer (internal/ai) -> LLM providers
      |-> Memory Layer (internal/memory) -> Qdrant Vector DB
      |-> Plugin Layer (internal/plugin)
WebUI (internal/web) -> HTTP API + dashboard
```

## Core Components

### 1. Configuration Layer (`internal/config`)

Uses Viper for configuration management:
- YAML configuration files (`schema_version: 3`)
- Environment variable overrides (prefix: `EZYAPPER_`, path uppercased with `_`)
- Validation at startup (no defaults)
- No hot-reload (requires restart)

Config file shape:

```yaml
schema_version: 3

core:
  discord: {}
  ai: {}
  decision: {}

memory_pipeline:
  embedding: {}
  memory: {}
  qdrant: {}

access_control:
  blacklist: {}
  whitelist: {}

operations:
  web: {}
  logging: {}
  plugins: {}
  mcp: {}
  runtime: {}
```

Runtime config is flattened into `Config` fields (Discord, AI, Embedding, Memory, Qdrant, Web, Logging, Plugins, MCP, Decision, Operations, Blacklist, Whitelist).

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
- Profiles are created during consolidation when missing, then upserted to Qdrant.

**Record Struct:**
```go
type Record struct {
  ID           string
  UserID       string
  GuildID      string
  ChannelID    string
  MemoryType   Type       // summary, fact, episode
  Content      string
  Summary      string
  Embedding    []float32  // Vector size matches embedding model
  Keywords     []string
  Metadata     map[string]any
  Confidence   float64
  MessageRange [2]int
  CreatedAt    time.Time
  UpdatedAt    time.Time
  AccessCount  int
}
```

**Profile Struct:**
```go
type Profile struct {
  UserID             string
  DisplayName        string
  Traits             []string
  Facts              map[string]string
  Preferences        map[string]string
  Interests          []string
  LastSummary        string
  PersonalitySummary string
  MessageCount       int
  MemoryCount        int
  FirstSeenAt        time.Time
  LastActiveAt       time.Time
  LastConsolidatedAt time.Time
  Embedding          []float32
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
- Uses the embedding model from config
- Vector size must match `memory_pipeline.qdrant.vector_size`

### 4. AI Layer (`internal/ai`)

#### Client (`client.go`)
- OpenAI-compatible API client
- Custom base URL support (Qwen, DeepSeek, etc.)
- Vision (image analysis) support
- Streaming responses
- Tool calling integration
- **NEW**: Embedding generation

#### Tools (`tools.go`)
Built-in Discord tools and tool registry:
- Server info retrieval
- Channel management
- User lookups
- Message operations
- Reaction handling
- **Tool schema caching with stable alphabetical ordering**
- **Schema hash generation for cache key tracking**

#### MCP Client (`mcp/mcp.go`)
- Connects to external MCP servers configured under `operations.mcp`
- Discovers tools and executes remote calls

#### Prompt Formatting and Cache Hygiene
- System prompt formatting in `config.FormatSystemPrompt`
- Dynamic context assembly in `internal/bot/handlers_context.go`
- User context appended to the user message to keep the system prompt stable

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

net/http-based server:
- Basic authentication with session cookies
- CSRF protection
- REST API endpoints and HTML templates
- Static file serving
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

1. Discord event received.
2. Rate limit, blacklist/whitelist, and decision checks.
3. Fetch short-term context from Discord if needed.
4. Retrieve relevant memories and user profile (when enabled).
5. Build dynamic context and format the system prompt.
6. Send AI request with tools; execute tool calls as needed.
7. Send response to Discord.
8. Increment counters and trigger consolidation when thresholds are met.

## Concurrency Model

### Goroutine Usage

| Component | Goroutines | Purpose |
|-----------|------------|---------|
| Discord Gateway | 1+ | WebSocket event handling |
| Message Processing | Per-message | Parallel message handling |
| Consolidation | Background worker | Memory processing |
| Web Server | Handler pool | HTTP request handling |

### Synchronization

- `atomic.Value` for config snapshots
- `sync.Mutex`/`sync.RWMutex` for caches and counters
- Context-based cancellation for graceful shutdown
- `sync.WaitGroup` for shutdown coordination

## Performance Considerations

### Memory Management

- **Short-term**: Only keeps last N messages (configurable)
- **Long-term**: Vector search in Qdrant (external service)
- **Runtime state**: Minimal in-memory caches only
- **Embeddings**: Vector size must match the configured model

### Qdrant Optimization

- **Vector dimensions**: Set by config and must match the embedding model
- **Distance metric**: Cosine similarity (configured at collection creation)
- **Payload indexes**: `user_id`, `memory_type` (memories collection)

### AI API Efficiency

- **Prompt caching hygiene**: Static system prompt separated from dynamic context; tool schemas sorted alphabetically for stable cache keys; schema hash available for cache routing
- **Context windowing**: Only relevant messages + memories sent
- **Vision mode**: Controls whether images require extra calls

## Error Handling

### Error Propagation

1. **Configuration errors**: Collected and reported at startup, then exit
2. **Qdrant/AI/plugin errors**: Returned to callers and logged
3. **Retries**: Use configured backoff and limits where applicable

## Security

### Authentication

- HTTP Basic Auth for WebUI
- Token-based Discord authentication
- API key for AI provider

### Input Validation

- Configuration validation at startup
- HTTP handlers validate request payloads

### Rate Limiting

- Per-user cooldown
- Per-channel rate limit

## Extensibility Points

1. **Plugins**: Add new features without modifying core
2. **Tools**: Register new tools in `internal/ai/tools` or via plugins/MCP
3. **WebUI**: Add handlers or middleware in `internal/web`
4. **Memory Types**: Add new memory types to Qdrant
5. **Handlers**: Register new Discord event handlers
