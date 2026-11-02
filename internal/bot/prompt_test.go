package bot

import (
	"strings"
	"testing"
	"time"

	"ezyapper/internal/memory"
	"ezyapper/internal/plugin"
	"ezyapper/internal/types"
)

func TestSystemPromptIsStatic(t *testing.T) {
	blocks := []plugin.ContextBlock{{Tag: "tts_rules", Content: "Keep it short."}}
	a := SystemPromptFor("You are Mei.", blocks)
	b := SystemPromptFor("You are Mei.", blocks)
	if a != b {
		t.Fatal("system prompt must be byte-identical across calls")
	}
	want := "<instruction>\nYou are Mei.\n</instruction>\n\n<tts_rules>\nKeep it short.\n</tts_rules>"
	if a != want {
		t.Fatalf("got\n%s\nwant\n%s", a, want)
	}
}

func promptFixture() promptInput {
	t0 := time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC)
	hist := []*types.DiscordMessage{ // newest first, like the fetcher
		{ID: "3", AuthorID: "999", Username: "mei", DisplayName: "Mei", IsBot: true, Content: "hi <@100>", Timestamp: t0.Add(2 * time.Minute)},
		{ID: "2", AuthorID: "200", Username: "bob", DisplayName: "Bob", Content: "same", Timestamp: t0.Add(time.Minute),
			ReplyToID: "1", ReplyToAuthorID: "100", ReplyToUsername: "alice", ReplyToContent: "hello <#9>"},
		{ID: "1", AuthorID: "100", Username: "alice", DisplayName: "Alice Mana", Content: "hello <#9>", Timestamp: t0},
	}
	cur := &types.DiscordMessage{ID: "4", AuthorID: "100", Username: "alice", DisplayName: "Alice Mana",
		Content: "<@999> what about <@200> & <b>?", Timestamp: t0.Add(3 * time.Minute)}
	return promptInput{
		GuildID: "g1", GuildName: "Guild", ChannelID: "c1", ChannelName: "general", ChannelTopic: "chat",
		BotID: "999", History: hist, Current: cur, ChannelNames: map[string]string{"9": "rules"},
		PluginBlocks: []plugin.ContextBlock{{Tag: "motion_status", Content: "running"}},
		Profile:      &memory.Profile{Traits: []string{"curious"}, Facts: map[string]string{"b": "2", "a": "1"}},
		Memories:     "<key_facts>\n- likes tea\n</key_facts>",
	}
}

func TestUserPromptSectionOrder(t *testing.T) {
	p := buildUserPrompt(promptFixture())
	order := []string{"<server_info>", "<channel_info>", "<context>", "<participants>", "<motion_status>", "<user_profile>", "<key_facts>", "<currentMessage>"}
	last := -1
	for _, tag := range order {
		i := strings.Index(p, tag)
		if i < 0 {
			t.Fatalf("missing %s in\n%s", tag, p)
		}
		if i < last {
			t.Fatalf("%s out of order in\n%s", tag, p)
		}
		last = i
	}
}

func TestUserPromptMessageFormat(t *testing.T) {
	p := buildUserPrompt(promptFixture())
	wants := []string{
		`[2026-11-01T05:00:00Z] [User] Alice Mana: "hello #rules"`,
		`[2026-11-01T05:01:00Z] [User] [reply to Alice Mana: "hello #rules"] Bob: "same"`,
		`[2026-11-01T05:02:00Z] [Assistant] Mei: "hi <@Alice Mana>"`,
		// current message: real timestamp, mentions resolved, no HTML escaping
		`[2026-11-01T05:03:00Z] [User] Alice Mana: "<@Mei> what about <@Bob> & <b>?"`,
		"Alice Mana (@alice, ID:100) — user",
		"Mei (@mei, ID:999) — assistant (self)",
		"<channel_description>chat</channel_description>",
		"a: 1\nb: 2",
	}
	for _, w := range wants {
		if !strings.Contains(p, w) {
			t.Errorf("missing %q in\n%s", w, p)
		}
	}
	if strings.Contains(p, "&lt;") || strings.Contains(p, "&amp;") {
		t.Errorf("prompt must not be HTML-escaped:\n%s", p)
	}
}

// Appending a message to the window must keep the prompt prefix up to the
// end of the previous context unchanged (prompt-cache friendliness).
func TestUserPromptPrefixStableWhenWindowAppends(t *testing.T) {
	in := promptFixture()
	in.PluginBlocks, in.Profile, in.Memories, in.Current = nil, nil, "", nil
	before := buildUserPrompt(in)
	ctxEnd := strings.Index(before, "\n</context>")

	in.History = append([]*types.DiscordMessage{{ID: "5", AuthorID: "200", Username: "bob", DisplayName: "Bob", Content: "new", Timestamp: time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)}}, in.History...)
	after := buildUserPrompt(in)
	if before[:ctxEnd] != after[:ctxEnd] {
		t.Fatalf("prefix changed:\n%s\n---\n%s", before[:ctxEnd], after[:ctxEnd])
	}
}

func TestMentionsToIDs(t *testing.T) {
	people := []participant{
		{ID: "100", Username: "alice", DisplayName: "Alice Mana"},
		{ID: "200", Username: "bob", DisplayName: "Bob"},
		{ID: "300", Username: "bob2", DisplayName: "Bob"}, // ambiguous display name
	}
	got := mentionsToIDs("hi <@Alice Mana>, <@alice>, <@bob2>, <@Bob>, <@123>, <@Nobody>", people)
	want := "hi <@100>, <@100>, <@300>, <@Bob>, <@123>, <@Nobody>"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
