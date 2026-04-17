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

## Notes

- plugin.json uses command: java, so Java must be available in PATH.
- This plugin is a command-manifest example and does not require a persistent plugin process.
