package tools

import (
	"context"
	"strings"
	"testing"

	"ezyapper/internal/discordvis"

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
	result, err := marshalJSON(map[string]any{"hello": "world"})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(result, "hello") {
		t.Fatalf("expected JSON, got: %s", result)
	}
	if !strings.Contains(result, "world") {
		t.Fatalf("expected world, got: %s", result)
	}
}

func TestMarshalJSON_Error(t *testing.T) {
	_, err := marshalJSON(make(chan int))
	if err == nil {
		t.Fatal("expected error for unmarshalable type, got nil")
	}
	if !strings.Contains(err.Error(), "failed to marshal result") {
		t.Fatalf("expected marshal error, got: %v", err)
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

func TestMarshalJSON_PropagatesErrorToHandler(t *testing.T) {
	handler := func(ctx context.Context, args map[string]any) (string, error) {
		return marshalJSON(make(chan int))
	}

	result, err := handler(context.Background(), nil)
	if err == nil {
		t.Fatalf("expected error for unmarshalable type, got success with result: %q", result)
	}
	if !strings.Contains(err.Error(), "failed to marshal result") {
		t.Fatalf("expected marshal error, got: %v", err)
	}
}

const visGuildID = "g1"

// visibilityState is a state with the bot as a member of a guild whose
// @everyone role can view channels, plus the given channels.
func visibilityState(t *testing.T, channels ...*discordgo.Channel) *discordgo.State {
	t.Helper()
	st := discordgo.NewState()
	st.Ready.User = &discordgo.User{ID: "bot"}
	g := &discordgo.Guild{
		ID: visGuildID, OwnerID: "owner",
		Roles:    []*discordgo.Role{{ID: visGuildID, Permissions: discordgo.PermissionViewChannel}},
		Channels: channels,
	}
	if err := st.GuildAdd(g); err != nil {
		t.Fatal(err)
	}
	if err := st.MemberAdd(&discordgo.Member{GuildID: visGuildID, User: &discordgo.User{ID: "bot"}}); err != nil {
		t.Fatal(err)
	}
	return st
}

func visibilityChannels() []*discordgo.Channel {
	deny := []*discordgo.PermissionOverwrite{{ID: visGuildID, Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel}}
	return []*discordgo.Channel{
		{ID: "10", GuildID: visGuildID, Name: "general", Topic: "chat", Type: discordgo.ChannelTypeGuildText},
		{ID: "11", GuildID: visGuildID, Name: "staff", Topic: "secret", Type: discordgo.ChannelTypeGuildText, PermissionOverwrites: deny},
		{ID: "12", GuildID: visGuildID, Name: discordvis.HiddenChannelName, Type: discordgo.ChannelTypeGuildText, Flags: discordvis.ChannelFlagObfuscated, PermissionOverwrites: deny},
		{ID: "13", GuildID: visGuildID, Name: "Text", Type: discordgo.ChannelTypeGuildCategory},
	}
}

func TestListChannelsResultFiltersHidden(t *testing.T) {
	tests := []struct {
		name     string
		withPerm bool
		wantIDs  []string
	}{
		{"permissions known", true, []string{"10", "13"}},
		{"permissions unknown keeps all but obfuscated", false, []string{"10", "11", "13"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channels := visibilityChannels()
			var perms discordvis.PermissionFunc
			if tt.withPerm {
				st := visibilityState(t, visibilityChannels()...)
				perms = discordvis.StatePermissions(st, discordvis.StateBotID(st))
			}
			got := listChannelsResult(channels, perms)
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("got %v, want ids %v", got, tt.wantIDs)
			}
			for i, id := range tt.wantIDs {
				if got[i]["id"] != id {
					t.Fatalf("got[%d] id = %v, want %s (all: %v)", i, got[i]["id"], id, got)
				}
				if got[i]["name"] == discordvis.HiddenChannelName {
					t.Fatalf("placeholder name leaked: %v", got)
				}
			}
		})
	}
}

func TestListChannelsToolUsesSessionState(t *testing.T) {
	d := NewDiscordTools(&discordgo.Session{State: visibilityState(t, visibilityChannels()...)})
	perms := d.botPermissions()
	if perms == nil {
		t.Fatal("botPermissions returned nil with READY user and member in state")
	}
	got := listChannelsResult(visibilityChannels(), perms)
	if len(got) != 2 {
		t.Fatalf("got %v, want general and Text", got)
	}
	if NewDiscordTools(&discordgo.Session{}).botPermissions() != nil {
		t.Fatal("botPermissions must be nil without state")
	}
}

func TestChannelInfoResultHidesHidden(t *testing.T) {
	st := visibilityState(t, visibilityChannels()...)
	perms := discordvis.StatePermissions(st, discordvis.StateBotID(st))
	tests := []struct {
		id         string
		wantName   string
		accessible bool
	}{
		{"10", "general", true},
		{"11", discordvis.UnknownChannelName, false},
		{"12", discordvis.UnknownChannelName, false},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			ch, err := st.Channel(tt.id)
			if err != nil {
				t.Fatal(err)
			}
			got := channelInfoResult(ch, perms)
			if got["name"] != tt.wantName {
				t.Fatalf("name = %v, want %s", got["name"], tt.wantName)
			}
			if !tt.accessible {
				if got["accessible"] != false {
					t.Fatalf("accessible = %v, want false", got["accessible"])
				}
				if _, ok := got["topic"]; ok {
					t.Fatalf("topic leaked for hidden channel: %v", got)
				}
			}
		})
	}
}
