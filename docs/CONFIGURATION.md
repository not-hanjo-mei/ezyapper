# Configuration

EZyapper is configured via a single YAML file (default: `config.yaml`). Pass a custom path with `-config`:

```bash
./ezyapper -config /path/to/config.yaml
```

> [!CAUTION]
> **Strict config — no defaults.** Every required field must be set. Missing one and the bot refuses to start. Validation errors are batched: if you have ten missing fields, you'll see all ten in one go.

> [!IMPORTANT]
> **No hot-reload.** Config edits require a restart. The WebUI's `/config` page applies updates to the running process and persists them, but anything read once at startup (e.g., the Discord session) won't pick up the change until you restart.

> [!WARNING]
> **WebUI is experimental.** Keep `operations.web.enabled: false` for production unless you're explicitly debugging.

---

## Top-level Shape

```yaml
schema_version: 4

core:
  discord: { ... }
  ai: { ... }
  decision: { ... }

memory_pipeline:
  embedding: { ... }
  memory: { ... }
  qdrant: { ... }

access_control:
  blacklist: { ... }
  whitelist: { ... }

operations:
  web: { ... }
  logging: { ... }
  plugins: { ... }
  mcp: { ... }
  runtime: { ... }
```

`schema_version` must be `4`. Older configs will be rejected at startup; the schema number bumps when fields are reorganized.

For a complete annotated example, see `examples/config.yaml.example`. For the JSON Schema (editor autocomplete), see `examples/config.schema.json`.

---

## Environment Variable Overrides

Every field can be overridden by an environment variable. Prefix `EZYAPPER_`, then convert the YAML path to uppercase with dots replaced by underscores.

```
core.discord.token                            → EZYAPPER_CORE_DISCORD_TOKEN
core.ai.api_key                               → EZYAPPER_CORE_AI_API_KEY
core.ai.vision.mode                           → EZYAPPER_CORE_AI_VISION_MODE
memory_pipeline.embedding.model               → EZYAPPER_MEMORY_PIPELINE_EMBEDDING_MODEL
memory_pipeline.qdrant.host                   → EZYAPPER_MEMORY_PIPELINE_QDRANT_HOST
memory_pipeline.memory.retrieval.top_k        → EZYAPPER_MEMORY_PIPELINE_MEMORY_RETRIEVAL_TOP_K
operations.web.enabled                        → EZYAPPER_OPERATIONS_WEB_ENABLED
operations.plugins.plugins_dir                → EZYAPPER_OPERATIONS_PLUGINS_PLUGINS_DIR
```

The full nested path is required — short forms like `EZYAPPER_DISCORD_TOKEN` (without `CORE_`) do **not** map to anything in `schema_version: 4` and are silently ignored.

---

## `core`

### `core.discord`

| Field | Type | Notes |
|-------|------|-------|
| `token` | string | Discord bot token. **Always required.** |
| `bot_name` | string | Display name used in prompts and logs. |
| `own_bot_id` | string | The bot's user ID. Required when memory retrieval (`top_k > 0`) or consolidation is enabled. |
| `reply_percentage` | float (0.0–1.0) | Random reply probability when not mentioned. |
| `cooldown_seconds` | int (>0) | Minimum gap between replies for the same user+channel. |
| `max_responses_per_minute` | int (>0) | Hard cap across all channels. |
| `rate_limit.reset_period_seconds` | int (>0) | Sliding window for the per-user rate limit. |
| `other_bot_policy` | enum | `"ignore"` / `"context_only"` / `"full"` — how to treat messages from other bots. See below. |
| `consolidation_timeout_sec` | int (>0) | Timeout for consolidation-triggered response generation. |
| `typing_indicator_interval_sec` | int (>0) | Refresh interval for typing indicator. |
| `long_response_delay_ms` | int (>0) | Pause before sending a response flagged as "long" (humanizing). |
| `image_cache_ttl_min` | int (>0) | TTL for cached Discord image data. |
| `image_cache_max_entries` | int (>0) | Max entries in the in-memory image cache. |

**`other_bot_policy`** controls how the bot treats messages from *other* bots (its own messages are always self-filtered):

