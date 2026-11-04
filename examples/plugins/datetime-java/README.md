# Datetime Java Plugin

Java command plugin that provides:

- get_current_datetime

## Requirements

- Java Runtime Environment (JRE) 17+
- Java compiler (javac) 17+

## Build

Windows PowerShell:

```powershell
Set-Location examples/plugins/datetime-java
New-Item -ItemType Directory -Force -Path out | Out-Null
javac -encoding UTF-8 -d out src/DatetimeTool.java
jar --create --file datetime-java.jar -C out .
```

Linux/macOS:

```bash
cd examples/plugins/datetime-java
mkdir -p out
javac -encoding UTF-8 -d out src/DatetimeTool.java
jar --create --file datetime-java.jar -C out .
```

## Deploy

Copy files into your plugin directory:

```text
plugins/
  datetime-java/
    plugin.json
    datetime-java.jar
    config.yaml   # optional
```

## Configuration

Config file location:

- plugins/datetime-java/config.yaml

Supported keys:

- timezone (string): timezone label returned in tool output
- utc_offset_hours (integer): fixed UTC offset in hours
- utc_offset_minutes (integer): fixed UTC offset in minutes (higher priority than utc_offset_hours)

Default behavior (when config file is missing):

- Uses Java system timezone (ZoneId.systemDefault()).

Example config:

```yaml
timezone: "Asia/Pyongyang"
utc_offset_hours: 9
# utc_offset_minutes: 540
```

## Config changes (no restart)

This is a `command` plugin: the bot starts a new process for every tool call and passes the config path in `EZYAPPER_PLUGIN_CONFIG`, so `config.yaml` is read again on every call. Edit and save it and the next `get_current_datetime` call uses the new timezone/offset; the bot and other plugins are not restarted, and the plugin needs no file watcher.

If the file is invalid (for example a non-integer `utc_offset_hours` or an unknown `timezone` region ID), that call fails with `failed to load config: ...` on stderr, which shows up in the bot's log as `plugin command execution failed`. There is no previous config to fall back to in a fresh process; the plugin stays loaded and works again on the first call after the file is fixed.

## Notes

- plugin.json uses command: java, so Java must be available in PATH.
- This plugin is a command-manifest example and does not require a persistent plugin process.
