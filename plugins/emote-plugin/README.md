# EZyapper Emote Plugin

Auto-steals emotes from Discord message image attachments and provides a
searchable emote library for the LLM via JSON-RPC tools.

## Features

- **Auto-steal** — Automatically detects and saves emotes/memes/stickers from
  image attachments using a Vision model
- **Search** — Find emotes by name, description, or tags via LLM tool calls
- **Per-guild storage** — Each Discord guild gets its own metadata and image
  directory
- **Deduplication** — SHA256 content hashing prevents saving the same image twice
- **Rate limiting** — Per-channel cooldown and per-minute cap to avoid spam
- **Blacklist/whitelist** — Channel and user filtering extends the main bot's rules

## Installation

### Prerequisites

- EZyapper main bot installed and configured
- A Discord bot token with message content intent
- An OpenAI-compatible Vision API key (for emote detection)

### Build

```bash
cd plugins/emote-plugin
go build -o emote-plugin.exe .
```

### Deploy

1. Create the plugin directory under the bot's plugins folder:

```bash
mkdir -p plugins/emote-plugin
cp emote-plugin.exe config.yaml plugins/emote-plugin/
```

2. Enable plugins in the main bot's `config.yaml`:

```yaml
operations:
  plugins:
    enabled: true
    plugins_dir: "plugins"
```

3. Restart the bot.

## Configuration

Copy `config.yaml` and edit the values. The plugin reads its config from the
path in the `EZYAPPER_PLUGIN_CONFIG` environment variable, falling back to
built-in defaults if no file is found.

### Full Configuration Reference

```yaml
# Storage — where emotes and metadata are saved
storage:
  data_dir: "data"             # Relative to plugin directory
  max_image_size_kb: 512       # Max download size in KB
  allowed_formats:             # Image formats to accept
    - "png"
    - "jpg"
    - "jpeg"
    - "webp"

# Vision — the model used to decide if an image is an emote
vision:
  api_key: ""                  # REQUIRED: OpenAI-compatible API key
  api_base_url: "https://api.openai.com/v1"
  model: "gpt-4o-mini"        # Must be a vision-capable model
  timeout_seconds: 30
  prompt: |                    # Prompt sent to the Vision model
    Analyze this image and determine if it is a "meme/emote/sticker"...

# Auto-steal — controls when and where stealing happens
auto_steal:
  enabled: true
  additional_blacklist_channels: []  # Channel IDs to always skip
  additional_whitelist_channels: []  # If non-empty, ONLY these channels work
  additional_blacklist_users: []     # User IDs to never steal from
  rate_limit_per_minute: 5           # Max steals per minute per channel
  cooldown_seconds: 2                # Minimum seconds between steals

# Logging
logging:
  enabled: true
  level: "info"                # debug, info, warn, error
```

### Defaults

When no config file is provided (or it is empty/missing), the plugin starts
with these defaults:

| Field | Default |
|-------|---------|
| `storage.data_dir` | `"data"` |
| `storage.max_image_size_kb` | `512` |
| `storage.allowed_formats` | `["png", "jpg", "jpeg", "webp"]` |
| `vision.api_base_url` | `"https://api.openai.com/v1"` |
| `vision.model` | `"gpt-4o-mini"` |
| `vision.timeout_seconds` | `30` |
| `auto_steal.enabled` | `true` |
| `auto_steal.rate_limit_per_minute` | `5` |
| `auto_steal.cooldown_seconds` | `2` |
| `logging.enabled` | `true` |
| `logging.level` | `"info"` |

## How It Works

### Auto-Steal Flow

1. The main bot forwards every Discord message to the plugin via `OnMessage`
2. If `auto_steal.enabled` is `false`, skip immediately
3. If there are no image attachments, skip
4. For each attachment URL:
   - Check blacklist/whitelist — block filtered channels and users
   - Check rate limit — enforce per-channel cooldown and per-minute cap
   - Download the image (max 10 MB)
   - Check file size against `max_image_size_kb`
   - Compute SHA256 and check dedup — skip if already stored
   - Send to Vision model for analysis — skip if not an emote
   - Check file format against `allowed_formats`
   - Save image to disk and add metadata entry

### Tools Exposed to LLM

The plugin registers three tools that the LLM can call:

| Tool | Description |
|------|-------------|
| `list_emotes` | List available emotes with optional guild filter and limit |
| `search_emote` | Search emotes by name, description, or tags (sorted by relevance) |
| `get_emote` | Get a specific emote by ID or name |

## Storage Layout

```
plugins/emote-plugin/data/
  <guild_id>/
    metadata.json        # Emote entries for this guild
    images/
      <uuid>.png         # Saved emote images
```

Each guild (or `"global"` for DMs) gets its own directory with atomic metadata
writes (temp file + rename) to prevent corruption on crash.

## Troubleshooting

**Plugin doesn't appear in bot logs** — Check that `plugins_dir` is correct and
`plugins.enabled` is `true` in the main bot config.

**No emotes being stolen** — Verify:
- `auto_steal.enabled` is `true`
- `vision.api_key` is set and valid
- Images are PNG/JPG/JPEG/WEBP (GIF is excluded)
- The channel/user is not blacklisted
- Cooldown hasn't been triggered recently

**"emote file not found on disk" errors** — This means the metadata entry
exists but the image file was deleted. Either restore from backup or remove the
metadata entry.

## License

GPL-3.0 — Same as EZyapper.
