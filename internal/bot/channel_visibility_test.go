package bot

import (
	"strings"
	"testing"

	"ezyapper/internal/discordvis"
	"ezyapper/internal/types"

	"github.com/bwmarrin/discordgo"
)

const visGuildID = "g1"

// visibilitySession returns a session whose state has a guild with a public
// channel, a staff channel the bot cannot view, an obfuscated channel as
// Discord sends it after the channel obfuscation change, a category and a
// thread under the public channel.
func visibilitySession(t *testing.T, botID string) *discordgo.Session {
	t.Helper()
	deny := []*discordgo.PermissionOverwrite{{ID: visGuildID, Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel}}
	st := discordgo.NewState()
	st.Ready.User = &discordgo.User{ID: botID}
	g := &discordgo.Guild{
		ID: visGuildID, Name: "Guild", OwnerID: "owner",
		Roles: []*discordgo.Role{{ID: visGuildID, Permissions: discordgo.PermissionViewChannel | discordgo.PermissionSendMessages}},
		Channels: []*discordgo.Channel{
			{ID: "10", GuildID: visGuildID, Name: "general", Topic: "chat", Type: discordgo.ChannelTypeGuildText},
			{ID: "11", GuildID: visGuildID, Name: "staff", Topic: "secret", Type: discordgo.ChannelTypeGuildText, PermissionOverwrites: deny},
			{ID: "12", GuildID: visGuildID, Name: discordvis.HiddenChannelName, Type: discordgo.ChannelTypeGuildText, Flags: discordvis.ChannelFlagObfuscated, PermissionOverwrites: deny},
			{ID: "13", GuildID: visGuildID, Name: "Text", Type: discordgo.ChannelTypeGuildCategory},
		},
		Threads: []*discordgo.Channel{
			{ID: "14", GuildID: visGuildID, ParentID: "10", Name: "a-thread", Type: discordgo.ChannelTypeGuildPublicThread},
		},
	}
	if err := st.GuildAdd(g); err != nil {
		t.Fatal(err)
	}
	if err := st.MemberAdd(&discordgo.Member{GuildID: visGuildID, User: &discordgo.User{ID: botID}}); err != nil {
		t.Fatal(err)
	}
	return &discordgo.Session{State: st}
}

func TestBasePromptInputChannelNames(t *testing.T) {
	tests := []struct {
		name      string
		botID     string // "" = permissions cannot be computed
		channelID string
		wantNames map[string]string
		wantName  string
		wantTopic string
	}{
		{
			name: "permissions known", botID: "999", channelID: "10",
			wantNames: map[string]string{"10": "general", "13": "Text", "14": "a-thread"},
			wantName:  "general", wantTopic: "chat",
		},
		{
			// Without permissions only obfuscated channels are hidden.
			name: "permissions unknown", botID: "", channelID: "10",
			wantNames: map[string]string{"10": "general", "11": "staff", "13": "Text", "14": "a-thread"},
			wantName:  "general", wantTopic: "chat",
		},
		{
			name: "current channel obfuscated", botID: "999", channelID: "12",
			wantNames: map[string]string{"10": "general", "13": "Text", "14": "a-thread"},
			wantName:  discordvis.UnknownChannelName, wantTopic: "",
		},
		{
			name: "current channel without view", botID: "999", channelID: "11",
			wantNames: map[string]string{"10": "general", "13": "Text", "14": "a-thread"},
			wantName:  discordvis.UnknownChannelName, wantTopic: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &Bot{}
			b.session.Store(visibilitySession(t, "999"))
			in := b.basePromptInput(ModeContext{GuildID: visGuildID, ChannelID: tt.channelID}, tt.botID)
			if len(in.ChannelNames) != len(tt.wantNames) {
				t.Fatalf("ChannelNames = %v, want %v", in.ChannelNames, tt.wantNames)
			}
			for id, n := range tt.wantNames {
				if in.ChannelNames[id] != n {
					t.Fatalf("ChannelNames[%s] = %q, want %q (all: %v)", id, in.ChannelNames[id], n, in.ChannelNames)
				}
			}
			for _, n := range in.ChannelNames {
				if n == discordvis.HiddenChannelName {
					t.Fatalf("placeholder name leaked: %v", in.ChannelNames)
				}
			}
			if in.ChannelName != tt.wantName || in.ChannelTopic != tt.wantTopic {
				t.Fatalf("channel = (%q, %q), want (%q, %q)", in.ChannelName, in.ChannelTopic, tt.wantName, tt.wantTopic)
			}
		})
	}
}

func TestBasePromptInputNoState(t *testing.T) {
	b := &Bot{}
	b.session.Store(&discordgo.Session{})
	in := b.basePromptInput(ModeContext{GuildID: visGuildID, ChannelID: "10"}, "999")
	if in.ChannelNames == nil || len(in.ChannelNames) != 0 {
		t.Fatalf("ChannelNames = %v, want empty non-nil", in.ChannelNames)
	}
}

func TestResolveChannelMentions(t *testing.T) {
	channels := map[string]string{"10": "general", "15": discordvis.HiddenChannelName, "16": ""}
	tests := []struct {
		name     string
		in       string
		channels map[string]string
		want     string
	}{
		{"known", "see <#10>", channels, "see #general"},
		{"hidden (not in map)", "see <#12>", channels, "see #unknown-channel"},
		{"placeholder in map", "see <#15>", channels, "see #unknown-channel"},
		{"empty name in map", "see <#16>", channels, "see #unknown-channel"},
		{"mixed", "<#10> and <#12>", channels, "#general and #unknown-channel"},
		{"empty map", "see <#10>", map[string]string{}, "see #unknown-channel"},
		{"nil map leaves raw", "see <#10>", nil, "see <#10>"},
		{"no mentions", "plain text", channels, "plain text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveMentions(tt.in, nil, tt.channels); got != tt.want {
				t.Fatalf("resolveMentions(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestUserPromptHidesHiddenChannels(t *testing.T) {
	b := &Bot{}
	b.session.Store(visibilitySession(t, "999"))
	in := b.basePromptInput(ModeContext{GuildID: visGuildID, ChannelID: "10"}, "999")
	in.History = []*types.DiscordMessage{
		{ID: "2", AuthorID: "200", Username: "bob", Content: "staff talk is in <#11>",
			ReplyToID: "1", ReplyToAuthorID: "100", ReplyToUsername: "alice", ReplyToContent: "where is <#12>?"},
		{ID: "1", AuthorID: "100", Username: "alice", Content: "where is <#12>?"},
	}
	in.Current = &types.DiscordMessage{ID: "3", AuthorID: "100", Username: "alice", Content: "<#10> or <#12> or <#14>?"}
	p := buildUserPrompt(in)
	for _, bad := range []string{discordvis.HiddenChannelName, "<#11>", "<#12>", "#staff", "secret"} {
		if strings.Contains(p, bad) {
			t.Fatalf("prompt contains %q:\n%s", bad, p)
		}
	}
	for _, want := range []string{"#general or #unknown-channel or #a-thread", "staff talk is in #unknown-channel", "where is #unknown-channel?", "<channel_name>general</channel_name>"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt missing %q:\n%s", want, p)
		}
	}
}

func TestGetChannelNameHidesHidden(t *testing.T) {
	b := &Bot{}
	b.session.Store(visibilitySession(t, "999"))
	tests := []struct{ id, want string }{
		{"10", "general"},
		{"11", "11"}, // no View Channel: fall back to the ID
		{"12", "12"}, // obfuscated: never "___hidden___"
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			if got := b.GetChannelName(tt.id); got != tt.want {
				t.Fatalf("GetChannelName(%s) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}
