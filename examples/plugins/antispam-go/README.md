# Antispam Go Plugin

Go persistent plugin example that blocks messages when a user exceeds configured message rate.

This plugin uses the persistent JSON-RPC runtime through `plugin.Serve(...)`.
For host/runtime details, see `docs/PLUGINS.md`.

## Features

- Tracks per-user message bursts in a sliding time window
- Blocks messages when threshold is exceeded
- Optional bot-user ignore behavior
- Optional detection logging

## Build

Windows:

```powershell
go build -o antispam-plugin.exe .
```

Linux/macOS:

```bash
go build -o antispam-plugin .
```

## Deploy

Windows:

```powershell
New-Item -ItemType Directory -Force -Path plugins/antispam-go | Out-Null
Copy-Item examples/plugins/antispam-go/antispam-plugin.exe plugins/antispam-go/antispam-plugin.exe
Copy-Item examples/plugins/antispam-go/config.yaml.example plugins/antispam-go/config.yaml
```

Linux/macOS:

```bash
mkdir -p plugins/antispam-go
cp examples/plugins/antispam-go/antispam-plugin plugins/antispam-go/
cp examples/plugins/antispam-go/config.yaml.example plugins/antispam-go/config.yaml
chmod +x plugins/antispam-go/antispam-plugin
```

Enable plugin system in main config:

```yaml
plugins:
  enabled: true
  plugins_dir: "plugins"
```

## Configuration

Config file path:

- `plugins/antispam-go/config.yaml`

Required keys:

- `max_messages` (positive integer)
- `time_window_seconds` (positive integer)
- `ignore_bots` (`true` or `false`)
- `log_detection` (`true` or `false`)

Example:

```yaml
max_messages: 6
time_window_seconds: 10
ignore_bots: true
log_detection: true
```

## Behavior

- `OnMessage` returns `false` when user exceeds limit, blocking normal bot processing for that message.
- `OnMessage` returns `true` when under limit.
- `OnResponse` is no-op.

## Troubleshooting

- Plugin not loading:
  - verify plugin binary exists in `plugins/antispam-go/`
  - verify `plugins.enabled=true` and `plugins.plugins_dir` points to correct root
  - verify `plugins/antispam-go/config.yaml` contains all required keys
- Linux/macOS execution issues:
  - ensure binary execute bit is set (`chmod +x ...`)
