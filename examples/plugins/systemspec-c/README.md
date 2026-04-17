# SystemSpec C Plugin

C command plugin that provides:

- get_system_spec

This plugin returns the same output fields as the Go systemspec example:

- cpu_model
- cpu_threads
- cpu_max_freq_mhz
- memory_total_gb

## Cross-Platform Notes

The implementation uses platform-native sources:

- Windows: Registry + Win32 APIs (`GetActiveProcessorCount`, `GlobalMemoryStatusEx`)
- macOS: `sysctl` (`machdep.cpu.brand_string`, `hw.logicalcpu`, `hw.cpufrequency_max`, `hw.memsize`)
- BSD family: `sysctl` (`hw.model`, `hw.ncpu`, `dev.cpu.0.freq`/`hw.clockrate`, `hw.physmem64`)
- Linux: `/proc/cpuinfo`, `/sys/devices/system/cpu/...`, `/proc/meminfo`

If a platform key is unavailable, the tool falls back to safe defaults (for example `Unknown`, `0.00`).

## Build

Windows (using zig cc):

```powershell
Set-Location examples/plugins/systemspec-c
zig cc -O2 -s -o systemspec-c.exe main.c -ladvapi32
```

Linux/macOS (using zig cc):

```bash
cd examples/plugins/systemspec-c
zig cc -O2 -s -o systemspec-c main.c
```

If you prefer GCC/Clang, equivalent commands also work.

## Cross-Compile with Zig

Common target triples:

- Linux amd64: `x86_64-linux-gnu`
- Linux arm64: `aarch64-linux-gnu`
- Windows amd64: `x86_64-windows-gnu`
- Windows arm64: `aarch64-windows-gnu`

Examples (can be run from any host OS):

Build Linux amd64 binary:

```bash
zig cc -O2 -s -target x86_64-linux-gnu -o systemspec-c main.c
```

Build Linux arm64 binary:

```bash
zig cc -O2 -s -target aarch64-linux-gnu -o systemspec-c main.c
```

Build Windows amd64 binary:

```bash
zig cc -O2 -s -target x86_64-windows-gnu -o systemspec-c.exe main.c -ladvapi32
```

Keep the output name aligned with `plugin.json` command (`./systemspec-c` on Unix-like systems, `./systemspec-c.exe` on Windows).

## Deploy

Copy files into your plugin directory:

```text
plugins/
  systemspec-c/
    plugin.json
    systemspec-c(.exe)
```

On Linux/macOS, ensure the binary has execute permission:

```bash
chmod +x plugins/systemspec-c/systemspec-c
```

If you build on Windows and then copy to Linux/macOS, execute permission may be missing after transfer.

## Notes

- This is a lightweight command-manifest example and does not require a persistent plugin process.
- No plugin-specific config file is required.
