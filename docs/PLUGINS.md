# Plugins

EZyapper plugins are external executable processes — not Go shared libraries. The host supports two runtime modes; pick whichever matches your plugin's shape.

| Runtime | Process model | Use it for |
|---------|---------------|-----------|
| `jsonrpc` | Persistent: bot starts the binary at boot, talks JSON-RPC 2.0 over stdio | Stateful plugins, hook pipelines, anything that benefits from in-memory state |
| `command` | Stateless: bot runs the binary per tool call with mapped args | Lightweight tools in any language (Zig, C, Java, Python, …) |

There's no Go-native plugin loader. All plugins are subprocesses regardless of language.

---

## Plugin Interfaces (Go)

If you're writing a Go plugin, your `main` ends in `plugin.Serve(impl)` where `impl` satisfies `plugin.Interface`. Optional capabilities are detected via type assertion.

### Required: `plugin.Interface`

```go
type Interface interface {
    Info() (Info, error)
    OnMessage(msg types.DiscordMessage) (bool, error)
    OnResponse(msg types.DiscordMessage, response string) error
    Shutdown() error
}
```

| Method | Behavior |
|--------|----------|
| `Info()` | Metadata: Name, Version, Author, Description, Priority. Higher Priority runs first in hook chains. |
| `OnMessage(msg)` | Per-message hook. Return `false` to **block** the message (bot won't process or respond). |
| `OnResponse(msg, response)` | Fire-and-forget hook called after the bot generates a reply. |
| `Shutdown()` | Called when the plugin is being stopped. Clean up resources here. |

### Optional: `plugin.ToolProvider`

```go
type ToolProvider interface {
    ListTools() ([]ToolSpec, error)
    ExecuteTool(name string, args map[string]any) (string, error)
}
```

Implements LLM-callable tools. Returned `ToolSpec`s are merged into the bot's tool registry alongside the built-in Discord tools and any MCP tools.

### Optional: `plugin.BeforeSendProvider`

```go
type BeforeSendProvider interface {
    BeforeSend(msg types.DiscordMessage, response string) (BeforeSendResult, error)
}

type BeforeSendResult struct {
    Response string
    Files    []LocalFile  // attached to Discord message
    SkipSend bool         // true → don't send anything to Discord
}

type LocalFile struct {
    Path              string
    Name              string
    ContentType       string
    Data              []byte // optional in-memory payload, takes precedence over Path
    DeleteAfterUpload bool
}
```

Lets a plugin rewrite the bot's response, attach files (e.g., a TTS plugin generating audio), or abort sending entirely. Hooks run in priority order, and `BeforeSendResult.Response` carries forward to the next plugin.

---

## Manifest (`plugin.json`)

A directory-based plugin should ship a `plugin.json` manifest. The bot uses it to identify the runtime and (for `command` plugins) the tool definitions.

```json
{
  "runtime": "command",
  "name": "datetime-zig",
  "version": "0.0.0",
  "author": "EZyapper",
  "description": "Zig datetime tool",
  "priority": 10,
  "tools": [
    {
      "name": "get_current_datetime",
      "description": "Get current date and time",
      "parameters": {
        "type": "object",
        "properties": {}
      },
      "command": "./datetime-zig",
      "args": ["get_current_datetime"],
      "arg_keys": [],
      "timeout_ms": 0
    }
  ]
}
```

Field notes:

- `runtime` — `"jsonrpc"` or `"command"`. Required in any present manifest. Missing manifest → `jsonrpc` runtime is assumed.
- `priority` — higher runs first in hook chains.
- `tools[]` — only used for `command` runtime. Each entry defines a single LLM-callable tool.
  - `parameters` — JSON Schema exposed to the AI.
  - `command` — executable path. Relative paths are resolved against the plugin directory.
  - `args` — static arguments always passed first.
  - `arg_keys` — argument names appended in order. The host pulls each name from the LLM-supplied `arguments` object and appends its value to the command line.
  - `timeout_ms` — per-tool override of the manager's default timeout. `0` falls back to `default_tool_timeout_ms`.

---

## Discovery

At startup, the bot scans `operations.plugins.plugins_dir`. For each entry:

- **Directory**:
  - If `plugin.json` is present and valid → use the runtime from the manifest.
  - If no manifest → look for the first executable file (sorted alphabetically) and load it as `jsonrpc`.
- **Executable file** → loaded as `jsonrpc`.

A directory whose manifest has `runtime` missing or unrecognized is **skipped with an error**, not silently downgraded.

---

## JSON-RPC Runtime

### Protocol

JSON-RPC 2.0 over stdin/stdout. Each message is a single JSON object on the stream (newline-delimited works, but the host uses `encoding/json`'s decoder so any valid framing is fine).

Request:

```json
{"jsonrpc": "2.0", "id": 1, "method": "...", "params": {...}}
```

Response:

```json
{"jsonrpc": "2.0", "id": 1, "result": ..., "error": null}
```

### Methods the host calls

| Method | Required | Params | Result |
|--------|----------|--------|--------|
| `info` | yes | none | `Info` struct |
| `on_message` | yes | `types.DiscordMessage` | `bool` (false = block) |
| `on_response` | yes | `{message, response}` | none |
| `before_send` | optional | `{message, response}` | `BeforeSendResult` |
| `list_tools` | optional | none | `[]ToolSpec` |
| `execute_tool` | optional | `{name, arguments}` | `string` (result text) |
| `shutdown` | yes | none | none |

The host probes `info` once at startup. If the plugin doesn't respond within `startup_timeout_sec`, it's considered dead and skipped. Other calls use `rpc_timeout_sec`, except `before_send` which uses its own (typically longer) `before_send_timeout_sec` to accommodate things like TTS generation.

### Implementing in Go

`internal/plugin/server` provides a `Serve()` helper that wires the dispatch loop for you. A minimal plugin:

```go
package main

import (
    "ezyapper/internal/plugin"
    "ezyapper/internal/plugin/server"
    "ezyapper/internal/types"
)

type myPlugin struct{}

func (p *myPlugin) Info() (plugin.Info, error) {
    return plugin.Info{
        Name:    "my-plugin",
        Version: "0.1.0",
        Author:  "you",
        Description: "does a thing",
        Priority: 50,
    }, nil
}

func (p *myPlugin) OnMessage(msg types.DiscordMessage) (bool, error) {
    return true, nil
}

func (p *myPlugin) OnResponse(msg types.DiscordMessage, response string) error {
    return nil
}

func (p *myPlugin) Shutdown() error { return nil }

func main() {
    server.Serve(&myPlugin{})
}
```

For tool-providing plugins, also implement `ListTools()` + `ExecuteTool()`. For response-mutating plugins, also implement `BeforeSend()`.

---

## Command Runtime

Used for stateless tools written in any language. The bot doesn't keep a long-running process — it executes the binary fresh on every tool call.

Lifecycle:

1. Manifest loaded at startup; tool paths normalized to absolute (so the working directory doesn't matter).
2. Tools registered with the AI tool registry.
3. On a tool call: `exec.CommandContext` runs `command + args + arg_keys-mapped-values`.
4. stdout is captured and returned to the LLM as the tool result. stderr is logged.
5. Timeout: per-tool `timeout_ms` if set, else `command_timeout_sec * 1000`, else fail with "no timeout configured."

The host injects two environment variables into every plugin process:

- `EZYAPPER_PLUGIN_PATH` — absolute path to the plugin's binary or directory.
- `EZYAPPER_PLUGIN_CONFIG` — absolute path to a `config.yaml` next to the plugin (if one exists).

All other env vars are filtered. Anything containing `TOKEN`, `SECRET`, `KEY`, `PASSWORD`, `PASSWD`, `CREDENTIAL`, or `AUTH` is stripped before the subprocess starts. Plugins that need their own credentials should ship a `config.yaml` next to the binary and read it via `EZYAPPER_PLUGIN_CONFIG`.

---

## Cross-Platform Notes

- **Windows**: bare command names like `"java"` are resolved through `PATH`. Local paths without an extension automatically pick up `.exe` (the host normalizes once at load).
- **Linux/macOS**: local binaries need the executable bit (`chmod +x`).
- Relative `command` paths in `plugin.json` are resolved against the plugin's directory and stored as absolute paths so subsequent `cd` operations don't break tool execution.

---

## Argument Sanitization

Command-runtime arguments derived from LLM tool calls are passed through `sanitizeCommandArg`:

- Reject newlines (`\n`, `\r`) and null bytes.
- Strip ASCII control characters (except space and tab).
- Reject anything longer than 4096 bytes.

If a value fails sanitization, the tool call fails with an error and the LLM gets a structured "argument rejected" response. Plugins still need to validate their inputs — sanitization isn't input parsing.

---

## Timeouts (all configurable)

| Setting | What it controls | Where |
|---------|------------------|-------|
| `default_tool_timeout_ms` | Default tool-call timeout when neither the manifest nor the JSON-RPC tool spec sets one | `operations.plugins.default_tool_timeout_ms` |
| `startup_timeout_sec` | Time to wait for the first `info` probe at boot | `operations.plugins.startup_timeout_sec` |
| `rpc_timeout_sec` | Default JSON-RPC call timeout (`on_message`, `on_response`, `execute_tool`, …) | `operations.plugins.rpc_timeout_sec` |
| `before_send_timeout_sec` | Specifically for `before_send` (longer because TTS, image gen, etc.) | `operations.plugins.before_send_timeout_sec` |
| `command_timeout_sec` | Default for command-runtime tool execution | `operations.plugins.command_timeout_sec` |
| `shutdown_timeout_sec` | Grace period during graceful shutdown | `operations.plugins.shutdown_timeout_sec` |
| `disable_timeout_sec` | Grace period when toggling a plugin off via the WebUI | `operations.plugins.disable_timeout_sec` |

There are no hardcoded plugin timeouts. Everything is config-driven.

---

## Enable / Disable at Runtime

The WebUI's `/plugins` page can toggle individual plugins on and off. Disabling a plugin shuts down its RPC process (or marks the command tools inactive), removes its tools from the AI registry, and refreshes the schema cache. Re-enabling re-loads the plugin from disk.

Toggles are **not persisted to disk** — they reset on restart. To permanently disable a plugin, remove it from `plugins_dir` or set `operations.plugins.enabled: false` to skip the whole subsystem.

---

## Reference Implementations

The repo ships eleven example plugins under `examples/plugins/`. Each has its own README.

### Persistent (`jsonrpc`)

| Directory | Language | Highlights |
|-----------|----------|-----------|
| `antispam-go` | Go | Hooks-only plugin: blocks `OnMessage` when a user exceeds a configurable rate |
| `clank-o-meter-go` | Go | Tool that returns a deterministic 0–100 score for a Discord user ID |
| `datetime-go` | Go | Tool returning current date/time with configurable timezone |
| `emote-go` | Go | Two tools: `search_emote`, `send_emote`. Auto-steals emotes when missing. |
| `kimi-tools-go` | Go | Loads tool definitions from Kimi's Formula API at startup |
| `openai-tts-go` | Go | TTS pipeline using `before_send` to attach generated audio. Optional AI rewriter step. |
| `systemspec-go` | Go | Returns CPU/memory info about the host |

### Command (`runtime: "command"`)

| Directory | Language | Highlights |
|-----------|----------|-----------|
| `clank-o-meter-zig` | Zig | Same surface as `clank-o-meter-go`, written in Zig |
| `datetime-zig` | Zig | Same surface as `datetime-go` |
| `datetime-java` | Java | Same surface, requires JRE 17+. Uses `java -jar` in the manifest. |
| `systemspec-c` | C | Same surface as `systemspec-go`, native APIs (links `advapi32` on Windows) |

CI builds prebuilt binaries for all of these on the platforms it targets — see [DEPLOYMENT.md](DEPLOYMENT.md).

---

## Troubleshooting

| Symptom | Likely cause |
|---------|-------------|
| `plugin failed to initialize jsonrpc` | Binary didn't respond to `info` within `startup_timeout_sec`, or response wasn't valid JSON-RPC |
| `jsonrpc ... method not found` | You called an optional method (e.g., `before_send`) on a plugin that doesn't implement it. Usually harmless. |
| `plugin command not found` | Manifest's `command` path is wrong, or the file isn't executable |
| `plugin command execution failed` | Subprocess returned non-zero. Check stderr in the bot's logs. |
| `jsonrpc call timeout` | The plugin took longer than `rpc_timeout_sec` (or `before_send_timeout_sec`) to reply. Either it's stuck or you need to bump the timeout. |
| `no timeout configured` | Both `default_tool_timeout_ms` and the per-tool/per-spec timeout are zero. Set one. |
