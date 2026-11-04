package filter

import (
	"strings"
	"testing"

	"ezyapper/internal/types"
)

func mustCompile(t *testing.T, cfg Config) *Filter {
	t.Helper()
	f, err := Compile(cfg)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return f
}

// The motivating example: ignore one bot's embed messages in one channel.
func TestIgnoreSpecificBotEmbedsInChannel(t *testing.T) {
	f := mustCompile(t, Config{Rules: []RuleConfig{{
		Name: "clank embeds in #general", Action: "ignore",
		Channel: "^456$", User: "^789$", Kinds: []string{types.KindEmbed},
	}}})

	cases := []struct {
		name string
		s    Subject
		want Action
	}{
		{"bot embed in channel", Subject{ChannelID: "456", UserID: "789", Kinds: []string{types.KindEmbed, types.KindBot}}, ActionIgnore},
		{"bot text in channel", Subject{ChannelID: "456", UserID: "789", Kinds: []string{types.KindText, types.KindBot}}, ActionAllow},
		{"bot embed elsewhere", Subject{ChannelID: "999", UserID: "789", Kinds: []string{types.KindEmbed}}, ActionAllow},
		{"other user embed", Subject{ChannelID: "456", UserID: "111", Kinds: []string{types.KindEmbed}}, ActionAllow},
	}
	for _, c := range cases {
		if got, _ := f.Decide(c.s); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestFirstMatchWinsAndWhitelistDefault(t *testing.T) {
	f := mustCompile(t, Config{
		DefaultAction: "ignore",
		Rules: []RuleConfig{
			{Name: "mute spammer", Action: "ignore", User: "^42$"},
			{Name: "allow guild", Action: "allow", Guild: "^1$"},
		},
	})
	if a, r := f.Decide(Subject{GuildID: "1", UserID: "42"}); a != ActionIgnore || r != "mute spammer" {
		t.Fatalf("expected first rule to win, got %s by %s", a, r)
	}
	if a, _ := f.Decide(Subject{GuildID: "1", UserID: "7"}); a != ActionAllow {
		t.Fatalf("expected allow rule to match, got %s", a)
	}
	if a, r := f.Decide(Subject{GuildID: "2", UserID: "7"}); a != ActionIgnore || r != "default" {
		t.Fatalf("expected default ignore, got %s by %s", a, r)
	}
}

func TestKindsAllAndContent(t *testing.T) {
	f := mustCompile(t, Config{Rules: []RuleConfig{
		{Name: "bot replies with images", Action: "ignore", KindsAll: []string{types.KindBot, types.KindImage}},
		{Name: "commands", Action: "ignore", Content: `^[!/]`},
	}})
	if !f.Ignored(Subject{Kinds: []string{types.KindBot, types.KindImage}}) {
		t.Fatal("kinds_all should match when all kinds are present")
	}
	if f.Ignored(Subject{Kinds: []string{types.KindBot}}) {
		t.Fatal("kinds_all must not match a subset")
	}
	if !f.Ignored(Subject{Content: "!play song"}) || f.Ignored(Subject{Content: "play !this"}) {
		t.Fatal("content regex mismatch")
	}
}

func TestCompileErrors(t *testing.T) {
	_, err := Compile(Config{DefaultAction: "drop", Rules: []RuleConfig{
		{Action: "block", User: "("},
		{Action: "ignore", Kinds: []string{"gif"}},
		{Action: "ignore"},
	}})
	if err == nil {
		t.Fatal("expected errors")
	}
	if !strings.Contains(err.Error(), "default_action") {
		t.Fatalf("expected default_action error first, got %v", err)
	}
	_, err = Compile(Config{Rules: []RuleConfig{
		{Action: "block", User: "("},
		{Action: "ignore", Kinds: []string{"gif"}},
		{Action: "ignore"},
	}})
	for _, want := range []string{"action must be allow or ignore", "invalid user regex", "unknown kind", "matches every message"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("expected %q in %v", want, err)
		}
	}
}

func TestNilFilterAllowsAndEmptyConfigIsNil(t *testing.T) {
	f, err := Compile(Config{})
	if err != nil || f != nil {
		t.Fatalf("empty config should compile to nil, got %v %v", f, err)
	}
	if f.Ignored(Subject{UserID: "x"}) {
		t.Fatal("nil filter must allow")
	}
}

func TestMessagesDropsIgnored(t *testing.T) {
	f := mustCompile(t, Config{Rules: []RuleConfig{{Action: "ignore", User: "^b$"}}})
	in := []*types.DiscordMessage{{ID: "1", AuthorID: "a"}, {ID: "2", AuthorID: "b"}, {ID: "3", AuthorID: "a"}}
	out := f.Messages(in)
	if len(out) != 2 || out[0].ID != "1" || out[1].ID != "3" || len(in) != 3 {
		t.Fatalf("unexpected result %v", out)
	}
}
