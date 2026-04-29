# EZyapper Emote Plugin

Semantic emote search via emote LLM, Discord CDN refresh, and independent emote sending.

## Features

- **Semantic search** — Dedicated emote LLM matches emotes by intent, not just keywords
- **URL + Local** — Emotes can be Discord CDN URLs (auto-refreshed) or local files
- **CDN refresh** — Discord CDN URLs refreshed via official API (24h cache)
- **Independent send** — Plugin sends emote separately from bot text (clean, no URL clutter)
- **MD5 dedup** — Emote ID = MD5(url) or MD5(file_name), natural deduplication
- **Per-guild storage** — Each Discord guild gets its own metadata directory
- **Blacklist/whitelist** — Channel and user filtering extends the main bot's rules

## Installation

### Prerequisites

- EZyapper main bot installed and configured
- A Discord bot token (for sending emote messages independently)
- An OpenAI-compatible Vision API key (for auto-steal emote detection)
- An OpenAI-compatible Emote LLM API key (for semantic search)

### Build

```bash
cd examples/plugins/emote-go
go build -o emote-go.exe .
```

### Deploy

```bash
mkdir -p plugins/emote-go
cp emote-go.exe config.yaml plugins/emote-go/
```

Enable plugins in main bot's `config.yaml`:

```yaml
operations:
  plugins:
    enabled: true
    plugins_dir: "plugins"
```

Restart the bot.

## Configuration

Copy `config.yaml` and edit the values. Built-in defaults apply when the file is missing.

### Full Configuration Reference

```yaml
# Storage
storage:
  data_dir: "data"
  max_image_size_kb: 512
  allowed_formats: ["png", "jpg", "jpeg", "webp", "gif"]

# Vision — for auto-steal detection
vision:
  api_key: ""
  api_base_url: "https://api.openai.com/v1"
  model: "gpt-4o-mini"
  timeout_seconds: 30
  prompt: |
    Analyze this image and determine if it is a "meme/emote/sticker"...

# Emote LLM — for semantic search
emote:
  model: ""
  api_key: ""
  api_base_url: ""

# Discord — for independent emote sending (same as main bot token)
discord:
  token: ""

# Auto-steal
auto_steal:
  enabled: true
  additional_blacklist_channels: []
  additional_whitelist_channels: []
  additional_blacklist_users: []
  rate_limit_per_minute: 5
  cooldown_seconds: 2

# Logging
logging:
  enabled: true
  level: "info"
```

### Defaults

| Field | Default |
|-------|---------|
| `storage.data_dir` | `"data"` |
| `storage.allowed_formats` | `["png", "jpg", "jpeg", "webp", "gif"]` |
| `emote.api_base_url` | `"https://asus.omgpizzatnt.top:3000/v1"` |

## How It Works

### 2 Tools Exposed to LLM

| Tool | Description |
|------|-------------|
| `search_emote` | Search emotes by describing what you want. Emote LLM matches by semantic intent. |
| `send_emote` | Send an emote to the channel. Plugin refreshes CDN URLs and queues for independent sending. |

### Auto-Steal Flow

1. Discord message arrives with image attachment
2. Plugin checks blacklist/whitelist
3. Strips Discord CDN query params (prevents token-based duplicates)
4. Stores bare URL with `ID = md5(URL)` in guild metadata
5. No file download — URL-only storage

### Semantic Search Flow

1. LLM calls `search_emote(query="网太卡了", guild_id="123")`
2. Plugin loads emotes from `global` + `guild_123` metadata
3. Merges and deduplicates by ID
4. Sends all emotes to emote LLM for matching
5. Returns top matches with relevance reasons

### Send Flow

1. LLM calls `send_emote(id="md5abc")`
2. Plugin finds emote in metadata (guild first, then global)
3. Discord CDN URL: refreshes via Discord API → queues
4. Local file: queues file path
5. Returns confirmation text
6. `OnResponse`: plugin sends emote as separate Discord message (pure URL = clean image render, or file attachment)

## Storage Layout

```
plugins/emote-go/data/
  <guild_id>/
    metadata.json        # Emote entries for this guild
    images/              # Local image files (for file_name emotes)
```

## Manual Management

### Adding a Local Image Emote

1. Place the image file in `data/<guild_id>/images/my-emote.png`
2. Add to `data/<guild_id>/metadata.json`:

```json
{
  "id": "md5_of_filename",
  "name": "my_emote",
  "description": "A custom emote added manually",
  "tags": ["custom"],
  "url": "",
  "file_name": "my-emote.png",
  "created_at": "2026-04-30T12:00:00+08:00"
}
```

### Adding a URL Emote

```json
{
  "id": "md5_of_url",
  "name": "sad_cat",
  "description": "A sad cat reaction image",
  "tags": ["cat", "sad"],
  "url": "https://cdn.discordapp.com/attachments/.../sad-cat.png",
  "file_name": "",
  "created_at": "2026-04-30T12:00:00+08:00"
}
```

> **Note**: Discord CDN URLs must be stored **without** query parameters (`?ex=&is=&hm=`).
> The plugin strips these during auto-steal and refreshes them before sending.

### Removing an Emote

Delete the image file (if local), then remove the entry from `metadata.json`.

## Troubleshooting

**Plugin doesn't appear in bot logs** — Verify `plugins_dir` and `plugins.enabled` in main bot config.

**No emotes being stolen** — Check `auto_steal.enabled`, `vision.api_key`, blacklist, and that images are in allowed formats.

**Emote LLM not configured** — The `emote` section in config.yaml needs `model` and `api_key` set. Without it, `search_emote` returns an error.

**CDN refresh failing** — Check `discord.token` is correctly set. CDN refresh requires a valid bot token.

**Emote not sending** — Ensure `discord.token` is set (for independent sending). Verify the emote has either a `url` or `file_name` (not both empty).

## License

GPL-3.0 — Same as EZyapper.
