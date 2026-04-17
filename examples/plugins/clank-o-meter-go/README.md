# Clank-O-Meter Plugin

Provides one AI tool via the plugin system:
- `get_clank_o_meter`

## Input

- `user_id`: Discord numeric user ID

## Output

A deterministic score from 0 to 100.
The same user ID always maps to the same value.

## Algorithm

- Uses MD5 hash of `user_id`
- Converts first 8 bytes to uint64
- Computes `value % 101`

## Build

Windows:

```powershell
go build -o clank-o-meter-plugin.exe .
```

Linux/macOS:

```bash
go build -o clank-o-meter-plugin .
```

## Usage

Create a dedicated plugin folder and copy the binary there.

Windows example:

```powershell
New-Item -ItemType Directory -Force -Path plugins/clank-o-meter-go | Out-Null
Copy-Item clank-o-meter-plugin.exe plugins/clank-o-meter-go/clank-o-meter-plugin.exe
```

Linux/macOS example:

```bash
mkdir -p plugins/clank-o-meter-go
cp clank-o-meter-plugin plugins/clank-o-meter-go/
```

The bot will load it on startup.