- `"ignore"` — skip entirely. No reply, no memory, no enrichment.
- `"context_only"` — include in memory pipeline (retrieval + consolidation), but never reply.
- `"full"` — treat other bots like humans. Replies + memory.

Anything other than `"full"` produces a startup warning so you remember why your bot doesn't talk back to bots.

### `core.ai`

The main chat model. All sub-fields here are required.

| Field | Type | Notes |
|-------|------|-------|
| `api_base_url` | string | OpenAI-compatible endpoint. |
| `api_key` | string | API key for the provider. |
| `model` | string | Chat model name. |
| `max_tokens` | int (>0) | Max tokens in completion. |
| `temperature` | float (0.0–2.0) | Higher = more creative. |
| `retry_count` | int (>0) | Retries for transient API errors. |
| `timeout` | int (>0, seconds) | Per-request timeout. The pipeline timeout is auto-derived from this. |
| `extra_params` | map | Optional. Forwarded to the API via reflection. Unknown keys log a warning at runtime. |
| `system_prompt` | string | The system prompt template. Supports `{BotName}`, `{AuthorName}`, `{ServerName}`, `{GuildID}`, `{ChannelID}`. |
| `max_tool_iterations` | int (>0) | Cap on tool-calling loops per turn. Prevents infinite tool-call cycles. |
| `max_image_bytes` | int (>0) | Maximum bytes for an image fetched from Discord CDN. |
| `require_image_content_type` | bool | If true, image responses without a `Content-Type` header are rejected. |

#### `core.ai.vision`

| Field | Type | Required when | Notes |
|-------|------|---------------|-------|
| `mode` | enum | always | `"text_only"` / `"hybrid"` / `"multimodal"`. See [VISION.md](VISION.md). |
| `model` | string | mode ≠ text_only | Vision model. |
| `description_prompt` | string | mode = hybrid | Prompt fed to the vision model for image descriptions. |
| `base64` | bool | always | If false, images are sent as URLs (may not work with local endpoints — emits a startup warning). |
| `max_images` | int (>0) | always | Max images processed per message. |
| `api_base_url` | string | mode ≠ text_only | Defaults to `core.ai.api_base_url` if empty. |
| `api_key` | string | mode ≠ text_only | Defaults to `core.ai.api_key` if empty. |
| `max_tokens` | int (>0) | mode ≠ text_only | Defaults to `core.ai.max_tokens` if empty. |
| `temperature` | float (0.0–2.0) | mode ≠ text_only | |
| `retry_count` | int (>0) | mode ≠ text_only | |
| `timeout` | int (>0) | mode ≠ text_only | |
| `extra_params` | map | optional | |

The "defaults to `core.ai.X` if empty" pattern means you can leave the fields blank to inherit, or override them per-component to route vision through a different (cheaper) endpoint or model.

### `core.decision`

The optional LLM-based reply classifier. When enabled, every non-trivial message is sent to a (typically small/fast) model that returns `{"should_respond": ..., "reason": ..., "confidence": ...}`. The bot uses this to decide whether to reply.

| Field | Type | Required when | Notes |
|-------|------|---------------|-------|
| `enabled` | bool | always | If false, all fields below are skipped. |
| `model` | string | enabled | Decision model (use a fast one). |
| `api_base_url` | string | enabled | |
| `api_key` | string | enabled | |
| `system_prompt` | string | enabled | Decision instructions. Supports `{BotName}`. Must instruct the model to return strict JSON. |
| `max_tokens` | int (>0) | enabled | |
| `temperature` | float (0.0–2.0) | enabled | Low values (0.0–0.3) recommended. |
| `retry_count` | int (≥0) | enabled | |
| `timeout` | int (>0) | enabled | Per-request timeout. |
| `extra_params` | map | optional | |

If the decision LLM fails (timeout, error, invalid JSON), the bot falls back to `core.discord.reply_percentage`. So a decision-service outage doesn't kill the bot — it just makes it less smart about when to chime in.

---

## `memory_pipeline`

The whole memory subsystem can be turned **off** by setting `memory.retrieval.top_k: 0` and `memory.consolidation.enabled: false`. In that case, embedding, qdrant, and consolidation fields are skipped during validation.

