# Architecture

EZyapper is a Go single-binary Discord bot with vector memory, vision, plugins, and an optional admin WebUI. It's designed for small/medium guilds — single instance, single Qdrant, no fancy clustering.

This doc walks through the layers and how a message flows through them. For configuration details see [CONFIGURATION.md](CONFIGURATION.md). For the WebUI see [API.md](API.md). For plugins see [PLUGINS.md](PLUGINS.md).

---

## Big Picture

```
        Discord Gateway (WSS)
                │
                ▼
   ┌──────────────────────────────────────────┐
   │ Bot Layer  (internal/bot)                │
   │  • event handlers                        │
   │  • access control / rate limit           │
   │  • message buffer + processing tracking  │
   └─────┬──────────┬─────────┬───────────────┘
         │          │         │
         ▼          ▼         ▼
    ┌────────┐ ┌────────┐ ┌────────────┐
    │ AI     │ │ Memory │ │ Plugin/MCP │
    │ Layer  │ │ Layer  │ │ Layer      │
    └─┬──────┘ └─┬──────┘ └─┬──────────┘
      │          │          │
      ▼          ▼          ▼
 OpenAI-compat  Qdrant   external bins
   endpoints   (gRPC)    (stdio)
                            │
                            ▼
                         MCP servers
                      (stdio/sse/http)

    Optional: WebUI (internal/web)  ──→ same Memory + Plugin layers
```

The bot process holds five LLM client instances:

1. main chat client (`core.ai.*`)
2. embedding client (`memory_pipeline.embedding.*`, may inherit from `core.ai`)
3. consolidation chat client (`memory_pipeline.memory.consolidation.*`, may inherit)
4. consolidation image describer (the image modality, optionally overridden by `consolidation.vision` to route image descriptions to a cheaper model)
5. media processor (image/audio/video clients derived from `core.ai.modalities.*`, used at request time)

