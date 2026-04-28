package web

import "testing"

// mockDiscordInfo implements DiscordInfoProvider for testing.
type mockDiscordInfo struct {
	channelName string
	userName    string
	guildName   string
}

func (m *mockDiscordInfo) GetChannelName(id string) string                        { return m.channelName }
func (m *mockDiscordInfo) GetUserName(guildID, userID string) string               { return m.userName }
func (m *mockDiscordInfo) GetGuildName(id string) string                           { return m.guildName }

// TestDiscordInfoProvider_Interface verifies mock implements interface at compile time.
func TestDiscordInfoProvider_Interface(t *testing.T) {
	var _ DiscordInfoProvider = (*mockDiscordInfo)(nil)
}

// TestDiscordAdapter_FallbackOnNil verifies fallback returns ID when method returns empty.
func TestDiscordAdapter_FallbackOnNil(t *testing.T) {
	mock := &mockDiscordInfo{channelName: "#general"}
	if mock.GetChannelName("123") != "#general" {
		t.Error("expected channel name")
	}
}

// TestDiscordAdapter_ChannelName verifies GetChannelName returns expected value.
func TestDiscordAdapter_ChannelName(t *testing.T) {
	mock := &mockDiscordInfo{channelName: "#general"}
	got := mock.GetChannelName("123")
	if got != "#general" {
		t.Errorf("expected '#general', got '%s'", got)
	}
}

// TestDiscordAdapter_UserName verifies GetUserName returns expected value.
func TestDiscordAdapter_UserName(t *testing.T) {
	mock := &mockDiscordInfo{userName: "testuser"}
	got := mock.GetUserName("guildID", "userID")
	if got != "testuser" {
		t.Errorf("expected 'testuser', got '%s'", got)
	}
}

// TestDiscordAdapter_GuildName verifies GetGuildName returns expected value.
func TestDiscordAdapter_GuildName(t *testing.T) {
	mock := &mockDiscordInfo{guildName: "My Server"}
	got := mock.GetGuildName("123")
	if got != "My Server" {
		t.Errorf("expected 'My Server', got '%s'", got)
	}
}

// TestDiscordAdapter_EmptyFallback verifies fallback returns ID for empty mock.
func TestDiscordAdapter_EmptyFallback(t *testing.T) {
	mock := &mockDiscordInfo{}
	if mock.GetChannelName("fallback-id") != "" {
		t.Error("expected empty string for channel name")
	}
	if mock.GetUserName("gid", "uid") != "" {
		t.Error("expected empty string for user name")
	}
	if mock.GetGuildName("fallback-gid") != "" {
		t.Error("expected empty string for guild name")
	}
}
