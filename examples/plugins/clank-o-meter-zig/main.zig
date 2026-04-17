const std = @import("std");

fn isNumeric(input: []const u8) bool {
    if (input.len == 0) {
        return false;
    }

    for (input) |c| {
        if (c < '0' or c > '9') {
            return false;
        }
    }

    return true;
}

fn computeScore(user_id: []const u8) u8 {
    var digest: [16]u8 = undefined;
    std.crypto.hash.Md5.hash(user_id, &digest, .{});

    const value = std.mem.readInt(u64, digest[0..8], .big);
    return @intCast(value % 101);
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

    if (args.len != 3) {
        try stderr.writeAll("usage: clank-o-meter-zig get_clank_o_meter <user_id>\n");
        try stderr.flush();
        std.process.exit(1);
    }

    if (!std.mem.eql(u8, args[1], "get_clank_o_meter")) {
        try stderr.print("unknown tool: {s}\n", .{args[1]});
        try stderr.flush();
        std.process.exit(1);
    }

    const user_id = args[2];
    if (!isNumeric(user_id)) {
        try stderr.writeAll("user_id must be a numeric Discord user ID\n");
        try stderr.flush();
        std.process.exit(1);
    }

    const score = computeScore(user_id);
    try stdout.print(
        "{{\n  \"user_id\": \"{s}\",\n  \"clank_o_meter\": {d}\n}}\n",
        .{ user_id, score },
    );
    try stdout.flush();
}