### `memory_pipeline.embedding`

Inherits from `core.ai.api_base_url` / `core.ai.api_key` if empty.

| Field | Type | Required when | Notes |
|-------|------|---------------|-------|
| `api_base_url` | string | optional | Inherits from `core.ai`. |
| `api_key` | string | optional | Inherits from `core.ai`. |
| `model` | string | memory enabled | Embedding model. **The vector size of this model must match `qdrant.vector_size`.** |
| `retry_count` | int (≥0) | memory enabled | |
| `timeout` | int (>0) | memory enabled | |
| `extra_params` | map | optional | |

### `memory_pipeline.memory`

The biggest subsection. Skim it in two passes — first the basics, then the maintenance/scoring tail.

**Core memory behavior**

| Field | Type | Notes |
|-------|------|-------|
| `consolidation_interval` | int (>0) | Trigger consolidation every N bot-handled messages. |
| `short_term_limit` | int (>0) | Recent Discord messages to fetch for short-term context. |
| `max_paginated_limit` | int (>0) | Max page size when paginating older messages. |
| `max_mentioned_users_per_memory` | int (≥0) | Cap on mentioned users tracked per memory entry. |
| `memory_strength_multiplier` | float (>0, ≤100) | Importance scoring multiplier. Presets: `0.0001` extreme, `0.1` goldfish, `1.0` human, `>1.0` elephant. |

**`long_term_memory`**

| Field | Type | Notes |
|-------|------|-------|
| `long_term_memory.enabled` | bool | When true, memories are consolidated into long-term storage. |

**`retrieval`**

| Field | Type | Notes |
|-------|------|-------|
| `retrieval.top_k` | int (≥0) | Memories returned per query. **`0` disables retrieval.** |
| `retrieval.min_score` | float (0.0–1.0) | Minimum similarity threshold. |
| `retrieval.include_channel_memories` | bool | Whether to include channel-scoped memories alongside user memories. |
| `retrieval.max_mentioned_memories` | int (≥0) | Memories that mention the sender (0 = use top_k). |
| `retrieval.max_channel_memories` | int (≥0) | Memories scoped to current channel (0 = use top_k). |

**`consolidation`** — same inheritance rules as the vision sub-config.

| Field | Type | Required when | Notes |
|-------|------|---------------|-------|
| `consolidation.enabled` | bool | always | Skips remaining fields when false. |
| `consolidation.model` | string | enabled | Inherits from `core.ai.model` if empty. |
| `consolidation.api_base_url` | string | enabled | Inherits from `core.ai`. |
| `consolidation.api_key` | string | enabled | Inherits from `core.ai`. |
| `consolidation.max_tokens` | int (>0) | enabled | |
| `consolidation.temperature` | float (0.0–2.0) | enabled | |
| `consolidation.retry_count` | int (>0) | enabled | |
| `consolidation.timeout` | int (>0) | enabled | |
| `consolidation.extra_params` | map | optional | |
| `consolidation.system_prompt` | string | enabled | The extraction prompt. Templates for each `other_bot_policy` value live in `examples/config.yaml.example` — start from one of those. |
| `consolidation.memory_search_limit` | int (>0) | always | How many existing memories to load for dedup during consolidation. Validated even when consolidation is disabled. |
| `consolidation.vision.*` | submap | optional | Mirrors `core.ai.vision.*`. Inherits anything left empty. Use this to route consolidation vision through a cheaper model. |

**Maintenance**

The maintenance worker runs cron-style; these settings shape its behavior.

| Field | Type | Notes |
|-------|------|-------|
| `maintenance_interval_sec` | int (>0) | Worker tick interval. |
| `merge_cron_hour_utc` | int (0–23) | Hour to run merge. Pick an off-peak hour. |
| `summarize_cron_day` | int (0–6) | Day to run summarize (0 = Sunday). |
| `merge_cosine_threshold` | float (0.0–1.0) | Memories closer than this are candidates for merging. |
| `prune_decay_threshold` | float (0.0–1.0) | Memories whose decayed score drops below this are eligible for pruning. |
| `prune_age_days` | int (>0) | Pruning ignores memories newer than this. |
| `relationship_prune_age_days` | int (>0) | Same idea for the relationship graph. |
| `max_maintenance_llm_calls_per_day` | int (≥0) | Hard cap on consolidation/summarize LLM calls per day. Keeps the budget under control. |
| `entropy_min_unique_word_ratio` | float (0.0–1.0) | Entropy gate: messages with too few unique words are filtered before consolidation. |

