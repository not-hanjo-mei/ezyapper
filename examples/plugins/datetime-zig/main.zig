const std = @import("std");

const WeekdayNames = [_][]const u8{
    "Sunday",
    "Monday",
    "Tuesday",
    "Wednesday",
    "Thursday",
    "Friday",
    "Saturday",
};

const DateTime = struct {
    year: i32,
    month: u8,
    day: u8,
    hour: u8,
    minute: u8,
    second: u8,
    weekday: []const u8,
};

const DateTimeConfig = struct {
    timezone: []const u8,
    utc_offset_minutes: i64,
};

fn defaultDateTimeConfig(init: std.process.Init) DateTimeConfig {
    const timezone = blk: {
        const timezone_raw = init.environ_map.get("EZYAPPER_SYSTEM_TIMEZONE") orelse break :blk "Local";
        const trimmed = std.mem.trim(u8, timezone_raw, " \t\r\n");
        if (trimmed.len == 0) {
            break :blk "Local";
        }
        break :blk trimmed;
    };

    const utc_offset_minutes = blk: {
        const offset_raw = init.environ_map.get("EZYAPPER_SYSTEM_UTC_OFFSET_MINUTES") orelse break :blk @as(i64, 0);
        const trimmed = std.mem.trim(u8, offset_raw, " \t\r\n");
        if (trimmed.len == 0) {
            break :blk @as(i64, 0);
        }
        break :blk std.fmt.parseInt(i64, trimmed, 10) catch @as(i64, 0);
    };

    return .{
        .timezone = timezone,
        .utc_offset_minutes = utc_offset_minutes,
    };
}

fn stripOptionalQuotes(input: []const u8) []const u8 {
    if (input.len < 2) {
        return input;
    }

    const first = input[0];
    const last = input[input.len - 1];
    if ((first == '"' and last == '"') or (first == '\'' and last == '\'')) {
        return input[1 .. input.len - 1];
    }

    return input;
}

fn parseDateTimeConfig(config_text: []const u8, defaults: DateTimeConfig) !DateTimeConfig {
    var cfg = defaults;
    var line_it = std.mem.tokenizeScalar(u8, config_text, '\n');

    while (line_it.next()) |raw_line| {
        var line = std.mem.trim(u8, raw_line, " \t\r");
        if (line.len == 0 or line[0] == '#') {
            continue;
        }

        if (std.mem.indexOfScalar(u8, line, '#')) |comment_start| {
            line = std.mem.trim(u8, line[0..comment_start], " \t");
            if (line.len == 0) {
                continue;
            }
        }

        if (std.mem.startsWith(u8, line, "timezone:")) {
            const raw_value = std.mem.trim(u8, line["timezone:".len..], " \t");
            const value = stripOptionalQuotes(raw_value);
            if (value.len == 0) {
                return error.InvalidConfig;
            }
            cfg.timezone = value;
            continue;
        }

        if (std.mem.startsWith(u8, line, "utc_offset_hours:")) {
            const raw_value = std.mem.trim(u8, line["utc_offset_hours:".len..], " \t");
            const value = stripOptionalQuotes(raw_value);
            cfg.utc_offset_minutes = (try std.fmt.parseInt(i64, value, 10)) * 60;
            continue;
        }

        if (std.mem.startsWith(u8, line, "utc_offset_minutes:")) {
            const raw_value = std.mem.trim(u8, line["utc_offset_minutes:".len..], " \t");
            const value = stripOptionalQuotes(raw_value);
            cfg.utc_offset_minutes = try std.fmt.parseInt(i64, value, 10);
            continue;
        }
    }

    return cfg;
}

