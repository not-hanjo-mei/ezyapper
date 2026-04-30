package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// --- getStringArg tests ---

func TestGetStringArg_Success(t *testing.T) {
	args := map[string]any{"key": "value"}
	val, err := getStringArg(args, "key")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if val != "value" {
		t.Fatalf("expected 'value', got %q", val)
	}
}

func TestGetStringArg_Missing(t *testing.T) {
	args := map[string]any{}
	_, err := getStringArg(args, "missing")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
	if !strings.Contains(err.Error(), "missing required argument") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetStringArg_WrongType(t *testing.T) {
	args := map[string]any{"key": 42}
	_, err := getStringArg(args, "key")
	if err == nil {
		t.Fatal("expected error for wrong type")
	}
	if !strings.Contains(err.Error(), "must be a string") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetStringArg_Empty(t *testing.T) {
	args := map[string]any{"key": ""}
	_, err := getStringArg(args, "key")
	if err == nil {
		t.Fatal("expected error for empty string")
	}
	if !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- extractLimit tests ---

func TestExtractLimit_Default(t *testing.T) {
	args := map[string]any{}
	limit := extractLimit(args, "limit", 5, 100)
	if limit != 5 {
		t.Fatalf("expected default 5, got %d", limit)
	}
}

func TestExtractLimit_WithinBounds(t *testing.T) {
	args := map[string]any{"limit": float64(7)}
	limit := extractLimit(args, "limit", 5, 100)
	if limit != 7 {
		t.Fatalf("expected 7, got %d", limit)
	}
}

func TestExtractLimit_ExceedsMax(t *testing.T) {
	args := map[string]any{"limit": float64(200)}
	limit := extractLimit(args, "limit", 5, 100)
	if limit != 200 {
		t.Fatalf("expected user value 200 to be honored, got %d", limit)
	}
}

func TestExtractLimit_WrongType(t *testing.T) {
	args := map[string]any{"limit": "seven"}
	limit := extractLimit(args, "limit", 5, 100)
	if limit != 5 {
		t.Fatalf("expected default 5 for wrong type, got %d", limit)
	}
}

func TestExtractLimit_Negative(t *testing.T) {
	args := map[string]any{"limit": float64(-1)}
	limit := extractLimit(args, "limit", 5, 100)
	if limit != -1 {
		t.Fatalf("expected -1 (extractLimit does not clamp to positive), got %d", limit)
	}
}

// --- marshalJSON tests ---

func TestMarshalJSON_Basic(t *testing.T) {
	result := marshalJSON(map[string]interface{}{"hello": "world"})
	if !strings.Contains(result, "hello") {
		t.Fatalf("expected JSON, got: %s", result)
	}
	if !strings.Contains(result, "world") {
		t.Fatalf("expected world, got: %s", result)
	}
}

func TestMarshalJSON_Error(t *testing.T) {
	result := marshalJSON(make(chan int))
	if !strings.Contains(result, "error") {
		t.Fatalf("expected error message, got: %s", result)
	}
}

// --- RegisterTools tests ---

func TestRegisterTools_AllToolsRegistered(t *testing.T) {
	dt := NewDiscordTools(nil)
	registry := NewToolRegistry()
	dt.RegisterTools(registry)

	tools := registry.GetTools()
	expectedNames := []string{
		"add_reaction",
		"create_thread",
		"get_channel_info",
		"get_channel_members",
		"get_recent_messages",
		"get_server_info",
		"get_user_info",
		"list_channels",
		"search_guild_members",
	}

	if len(tools) != len(expectedNames) {
		t.Fatalf("expected %d tools, got %d", len(expectedNames), len(tools))
	}

	for i, name := range expectedNames {
		if tools[i].Function == nil || tools[i].Function.Name != name {
			t.Fatalf("tool %d: expected %q, got %+v", i, name, tools[i].Function)
		}
	}
}

func TestRegisterTools_SchemaHashStable(t *testing.T) {
	r1 := NewToolRegistry()
	dt1 := NewDiscordTools(nil)
	dt1.RegisterTools(r1)
	hash1 := r1.GetSchemaHash()

	r2 := NewToolRegistry()
	dt2 := NewDiscordTools(nil)
	dt2.RegisterTools(r2)
	hash2 := r2.GetSchemaHash()

	if hash1 != hash2 {
		t.Fatalf("expected identical hashes, got %s and %s", hash1, hash2)
	}
}

// --- Handler argument validation (nil session — tests argument parsing only) ---

func TestGetServerInfo_MissingGuildID(t *testing.T) {
	dt := NewDiscordTools(nil)
	_, err := dt.getServerInfo(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing guild_id")
	}
	if !strings.Contains(err.Error(), "guild_id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetChannelInfo_MissingChannelID(t *testing.T) {
	dt := NewDiscordTools(nil)
	_, err := dt.getChannelInfo(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing channel_id")
	}
	if !strings.Contains(err.Error(), "channel_id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetUserInfo_MissingArgs(t *testing.T) {
	dt := NewDiscordTools(nil)

	_, err := dt.getUserInfo(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing guild_id")
	}
	if !strings.Contains(err.Error(), "guild_id") {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = dt.getUserInfo(context.Background(), map[string]any{"guild_id": "123"})
	if err == nil {
		t.Fatal("expected error for missing user_id")
	}
}

func TestListChannels_MissingGuildID(t *testing.T) {
	dt := NewDiscordTools(nil)
	_, err := dt.listChannels(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing guild_id")
	}
}

func TestGetRecentMessages_MissingChannelID(t *testing.T) {
	dt := NewDiscordTools(nil)
	_, err := dt.getRecentMessages(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing channel_id")
	}
}

func TestCreateThread_MissingArgs(t *testing.T) {
	dt := NewDiscordTools(nil)

	_, err := dt.createThread(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing channel_id")
	}

	_, err = dt.createThread(context.Background(), map[string]any{"channel_id": "123"})
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestAddReaction_MissingArgs(t *testing.T) {
	dt := &DiscordTools{session: &discordgo.Session{}}
	// With an invalid session, any Discord API call will fail, but we test argument parsing first.

	_, err := dt.addReaction(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing channel_id")
	}

	_, err = dt.addReaction(context.Background(), map[string]any{"channel_id": "123"})
	if err == nil {
		t.Fatal("expected error for missing message_id")
	}

	_, err = dt.addReaction(context.Background(), map[string]any{"channel_id": "123", "message_id": "456"})
	if err == nil {
		t.Fatal("expected error for missing emoji")
	}
}

func TestGetChannelMembers_MissingGuildID(t *testing.T) {
	dt := NewDiscordTools(nil)
	_, err := dt.getChannelMembers(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing guild_id")
	}
}

func TestSearchGuildMembers_MissingArgs(t *testing.T) {
	dt := &DiscordTools{session: &discordgo.Session{}}

	_, err := dt.searchGuildMembers(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing guild_id")
	}

	_, err = dt.searchGuildMembers(context.Background(), map[string]any{"guild_id": "123"})
	if err == nil {
		t.Fatal("expected error for missing query")
	}
}

// --- execute tool via registry ---

func TestDiscordToolExecution_NotFound(t *testing.T) {
	registry := NewToolRegistry()
	_, err := registry.ExecuteTool(context.Background(), "nonexistent", map[string]any{})
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if !strings.Contains(err.Error(), "tool not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- NewDiscordTools ---

func TestNewDiscordTools_NilSession(t *testing.T) {
	dt := NewDiscordTools(nil)
	if dt == nil {
		t.Fatal("expected non-nil DiscordTools")
	}
	if dt.session != nil {
		t.Fatal("expected nil session")
	}
}

func TestNewDiscordTools_WithSession(t *testing.T) {
	session := &discordgo.Session{}
	dt := NewDiscordTools(session)
	if dt.session != session {
		t.Fatal("expected populated session")
	}
}