**Scoring & retrieval tuning**

| Field | Type | Notes |
|-------|------|-------|
| `decay_rates.fact` | float (>0) | Lower = decays slower. |
| `decay_rates.episode` | float (>0) | |
| `decay_rates.interest` | float (>0) | |
| `decay_rates.summary` | float (>0) | |
| `scoring.importance_weight` | float (≥0) | Final-score blending weight. |
| `scoring.recency_weight` | float (≥0) | |
| `scoring.access_weight` | float (≥0) | |
| `scoring.confidence_weight` | float (≥0) | |
| `rrf_k` | int (>0) | Reciprocal rank fusion constant for hybrid (dense + sparse BM25) search. |
| `context_max_memories` | int (>0) | Max memories included in the dynamic context per request. |

### `memory_pipeline.qdrant`

| Field | Type | Required when | Notes |
|-------|------|---------------|-------|
| `host` | string | memory enabled | Qdrant host. |
| `port` | int (>0) | memory enabled | gRPC port (default 6334). |
| `api_key` | string | optional | For Qdrant Cloud or secured instances. |
| `vector_size` | int (>0) | memory enabled | Vector dimensions. **Must match your embedding model** (cross-checked at startup for known OpenAI models). |
| `retry_base_delay_ms` | int (>0) | memory enabled | Exponential backoff base. |
| `retry_max_delay_ms` | int (>0) | memory enabled | Backoff ceiling. |
| `max_retries` | int (>0) | memory enabled | Retries per Qdrant op. |

#### Vector dimensions cheat sheet

| Model | Size |
|-------|------|
| `text-embedding-3-small`, `text-embedding-ada-002` | 1536 |
| `text-embedding-3-large` | 3072 |
| MiniLM, M3E variants | 384–1024 |
| BGE | 1024 |

If you switch embedding models to one with a different dimension, you must delete the existing Qdrant collections — they're created with a fixed size. See "Switching embedding models" at the bottom of this doc.

---

## `access_control`

Blacklist and whitelist are mutually exclusive **per category** — you can't blacklist channels and also whitelist channels in the same config.

### `access_control.blacklist`

```yaml
access_control:
  blacklist:
    users: ["111111111111111111"]
    guilds: []
    channels: ["222222222222222222"]
```

Bot ignores anything matching a blacklist entry. Default mode is "responding everywhere except blacklisted spots."

### `access_control.whitelist`

```yaml
access_control:
  whitelist:
    users: []
    guilds: []
    channels: ["333333333333333333"]
```

When a category has whitelist entries, the bot **only** responds in/from those targets. Empty whitelist = no whitelist applied for that category.

---

## `operations`

### `operations.web`

The optional admin dashboard. Off by default. See [API.md](API.md) for what it actually does.

| Field | Type | Required when | Notes |
|-------|------|---------------|-------|
| `enabled` | bool | always | If false, all fields below are skipped. |
| `port` | int (>0) | enabled | HTTP listener port. |
| `username` | string | enabled | |
| `password` | string | enabled | **Don't reuse `changeme123`.** |
| `memories_page_limit` | int (>0) | enabled | Memories listed per page. |
| `session_ttl_min` | int (>0) | enabled | Cookie session lifetime. |
| `session_cleanup_interval_min` | int (>0) | enabled | Background sweep interval. |
| `stats_query_timeout_sec` | int (>0) | enabled | Dashboard stats query timeout. |
| `log_default_lines` | int (>0) | enabled | Default `?lines=` value on `/logs`. |
| `log_max_lines` | int (>0) | enabled | Cap on `?lines=`. |
| `log_max_read_bytes` | int (>0) | enabled | Cap on bytes read from the log file. |

### `operations.logging`

