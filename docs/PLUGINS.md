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
    Shutdown() error
}
```

| Method | Behavior |
|--------|----------|
| `Info()` | Metadata: Name, Version, Author, Description, Priority. Higher Priority runs first in hook chains. |
| `OnMessage(msg)` | Per-message hook. Return `false` to **block** the message (bot won't process or respond). If the call fails (an error, a timeout or a dead process), that plugin is skipped for this message and the next plugin is asked; a failing plugin never blocks a message. |
| `Shutdown()` | Called when the plugin is being stopped. Clean up resources here. |

### Hook chain

Every message goes through these hooks, in plugin priority order:

| Hook (JSON-RPC method) | When | Can do |
|------------------------|------|--------|
| `OnMessage` (`on_message`) | every incoming message | block all further processing |
| `AfterDecision` (`after_decision`) | only when the bot decided to reply, before generation | cancel the reply; inject prompt context |
| `BeforeSend` (`before_send`) | after generation, before sending | rewrite the text, attach files, cancel sending |
| `AfterSend` (`after_send`) | after the reply was sent | react to the final text and sent message IDs |

`OnResponse` no longer exists: what it did belongs in `AfterSend`, which
receives the text that was actually sent (after `BeforeSend` rewrites) and the
IDs of the Discord messages (several when a long reply was split).

### Optional: `plugin.AfterDecisionProvider`

```go
type AfterDecisionProvider interface {
    AfterDecision(args AfterDecisionArgs) (AfterDecisionResult, error)
}

type AfterDecisionArgs struct {
    Message types.DiscordMessage
    Reason  string // why the bot decided to reply
}

type AfterDecisionResult struct {
    Cancel  bool           // true → do not generate a reply
    Context []ContextBlock // prompt sections to inject for this reply
}

type ContextBlock struct {
    Tag     string // XML tag name: letters, digits, '_' and '-'
    Content string
}
```

Injected blocks are rendered as `<Tag>...</Tag>` sections in the plugin slot
of the **user** prompt (after the conversation and participants, before the
user profile and key facts). They never go into the system prompt, which
stays byte-identical across requests so prompt caching keeps working.

### Optional: `plugin.SystemPromptProvider`

```go
type SystemPromptProvider interface {
    SystemPrompt() (ContextBlock, error)
}
```

Queried once when the plugin loads. The block is appended to the system
prompt after the configured instructions. It must be static: do not put
per-message data here (use `AfterDecision` context blocks instead).

### Optional: `plugin.AfterSendProvider`

```go
type AfterSendProvider interface {
    AfterSend(args AfterSendArgs) error
}

type AfterSendArgs struct {
    Message    types.DiscordMessage
    Response   string   // final text that was sent
    MessageIDs []string // IDs of the sent Discord messages, in order
}
```

### Optional: `plugin.ToolProvider`

```go
type ToolProvider interface {
    ListTools() ([]ToolSpec, error)
    ExecuteTool(name string, args map[string]any) (string, error)
}
```

Implements LLM-callable tools. Returned `ToolSpec`s are merged into the bot's tool registry alongside the built-in Discord tools and any MCP tools.

`ToolSpec.TimeoutMs` is a `*int` (JSON `timeout_ms`). Leave it `nil` (the key is then absent on the wire) to inherit `operations.plugins.default_tool_timeout_ms`; set it with `new(15000)` to use your own. An explicit value must be greater than 0: a tool list with `timeout_ms: 0` is rejected (at startup the plugin fails to load; on `tools_changed` the previous list is kept). Before this was a pointer, `TimeoutMs: 0` meant "inherit"; that zero-means-absent rule is gone.

### Optional: `plugin.BeforeSendProvider`

```go
type BeforeSendProvider interface {
    BeforeSend(args BeforeSendArgs) (BeforeSendResult, error)
}

