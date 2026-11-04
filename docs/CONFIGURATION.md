# Configuration

EZyapper is configured via a single YAML file. Pass its path with `--config` (short form `-c`):

```bash
./ezyapper --config /path/to/config.yaml
```

Without the flag the bot looks for `config.yaml` in `.`, `./config` and `/etc/ezyapper`, in that order.

> [!CAUTION]
> **Strict config — no built-in defaults.** The bot never fills in a value you left out. The values in `examples/config.yaml.example` (and the "Suggested (example)" columns below) are suggestions to copy, not defaults. A required field that is missing or zero stops the bot at startup, and a key that is required when its feature is enabled (a modality, the decision model, the dashboard, plugins, a filter rule list, ...) must be set as soon as you enable that feature. Validation errors are batched: if you have ten missing fields, you'll see all ten in one go, each naming the key and pointing at the example.
>
> What is *not* a default: an optional section or switch that is absent is off (`enabled: false`, no rules, no servers); a few zero values have a documented meaning (`retrieval.top_k: 0` turns retrieval off, `context_flush_window: 0` is the plain sliding window, `reply_percentage: 0` never replies at random); and some keys explicitly inherit another key when empty (a modality's endpoint fields inherit `core.ai`, see the tables). Optional maps such as `extra_params` can be left out. Start from `examples/config.yaml.example`, which sets every field.

> [!TIP]
> **Hot reload.** With `operations.runtime.hot_reload.enabled: true` the bot watches its config file and applies every change on save, without a restart: the Discord token, models, prompts, the decision provider, Qdrant, MCP servers and the dashboard port included. A file that does not validate, or a change that cannot be applied (bad token, unreachable server, port in use), is rejected as a whole and the bot keeps running on the previous config. See [Hot reload](#hot-reload).

> [!WARNING]
> **WebUI is experimental.** Keep `operations.web.enabled: false` for production unless you're explicitly debugging.

---

## Top-level Shape

```yaml
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

For a complete annotated example, see `examples/config.yaml.example`. For the JSON Schema (editor autocomplete), see `examples/config.schema.json`.

---

## Environment Variable Overrides

An environment variable can override a field **that is present in the config file**. Prefix `EZYAPPER_`, then convert the YAML path to uppercase with dots replaced by underscores.

```
core.discord.token                            → EZYAPPER_CORE_DISCORD_TOKEN
core.ai.api_key                               → EZYAPPER_CORE_AI_API_KEY
core.ai.modalities.audio.enabled              → EZYAPPER_CORE_AI_MODALITIES_AUDIO_ENABLED
memory_pipeline.embedding.model               → EZYAPPER_MEMORY_PIPELINE_EMBEDDING_MODEL
memory_pipeline.qdrant.host                   → EZYAPPER_MEMORY_PIPELINE_QDRANT_HOST
memory_pipeline.memory.retrieval.top_k        → EZYAPPER_MEMORY_PIPELINE_MEMORY_RETRIEVAL_TOP_K
operations.web.enabled                        → EZYAPPER_OPERATIONS_WEB_ENABLED
operations.plugins.plugins_dir                → EZYAPPER_OPERATIONS_PLUGINS_PLUGINS_DIR
```

Overrides apply only to keys that appear in the file (the loader has no list of known keys, so it only looks up environment variables for the keys it read). To supply a secret from the environment, keep the key in the file with a placeholder value, for example `token: ""`, and set `EZYAPPER_CORE_DISCORD_TOKEN`. A variable for a key that is not in the file is silently ignored.

The full nested path is required — short forms like `EZYAPPER_DISCORD_TOKEN` (without `CORE_`) do **not** map to anything and are silently ignored.

### Key case

Config field names are case-insensitive (`Core:` and `core:` are the same key). Keys inside maps that hold your own data keep the case you write: `operations.mcp.servers[].env`, `operations.mcp.servers[].headers` and every `extra_params` block, at any depth (for example `extra_params.metadata.TraceID`). An environment variable can override an entry in such a map (`EZYAPPER_CORE_AI_EXTRA_PARAMS_METADATA_TRACEID`); the key keeps the spelling from the file. Two keys in one map that differ only in case (`Path` and `PATH`) are kept as two keys.

---

## `core`

### `core.discord`

| Field | Type | Notes |
|-------|------|-------|
| `token` | string | Discord bot token. **Always required.** |
| `bot_name` | string | Required. The bot's name. It is currently passed only to the decision provider, and both providers ignore it: nothing substitutes it into a prompt. Write the name literally in `core.ai.system_prompt` and the decision prompt or instructions. |
| `own_bot_id` | string | The bot's user ID. Used to mark the bot's own messages in memory search queries and consolidation (`BOT=SELF`). Required when memory retrieval (`top_k > 0`) or consolidation is enabled. Replying to itself is prevented separately, using the session's own user ID. |
| `reply_percentage` | float (0.0–1.0) | Probability of replying to a message that neither mentions the bot nor replies to it, when the decision model is disabled or has failed. `0` never replies at random. |
| `cooldown_seconds` | int (>0) | After the bot replies to a user, that user's messages are ignored for this many seconds, in every channel (the cooldown is per user). |
| `max_responses_per_minute` | int (>0) | Messages per user **per channel** that are considered within one `rate_limit.reset_period_seconds` window. Every message that passes the access checks counts, including ones the bot then does not answer; further messages in the window are ignored. It is not a global cap. |
| `rate_limit.reset_period_seconds` | int (>0) | Length of the window for `max_responses_per_minute`. The window starts at the first counted message and resets when it expires (a fixed window, not a sliding one). |
| `other_bot_policy` | enum | `"ignore"` / `"context_only"` / `"full"` — how to treat messages from other bots. See below. |
| `consolidation_timeout_sec` | int (>0) | Deadline for one background consolidation run: fetching the channel's messages, turning their media into text and extracting memories. |
| `typing_indicator_interval_sec` | int (>0) | How often the typing indicator is re-sent while a reply is generated. |
| `long_response_delay_ms` | int (>0) | Pause between the chunks of a reply longer than Discord's 2000-character limit (such a reply is split into chunks of up to 1900 characters). |
| `image_cache_ttl_min` | int (>0) | Idle TTL of the media text cache (image descriptions, audio transcripts, video descriptions). Media still in a channel's prompt window is pinned and never expires; other entries expire after this long unused, and each use refreshes them. |
| `image_cache_max_entries` | int (>0) | Size bound of that cache: the least recently used unpinned entries are dropped first; pinned entries are never evicted. |

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
| `timeout` | int (>0, seconds) | Per-request timeout. Required. It is also the timeout of a modality that sets none (an ASR endpoint included). The per-message processing deadline is derived from it: `(timeout + max plugin timeout) × max_tool_iterations + 10 s`. |
| `extra_params` | map | Optional. Forwarded to the API via reflection. Unknown keys log a warning at runtime. Nested keys keep their case. Also used for embedding requests when `memory_pipeline.embedding.extra_params` is empty. |
| `system_prompt` | string | The static system prompt. Sent unchanged on every request (no placeholders); see [System prompt](#system-prompt). |
| `max_tool_iterations` | int (>0) | Cap on tool-calling loops per turn. Prevents infinite tool-call cycles. |
| `max_image_bytes` | int (>0) | Maximum bytes for an image fetched from Discord CDN. |

`core.ai.require_image_content_type` was removed: image downloads already reject responses whose `Content-Type` is not an image type. A config that still sets it loads and logs a deprecation warning.

`core.ai.modalities.sticker.mode` was removed: `sticker.enabled` is the only switch (`true` reads the image with a vision model, `false` uses the Discord text). A config that still sets it loads and logs a deprecation warning.

#### `core.ai.modalities`

Text is always on: `core.ai` is the base model that reasons, runs tools and writes every reply. It is treated as text-only: the reply request never carries media. There are four media modalities: image, audio, video and sticker. Image, audio and video are optional, each with its own `enabled` switch and its own model; an absent section is off. Stickers are always added to the message (as their Discord text when `sticker.enabled` is false), so `sticker.max_items`, `sticker.cache_ttl_min` and `sticker.cache_max_entries` are **always required**. See [MEDIA.md](MEDIA.md) for how the pipeline works.

There are no built-in defaults here either: when a modality is enabled, every key marked "required when enabled" below must be set. The "Suggested (example)" column shows the value `examples/config.yaml.example` uses.

Every modality accepts these endpoint fields. Left empty (or 0) they inherit the matching `core.ai` value (documented inheritance between keys, not a built-in value), so a modality on the same gateway does not repeat the endpoint.

| Field | Type | Notes |
|-------|------|-------|
| `model` | string | The modality's model. Required when the modality is enabled (an enabled sticker modality may fall back to `modalities.image.model`). |
| `api_base_url`, `api_key` | string | Fall back to `core.ai`. |
| `timeout` | int (≥0, s) | Per-request timeout. Falls back to `core.ai.timeout` (ASR endpoints included). |
| `max_tokens` | int (>0) | Falls back to `core.ai.max_tokens`. Reasoning models describing video need a lot (≈12000) or they return no content. |
| `temperature` | float (0–2) | Falls back to `core.ai.temperature`. |
| `retry_count` | int (≥0) | Falls back to `core.ai.retry_count`. |
| `extra_params` | map | Replaces `core.ai.extra_params` for this modality. |

Media reaches the base model only as text:

- A dedicated model per modality turns the media into text, added to the message in the user prompt: `[Image 1: ...]`, `[Audio 1 transcript (voice message, 4s): ...]`, `[Video 1 (clip.mp4): ...]`. Results are cached by a stable key (the attachment ID, or the CDN path without its signed query) and shared by the reply prompt, history and memory consolidation, so each attachment is processed once while it is in context, and plain-text messages never re-process history media. Media still in a channel's prompt window is pinned in the cache; other entries expire after `core.discord.image_cache_ttl_min` minutes unused and are evicted least recently used first beyond `image_cache_max_entries`. Permanent failures (over the size or duration limit, wrong content type, a download 4xx other than 408/429, a model 400/404/413/415/422, a transcode error) are remembered for 5 minutes; timeouts, 429, 5xx and empty output are retried. On hot reload, a kind's cache is dropped only when its model, endpoint, prompt or transcode settings change. Stickers follow the same rules with `sticker.cache_ttl_min` / `cache_max_entries`. For the message being answered, media is always processed; history messages use cached results, and at most one history message per kind is processed on demand: the most recent one, and only when the user replied to it or mentions it (e.g. "image", "voice", "video").
- Media whose text is not available (not cached, or processing failed) shows a placeholder such as `[Audio 1: voice message, 4s]`.
- There is no "direct" mode. `image.mode` and `video.mode` were removed: any value fails validation with a message asking to remove the key. `audio.mode` takes only `asr` or `describe`; `direct` fails validation. Omni models given the media inside the long, tool-carrying reply request tended to ignore it, so a dedicated model describes it instead.

The system prompt stays byte-identical: everything media-related is in the user prompt.

**`image`**

| Field | Type | Suggested (example) | Notes |
|-------|------|---------------------|-------|
| `enabled` | bool | `false` | Absent = off. |
| `model` | string | `gpt-4o` | Required when enabled: the vision model that describes each image (`image_url` part). |
| `prompt` | string | "Describe this image in 1-2 sentences. Be compact but informative. Transcribe any visible text." | Required when enabled. Description prompt. |
| `max_items` | int (>0) | `4` | Required when enabled. Images processed per message. |
| `transport` | enum | `base64` | Required when enabled. `base64` downloads the image (limit `core.ai.max_image_bytes`) and inlines it as a data URI; `url` passes the Discord CDN URL. |

**`audio`** (audio attachments and Discord voice messages)

| Field | Type | Suggested (example) | Notes |
|-------|------|---------------------|-------|
| `enabled` | bool | `false` | Absent = off. |
| `mode` | enum | `asr` | Required when enabled. `asr`: speech-to-text via an OpenAI-compatible `POST {api_base_url}/audio/transcriptions` (multipart `file` + `model`), e.g. Whisper or `IFM/Jais-ASR`; added as `[Audio 1 transcript (voice message, 4s): ...]`. `describe`: an audio-capable chat-completions model receives the audio (as set by `part_type`) and returns a transcript or description, added as `[Audio 1 (voice message, 4s): ...]`. |
| `model` | string | `whisper-1` | Required when enabled. |
| `language` | string | | Optional ISO-639-1 hint (asr); empty sends none. |
| `prompt` | string | describe: "Transcribe the speech in this audio verbatim, in its original language. If there is no speech, describe the sound or music in one sentence. Reply with the transcript or description only." | asr: optional vocabulary/spelling hint (empty sends none). describe: required, the instruction for the audio model. |
| `max_items` | int (>0) | `2` | Required when enabled. |
| `max_bytes` | int (>0) | `26214400` (25 MiB) | Required when enabled. Larger attachments are skipped. |
| `max_duration_sec` | int (>0) | `600` | Required when enabled. Checked when Discord reports a duration (voice messages). |
| `transcode` | enum | (empty) | Optional. `mp3` or `wav`: convert with ffmpeg (mono, 16 kHz) before sending; empty sends the audio as is. Only needed for an ASR endpoint that rejects ogg/opus voice messages, or for `part_type: input_audio`. Requires ffmpeg (and `ffmpeg_path`), which the Docker image does not include (see [DEPLOYMENT.md](DEPLOYMENT.md#ffmpeg-in-docker)). |
| `part_type` | enum | `audio_url` | Required in describe mode: `audio_url` (data URI, any format the model accepts; ogg/opus voice messages work as is) or `input_audio` (OpenAI format, mp3/wav only, so `transcode` is required). |

**`video`**

| Field | Type | Suggested (example) | Notes |
|-------|------|---------------------|-------|
| `enabled` | bool | `false` | Absent = off. |
| `model` | string | `gpt-4o` | Required when enabled: a video-capable model (`video_url` parts) that describes each video. |
| `prompt` | string | "Describe this video in 2-4 sentences: what happens, who or what appears, and any speech or on-screen text. Be compact but informative." | Required when enabled. |
| `max_items` | int (>0) | `1` | Required when enabled. |
| `max_bytes` | int (>0) | `52428800` (50 MiB) | Required when enabled. |
| `max_duration_sec` | int (>0) | `120` | Required when enabled. Checked when Discord reports a duration. |
| `transport` | enum | `base64` | Required when enabled. `base64` (data URI) or `url` (provider fetches the CDN URL; slower on some gateways). |
| `transcode` | bool | `false` | Optional. Re-encode with ffmpeg before sending (H.264/AAC mp4): trimmed to `max_duration_sec` instead of skipping longer videos, scaled down to `max_height`. Needs `transport: base64`. Requires ffmpeg (and `ffmpeg_path`). |
| `max_height` | int (>0) | `480` | Required when `transcode` is true. |

**ffmpeg**

ffmpeg is an optional dependency. It is only needed by `audio.transcode` and `video.transcode`; nothing else calls it, and without those options the bot never looks for it. The published Dockerfile does not install it. When an enabled modality uses one of them, `core.ai.modalities.ffmpeg_path` is required (suggested: `ffmpeg`, looked up in `PATH`, or a full path), and the bot checks at startup (and on a WebUI config reload) that the binary exists and `ffmpeg -version` runs; if not, it refuses to start with an error naming the options that need it.

**`sticker`**

| Field | Type | Suggested (example) | Notes |
|-------|------|---------------------|-------|
| `enabled` | bool | `false` | The only sticker switch; it decides whether the image is read. Stickers are always added to the message. `false`: `[Sticker: name — description (tags: ...)]` from Discord (one `GET /stickers/{id}` per sticker, cached; no model call). `true`: a vision model describes the sticker image (`media.discordapp.net/stickers/{id}.png`/`.gif`) once per sticker ID; custom guild stickers included. Lottie stickers cannot be rendered and always use the text. In history, a sticker without a cached description falls back to the text (no model call). |
| model fields | | | When enabled, empty fields inherit `modalities.image`, then `core.ai`. `enabled: true` needs a model from `sticker.model` or `image.model`. |
| `prompt` | string | "Describe this Discord sticker in one short sentence: what it shows and the emotion or reaction it conveys. Include any visible text." | Required when enabled. Vision prompt. |
| `max_items` | int (>0) | `3` | **Always required.** Stickers annotated per message. |
| `cache_ttl_min` | int (>0) | `1440` | **Always required.** Sticker details and descriptions are cached by sticker ID (not by URL). Stickers in a channel's prompt window are pinned; others expire after this long unused. |
| `cache_max_entries` | int (>0) | `1000` | **Always required.** Least recently used unpinned entries are dropped first. |

`sticker.mode` was removed. A config that still sets it loads and logs a deprecation warning. `mode: text` together with `enabled: true` logs a second warning: that combination used to mean text only and now reads the image, so set `enabled: false` to keep text only (without any `sticker.model`/`image.model` it fails validation with that hint).

Example (one OpenAI-compatible gateway for chat, vision and video; a separate ASR service):

```yaml
core:
  ai:
    api_base_url: "https://gateway.example/v1"
    api_key: "..."
    model: "Beam-1.0-501B-A23B"
    modalities:
      image:
        enabled: true
        model: "nvidia/nemotron-3.5-super-vl-120b-a12b"
        prompt: "Describe this image in 1-2 sentences. Be compact but informative. Transcribe any visible text."
        max_items: 4
        transport: base64
      audio:
        enabled: true
        mode: asr
        model: "IFM/Jais-ASR"
        api_base_url: "https://api.ifm.ai/v1"
        api_key: "..."
        max_items: 2
        max_bytes: 26214400
        max_duration_sec: 600
      video:
        enabled: true
        model: "nvidia/nemotron-3-nano-omni-30b-a3b-reasoning"
        max_tokens: 12000
        prompt: "Describe this video in 2-4 sentences: what happens, who or what appears, and any speech or on-screen text. Be compact but informative."
        max_items: 1
        max_bytes: 52428800
        max_duration_sec: 120
        transport: base64
      sticker:
        enabled: false
        max_items: 3
        cache_ttl_min: 1440
        cache_max_entries: 1000
```

For `audio.mode: describe`, point the audio modality at an audio-capable chat model instead, e.g. `model: "nvidia/nemotron-3-nano-omni-30b-a3b-reasoning"` with `max_tokens: 12000`, and set `prompt` and `part_type` (both required in that mode).

#### `core.ai.vision` (deprecated)

The pre-modalities block still loads and is mapped onto `core.ai.modalities.image` with a startup warning: `text_only` → image off, `hybrid` and `multimodal` → image on (`vision.model` describes images; `multimodal` no longer sends images to the reply model). `model`, `description_prompt` (→ `prompt`), `max_images` (→ `max_items`), `base64` (→ `transport`), and the endpoint fields carry over. There are no built-in defaults for the mapped block either: a legacy `hybrid`/`multimodal` block must set `description_prompt` and `max_images`. When `core.ai.modalities.image` is present, `core.ai.vision` is ignored. Saving the config from the WebUI writes the new block.

### `core.decision`

The optional reply classifier. When enabled, every message that neither mentions the bot nor replies to it is sent to a decision model, and the bot replies only when the model says so. Two providers are available:

- `llm`: a (typically small/fast) chat model returns `{"should_respond": ..., "reason": ..., "confidence": ...}`.
- `system_one`: a [System One](https://docs.typesafe.ai/concepts/system-one.md) decision model such as TypeSafe Jev answers one yes/no ("noul") question with a calibrated probability P(yes). It is not an LLM: it generates no text, so there is no prompt or JSON format to get wrong.

| Field | Type | Required when | Notes |
|-------|------|---------------|-------|
| `enabled` | bool | always | If false, all fields below are skipped (an invalid `provider` is still reported). |
| `provider` | `llm` \| `system_one` | enabled | No default provider: required when `enabled` is true (suggested in the example: `llm`). Only the selected provider's fields are validated. |

#### `provider: llm`

| Field | Type | Required when | Notes |
|-------|------|---------------|-------|
| `model` | string | enabled | Decision model (use a fast one). |
| `api_base_url` | string | enabled | |
| `api_key` | string | enabled | |
| `system_prompt` | string | enabled | Decision instructions (no placeholders; write the bot name literally). Must instruct the model to return strict JSON. |
| `max_tokens` | int (>0) | enabled | |
| `temperature` | float (0.0–2.0) | enabled | Low values (0.0–0.3) recommended. |
| `retry_count` | int (≥0) | enabled | |
| `timeout` | int (>0) | enabled | Per-request timeout. |
| `extra_params` | map | optional | |

#### `provider: system_one` (`core.decision.system_one`)

| Field | Type | Required when | Notes |
|-------|------|---------------|-------|
| `api_base_url` | string | enabled | e.g. `https://api.typesafe.ai/v1`. Requests go to `<api_base_url>/systemone`. |
| `api_key` | string | enabled | Sent as `Authorization: Bearer <key>`. |
| `model` | string | enabled | e.g. `jev-latest` (or `jev-preview`). |
| `timeout` | int (>0) | enabled | Per-request timeout in seconds. |
| `retry_count` | int (≥0) | enabled | Retries on 429, 529, 5xx and network errors. 400/401/413/422 fail at once. |
| `threshold` | float (0 ≤ t < 1) | enabled | The bot replies only when Jev says yes (P(yes) > 0.5) **and** P(yes) > threshold. Both comparisons are strict: P(yes) = 0.5 is not a yes, and P(yes) = threshold does not reply. A threshold at or below 0.5 therefore just means "reply when Jev says yes"; raise it above 0.5 to make the bot quieter. 0.7 is TypeSafe's suggested starting point. |
| `instructions` | string | enabled | The yes/no question, e.g. `Should the Discord bot Ezy send a reply to current_message?`. Sent verbatim: there is **no** `{BotName}` substitution, write the bot's name literally. |
| `criteria_true` / `criteria_false` | string | optional | What a yes / a no means. Set both or neither. Sent verbatim. |
| `max_state_bytes` | int (>0) | enabled | Budget for the JSON state. 48000 keeps even CJK-heavy text well inside Jev's 64k-token context. |

The state sent with the question is a JSON object:

```json
{
  "recent_messages": [
    {"author": "alice", "is_bot": false, "content": "...", "timestamp": "2026-11-01T08:00:00Z", "reply_to": "bob"}
  ],
  "older_messages_omitted": 3,
  "current_message": {"author": "carol", "is_bot": false, "content": "...", "timestamp": "2026-11-01T08:02:00Z", "image_count": 1}
}
```

`recent_messages` is oldest first; `reply_to` (the replied-to author) is omitted when the message is not a reply, and author IDs are not sent. When the encoded state exceeds `max_state_bytes`, the oldest messages are dropped first (`older_messages_omitted` says how many); if the current message alone is still too large, its content is cut. Images are not sent (text only); `image_count` tells the model they exist. The decision is logged like `system_one decision: p(yes)=0.8300 jev_yes=true (p>0.5) above_threshold=true (p>0.7) (confidence: 0.83)`, so the log shows which check failed; the reported confidence is the probability of the chosen verdict: P(yes) when replying, 1 − P(yes) when not.

Tuning: with the criteria from `config.yaml.example` and `jev-latest` (jev-1.14.0, November 2026), a message addressed to the bot by name scored about 0.94, a follow-up in a conversation with the bot 0.97, two users talking to each other 0.04, and an open question to the whole channel about 0.65. With the default threshold of 0.7 the bot therefore stays out of open channel questions; lower it to about 0.6 if it should take those (anything at or below 0.5 still needs Jev to say yes, P(yes) > 0.5). Instructions alone (no criteria) separate the cases less sharply (0.84 / 0.94 / 0.14 / 0.71).

If the decision model fails (timeout, error, invalid JSON for `llm`, an HTTP error or missing answer for `system_one`), the bot falls back to `core.discord.reply_percentage`. So a decision-service outage doesn't kill the bot — it just makes it less smart about when to chime in.

---

## `memory_pipeline`

The whole memory subsystem can be turned **off** by setting `memory.retrieval.top_k: 0` and `memory.consolidation.enabled: false`. In that case, embedding, qdrant, and consolidation fields are skipped during validation.

### `memory_pipeline.embedding`

Empty `api_base_url`, `api_key` and `extra_params` are taken from `core.ai`. `retry_count` and `timeout` are not inherited.

| Field | Type | Required when | Notes |
|-------|------|---------------|-------|
| `api_base_url` | string | optional | Inherits from `core.ai`. |
| `api_key` | string | optional | Inherits from `core.ai`. |
| `model` | string | memory enabled | Embedding model. **The vector size of this model must match `qdrant.vector_size`.** |
| `retry_count` | int (≥0) | memory enabled | |
| `timeout` | int (>0) | memory enabled | |
| `extra_params` | map | optional | Falls back to `core.ai.extra_params` when empty (under review: chat-only parameters may not suit embedding requests). |

### `memory_pipeline.memory`

The biggest subsection. Skim it in two passes — first the basics, then the maintenance/scoring tail.

**Core memory behavior**

| Field | Type | Notes |
|-------|------|-------|
| `consolidation_interval` | int (>0) | Trigger consolidation every N bot-handled messages. |
| `short_term_limit` | int (>0) | Recent Discord messages to fetch for short-term context. |
| `context_flush_window` | int (>=0, optional) | Anchored window: history grows up to `short_term_limit` + this many messages, then the oldest block is dropped at once so the prompt prefix stays cacheable between flushes. `0` (or absent) = plain sliding window. See [PROMPT_OPTIMIZATION.md](PROMPT_OPTIMIZATION.md). |
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

**`consolidation`** — empty `model`, `api_base_url` and `api_key` are taken from `core.ai`. `max_tokens`, `retry_count` and `timeout` must be set when consolidation is enabled. `extra_params` is **not** inherited: only the consolidation block's own `extra_params` is sent.

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
| `consolidation.extra_params` | map | optional | Not inherited from `core.ai.extra_params`. |
| `consolidation.system_prompt` | string | enabled | The extraction prompt. Templates for each `other_bot_policy` value live in `examples/config.yaml.example` — start from one of those. |
| `consolidation.memory_search_limit` | int (>0) | always | How many existing memories to load for dedup during consolidation. Validated even when consolidation is disabled. |
| `consolidation.vision.*` | submap | optional | Overrides for describing images during consolidation. Only these keys are read: `model`, `api_base_url`, `api_key`, `max_tokens`, `temperature`, `retry_count`, `timeout`, `description_prompt`. Other keys (`extra_params`, `base64`, `max_images`, `mode`) are ignored; extra params, transport and the item limit come from `core.ai.modalities.image`. Anything left empty inherits from `core.ai.modalities.image` (then `core.ai`). Only used when the image modality is enabled. |

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
| `port` | int (>0) | memory enabled | gRPC port (Qdrant serves gRPC on 6334 unless configured otherwise). |
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

Switching embedding models is safe: memories are re-embedded automatically into a new per-model vector collection. See "Switching embedding models" at the bottom of this doc.

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


### `access_control.message_filters`

Optional regex rules for incoming messages. Rules run in order and the first
match wins; if none matches, `default_action` applies (`allow` lets it
through, `ignore` turns the list into a whitelist). `default_action` has no
built-in default: it is required as soon as `rules` is non-empty. An absent
block filters nothing. An ignored message is not
answered, and it is left out of history, the decision model and memory
consolidation. The bot's own messages are never filtered.

```yaml
access_control:
  message_filters:
    default_action: allow
    rules:
      - name: "status bot embeds in #general"
        action: ignore
        channel: "^123456789012345678$"
        user: "^234567890123456789$"
        kinds: [embed]
```

| Field | Meaning |
|-------|---------|
| `action` | `allow` or `ignore` (required) |
| `guild`, `channel`, `user` | Regex searched in the guild, channel or author ID. Anchor with `^...$` for an exact ID. |
| `username` | Regex searched in the author's username |
| `content` | Regex searched in the message text plus rendered embeds |
| `kinds` | Matches if the message has **any** of these kinds |
| `kinds_all` | Matches if the message has **all** of these kinds |

Kinds: `text`, `embed`, `image`, `sticker`, `audio`, `voice` (voice message),
`video`, `file`, `reply`, `bot`, `webhook`. A rule needs at least one
condition. Invalid regexes and unknown kinds fail config validation.

## `operations`

### `operations.web`

The optional admin dashboard. Off when `enabled` is false or absent. See [API.md](API.md) for what it actually does.

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
| `default_tool_timeout_ms` | int (≥0) | always | Timeout of a JSON-RPC tool whose plugin sets none. `0` means no fallback: such a tool call fails (a warning is logged). |
| `startup_timeout_sec` | int (>0) | enabled | `info` probe timeout. |
| `rpc_timeout_sec` | int (>0) | enabled | Timeout of the JSON-RPC lifecycle and hook calls (`info`, `on_message`, `after_*`, shutdown). |
| `before_send_timeout_sec` | int (>0) | enabled | `before_send` hook timeout. |
| `command_timeout_sec` | int (>0) | enabled | Timeout of a command-runtime tool whose plugin.json sets no `timeout_ms`. |
| `shutdown_timeout_sec` | int (>0) | enabled | Graceful shutdown grace period. |
| `disable_timeout_sec` | int (>0) | enabled | Grace period for runtime disable via WebUI. |
| `max_restarts` | int (>0) | enabled | Max auto-restart attempts before cooldown. Warns if >10. |
| `restart_cooldown_sec` | int (>0) | enabled | Cooldown after max restarts exhausted. Warns if >3600. |

### `operations.mcp`

| Field | Type | Required when | Notes |
|-------|------|---------------|-------|
| `enabled` | bool | always | If false, `servers` is not validated. |
| `servers[]` | list | enabled | At least one entry. |

Each `servers[]` entry:

| Field | Type | Required when | Notes |
|-------|------|---------------|-------|
| `name` | string | mcp enabled | Identifier used in logs and tool routing. |
| `type` | string | mcp enabled | `"stdio"`, `"sse"` or `"http"` (Streamable HTTP). |
| `command` | string | type=stdio | Executable name or path. |
| `args` | list | optional | Arguments. |
| `env` | map | optional | Env vars set for the subprocess, names exactly as written (`GITHUB_TOKEN` stays `GITHUB_TOKEN`). The bot's own environment is passed on with names containing TOKEN, SECRET, KEY, PASSWORD and similar removed; vars listed here are always set. |
| `url` | string | type=sse, http | Endpoint URL. |
| `headers` | map | optional, type=http | Headers sent with every request. Values are expanded with `$VAR` from the bot's environment. Only the `http` transport sends them: an `sse` server ignores `headers`, so use `http` for a server that needs an `Authorization` header. |

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
          GITHUB_TOKEN: "ghp_..."
      - name: remote-tool
        type: sse
        url: "http://localhost:8080/sse"
      - name: remote-http
        type: http
        url: "http://localhost:8080/mcp"
        headers:
          Authorization: "Bearer $ANYSEARCH_API_KEY"
```

### `operations.runtime`

| Field | Type | Notes |
|-------|------|-------|
| `shutdown_timeout_sec` | int (>0) | Total grace period for graceful shutdown. |
| `cleanup_interval_min` | int (>0) | Periodic cleanup ticker (rate limiter pruning, image cache eviction, etc.). |
| `hot_reload.enabled` | bool | Watch the config file and apply changes on save without a restart. Absent = off. See [Hot reload](#hot-reload). |
| `hot_reload.debounce_ms` | int (50-60000) | Required when `hot_reload.enabled` is true. Quiet period after the last file event before the file is re-read (editors emit several events per save). Must be `>= 0` when disabled. |
---

## Hot reload

Enable it in `operations.runtime`:

```yaml
operations:
  runtime:
    hot_reload:
      enabled: true
      debounce_ms: 500
```

The bot then watches the file it was started with (`--config`/`-c`, or the `config.yaml` it found). The parent directory is watched, so editors that save by writing a temporary file and renaming it (vim, VS Code, JetBrains, `sed -i`, Kubernetes ConfigMaps) are handled. After `debounce_ms` without further events the file is re-read.

### What happens on save

1. **Load and validate.** The whole file is parsed and validated exactly as at startup (environment overrides included). Any error rejects the reload; nothing changes.
2. **Diff.** The new config is compared with the running one. The changed key paths are logged, for example `[hotreload] 2 config key(s) changed: core.ai.model, core.discord.token`. Values are never logged, so tokens, API keys, passwords and MCP headers stay out of the log. A save that changes nothing does nothing.
3. **Prepare.** Every part of the bot affected by the change builds what it needs *next to* the running one: a new Discord session is opened, MCP servers are connected, a new memory service is built, the new dashboard port is bound, and so on. Nothing the bot is doing changes during this step.
4. **Commit or roll back.** If everything prepared successfully, the new config is published and each part switches over (pointer swaps that cannot fail); the old Discord session, MCP connections and memory service are closed after the switch. If anything failed, everything prepared so far is discarded and the log says `config reload failed, keeping the previous configuration: <part>: <reason>`. Fix the file and save again.

Saving from the WebUI `/config` page goes through the same steps; the file is written only after the change was applied.

### What reloads in place

| Change | How it is applied |
|--------|-------------------|
| `core.discord.token` | The token is checked against the Discord API, a new gateway session is opened with the bot's handlers attached, then swapped in and the old session closed. During the switch only events from the current session are handled, so no message is answered twice. A rejected token keeps the bot connected with the old one. |
| Other `core.discord.*` (bot name, reply percentage, cooldowns, rate limits, other-bot policy, image cache, typing/long-response timing) | Read from the live config on every message; the rate limiter is updated immediately. |
| `core.discord.clear_command` | Enabling registers `/clear-context`; disabling leaves the global command registered but refuses it. |
| `core.ai.*` (model, endpoint, key, prompt, tool iterations, vision) | The chat client is built per request from the live config; the media describers (image, audio, video, sticker) are rebuilt. A kind's cached media text is dropped only when its model, endpoint, prompt or transcode settings change. |
| `core.decision.*` | The decision provider (`llm` or `system_one`) is rebuilt; a provider that cannot be built rejects the reload. |
| `memory_pipeline.embedding.*` with the same `model` | A new embedding client is swapped into the running memory service. |
| `memory_pipeline.embedding.model`, `memory_pipeline.qdrant.*`, `memory_pipeline.memory.*`, consolidation settings, memory on/off | A complete new memory service is built first (connects to Qdrant, prepares the collections). For a new embedding model its vector collection is selected and filled from the source tier in the background, as on a restart (see [Switching Embedding Models](#switching-embedding-models)). The old service finishes in-flight calls and is closed after 30 s. |
| `memory_pipeline.memory.max_paginated_limit` | Also applied to the running Discord history fetcher when the reload is committed (in addition to the rebuilt memory service). |
| `access_control.*` | Read from the live config on every message. |
| `operations.web.*` | The dashboard's routes are rebuilt and swapped under the running listener. A new port (or enabling it) is bound before the switch; a port in use rejects the reload. Changing the username, password or session lifetime signs everyone out. |
| `operations.logging.*` | A level change is applied to the running logger; a new file or rotation settings open the new log file first (an unwritable path rejects the reload). |
| `operations.plugins.*` timeouts and restart policy | Applied to the running plugin manager; calls already in flight keep their timeout. |
| `operations.plugins.enabled` / `plugins_dir` | All plugins are stopped and the (new) directory is loaded. The directory must exist. |
| `operations.mcp.*` | A new set of MCP connections is made and its tools listed; any server failing rejects the reload. Tools of removed servers are unregistered; the old connections are closed after 30 s so running tool calls finish. |
| `operations.runtime.*` | `cleanup_interval_min` applies from the next tick, `shutdown_timeout_sec` at shutdown, `hot_reload.*` immediately (turning it off stops watching; the WebUI can turn it back on). |

### Plugins

Each plugin owns its config file; the bot and the other plugins are not touched when it changes.

- **`jsonrpc` plugins** (all Go examples) watch their own config file and reload themselves on save. A plugin whose new file is invalid keeps its previous config and logs why. See [PLUGINS.md](PLUGINS.md#config-hot-reload).
- **`command` plugins** (`datetime-zig`, `datetime-java`) are started fresh for every tool call and read `config.yaml` (from `EZYAPPER_PLUGIN_CONFIG`) each time, so the next call after a save uses the new values. A broken file fails only the calls made while it is broken (the error is in the bot's log); there is no previous config to keep. `clank-o-meter-zig` and `systemspec-c` have no config file. See [PLUGINS.md](PLUGINS.md#config-changes).

### Limitations

- In-memory state tied to a rebuilt part starts fresh: rebuilding the memory service resets its per-user/channel consolidation counters; changing dashboard credentials signs everyone out; changing `plugins_dir` restarts all plugins.
- Turning plugins on or changing `plugins_dir` happens after the new config was published: a plugin that fails to start is logged, the others keep running (plugins are processes and cannot run twice side by side to be prepared in advance).
- The Discord gateway intents are fixed in code, so nothing in the config needs a reconnect except the token.
- Hot reload watches only the config file. Environment variable overrides (`EZYAPPER_*`) are re-read with it, but changing an environment variable alone does not trigger a reload.

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
    modalities:
      image: { enabled: true, model: "gpt-4o" }
      audio: { enabled: true, mode: asr, model: "whisper-1" }
```

**DeepSeek**

```yaml
core:
  ai:
    api_base_url: "https://api.deepseek.com/v1"
    api_key: "sk-..."
    model: "deepseek-chat"
```

DeepSeek doesn't currently ship a vision model on its OpenAI-compatible surface; leave the modalities off or point `core.ai.modalities.image` at a different provider.

**Qwen (Alibaba Cloud)**

```yaml
core:
  ai:
    api_base_url: "https://dashscope.aliyuncs.com/compatible-mode/v1"
    api_key: "sk-..."
    model: "qwen-plus"
    modalities:
      image: { enabled: true, model: "qwen-vl-plus" }
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
```

If you enable a modality against a local endpoint, keep `transport: base64` (as in the example): a local server usually cannot fetch Discord CDN URLs itself.

---

## Mixing Endpoints

Each LLM-using component can have its own endpoint, key, and model. Where the tables above say so, empty endpoint fields inherit `core.ai.*` (modalities, consolidation's model/endpoint/key, embedding's endpoint/key); the decision model and System One take no inherited values. So you can run, say, the main chat on Qwen, embeddings on a local model, decisions on a fast cheap model, and consolidation on `gpt-4o-mini`:

```yaml
core:
  ai:
    api_base_url: "https://dashscope.aliyuncs.com/compatible-mode/v1"
    api_key: "sk-qwen"
    model: "qwen-plus"
  decision:
    enabled: true
    provider: llm
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

## System Prompt

`core.ai.system_prompt` and `core.decision.system_prompt` are sent exactly as
written. The old `{BotName}`, `{AuthorName}`, `{ServerName}`, `{GuildID}` and
`{ChannelID}` placeholders were removed so the system prompt stays
byte-identical across requests and provider prompt caches can reuse it; a
config that still contains one fails validation with a message naming the
field. Write the bot's name literally. Per-request facts (server and channel
name and ID, participants with their IDs, history, the current message) are
in the user message — see [PROMPT_OPTIMIZATION.md](PROMPT_OPTIMIZATION.md).

For dates and times, use the `get_current_datetime` tool from the `datetime` plugin instead of putting a timestamp into the prompt. Message timestamps are already in the user message.

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

Update `embedding.model` and `qdrant.vector_size` and restart. Do **not**
delete collections: memory text and metadata live in `memories_source`, which
does not depend on the model. On startup the bot creates
`memories_vec_<model>_<dim>` for the new model and re-embeds every memory into
it in the background (resumable; progress is logged). Semantic search covers
more memories as the job proceeds.

The previous model's vector collection is kept so you can switch back without
re-embedding. Delete it when you no longer need it:

```bash
curl -X DELETE http://localhost:6333/collections/memories_vec_text_embedding_3_small_1536 \
  -H "api-key: your-api-key"
```

Deleting `memories_source` deletes all memories. The old single-collection
`memories` (from versions before the two-tier store) is migrated automatically
and can be deleted once the bot has started successfully with this version.

---

## Security Notes

- **Use environment variables for secrets** in production. Don't commit `config.yaml` with a Discord token in it.
- **Restrict `config.yaml`** — `chmod 400 config.yaml` if it does end up on disk.
- **Set a real WebUI password** if you enable the dashboard.
- **Use a reverse proxy** (nginx, Caddy) with TLS in front of the WebUI — there's no built-in HTTPS.
- **Blacklist before deploying** — at minimum, add yourself if you're a bot operator who tests in the same channels.

## `core.discord.clear_command`

Optional `/clear-context` slash command. Run in a channel, it makes the bot
forget that channel's conversation context: older messages are hidden from
the model and replies still being generated there are cancelled. Other
channels, Discord's own history, stored data and long-term memory are not
touched. Replies are ephemeral (only the caller sees them) and the command
never appears in the channel's message history.

```yaml
core:
  discord:
    clear_command:
      enabled: true
      allowed_user_ids: ["446248181886484480"]
```

Only users in `allowed_user_ids` can run it; everyone else gets an ephemeral
refusal. The cut-off is kept in memory, so a restart brings the older
context back. The bot needs the `applications.commands` scope.
