# Datetime Zig Plugin

Zig command plugin that provides:

- `get_current_datetime`

## Build

Windows:

```powershell
zig build-exe .\main.zig -O ReleaseSmall -fstrip -femit-bin=datetime-zig.exe
```

Linux/macOS:

```bash
zig build-exe ./main.zig -O ReleaseSmall -fstrip -femit-bin=datetime-zig
```

## Cross-Compile with Zig

Common target triples:

- Linux amd64: `x86_64-linux-gnu`
- Linux arm64: `aarch64-linux-gnu`
- Windows amd64: `x86_64-windows-gnu`
- Windows arm64: `aarch64-windows-gnu`

Examples (can be run from any host OS):

Build Linux amd64 binary:

```bash
zig build-exe ./main.zig -O ReleaseSmall -fstrip -target x86_64-linux-gnu -femit-bin=datetime-zig
```

Build Linux arm64 binary:

```bash
zig build-exe ./main.zig -O ReleaseSmall -fstrip -target aarch64-linux-gnu -femit-bin=datetime-zig
```

Build Windows amd64 binary:

```bash
zig build-exe ./main.zig -O ReleaseSmall -fstrip -target x86_64-windows-gnu -femit-bin=datetime-zig.exe
```

Keep the output name aligned with `plugin.json` command (`./datetime-zig` on Unix-like systems, `./datetime-zig.exe` on Windows).

## Deploy

Copy files into your plugin directory:

```text
plugins/
  datetime-zig/
    plugin.json
    datetime-zig(.exe)
    config.yaml   # optional
```

On Linux/macOS, ensure the binary has execute permission:

```bash
chmod +x plugins/datetime-zig/datetime-zig
```

If you build on Windows and then copy to Linux/macOS, execute permission may be missing after transfer.

## Configuration

Config file location:

- `plugins/datetime-zig/config.yaml`

Supported keys:

- `timezone` (string): timezone label returned in tool output
- `utc_offset_hours` (integer): fixed UTC offset in hours
- `utc_offset_minutes` (integer): fixed UTC offset in minutes (higher priority than `utc_offset_hours`)

Default behavior (when config file is missing):

- Uses host system timezone metadata provided by the plugin manager.

Example config:

```yaml
timezone: "Asia/Pyongyang"
utc_offset_hours: 9
```

## Notes

- By default, timezone follows the host system timezone.
- You can pin a fixed timezone/offset with `config.yaml`.
- This plugin is a command-manifest example and does not require a persistent plugin process.