fn loadDateTimeConfig(init: std.process.Init) !DateTimeConfig {
    const defaults = defaultDateTimeConfig(init);

    const config_path_raw = init.environ_map.get("EZYAPPER_PLUGIN_CONFIG") orelse return defaults;
    const config_path = std.mem.trim(u8, config_path_raw, " \t\r\n");
    if (config_path.len == 0) {
        return defaults;
    }

    const config_text = std.Io.Dir.cwd().readFileAlloc(
        init.io,
        config_path,
        init.arena.allocator(),
        .limited(64 * 1024),
    ) catch |err| switch (err) {
        error.FileNotFound => return defaults,
        else => return err,
    };

    return parseDateTimeConfig(config_text, defaults);
}

fn civilFromDays(z: i64) struct { year: i32, month: u8, day: u8 } {
    const z_adj = z + 719468;
    const era = @divFloor(z_adj, 146097);
    const doe = z_adj - era * 146097;
    const yoe = @divFloor(doe - @divFloor(doe, 1460) + @divFloor(doe, 36524) - @divFloor(doe, 146096), 365);
    var year: i64 = yoe + era * 400;
    const doy = doe - (365 * yoe + @divFloor(yoe, 4) - @divFloor(yoe, 100));
    const mp = @divFloor(5 * doy + 2, 153);
    const day = doy - @divFloor(153 * mp + 2, 5) + 1;
    const month = mp + (if (mp < 10) @as(i64, 3) else @as(i64, -9));
    year += if (month <= 2) @as(i64, 1) else @as(i64, 0);

    return .{
        .year = @intCast(year),
        .month = @intCast(month),
        .day = @intCast(day),
    };
}

fn timestampToDateTime(unix_seconds: i64) DateTime {
    const days = @divFloor(unix_seconds, 86400);
    var day_seconds = @mod(unix_seconds, 86400);
    if (day_seconds < 0) {
        day_seconds += 86400;
    }

    const civil = civilFromDays(days);
    const weekday_index = @mod(days + 4, 7);

    return .{
        .year = civil.year,
        .month = civil.month,
        .day = civil.day,
        .hour = @intCast(@divFloor(day_seconds, 3600)),
        .minute = @intCast(@divFloor(@mod(day_seconds, 3600), 60)),
        .second = @intCast(@mod(day_seconds, 60)),
        .weekday = WeekdayNames[@intCast(weekday_index)],
    };
}

pub fn main(init: std.process.Init) !void {
    const arena = init.arena.allocator();
    const args = try init.minimal.args.toSlice(arena);

    const io = init.io;
    var stdout_buffer: [512]u8 = undefined;
    var stdout_writer = std.Io.File.stdout().writer(io, &stdout_buffer);
    const stdout = &stdout_writer.interface;

    var stderr_buffer: [256]u8 = undefined;
    var stderr_writer = std.Io.File.stderr().writer(io, &stderr_buffer);
    const stderr = &stderr_writer.interface;

    if (args.len != 2) {
        try stderr.writeAll("usage: datetime-zig get_current_datetime\n");
        try stderr.flush();
        std.process.exit(1);
    }

    if (!std.mem.eql(u8, args[1], "get_current_datetime")) {
        try stderr.print("unknown tool: {s}\n", .{args[1]});
        try stderr.flush();
        std.process.exit(1);
    }

    const config = loadDateTimeConfig(init) catch |err| {
        try stderr.print("failed to load config: {s}\n", .{@errorName(err)});
        try stderr.flush();
        std.process.exit(1);
    };

    const unix_seconds_utc = std.Io.Clock.real.now(io).toSeconds();
    const unix_seconds_local = unix_seconds_utc + config.utc_offset_minutes * 60;
    const dt = timestampToDateTime(unix_seconds_local);

    try stdout.print(
        "{{\n  \"date\": \"{d:0>4}-{d:0>2}-{d:0>2}\",\n  \"time\": \"{d:0>2}:{d:0>2}:{d:0>2}\",\n  \"timezone\": \"{s}\",\n  \"weekday\": \"{s}\",\n  \"unix_seconds\": {d}\n}}\n",
        .{ @as(u32, @intCast(dt.year)), dt.month, dt.day, dt.hour, dt.minute, dt.second, config.timezone, dt.weekday, unix_seconds_utc },
    );
    try stdout.flush();
}
