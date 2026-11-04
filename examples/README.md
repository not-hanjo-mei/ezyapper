# EZyapper Examples

This directory contains example configurations and plugins for EZyapper.

## Structure

```
examples/
├── config.yaml.example    # Example main configuration file
├── .env.example          # Example environment variables
└── plugins/
    ├── antispam-go/      # Example anti-spam external plugin (Go)
    ├── clank-o-meter-go/ # Example deterministic score tool plugin (Go)
    ├── datetime-go/      # Example datetime tool plugin (Go)
    ├── openai-tts-go/    # Example OpenAI-compatible TTS plugin (Go)
    ├── datetime-zig/     # Example datetime command plugin (Zig)
    ├── datetime-java/    # Example datetime command plugin (Java)
    ├── clank-o-meter-zig/# Example deterministic score command plugin (Zig)
    ├── systemspec-go/    # Example system specification plugin (Go)
    ├── systemspec-c/     # Example system specification command plugin (C)
    └── kimi-tools-go/    # Kimi official tools integration plugin (Go)
```

## Quick Start

### 1. Configuration

Copy the example config to your project root:

```bash
cp examples/config.yaml.example config.yaml
# Edit config.yaml with your settings
```

Or use environment variables:

```bash
cp examples/.env.example .env
# Edit .env with your settings
```

### 2. Example Plugins

Available plugin examples:

- `plugins/antispam-go/`: message rate limiting with plugin config
- `plugins/clank-o-meter-go/`: deterministic score tool provider example
- `plugins/datetime-go/`: simple datetime tool provider example
- `plugins/openai-tts-go/`: OpenAI-compatible text-to-speech tool plugin
- `plugins/datetime-zig/`: lightweight Zig command plugin for datetime tool
- `plugins/datetime-java/`: lightweight Java command plugin for datetime tool
- `plugins/clank-o-meter-zig/`: lightweight Zig command plugin for deterministic score tool
- `plugins/systemspec-go/`: host system information tool
- `plugins/systemspec-c/`: lightweight C command plugin for host system information tool
- `plugins/kimi-tools-go/`: Kimi Formula official tools integration

See each plugin's `README.md` for details.

## Creating Your Own Plugin

1. Copy the antispam example:
```bash
cp -r examples/plugins/antispam-go examples/plugins/myplugin
cd examples/plugins/myplugin
```

2. Edit `main.go` to implement your logic

3. Build the plugin:
```bash
go build -o myplugin main.go
```

4. Copy to your bot's plugins directory:
```bash
mkdir -p /path/to/bot/plugins/myplugin
cp myplugin config.yaml /path/to/bot/plugins/myplugin/
```

**Note:** EZyapper uses executable process plugins with JSON-RPC over stdio for persistent plugins, plus `plugin.json` command runtime for stateless tools.

## Configuration Reference

### Main Config (config.yaml)

See `config.yaml.example` for all available options (`config.schema.json`
describes the same keys for editor validation). Top-level sections:
- `core`: `discord` (bot token, reply settings), `ai` (main model, system
  prompt, `modalities` for image/audio/video/sticker handling) and `decision`
  (reply classifier)
- `memory_pipeline`: `embedding`, `memory` (short-term window, retrieval,
  consolidation) and `qdrant` (vector database)
- `access_control`: `blacklist` / `whitelist` and `message_filters`
- `operations`: `web`, `logging`, `plugins`, `mcp` and `runtime`

### Plugin Config

Each plugin can have its own `config.yaml` in its directory. The plugin receives its path via the `EZYAPPER_PLUGIN_PATH` environment variable.

Example plugin config:
```yaml
# Located at: plugins/myplugin/config.yaml
max_messages: 5
time_window: 10s
enabled: true
```

## Environment Variables

Config keys can be overridden via environment variables with the `EZYAPPER_`
prefix and the full YAML path, dots replaced by underscores:

```bash
EZYAPPER_CORE_DISCORD_TOKEN=your_token
EZYAPPER_CORE_AI_API_KEY=your_key
EZYAPPER_CORE_AI_MODEL=gpt-4o-mini
```

An override only applies to a key that is present in `config.yaml`; a key
that is missing (or commented out) there is not read from the environment, so
keep a placeholder value for anything you set via env. List values (access
control IDs, MCP servers) are configured in `config.yaml`.

See `.env.example` for more.

## Prompt Optimization Example

The bot includes built-in prompt caching optimizations. Here's how to verify they're working:

### 1. Enable Debug Logging

```yaml
# config.yaml
operations:
  logging:
    level: "debug"
    file: "logs/ezyapper.log"
```

### 2. Check Token Usage

The bot does not log provider cache hits itself. At debug level it logs the
token usage of every response; compare prompt tokens across requests, or
check your provider's dashboard for cached tokens:

```bash
grep -A4 "\[ai\] received response" logs/ezyapper.log

# Example output:
# [ai] received response:
#   Finish reason: stop
#   Prompt tokens: 1850
#   Completion tokens: 120
#   Total tokens: 1970
```

### 3. Monitor Prompt Structure

```bash
grep "\[prompt\]" logs/ezyapper.log

# Example output:
# [prompt] system prompt: 1250 chars (static), user prompt: 450 chars
```

### 4. Writing the System Prompt

`core.ai.system_prompt` is sent as written, so it stays identical across
requests and the provider can cache it. No placeholders are substituted:
write the bot's name literally. The author, server and channel of each
message are sent in the user prompt instead. The old placeholders
`{BotName}`, `{AuthorName}`, `{ServerName}`, `{GuildID}` and `{ChannelID}`
are rejected at startup (in `core.ai.system_prompt` and
`core.decision.system_prompt`).

```yaml
core:
  ai:
    system_prompt: |
      You are EZyapper, a helpful Discord bot.
      Keep replies short and friendly.
```

### 5. Custom Tool Schema

If you're adding built-in tools inside this module, register them on the tool
registry, which keeps the schema sorted and cached:

```go
import "ezyapper/internal/ai/tools"

func registerCustomTools(registry *tools.ToolRegistry) {
    registry.Register(&tools.Tool{
        Name:        "my_custom_tool",
        Description: "Does something useful",
        Parameters: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "param1": map[string]string{
                    "type": "string",
                },
            },
        },
        Handler: myHandler,
    })
    // Schema is automatically cached and sorted
}
```

### 6. Using get_current_datetime Tool

When the `datetime` plugin is built and loaded, the bot exposes a `get_current_datetime` tool that returns the current date/time:

```go
// The LLM can call this tool when it needs temporal information
// Example user queries that trigger this tool:
// - "What's today's date?"
// - "How many days until my birthday?"
// - "What time is it now?"
```

**Tool Response:**
```json
{
  "date": "2026-02-26",
  "time": "15:47:22",
    "timezone": "SYSTEM_OR_CONFIG_TZ",
  "weekday": "Thursday",
  "unix_seconds": 1708932442
}
```

`timezone` defaults to the system timezone and can be fixed via plugin `config.yaml`.

**Note:** This tool preserves prompt caching by not including date in the system prompt.

## More Examples

Want to contribute an example? Create a new directory under `examples/` with:
- Clear README.md
- Working code/configuration
- Usage instructions

Examples we're looking for:
- Custom moderation plugins
- Integration examples (GitHub, Jira, etc.)
- Message filtering plugins
- Analytics/statistics plugins
