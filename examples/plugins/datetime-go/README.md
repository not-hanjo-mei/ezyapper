# Datetime Plugin

Provides one AI tool via the plugin system:
- `get_current_datetime`

The timezone output and local time calculation are configurable via plugin config.

## Build

Windows:

```powershell
go build -o datetime-plugin.exe .
```

Linux/macOS:

```bash
go build -o datetime-plugin .
```

## Usage

Create a dedicated plugin folder and copy the binary there.

Windows example:

```powershell
New-Item -ItemType Directory -Force -Path plugins/datetime-go | Out-Null
Copy-Item datetime-plugin.exe plugins/datetime-go/datetime-plugin.exe
```

Linux/macOS example:

```bash
mkdir -p plugins/datetime-go
cp datetime-plugin plugins/datetime-go/
```

The bot will load it on startup.

## Configuration

Config file location:

- `plugins/datetime-go/config.yaml`

Supported keys:

- `timezone` (string):
	- If used alone: IANA timezone to load (for example `Asia/Shanghai`)
	- If used with fixed offset: label returned in output (for example `Beijing`)
- `utc_offset_hours` (integer): fixed UTC offset in hours
- `utc_offset_minutes` (integer): fixed UTC offset in minutes (higher priority than `utc_offset_hours`)

Default behavior (when config file is missing):

- Uses system timezone from host runtime

Example config:

```yaml
# Mode 1: fixed IANA timezone
timezone: "Asia/Pyongyang"

# Mode 2: fixed offset (optional timezone label)
# timezone: "UTC+09"
# utc_offset_hours: 9
# utc_offset_minutes: 540
```