Each can have its own endpoint, key, model, and `extra_params`. They are built during boot and rebuilt when a hot reload changes their settings (see [CONFIGURATION.md](CONFIGURATION.md#hot-reload)).

---

## Bootstrap (`cmd/bot/main.go`)

Eleven sequential phases. Anything that fails is fatal — the bot does not start "partially."

| # | Phase | What happens |
|---|-------|--------------|
| 1 | CLI flags | `pflag` parses `--config` / `-c`. Without it, `config.yaml` is looked up in `.`, `./config` and `/etc/ezyapper`. |
| 2 | Config load | `config.Load(path)` reads YAML, applies env overrides (only for keys present in the file) and runs every validator. It fills in no values: there are no built-in defaults. All errors joined and reported at once. |
| 3 | Logger init | Zap logger with file rotation via lumberjack. The global `logger.L()` helper returns a no-op logger before this runs (so `init()`-free packages don't NPE). |
| 4 | Memory service | Builds the embedding client, Qdrant gRPC client, embedder, image describer, consolidator. If both `retrieval.top_k=0` and `consolidation.enabled=false`, returns a `NoopService` instead. Calls `RepairOrDeleteDamagedMemories()` (best-effort startup repair) and starts the maintenance worker. |
| 5 | Plugin manager | `plugin.NewManager(...)` with all the configured timeouts (startup, RPC, before-send, command, shutdown, disable). Plugins aren't loaded yet. |
| 6 | Config store | `atomic.Value` holding `*config.Config`. Used for copy-on-write reads from anywhere in the app. |
| 7 | Bot creation | `bot.New(...)` — wires every dependency into the `Bot` struct. Creates the Discord session (not connected), tool registry, MCP manager, decision service (or nil), rate limiter, media processor (when any modality is enabled). Registers Discord tools and any MCP/plugin tools available at construction time. |
| 8 | Bot start | Opens the Discord WebSocket and connects MCP + plugin servers. Plugins are scanned, loaded, and probed here. |
| 9 | Web server | If `operations.web.enabled`, builds a Discord adapter (channel/user/guild name lookups) and starts the HTTP listener. Otherwise no-op. |
| 10 | Cleanup goroutine | Periodic ticker calls `Bot.CleanupCache()` (rate limiter pruning + image description cache eviction) every `operations.runtime.cleanup_interval_min`. |
| 11 | Signal + shutdown | `signal.Notify` on SIGINT/SIGTERM. On signal: WebServer.Stop → Bot.Shutdown → Bot.Stop → PluginManager.Shutdown, each with its own deadline. |

There are **no `init()` functions anywhere in the codebase**. Every side effect is explicit and ordered in `main.go`.

---

## Components

### `internal/config` — Strict Typed Config

Viper-backed YAML loader with env overrides. The runtime `Config` struct has 13 sub-structs covering Discord, AI (incl. modalities), Decision, Embedding, Memory, Qdrant, Web, Logging, Plugins, MCP, Operations runtime, and access control.

Key invariants:

- No field has an in-code default. Every required key must be set in the file, and a key required by a feature must be set when that feature is enabled; the values in `examples/config.yaml.example` are suggestions. An absent optional section is off, a few zero values have a documented meaning, and some keys explicitly inherit another key when empty. See [CONFIGURATION.md](CONFIGURATION.md).
- Environment overrides (`EZYAPPER_...`) apply only to keys present in the file.
- The validators run in sequence; errors are collected via `errors.Join` and reported all at once.
- Updates persisted via `Save()` go through the same validation path before being written.

Hot reload is supported (`internal/hotreload`). With `operations.runtime.hot_reload.enabled: true` the config file is watched; on save it is loaded and validated, the changed keys are diffed, every affected component prepares its new state next to the running one, and then all of them commit or all roll back. WebUI config changes go through the same path. Details and the per-section behaviour are in [CONFIGURATION.md](CONFIGURATION.md#hot-reload).

### `internal/logger` — Zap Wrapper

Thin wrapper around `go.uber.org/zap` with lumberjack rotation. Exposes a global `L()` getter used by the rest of the codebase. Calling `L()` before `Init()` returns a no-op logger that prints a stderr warning, so the bot never crashes from a logger NPE.

### `internal/ai` — LLM Client Family

`internal/ai/client.go` is the OpenAI-compatible client. It handles chat completions (including media content parts such as `audio_url`/`video_url`, spliced into the JSON go-openai produces), embeddings, retries, and `extra_params` injection (via reflection — unknown keys are warned about, not silently ignored). Sub-packages:

- `internal/ai/tools` — Tool registry and the nine built-in Discord tools (server/channel info, member lookup, recent messages, reactions, threads, etc.). Schemas are sorted alphabetically and SHA-256 hashed for prompt-cache stability.
- `internal/media` — the image/audio/video/sticker modalities: describers, the ASR client, and the ID-keyed result cache. Media only ever reaches the reply model as text.
- `internal/ai/decision` — Optional LLM-based reply decision. Image-aware: includes attachment count in the prompt context. Has its own retry loop (100ms base delay vs 1s for the main client).
- `internal/ai/mcp` — MCP client manager. Connects to servers over three transports, `stdio`, `sse` and `http` (Streamable HTTP), lists/invokes tools, and filters secrets out of the env passed to stdio subprocesses. Configured `headers` are sent only by the `http` transport.

### `internal/bot` — Discord Glue

The `Bot` struct is the dependency-injection hub. It owns the Discord session, three views of the memory service (`MemoryStore`, `ProfileStore`, an extended `consolidationManager`), the tool registry, plugin/MCP managers, decision service, rate limiter, media processor, and several caches:

- **Channel message buffer** — recent messages per channel, drained during channel-scoped consolidation.
- **Channel consolidating** — dedup flag set while a channel batch is in flight.
- **Historical image description cache** — TTL-bounded cache of vision describer output keyed by image URL set, so a repeated message+image set doesn't re-describe.
- **Processing messages** — tracks in-flight messages by ID with phase (`Received → Deciding → Generating → Sending`) and a cancel func, so edits and deletes can preempt cleanly.

Event handlers live in `handlers*.go` files split by concern:

- `handlers.go` — registration + `onMessageCreate` dispatch
- `handlers_process.go` — the long pipeline (~290 lines) that runs per non-blacklisted message
- `handlers_response.go` — modality handling for the current message + completion calls
- `handlers_context.go` / `handlers_history.go` — dynamic-context + conversation-history string building
- `handlers_send.go` — Discord-side sending, chunking at 1900 chars, file uploads from `before_send` hooks
- `handlers_consolidation.go` — async consolidation trigger + recursion guard
- `handlers_events_misc.go` — message-edit/message-delete/guild-add/guild-remove
- `handlers_tools.go` — tiny adapter from `ai.ToolCall` → registry → result

### `internal/discordvis` — Channel Visibility

Decides which guild channels may be shown to the model by name. Discord obfuscates channels the bot cannot view: over the Gateway they arrive with the `CHANNEL_OBFUSCATED` flag (`1 << 17`) and the placeholder name `___hidden___`. Everything that lists channels or renders a channel name for the model goes through this package.

A channel is **hidden** when:

- it has the obfuscated flag, or its name is `___hidden___`; or
- the bot's computed permissions in it lack View Channel.

Rules:

- If permissions cannot be computed (no session state, unknown bot user, or an error from the state), the channel is **kept**: failing to compute permissions must not hide every channel. Obfuscated channels are still hidden.
- Threads are checked against their parent channel's permissions.
- Categories skip the permission check (Discord sends a category in full when any child is viewable); an obfuscated category is still hidden.

Where it applies:

- The prompt's channel map only contains visible channels, so a `<#id>` mention of a hidden or unknown channel (including deleted channels and channels in other servers) renders as `#unknown-channel`. The current channel, if Discord sent it obfuscated, is named `unknown-channel` and its topic is left out.
- The `list_channels` tool leaves hidden channels out.
- The `get_channel_info` tool returns only `id`, `type` and `guild_id` for a hidden channel, with `name: "unknown-channel"` and `accessible: false`.
- `Bot.GetChannelName` (used by the WebUI) returns the channel ID instead of the name of a hidden channel.

### `internal/memory` — Vector Store + Consolidation

Backed by Qdrant via gRPC. Long-term memory is stored in two tiers so that
changing the embedding model never loses data:

| Collection | Purpose | Notable |
|------------|---------|---------|
| `memories_source` | **Source of truth.** One point per memory with the text and every metadata field (user, guild, channel, type, keywords, confidence, importance, timestamps, access counts, decay). | No vectors at all; independent of the embedding model. |
| `memories_vec_<model>_<dim>` | Vector tier for the current embedding model, e.g. `memories_vec_baai_bge_m3_1024`. Same point IDs; holds the dense vector, the sparse `bm25_keywords` vector and only the payload fields search filters on (user, guild, channel, type, mentioned users/channels). | HNSW + Int8 scalar quantization. Long model names are shortened with a hash suffix. |
| `profiles` | One point per user — display name, traits, facts, preferences, interests, counters, timestamps | Not searched by vector; new installs create it without vectors. |
| `relationships` | User-to-user mention/reply/reaction links, deterministic UUID v5 IDs | Indexes on `user_a`, `user_b`, `type`, `weight`; no vectors on new installs. |

Search queries the vector tier, then loads the full records from the source
tier by ID in ranked order (vector points without a record are cleaned up).
Writes and deletes hit the source tier first, then the vector tier.

**Changing the embedding model:** on startup, if the vector collection for the
configured model does not exist or is missing records, a background job
re-embeds the source records into it (pages of 128, at most 4 concurrent
embedding requests, resumable, only missing records). Until it finishes,
semantic search only sees records that already have vectors. Vector
collections of other models are left in place; delete them manually when no
longer needed.

**Migration from the single `memories` collection:** if it exists and the
source tier is empty, startup copies its payloads (same IDs) into the source
tier, resuming if interrupted. Its vectors are reused only when the dimension
matches and re-embedding a sample reproduces them; otherwise they are
re-embedded. The old collection is never modified.

Highlights:

- **Hybrid retrieval** — dense + sparse via reciprocal rank fusion (`rrf_k`).
- **Media in consolidation** — the conversation text sent to the consolidation model includes image descriptions (`[Attached Image N: ...]`), audio transcripts or descriptions (`[Attached Audio N transcript: ...]`, `[Attached Audio N: ...]`) and video descriptions (`[Attached Video N: ...]`) for the enabled modalities, taken from the shared media cache or computed once.
- **Empty-embedding guard** — empty or whitespace-only text is never sent to the embedding API (which rejects it): the embedder returns `ErrEmptyEmbeddingInput`, search with a blank query returns no results, and the memory search query for the current message includes the text of its media, so a voice message is searched by its transcript. A memory record without a vector stays in the source tier and is embedded by the next vector sync; maintenance skips records without an embedding.
- **Scoring decay** — every memory has a `decay_category` and a per-type rate; `prune_decay_threshold` removes stale ones during maintenance.
- **Mention tracking** — extracted at consolidation time, stored in payload, queryable via `SearchByMentionedUser`. Supports cross-user recall ("what does the bot remember about Alice from when Bob mentioned her").
- **Channel scoping** — `SearchByChannel` returns memories tied to the current channel only, useful for channel-specific context.
- **Maintenance worker** — periodic merge (cosine threshold), summarize, prune. Cron-style trigger via `merge_cron_hour_utc` and `summarize_cron_day` so heavy LLM-driven maintenance doesn't run during peak chat hours.
- **Damaged-payload repair** — `RepairOrDeleteDamagedMemories()` runs at startup, scans up to 1000 points within 30s, fixes or deletes anything with corrupted payloads (a class of bug fixed historically by switching from `OverwritePayload` to `SetPayload`).
- **Async access counter** — `last_accessed_at` and `access_count` are updated through a buffered channel + 2s/50-item batcher, avoiding hot-path Qdrant writes on every retrieval.

Memory services implement a composite `Service` interface (`MemoryStore` + `ProfileStore` + `ConsolidationManager` + `RelationshipStore` + lifecycle methods).

### `internal/plugin` — External Tool Plugins

Plugins are external binaries. Two runtime modes:

- **JSON-RPC over stdio (`runtime: "jsonrpc"` or no manifest)** — persistent process; the host calls `info`, `on_message`, `after_decision`, `before_send`, `after_send`, `system_prompt`, `list_tools`, `execute_tool` and `shutdown`. A plugin whose transport died is restarted automatically, limited by `max_restarts` and `restart_cooldown_sec`.
- **Command (`runtime: "command"`)** — stateless; the bot invokes the binary per tool call with arguments mapped from the LLM's tool-call payload.

Manager handles discovery (scan `plugins_dir`), startup (probe `info`), tool registration, hook dispatch in priority order, enable/disable at runtime, and graceful shutdown. All timeouts are configurable — there are no hardcoded ones.

See [PLUGINS.md](PLUGINS.md) for the full contract.

### `internal/web` — Optional Admin Dashboard

Pure `net/http`. Server-rendered HTML with embedded templates, on-disk static assets, session cookies, CSRF protection, and a secrets-aware config editor. Off unless `operations.web.enabled` is true.

See [API.md](API.md) for routes, auth, and page data shapes.

### Smaller leaf packages

- `internal/ratelimit` — per-user cooldown + a fixed-window message count per user per channel.
- `internal/retry` — generic `Retry[T]` with functional options for backoff/jitter/max attempts.
- `internal/types` — the shared `DiscordMessage` type used as the wire format between layers.
- `internal/utils` — `SplitMessage` (chunking) and a few helpers.

---

## Message Flow

A typical user message in a non-blacklisted, non-rate-limited channel:

```
1. discordgo dispatches MessageCreate
2. onMessageCreate normalizes to types.DiscordMessage
3. Buffer the message in the channel-level consolidation buffer
4. Increment channel message counter; trigger consolidation goroutine if threshold hit
5. Fire plugin OnMessage hooks (in priority order)
   ↳ if any plugin returns false → stop
6. Register a ProcessingMessage (phase: Received) so edits/deletes can cancel it
7. Fetch recent messages (paginated Discord API call) for short-term context
8. ShouldRespond pipeline:
      own message? → false
      other bot? → policy check (ignore / context_only / drop)
      blacklisted user? → false
      channel not allowed? → false
      rate limited? → false
      mentioned? → true
      reply to bot? → true
      decision LLM enabled? → JSON classifier verdict
      otherwise → random reply%
   If true: plugin AfterDecision hooks may cancel the reply or inject prompt context.
9. Dispatch to processMessageWithoutImages or processMessage based on
   whether the image modality is enabled
10. processMessageCore (phase: Generating):
      a. Image extraction; image describe mode invokes the describer with cache
      b. 3-way parallel memory search:
         - user memories (by author)
         - mentioned-user memories (anyone @-pinged)
         - channel-scoped memories
      c. Merge + dedup by ID; PostProcessResults applies scoring weights
      d. Profile fetch (display name, traits, facts) for richer context
      e. Build the static system prompt (instructions + static plugin sections)
      f. Build the user prompt: server, channel, context, participants,
         plugin blocks, user profile, key facts, current message
      g. generateResponse → modalities: each modality's model turns media
           into text (descriptions, transcripts; cached by attachment ID),
           added to the messages → text-only base model + tools
      h. executeToolLoop runs tool calls (Discord + plugin + MCP) until no more
         calls or MaxToolIterations hit
11. Phase: Sending. BeforeSend plugin hooks may rewrite the response, attach files, or cancel sending
12. sendResponse:
      - splits into chunks at ~1900 chars
      - reply vs send based on mention/reference
      - uploads any plugin-provided files
      - adds the bot's own reply back into the channel buffer
13. Plugin AfterSend hooks with the final text and sent message IDs
14. SetCooldown for the user (applies in every channel)
```

Cancellation: every step lives under a per-message context. Edits cancel the in-flight processing; deletes trigger cleanup of the partial state.

Channel-level consolidation runs in its own goroutine, drains the buffer, sends one batched LLM call covering all participating users, then upserts memories + relationship updates. There's a recursion guard (depth ≤ 5) so a fast-talking channel doesn't trigger an unbounded chain of consolidations.

---

## Concurrency Model

- **Discord gateway** — owned by `discordgo`, single goroutine for the WS read loop.
- **Per-message processing** — one goroutine per inbound message. Limit is implicit (channel buffer + Discord's pacing).
- **Consolidation** — one goroutine per channel batch. Dedup flag prevents two from running concurrently for the same channel.
- **Memory access counter worker** — one goroutine, drains the access queue every 2s or 50 items.
- **Memory maintenance worker** — one goroutine, ticker-driven; runs merge/summarize/prune on schedule.
- **Plugin RPC** — one persistent stdio process per `jsonrpc` plugin, plus per-call subprocess for `command` plugins.
- **Web server** — `http.Server` handler pool.

Synchronization:

- `atomic.Value` for the live config snapshot.
- `sync.RWMutex` for read-heavy caches (channel buffer, image description cache, processing-message map).
- `sync.WaitGroup` to wait out in-flight goroutines on shutdown.
- Per-call contexts derived from a root context cancelled at `Stop()`.

---

## Performance Notes

- **Embeddings are batched implicitly** via the Qdrant access-update worker, but generation itself is per-call. Heavy chats with retrieval enabled push load onto your embedding endpoint — pick a fast/cheap model.
- **Tool schemas are cached and SHA-256 hashed** so prompt caches on provider side stay warm. Don't mutate tool definitions at runtime.
- **System prompt is static, dynamic context lives in user message** so the static prefix is cache-friendly. See [PROMPT_OPTIMIZATION.md](PROMPT_OPTIMIZATION.md).
- **Switching embedding models re-embeds in the background.** Memory text lives in the model-independent source tier, so nothing is lost; the new model's vector collection fills up after startup.
- **Maintenance is deliberate, not aggressive.** `max_maintenance_llm_calls_per_day` caps the consolidation/summarize budget so memory upkeep doesn't blow your AI budget.

---

## Error Handling

| Failure | Behavior |
|---------|----------|
| Config invalid | Joined errors printed to stderr; bot exits non-zero. |
| Qdrant unavailable | Memory ops return errors; calling code logs and continues without memory enrichment. |
| AI API error | Retried per `retry_count` with exponential backoff; final failure surfaces as a logged error and (often) a brief Discord reply explaining the failure. |
| Plugin RPC timeout | Marked dead, transport closed. The next hook or tool call that finds it dead skips it and triggers an automatic restart in the background; after `max_restarts` consecutive failed restarts, the next attempt waits `restart_cooldown_sec`. A failing `on_message` plugin is skipped, never blocking the message. |
| Discord 5xx | discordgo handles its own retries; we don't wrap them. |
| Damaged Qdrant payload | Detected at startup; either repaired (set defaults) or deleted, with a warning log. |

---

## Security

- Discord token, AI API keys, and Qdrant API keys are read from config (or env via `EZYAPPER_` prefix). Use environment variables in production — keep secrets out of `config.yaml`.
- WebUI uses HTTP Basic Auth + session cookies + CSRF. Put a TLS-terminating reverse proxy in front of it.
- Plugin processes inherit only a filtered env (we strip variables matching TOKEN/SECRET/KEY/PASSWORD/etc.). Plugins still need an explicit way to receive their own credentials — typically through a per-plugin config file or a deliberately whitelisted env var.

---

## Extension Points

- **New Discord tool** — add to `internal/ai/tools/discord.go`'s `RegisterTools()`.
- **New plugin** — implement `plugin.Interface` and call `plugin.Serve()` in `main()`. See [PLUGINS.md](PLUGINS.md).
- **New MCP server** — add a `stdio`, `sse` or `http` entry under `operations.mcp.servers`; hot reload connects it (or restart).
- **New memory type** — extend the `MemoryType` enum + the consolidation prompt to cover it.
- **New WebUI page** — add a handler + template + nav entry. The middleware chain is shared.

---

## Code Layout (for orientation)

```
cmd/bot/main.go           # entry point, 11-phase bootstrap
internal/
  ai/                     # LLM client, retry, ExtraParams reflection
    client.go             # main openai-compat client
    tools/                # registry + 9 Discord tools

    decision/             # optional LLM reply classifier
    mcp/                  # MCP client manager
  bot/                    # Discord glue, message pipeline, caches
  config/                 # strict YAML config + validators
  discordvis/             # which channels the model may see by name
  hotreload/              # config file watcher, diff, prepare/commit
  logger/                 # Zap wrapper, global L()
  media/                  # image/audio/video/sticker to text, shared media cache
  memory/                 # Qdrant store, consolidation, relationships, maintenance
  plugin/                 # JSON-RPC stdio runtime + command runtime
    server/               # helper for plugin authors (Serve())
  ratelimit/              # per-user cooldown + per-user-per-channel limit
  retry/                  # generic Retry[T] with backoff
  types/                  # shared DiscordMessage type
  utils/                  # SplitMessage etc.
  web/                    # optional admin dashboard
examples/
  config.yaml.example     # canonical config
  .env.example            # env-var reference
  plugins/                # 11 example plugins (Go, Zig, C, Java)
plugins/                  # runtime plugin binaries (gitignored, build output)
```