| Field | Type | Notes |
|-------|------|-------|
| `level` | string | One of `debug`/`info`/`warn`/`error`. Validated via `zapcore.ParseLevel`. |
| `file` | string | Log file path. |
| `max_size` | int (>0) | Megabytes before rotation. |
| `max_backups` | int (>0) | Rotated files kept. |
| `max_age` | int (>0) | Days before rotated logs are deleted. |

### `operations.plugins`

See [PLUGINS.md](PLUGINS.md) for what each timeout actually does.

| Field | Type | Required when | Notes |
|-------|------|---------------|-------|
| `enabled` | bool | always | If false, plugin-specific fields are skipped, but `default_tool_timeout_ms` is still validated. |
| `plugins_dir` | string | enabled | Directory scanned at startup. |
| `default_tool_timeout_ms` | int (≥0) | always | Fallback per-tool timeout. **0 will cause tool calls to hang** — emits a warning. |
| `startup_timeout_sec` | int (>0) | enabled | `info` probe timeout. |
| `rpc_timeout_sec` | int (>0) | enabled | Default JSON-RPC call timeout. |
| `before_send_timeout_sec` | int (>0) | enabled | `before_send` hook timeout. |
| `command_timeout_sec` | int (>0) | enabled | Default command-runtime tool timeout. |
| `shutdown_timeout_sec` | int (>0) | enabled | Graceful shutdown grace period. |
| `disable_timeout_sec` | int (>0) | enabled | Grace period for runtime disable via WebUI. |

### `operations.mcp`

| Field | Type | Required when | Notes |
|-------|------|---------------|-------|
| `enabled` | bool | always | If false, `servers` is not validated. |
| `servers[]` | list | enabled | At least one entry. |

Each `servers[]` entry:

| Field | Type | Required when | Notes |
|-------|------|---------------|-------|
| `name` | string | mcp enabled | Identifier used in logs and tool routing. |
| `type` | string | mcp enabled | `"stdio"` or `"sse"`. |
| `command` | string | type=stdio | Executable name or path. |
| `args` | list | optional | Arguments. |
| `env` | map | optional | Extra env vars passed to the subprocess (filtered for secrets). |
| `url` | string | type=sse | Endpoint URL. |

```yaml
operations:
  mcp:
    enabled: true
    servers:
      - name: datetime
        type: stdio
        command: npx
        args: ["-y", "@pinkpixel/datetime-mcp"]
        env:
          TZ: "UTC"
      - name: remote-tool
        type: sse
        url: "http://localhost:8080/sse"
```

### `operations.runtime`

| Field | Type | Notes |
|-------|------|-------|
| `shutdown_timeout_sec` | int (>0) | Total grace period for graceful shutdown. |
| `cleanup_interval_min` | int (>0) | Periodic cleanup ticker (rate limiter pruning, image cache eviction, etc.). |

---

## AI Provider Quickstarts

Drop one of these into `core.ai`:

**OpenAI**

```yaml
core:
  ai:
    api_base_url: "https://api.openai.com/v1"
    api_key: "sk-..."
    model: "gpt-4o-mini"
    vision:
      mode: "multimodal"
      model: "gpt-4o"
```

**DeepSeek**

```yaml
core:
  ai:
    api_base_url: "https://api.deepseek.com/v1"
    api_key: "sk-..."
    model: "deepseek-chat"
    vision:
      mode: "text_only"
```

DeepSeek doesn't currently ship a vision model on its OpenAI-compatible surface; pair it with `text_only` or wire `core.ai.vision` to a different provider.

**Qwen (Alibaba Cloud)**

```yaml
core:
  ai:
    api_base_url: "https://dashscope.aliyuncs.com/compatible-mode/v1"
    api_key: "sk-..."
    model: "qwen-plus"
    vision:
      mode: "multimodal"
      model: "qwen-vl-plus"
```

**Azure OpenAI**

```yaml
core:
  ai:
    api_base_url: "https://YOUR_RESOURCE.openai.azure.com"
    api_key: "your-azure-key"
    model: "gpt-4o-mini"
```

**Local (Ollama / LM Studio)**

