import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Instant;
import java.time.ZoneId;
import java.time.ZoneOffset;
import java.time.ZonedDateTime;
import java.time.format.DateTimeFormatter;
import java.time.format.TextStyle;
import java.util.List;
import java.util.Locale;

public final class DatetimeTool {
    private static final String TOOL_NAME = "get_current_datetime";
    private static final DateTimeFormatter DATE_FORMAT = DateTimeFormatter.ofPattern("yyyy-MM-dd", Locale.ROOT);
    private static final DateTimeFormatter TIME_FORMAT = DateTimeFormatter.ofPattern("HH:mm:ss", Locale.ROOT);

    private DatetimeTool() {}

    private static final class Config {
        final ZoneId zoneId;
        final String timezoneLabel;

        Config(ZoneId zoneId, String timezoneLabel) {
            this.zoneId = zoneId;
            this.timezoneLabel = timezoneLabel;
        }
    }

    public static void main(String[] args) {
        if (args.length != 1) {
            System.err.println("usage: DatetimeTool get_current_datetime");
            System.exit(1);
            return;
        }

        if (!TOOL_NAME.equals(args[0])) {
            System.err.println("unknown tool: " + args[0]);
            System.exit(1);
            return;
        }

        final Config config;
        try {
            config = loadConfig();
        } catch (Exception ex) {
            System.err.println("failed to load config: " + ex.getMessage());
            System.exit(1);
            return;
        }

        ZonedDateTime now = ZonedDateTime.now(config.zoneId);
        long unixSeconds = Instant.now().getEpochSecond();
        String weekday = now.getDayOfWeek().getDisplayName(TextStyle.FULL, Locale.ENGLISH);

        StringBuilder sb = new StringBuilder(256);
        sb.append("{\n");
        sb.append("  \"date\": \"").append(DATE_FORMAT.format(now)).append("\",\n");
        sb.append("  \"time\": \"").append(TIME_FORMAT.format(now)).append("\",\n");
        sb.append("  \"timezone\": \"").append(jsonEscape(config.timezoneLabel)).append("\",\n");
        sb.append("  \"weekday\": \"").append(jsonEscape(weekday)).append("\",\n");
        sb.append("  \"unix_seconds\": ").append(unixSeconds).append("\n");
        sb.append("}\n");

        System.out.print(sb);
    }

    private static Config defaultConfig() {
        ZoneId zoneId = ZoneId.systemDefault();
        String label = zoneId.getId();
        if (label == null || label.isBlank()) {
            label = "Local";
        }
        return new Config(zoneId, label);
    }

    private static Config loadConfig() throws IOException {
        Config defaults = defaultConfig();

        String configPathRaw = System.getenv("EZYAPPER_PLUGIN_CONFIG");
        if (configPathRaw == null || configPathRaw.trim().isEmpty()) {
            return defaults;
        }

        Path configPath = Path.of(configPathRaw.trim());
        if (!Files.exists(configPath)) {
            return defaults;
        }

        String timezone = null;
        Integer utcOffsetHours = null;
        Integer utcOffsetMinutes = null;

        List<String> lines = Files.readAllLines(configPath, StandardCharsets.UTF_8);
        for (String line : lines) {
            String trimmed = line.trim();
            if (trimmed.isEmpty() || trimmed.startsWith("#")) {
                continue;
            }

            int commentIdx = trimmed.indexOf('#');
            if (commentIdx >= 0) {
                trimmed = trimmed.substring(0, commentIdx).trim();
                if (trimmed.isEmpty()) {
                    continue;
                }
            }

            int sep = trimmed.indexOf(':');
            if (sep <= 0) {
                continue;
            }

            String key = trimmed.substring(0, sep).trim();
            String value = stripOptionalQuotes(trimmed.substring(sep + 1).trim());
            if (value.isEmpty()) {
                continue;
            }

            switch (key) {
                case "timezone":
                    timezone = value;
                    break;
                case "utc_offset_hours":
                    utcOffsetHours = Integer.parseInt(value);
                    break;
                case "utc_offset_minutes":
                    utcOffsetMinutes = Integer.parseInt(value);
                    break;
                default:
                    break;
            }
        }

        if (utcOffsetMinutes != null || utcOffsetHours != null) {
            int minutes = utcOffsetMinutes != null ? utcOffsetMinutes : utcOffsetHours * 60;
            ZoneOffset offset = ZoneOffset.ofTotalSeconds(minutes * 60);
            String label = (timezone == null || timezone.isBlank()) ? formatUtcOffsetLabel(minutes) : timezone;
            return new Config(offset, label);
        }

        if (timezone != null && !timezone.isBlank()) {
            ZoneId zoneId = ZoneId.of(timezone);
            return new Config(zoneId, timezone);
        }

        return defaults;
    }

    private static String stripOptionalQuotes(String value) {
        if (value.length() < 2) {
            return value;
        }

        char first = value.charAt(0);
        char last = value.charAt(value.length() - 1);
        if ((first == '"' && last == '"') || (first == '\'' && last == '\'')) {
            return value.substring(1, value.length() - 1);
        }

        return value;
    }

    private static String formatUtcOffsetLabel(int offsetMinutes) {
        int absMinutes = Math.abs(offsetMinutes);
        int hours = absMinutes / 60;
        int minutes = absMinutes % 60;
        String sign = offsetMinutes >= 0 ? "+" : "-";
        return String.format(Locale.ROOT, "UTC%s%d:%02d", sign, hours, minutes);
    }

    private static String jsonEscape(String input) {
        StringBuilder escaped = new StringBuilder(input.length() + 8);
        for (int i = 0; i < input.length(); i++) {
            char c = input.charAt(i);
            switch (c) {
                case '\\':
                    escaped.append("\\\\");
                    break;
                case '"':
                    escaped.append("\\\"");
                    break;
                case '\b':
                    escaped.append("\\b");
                    break;
                case '\f':
                    escaped.append("\\f");
                    break;
                case '\n':
                    escaped.append("\\n");
                    break;
                case '\r':
                    escaped.append("\\r");
                    break;
                case '\t':
                    escaped.append("\\t");
                    break;
                default:
                    if (c < 0x20) {
                        escaped.append(String.format(Locale.ROOT, "\\u%04x", (int) c));
                    } else {
                        escaped.append(c);
                    }
                    break;
            }
        }
        return escaped.toString();
    }
}
