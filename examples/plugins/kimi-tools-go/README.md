# Kimi Official Tools Plugin

Standalone Kimi-specific tool plugin that integrates Kimi official tools via the Formula API.

## Features

- Loads tool schemas from formulas/{uri}/tools at startup.
- Exposes loaded tools to the EZyapper AI tool registry.
- Executes tool calls through formulas/{uri}/fibers.
- Supports multiple formulas in one plugin instance (for example web-search, convert, date).

## Configuration

Copy the config example into the plugin runtime folder.

Windows (PowerShell):

```powershell
New-Item -ItemType Directory -Force -Path plugins/kimi-tools-go | Out-Null
Copy-Item examples/plugins/kimi-tools-go/config.yaml.example plugins/kimi-tools-go/config.yaml
```

Linux/macOS:

```bash
mkdir -p plugins/kimi-tools-go
cp examples/plugins/kimi-tools-go/config.yaml.example plugins/kimi-tools-go/config.yaml
```

Config fields:

- base_url: Kimi API base URL, usually https://api.moonshot.cn/v1
- api_key: Kimi API key
- timeout_seconds: HTTP timeout in seconds
- formulas: formula URI list, at least one entry

Notes:

- function.name must be unique across all loaded formulas; duplicate names fail plugin startup.
- For protected tools such as web-search, encrypted_output is returned unchanged to the model.

## Build

Windows:

```powershell
go build -o kimi-tools-plugin.exe .
```

Linux/macOS:

```bash
go build -o kimi-tools-plugin .
```

## Deploy

Windows:

```powershell
Copy-Item examples/plugins/kimi-tools-go/kimi-tools-plugin.exe plugins/kimi-tools-go/kimi-tools-plugin.exe
```

Linux/macOS:

```bash
cp examples/plugins/kimi-tools-go/kimi-tools-plugin plugins/kimi-tools-go/
chmod +x plugins/kimi-tools-go/kimi-tools-plugin
```

If you build on Windows and then copy to Linux/macOS, execute permission may be missing after transfer.

Ensure plugin system is enabled in main config:

```yaml
operations:
  plugins:
    enabled: true
    plugins_dir: "plugins"
```