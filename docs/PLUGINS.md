# Plugin Development Guide

EZyapper plugins are external executable processes. The host supports two runtimes:

- `jsonrpc`: persistent lifecycle plugins over JSON-RPC 2.0 on stdio
- `command`: stateless command tools declared by `plugin.json`

EZyapper does not use Go native shared-library plugins.

## Runtime Modes

| Runtime | Startup | Hooks | Tool calls | Best for |
|---|---|---|---|---|
| `jsonrpc` | Host starts plugin binary and probes `info` over stdio JSON-RPC | `on_message`, `on_response`, optional `before_send`, `shutdown` | `list_tools`, `execute_tool` over JSON-RPC | Stateful plugins and hook pipelines |
| `command` | Host reads `plugin.json` tool definitions | Not used | Host executes command per tool call | Lightweight tools in Zig/C/Java/Python/etc. |

## Discovery and Loading

- Bot scans entries under `plugins.plugins_dir`.
- If an entry is a directory:
  - If `plugin.json` resolves to `runtime=command`, it is loaded as command runtime.
  - Otherwise the first executable file in that directory (sorted by filename) is loaded as persistent runtime.
- If an entry is an executable file, it is loaded as persistent runtime.

Runtime resolution from `plugin.json`:

- Explicit `"runtime": "command"` -> command runtime
- Explicit `"runtime": "jsonrpc"` -> jsonrpc runtime
- Missing `runtime` -> invalid manifest (plugin is not loaded)
- Missing manifest -> jsonrpc runtime

## Command Runtime Manifest

`plugin.json` example:

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
      "arg_keys": []
    }
  ]
}
```

Field notes:

- `command`: executable path or command name
- `args`: static arguments always passed first
- `arg_keys`: argument names appended in order from AI tool args
- `parameters`: JSON schema exposed to the AI tool registry

Execution behavior:

- Host resolves command path once during load (absolute path normalization)
- On each call, host executes `command + args + arg_keys values`
- Output is read from stdout and returned as tool result
- Timeout: 45 seconds per command tool call
- Host injects environment:
  - `EZYAPPER_PLUGIN_PATH`
  - `EZYAPPER_PLUGIN_CONFIG`

## JSON-RPC Runtime Interface

Persistent plugins implement `plugin.PluginInterface` and run `plugin.Serve(...)` from `main`:

```go
package plugin

type PluginInterface interface {
    Info() (PluginInfo, error)
    OnMessage(msg DiscordMessage) (bool, error)
    OnResponse(msg DiscordMessage, response string) error
    Shutdown() error
}
```

Optional interfaces:

- `ToolProvider` (`ListTools`, `ExecuteTool`)
- `BeforeSendProvider` (`BeforeSend`)

JSON-RPC methods used by host:

- `info`
- `on_message`
- `on_response`
- `before_send` (optional)
- `shutdown`
- `list_tools`
- `execute_tool`

JSON-RPC notes:

- Protocol: JSON-RPC 2.0 over stdin/stdout
- Request/response framing: JSON object stream (`encoding/json` decoder compatible)

## Environment Variables

Common (both runtimes):

- `EZYAPPER_PLUGIN_PATH`
- `EZYAPPER_PLUGIN_CONFIG`

## Timeouts

- Persistent plugin startup probe timeout: 90s
- Persistent plugin general call timeout: 5s
- `before_send` timeout: 180s
- Command tool execution timeout: 45s
- Shutdown graceful wait: 5s

## Cross-Platform Notes

- Windows:
  - `.exe` is auto-resolved for local command paths without extension
  - bare command names are resolved via `PATH` (for example `java`)
- Linux/macOS:
  - local binaries require executable bit (`chmod +x`)
- Relative command paths in `plugin.json` are resolved from plugin directory and stored as absolute paths to avoid breakage when process working directory changes.

## Troubleshooting

- `plugin failed to initialize jsonrpc`: plugin process did not respond to `info` probe within startup timeout or emitted invalid JSON-RPC responses
- `jsonrpc ... method not found`: plugin does not implement optional hook/tool method (for example `before_send`)
- `plugin command not found`: command runtime `command` path invalid or missing executable bit
- `plugin command execution failed`: command returned non-zero status; inspect stderr
- `jsonrpc call timeout`: plugin hook/tool blocked too long

## Reference Implementations

Persistent runtime examples:

- `examples/plugins/antispam-go/`
- `examples/plugins/openai-tts-go/`
- `examples/plugins/kimi-tools-go/`

Command runtime examples:

- `examples/plugins/datetime-zig/`
- `examples/plugins/clank-o-meter-zig/`
- `examples/plugins/systemspec-c/`
- `examples/plugins/datetime-java/`
