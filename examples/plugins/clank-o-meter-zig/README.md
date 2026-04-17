# Clank-O-Meter Zig Plugin

Zig command plugin that provides the same tool behavior as the Go version:

- `get_clank_o_meter`

## Build

Windows:

```powershell
zig build-exe .\main.zig -O ReleaseSmall -fstrip -femit-bin=clank-o-meter-zig.exe
```

Linux/macOS:

```bash
zig build-exe ./main.zig -O ReleaseSmall -fstrip -femit-bin=clank-o-meter-zig
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
zig build-exe ./main.zig -O ReleaseSmall -fstrip -target x86_64-linux-gnu -femit-bin=clank-o-meter-zig
```

Build Linux arm64 binary:

```bash
zig build-exe ./main.zig -O ReleaseSmall -fstrip -target aarch64-linux-gnu -femit-bin=clank-o-meter-zig
```

Build Windows amd64 binary:

```bash
zig build-exe ./main.zig -O ReleaseSmall -fstrip -target x86_64-windows-gnu -femit-bin=clank-o-meter-zig.exe
```

Keep the output name aligned with `plugin.json` command (`./clank-o-meter-zig` on Unix-like systems, `./clank-o-meter-zig.exe` on Windows).

## Deploy

Copy files into your plugin directory:

```text
plugins/
  clank-o-meter-zig/
    plugin.json
    clank-o-meter-zig(.exe)
```

On Linux/macOS, ensure the binary has execute permission:

```bash
chmod +x plugins/clank-o-meter-zig/clank-o-meter-zig
```

If you build on Windows and then copy to Linux/macOS, execute permission may be missing after transfer.

## Notes

- This is a lightweight command-manifest example and does not require a persistent plugin process.
- Output format matches the existing Go plugin behavior.
