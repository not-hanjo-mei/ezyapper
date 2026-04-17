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

See `config.yaml.example` for all available options. Key sections:
- `discord`: Bot token, reply settings
- `ai`: LLM configuration, vision settings
- `embedding`: Vector embedding configuration
- `memory`: Long-term memory settings
- `qdrant`: Vector database configuration
- `blacklist`/`whitelist`: Access control
- `plugins`: Plugin system settings

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

All config options can be overridden via environment variables with `EZYAPPER_` prefix:

```bash
EZYAPPER_DISCORD_TOKEN=your_token
EZYAPPER_AI_API_KEY=your_key
EZYAPPER_AI_MODEL=gpt-4o-mini
```

See `.env.example` for the complete list.

## Prompt Optimization Example

The bot includes built-in prompt caching optimizations. Here's how to verify they're working:

### 1. Enable Debug Logging

```yaml
# config.yaml
logging:
  level: "debug"
  file: "bot.log"
```

### 2. Check Cache Hit Rates

After running the bot for a while, check the logs:

```bash
# View cache hit statistics
grep "\[cache\]" bot.log

# Example output:
# [cache] prompt cache hit: 850/1000 tokens (85.0%)
# [cache] prompt cache hit: 920/1050 tokens (87.6%)
```

### 3. Monitor Prompt Structure

```bash
# View prompt structure
grep "\[prompt\]" bot.log

# Example output:
# [prompt] system prompt length: 1250 chars (static)
# [prompt] dynamic context length: 450 chars
```

### 4. Using the Prompt Compiler

Example of using the prompt package in your own code:

```go
package main

import (
    "ezyapper/internal/prompt"
)

func main() {
    // Create a registry for your templates
    registry := prompt.NewRegistry()
    
    // Register a template
    registry.Register("greeting", 
        "Hello {UserName}! I'm {BotName} in {ServerName}.")
    
    // Compile with variables
    result := registry.GetWithCompile("greeting", map[string]string{
        "{UserName}":   "Alice",
        "{BotName}":    "MyBot",
        "{ServerName}": "Cool Server",
    })
    
    // Output: "Hello Alice! I'm MyBot in Cool Server."
    println(result)
}
```

### 5. Custom Tool Schema

If you're adding custom tools, ensure they're registered for caching:

```go
import "ezyapper/internal/ai"

func registerCustomTools(registry *ai.ToolRegistry) {
    registry.Register(&ai.Tool{
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
