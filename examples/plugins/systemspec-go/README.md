# SystemSpec Plugin

Provides one AI tool via the plugin system:
- `get_system_spec`

`cpu_threads` is reported as logical CPU count (for example 16, 20), not as CPU package/socket count.

## Windows Note

On Windows, counting entries from `cpu.Info()` can incorrectly return `1` on some systems.
This plugin uses `cpu.Counts(true)` to provide a stable logical thread count.

## Build

Windows:

```powershell
go build -o systemspec-plugin.exe .
```

Linux/macOS:

```bash
go build -o systemspec-plugin .
```

## Usage

Create a dedicated plugin folder and copy the binary there.

Windows example:

```powershell
New-Item -ItemType Directory -Force -Path plugins/systemspec-go | Out-Null
Copy-Item systemspec-plugin.exe plugins/systemspec-go/systemspec-plugin.exe
```

Linux/macOS example:

```bash
mkdir -p plugins/systemspec-go
cp systemspec-plugin plugins/systemspec-go/
```

The bot will load it on startup.
