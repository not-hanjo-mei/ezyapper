package types

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestSummarizeAndRenderEmbeds(t *testing.T) {
	embeds := []*discordgo.MessageEmbed{
		{
			Type:        discordgo.EmbedTypeRich,
			Title:       "Server status",
			Description: "All systems\noperational",
			Author:      &discordgo.MessageEmbedAuthor{Name: "StatusBot"},
			Fields:      []*discordgo.MessageEmbedField{{Name: "CPU", Value: "12%"}, {Name: "", Value: ""}},
			Footer:      &discordgo.MessageEmbedFooter{Text: "updated now"},
		},
		{Type: discordgo.EmbedTypeImage, Thumbnail: &discordgo.MessageEmbedThumbnail{URL: "https://x/y.png"}},
		nil,
	}
	got := SummarizeEmbeds(embeds)
	if len(got) != 1 {
		t.Fatalf("expected only the text-bearing embed, got %d", len(got))
	}
	want := `[Embed "Server status" by StatusBot: All systems operational | CPU: 12% | updated now]`
	if r := got[0].Render(); r != want {
		t.Fatalf("render mismatch\n got: %s\nwant: %s", r, want)
	}
}

func TestFullTextJoinsContentAndEmbeds(t *testing.T) {
	m := DiscordMessage{Content: "check this", Embeds: []EmbedSummary{{Provider: "YouTube", Title: "Video"}}}
	if got := m.FullText(); got != "check this\n[Embed (YouTube) \"Video\"]" {
		t.Fatalf("unexpected full text: %q", got)
	}
	embedOnly := DiscordMessage{Embeds: []EmbedSummary{{Title: "Only"}}}
	if got := embedOnly.FullText(); got != `[Embed "Only"]` {
		t.Fatalf("unexpected embed-only text: %q", got)
	}
	if got := (DiscordMessage{Content: "plain"}).FullText(); got != "plain" {
		t.Fatalf("unexpected plain text: %q", got)
	}
}

func TestRenderTruncatesHugeEmbeds(t *testing.T) {
	s := EmbedSummary{Description: strings.Repeat("a", 5000)}
	if n := len([]rune(s.Render())); n > maxEmbedTextRunes {
		t.Fatalf("expected at most %d runes, got %d", maxEmbedTextRunes, n)
	}
}

// A reply to an embed-only message (no text content) quotes the embed, so
// the "[reply to X: ...]" marker says what is being replied to even when
// the embed is no longer in the history window.
func TestQuoteTextIncludesEmbeds(t *testing.T) {
	ref := &discordgo.Message{Embeds: []*discordgo.MessageEmbed{{
		Type:        discordgo.EmbedTypeRich,
		Title:       "Server Maintenance Window",
		Description: "Database cluster b-14 will be offline.",
		Author:      &discordgo.MessageEmbedAuthor{Name: "status-bot"},
		Fields:      []*discordgo.MessageEmbedField{{Name: "Start", Value: "Saturday 02:00 UTC"}, {Name: "Owner", Value: "team Halcyon"}},
		Footer:      &discordgo.MessageEmbedFooter{Text: "Posted by infra on-call"},
	}}}
	want := `[Embed "Server Maintenance Window" by status-bot: Database cluster b-14 will be offline. | Start: Saturday 02:00 UTC | Owner: team Halcyon | Posted by infra on-call]`
	if got := QuoteText(ref); got != want {
		t.Fatalf("embed-only quote:\n got %q\nwant %q", got, want)
	}
	ref.Content = "see below"
	if got := QuoteText(ref); got != "see below\n"+want {
		t.Fatalf("content + embed quote: %q", got)
	}
	if got := QuoteText(&discordgo.Message{Content: "plain"}); got != "plain" {
		t.Fatalf("plain quote: %q", got)
	}
	if QuoteText(nil) != "" {
		t.Fatal("nil quote must be empty")
	}
}