type BeforeSendArgs struct {
    Message        types.DiscordMessage
    Response       string
    DeadlineUnixMs int64 // the host stops waiting at this time; finish before it
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
      "timeout_ms": 5000
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
  - `timeout_ms` — per-tool timeout in ms. Absent → inherits `operations.plugins.command_timeout_sec`; an explicit value must be greater than 0 (`0` is rejected when the plugin loads; remove the key to inherit). `default_tool_timeout_ms` is not used for command tools.

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

| Method | Required | Params | Result | Timeout |
|--------|----------|--------|--------|---------|
| `info` | yes | none | `Info` struct | `startup_timeout_sec` |
| `on_message` | yes | `types.DiscordMessage` | `bool` (false = block) | `rpc_timeout_sec` |
| `after_decision` | optional | `{message, reason}` | `AfterDecisionResult` | `rpc_timeout_sec` |
| `before_send` | optional | `{Message, Response, deadline_unix_ms}` (see note) | `BeforeSendResult` | `before_send_timeout_sec` |
| `after_send` | optional | `{message, response, message_ids}` | none | `rpc_timeout_sec` |
| `system_prompt` | optional | none | `ContextBlock` | `rpc_timeout_sec` |
| `list_tools` | optional | none | `[]ToolSpec` | `rpc_timeout_sec` |
| `execute_tool` | optional | `{Name, Arguments}` (see note) | `string` (result text) | the tool spec's `timeout_ms`, else `default_tool_timeout_ms` |
| `shutdown` | yes | none | none | `rpc_timeout_sec` |

These are the only methods; `on_response` was removed (use `after_send`). "Optional" means a plugin may answer with JSON-RPC "method not found" (`-32601`) and the host moves on. The Go `server.Serve` helper instead answers an optional method the plugin does not implement with an empty result (no tools, no context, unchanged response), except `execute_tool`, which returns an error.

Note on parameter names: `before_send` and `execute_tool` params are encoded from Go structs without JSON tags for most fields, so the keys are capitalised: `{"Message": ..., "Response": "...", "deadline_unix_ms": ...}` and `{"Name": "...", "Arguments": {...}}`. Go plugins decode them case-insensitively; a plugin in another language must read these exact keys. The other params use lowercase keys (`message`, `reason`, `response`, `message_ids`).

The host probes `info` once at startup. If the plugin doesn't respond within `startup_timeout_sec`, it's considered dead and skipped. `list_tools` and `system_prompt` are called right after `info`, and again when the plugin sends `tools_changed`. `before_send` uses its own (typically longer) `before_send_timeout_sec` to accommodate things like TTS generation. If neither the tool spec nor `default_tool_timeout_ms` gives `execute_tool` a timeout, the call fails with "no timeout configured".

### Failures and automatic restart

- A call that times out marks the plugin's transport dead and closes its pipes.
- `on_message`, `after_decision` and `after_send`: a failing plugin is skipped for this message and the remaining plugins still run. `on_message` therefore never blocks a message because of an error.
- `before_send`: a timeout or dead plugin is skipped for this message; any other error returned by the plugin aborts sending the reply.
- When a hook or tool call finds a plugin's transport dead, the host restarts the plugin in the background (stops the process, starts it again and re-runs `info`, `list_tools` and `system_prompt`). A successful restart resets the failure count. After `max_restarts` consecutive failed restarts, no further attempt is made until `restart_cooldown_sec` has passed since the last failed one; each attempt after that which fails waits for the cooldown again.

### Notifications the plugin may send

| Notification | Meaning |
|--------------|---------|
| `tools_changed` | The plugin's tool list or system prompt section changed (typically after it reloaded its own config). The host re-queries `list_tools` and `system_prompt`, validates the result (an invalid tool list is rejected and the previous one kept; a failing `system_prompt` keeps the previous section) and re-registers the plugin's tools with the model. |

A notification is a JSON-RPC message with a `method` and **no** `id`:

```json
{"jsonrpc": "2.0", "method": "tools_changed"}
```

The transport is strictly request/response, so the host reads a notification during its next exchange with the plugin. Every incoming Discord message reaches each plugin through `on_message`, so in practice the refresh lands with the next message. This was chosen over periodic `list_tools` polling (constant traffic for a rare event, plus a new config knob) and over a background reader on the plugin's stdout (a larger change to a transport that is shared with request timeouts and restarts). Go plugins call `server.NotifyToolsChanged()`, which is safe from any goroutine because the server serialises all writes to stdout.

### Config hot reload

A plugin owns its config file and reloads it itself; the host is not involved. Go plugins use `internal/plugin/pluginconfig`:

```go
cfg := pluginconfig.NewValue(initialConfig) // read with cfg.Load()

w, err := pluginconfig.Watch(pluginconfig.Path(), pluginconfig.Options{Name: "MYPLUGIN"}, func() error {
    next, err := loadConfig() // read AND validate; build anything derived from it
    if err != nil {
        return err // logged to stderr, the previous config stays live
    }
    cfg.Store(next)
    _ = server.NotifyToolsChanged() // tool specs or the system prompt section may have changed
    return nil
})
if err == nil {
    defer w.Close()
}
```

`Watch` debounces file events and handles editors that save by renaming a temporary file. When the config feeds clients or state built at startup (HTTP clients, fetched tool lists, data directories), build a complete new instance and swap it in only if that succeeded; `kimi-tools-go`, `openai-tts-go` and `emote-go` do this with a small `reloadingPlugin` wrapper. On `tools_changed` the host re-reads both `list_tools` and `system_prompt`.

Environment knobs: `EZYAPPER_PLUGIN_CONFIG_WATCH=off` disables watching, `EZYAPPER_PLUGIN_CONFIG_DEBOUNCE_MS` changes the debounce (default 500).

All Go example plugins (`antispam`, `clank-o-meter`, `datetime`, `emote`, `kimi-tools`, `openai-tts`, `systemspec`) reload their config on save.

Command-runtime plugins need no watcher: the host starts a new process for every tool call, so a plugin that reads `EZYAPPER_PLUGIN_CONFIG` on each run picks up an edited `config.yaml` on the next call, without a restart. See [Config changes](#config-changes) under Command Runtime.

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
5. Timeout: per-tool `timeout_ms` if the key is present, else `command_timeout_sec`. The process is killed when it runs out.

The host injects two environment variables into every plugin process:

- `EZYAPPER_PLUGIN_PATH` — absolute path to the plugin's binary or directory.
- `EZYAPPER_PLUGIN_CONFIG` — absolute path of the plugin's `config.yaml` (always set; the file itself may not exist).

All other env vars are filtered. Anything containing `TOKEN`, `SECRET`, `KEY`, `PASSWORD`, `PASSWD`, `CREDENTIAL`, or `AUTH` is stripped before the subprocess starts. Plugins that need their own credentials should ship a `config.yaml` next to the binary and read it via `EZYAPPER_PLUGIN_CONFIG`.

### Config changes

Because every tool call is a fresh process, a command plugin reads its `config.yaml` again on every call. Edit and save the file and the next call uses the new values; the bot, the plugin's registration and other plugins are not touched. Nothing needs to watch the file.

The difference from a `jsonrpc` plugin's hot reload: a fresh process has no previous config to fall back to. If the file is broken, the plugin exits non-zero, that call fails and its stderr appears in the bot's log (`plugin command execution failed: ...`); the plugin stays loaded and the first call after the file is fixed succeeds again.

The manifest (`plugin.json`, including tool definitions and `timeout_ms`) is read when the plugin is loaded; changing it needs the plugin to be disabled and re-enabled (WebUI `/plugins`) or a plugin directory reload.

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
| `default_tool_timeout_ms` | `execute_tool` timeout for a JSON-RPC tool whose spec has no `timeout_ms` key (not used by command tools) | `operations.plugins.default_tool_timeout_ms` |
| `startup_timeout_sec` | Time to wait for the first `info` probe at boot | `operations.plugins.startup_timeout_sec` |
| `rpc_timeout_sec` | JSON-RPC calls other than `info`, `before_send` and `execute_tool`: `on_message`, `after_decision`, `after_send`, `system_prompt`, `list_tools`, `shutdown` | `operations.plugins.rpc_timeout_sec` |
| `before_send_timeout_sec` | Specifically for `before_send` (longer because TTS, image gen, etc.) | `operations.plugins.before_send_timeout_sec` |
| `command_timeout_sec` | Command-runtime tool execution when the manifest has no `timeout_ms` key | `operations.plugins.command_timeout_sec` |
| `max_restarts` | Consecutive failed automatic restarts before the cooldown applies | `operations.plugins.max_restarts` |
| `restart_cooldown_sec` | Wait after `max_restarts` failed restarts before trying again | `operations.plugins.restart_cooldown_sec` |
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
| `datetime-java` | Java | Same surface, requires JRE 17+. Runs `java -cp ./datetime-java.jar DatetimeTool` from the manifest. |
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
| `no timeout configured` | A JSON-RPC tool has no `timeout_ms` in its spec and `default_tool_timeout_ms` is `0`. Set one. (Command tools fall back to `command_timeout_sec` instead.) |