```yaml
core:
  ai:
    api_base_url: "http://localhost:1234/v1"
    api_key: "not-needed"
    model: "local-model"
    vision:
      mode: "text_only"
      base64: false
```

Set `vision.base64: false` when the local endpoint can't fetch URLs from outside its network.

---

## Mixing Endpoints

Each LLM-using component can have its own endpoint, key, and model. Empty fields fall back to `core.ai.*`. So you can run, say, the main chat on Qwen, embeddings on a local model, decisions on a fast cheap model, and consolidation on `gpt-4o-mini`:

```yaml
core:
  ai:
    api_base_url: "https://dashscope.aliyuncs.com/compatible-mode/v1"
    api_key: "sk-qwen"
    model: "qwen-plus"
  decision:
    enabled: true
    api_base_url: "https://api.openai.com/v1"
    api_key: "sk-openai"
    model: "gpt-4o-mini"

memory_pipeline:
  embedding:
    api_base_url: "http://localhost:8000/v1"
    api_key: "local"
    model: "bge-small-en"
  memory:
    consolidation:
      enabled: true
      api_base_url: "https://api.openai.com/v1"
      api_key: "sk-openai"
      model: "gpt-4o-mini"
```

Just make sure `qdrant.vector_size` matches whatever your embedding model produces.

---

## System Prompt Variables

`core.ai.system_prompt` and `core.decision.system_prompt` support these:

| Variable | Replaced with |
|----------|---------------|
| `{BotName}` | `core.discord.bot_name` |
| `{AuthorName}` | The current message author's display name |
| `{ServerName}` | The Discord server (guild) name |
| `{GuildID}` | Guild ID |
| `{ChannelID}` | Channel ID |

For dates and times, use the `get_current_datetime` tool from the `datetime` plugin instead of templating `{Time}` into the prompt — embedding a timestamp invalidates provider-side prompt caches every minute. See [PROMPT_OPTIMIZATION.md](PROMPT_OPTIMIZATION.md).

---

## Common Tuning

### Memory off entirely

```yaml
memory_pipeline:
  memory:
    consolidation_interval: 50
    short_term_limit: 20
    max_paginated_limit: 100
    long_term_memory:
      enabled: false
    retrieval:
      top_k: 0           # ← disables retrieval
      min_score: 0.0
      include_channel_memories: false
      max_mentioned_memories: 0
      max_channel_memories: 0
    consolidation:
      enabled: false      # ← disables consolidation
      memory_search_limit: 1
    # ... maintenance fields still required for validation but ignored at runtime
```

### Memory on, conservative retrieval

```yaml
memory_pipeline:
  memory:
    retrieval:
      top_k: 5
      min_score: 0.75
      include_channel_memories: true
      max_mentioned_memories: 3
      max_channel_memories: 5
```

### Different consolidation cadence

```yaml
memory_pipeline:
  memory:
    consolidation_interval: 30   # active channels
    # consolidation_interval: 100  # quieter servers
```

---

## Switching Embedding Models

Qdrant collections lock in a vector size at creation. To switch from a 1536-dim model to a 3072-dim one (or vice versa), drop the collections first:

```bash
# Default Qdrant REST port is 6333 (gRPC is 6334)
curl -X DELETE http://localhost:6333/collections/memories
curl -X DELETE http://localhost:6333/collections/profiles
curl -X DELETE http://localhost:6333/collections/relationships

# Authenticated:
curl -X DELETE https://your-cluster.qdrant.io:6333/collections/memories \
  -H "api-key: your-api-key"
```

Then update `embedding.model` and `qdrant.vector_size`, restart the bot. Collections will be recreated automatically.

> Heads up: this deletes all stored memories, profiles, and relationships. Snapshot first if you care about that data.

---

## Security Notes

- **Use environment variables for secrets** in production. Don't commit `config.yaml` with a Discord token in it.
- **Restrict `config.yaml`** — `chmod 400 config.yaml` if it does end up on disk.
- **Set a real WebUI password** if you enable the dashboard.
- **Use a reverse proxy** (nginx, Caddy) with TLS in front of the WebUI — there's no built-in HTTPS.
- **Blacklist before deploying** — at minimum, add yourself if you're a bot operator who tests in the same channels.
