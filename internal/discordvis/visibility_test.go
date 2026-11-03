package discordvis

import (
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
)

const (
	guildID = "g1"
	botID   = "bot"
)

// newState builds a state cache with one guild whose @everyone role can view
// channels, the bot as a member, and the given channels.
func newState(t *testing.T, channels ...*discordgo.Channel) *discordgo.State {
	t.Helper()
	st := discordgo.NewState()
	st.Ready.User = &discordgo.User{ID: botID}
	for _, ch := range channels {
		ch.GuildID = guildID
	}
	g := &discordgo.Guild{
		ID:       guildID,
		OwnerID:  "owner",
		Roles:    []*discordgo.Role{{ID: guildID, Permissions: discordgo.PermissionViewChannel | discordgo.PermissionSendMessages}},
		Channels: channels,
	}
	if err := st.GuildAdd(g); err != nil {
		t.Fatal(err)
	}
	if err := st.MemberAdd(&discordgo.Member{GuildID: guildID, User: &discordgo.User{ID: botID}}); err != nil {
		t.Fatal(err)
	}
	return st
}

func denyEveryoneView() []*discordgo.PermissionOverwrite {
	return []*discordgo.PermissionOverwrite{{ID: guildID, Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel}}
}

func TestIsObfuscated(t *testing.T) {
	tests := []struct {
		name string
		ch   *discordgo.Channel
		want bool
	}{
		{"nil", nil, false},
		{"plain", &discordgo.Channel{ID: "1", Name: "general"}, false},
		{"flag", &discordgo.Channel{ID: "1", Name: "general", Flags: ChannelFlagObfuscated}, true},
		{"flag among others", &discordgo.Channel{ID: "1", Name: "x", Flags: ChannelFlagObfuscated | discordgo.ChannelFlagPinned}, true},
		{"placeholder name", &discordgo.Channel{ID: "1", Name: HiddenChannelName}, true},
		{"other flags only", &discordgo.Channel{ID: "1", Name: "x", Flags: discordgo.ChannelFlagRequireTag}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsObfuscated(tt.ch); got != tt.want {
				t.Fatalf("IsObfuscated = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVisible(t *testing.T) {
	view := func(string) (int64, error) { return discordgo.PermissionViewChannel, nil }
	noView := func(string) (int64, error) { return discordgo.PermissionSendMessages, nil }
	fail := func(string) (int64, error) { return 0, errors.New("member not cached") }
	parentOnly := func(id string) (int64, error) {
		if id == "parent" {
			return discordgo.PermissionViewChannel, nil
		}
		return 0, nil
	}
	text := &discordgo.Channel{ID: "1", Name: "general", Type: discordgo.ChannelTypeGuildText}
	tests := []struct {
		name  string
		ch    *discordgo.Channel
		perms PermissionFunc
		want  bool
	}{
		{"nil channel", nil, view, false},
		{"no perms func keeps channel", text, nil, true},
		{"perms error keeps channel", text, fail, true},
		{"view channel", text, view, true},
		{"no view channel", text, noView, false},
		{"obfuscated flag beats perms", &discordgo.Channel{ID: "1", Name: "general", Flags: ChannelFlagObfuscated}, view, false},
		{"obfuscated without perms", &discordgo.Channel{ID: "1", Name: HiddenChannelName}, nil, false},
		{"obfuscated with perms error", &discordgo.Channel{ID: "1", Flags: ChannelFlagObfuscated}, fail, false},
		{"category skips perms", &discordgo.Channel{ID: "c", Name: "Staff", Type: discordgo.ChannelTypeGuildCategory}, noView, true},
		{"obfuscated category", &discordgo.Channel{ID: "c", Name: HiddenChannelName, Type: discordgo.ChannelTypeGuildCategory, Flags: ChannelFlagObfuscated}, view, false},
		{"thread checks parent", &discordgo.Channel{ID: "t", Name: "thread", Type: discordgo.ChannelTypeGuildPublicThread, ParentID: "parent"}, parentOnly, true},
		{"thread without parent checks itself", &discordgo.Channel{ID: "t", Name: "thread", Type: discordgo.ChannelTypeGuildPublicThread}, parentOnly, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Visible(tt.ch, tt.perms); got != tt.want {
				t.Fatalf("Visible = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVisibleWithState(t *testing.T) {
	general := &discordgo.Channel{ID: "1", Name: "general", Type: discordgo.ChannelTypeGuildText}
	staff := &discordgo.Channel{ID: "2", Name: "staff", Type: discordgo.ChannelTypeGuildText, PermissionOverwrites: denyEveryoneView()}
	hidden := &discordgo.Channel{ID: "3", Name: HiddenChannelName, Type: discordgo.ChannelTypeGuildText, Flags: ChannelFlagObfuscated, PermissionOverwrites: denyEveryoneView()}
	botAllowed := &discordgo.Channel{ID: "4", Name: "bot-room", Type: discordgo.ChannelTypeGuildText, PermissionOverwrites: append(denyEveryoneView(),
		&discordgo.PermissionOverwrite{ID: botID, Type: discordgo.PermissionOverwriteTypeMember, Allow: discordgo.PermissionViewChannel})}
	st := newState(t, general, staff, hidden, botAllowed)
	perms := StatePermissions(st, StateBotID(st))
	if perms == nil {
		t.Fatal("StatePermissions returned nil with a bot user in state")
	}
	notInState := &discordgo.Channel{ID: "99", Name: "fetched-over-http", GuildID: guildID}

	tests := []struct {
		ch   *discordgo.Channel
		want bool
	}{
		{general, true},
		{staff, false},
		{hidden, false},
		{botAllowed, true},
		{notInState, true}, // permissions cannot be computed: keep it
	}
	for _, tt := range tests {
		t.Run(tt.ch.Name, func(t *testing.T) {
			if got := Visible(tt.ch, perms); got != tt.want {
				t.Fatalf("Visible(%s) = %v, want %v", tt.ch.Name, got, tt.want)
			}
		})
	}
}

func TestStatePermissionsUnavailable(t *testing.T) {
	if StatePermissions(nil, botID) != nil {
		t.Fatal("nil state must give nil PermissionFunc")
	}
	if StatePermissions(discordgo.NewState(), "") != nil {
		t.Fatal("empty bot ID must give nil PermissionFunc")
	}
	if StateBotID(nil) != "" || StateBotID(discordgo.NewState()) != "" {
		t.Fatal("StateBotID must be empty without READY")
	}
}

func TestName(t *testing.T) {
	tests := []struct {
		name     string
		ch       *discordgo.Channel
		wantName string
		wantOK   bool
	}{
		{"visible", &discordgo.Channel{ID: "1", Name: "general"}, "general", true},
		{"hidden", &discordgo.Channel{ID: "1", Name: HiddenChannelName, Flags: ChannelFlagObfuscated}, "", false},
		{"empty name", &discordgo.Channel{ID: "1"}, "", false},
		{"nil", nil, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Name(tt.ch, nil)
			if got != tt.wantName || ok != tt.wantOK {
				t.Fatalf("Name = (%q, %v), want (%q, %v)", got, ok, tt.wantName, tt.wantOK)
			}
		})
	}
}

func TestFilterVisible(t *testing.T) {
	a := &discordgo.Channel{ID: "1", Name: "a"}
	b := &discordgo.Channel{ID: "2", Name: HiddenChannelName, Flags: ChannelFlagObfuscated}
	c := &discordgo.Channel{ID: "3", Name: "c"}
	d := &discordgo.Channel{ID: "4", Name: "d"}
	noViewOn4 := func(id string) (int64, error) {
		if id == "4" {
			return 0, nil
		}
		return discordgo.PermissionViewChannel, nil
	}
	got := FilterVisible([]*discordgo.Channel{a, b, nil, c, d}, noViewOn4)
	if len(got) != 2 || got[0] != a || got[1] != c {
		t.Fatalf("FilterVisible kept %v", got)
	}
	if got := FilterVisible(nil, nil); got == nil || len(got) != 0 {
		t.Fatalf("FilterVisible(nil) = %v, want empty non-nil", got)
	}
}
