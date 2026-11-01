package types

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestExtractImageURLsSkipsLinkPreviewThumbnails(t *testing.T) {
	msg := &discordgo.Message{
		Attachments: []*discordgo.MessageAttachment{{URL: "https://cdn.discordapp.com/a.png", ContentType: "image/png"}},
		Embeds: []*discordgo.MessageEmbed{
			{Type: discordgo.EmbedTypeArticle, Thumbnail: &discordgo.MessageEmbedThumbnail{URL: "https://site/preview.jpg"}},
			{Type: discordgo.EmbedTypeLink, Thumbnail: &discordgo.MessageEmbedThumbnail{URL: "https://site/link.jpg"}},
			{Type: discordgo.EmbedTypeImage, Thumbnail: &discordgo.MessageEmbedThumbnail{URL: "https://site/posted.png"}},
			{Type: discordgo.EmbedTypeRich, Image: &discordgo.MessageEmbedImage{URL: "https://site/rich.png"}},
		},
	}
	got := ExtractImageURLs(msg, 0)
	want := []string{"https://cdn.discordapp.com/a.png", "https://site/posted.png", "https://site/rich.png"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if capped := ExtractImageURLs(msg, 2); len(capped) != 2 {
		t.Fatalf("expected cap of 2, got %v", capped)
	}
}

func TestStableImageKeyIgnoresDiscordSignature(t *testing.T) {
	a := StableImageKey("https://cdn.discordapp.com/attachments/1/2/x.png?ex=aaa&is=bbb&hm=ccc&")
	b := StableImageKey("https://media.discordapp.net/attachments/1/2/x.png?ex=ddd&is=eee&hm=fff&")
	if a != b {
		t.Fatalf("expected re-signed URLs to share a key: %q vs %q", a, b)
	}
	other := "https://example.com/x.png?v=1"
	if StableImageKey(other) != other {
		t.Fatal("non-Discord URLs must keep their query string")
	}
}

func TestMessageKinds(t *testing.T) {
	m := &discordgo.Message{
		Content:          "hi",
		Author:           &discordgo.User{Bot: true},
		Embeds:           []*discordgo.MessageEmbed{{Type: discordgo.EmbedTypeRich, Title: "x"}},
		StickerItems:     []*discordgo.StickerItem{{ID: "s"}},
		Attachments:      []*discordgo.MessageAttachment{{ContentType: "audio/ogg"}, {ContentType: "video/mp4"}, {ContentType: "application/pdf"}},
		Flags:            discordgo.MessageFlagsIsVoiceMessage,
		MessageReference: &discordgo.MessageReference{MessageID: "1"},
		WebhookID:        "w",
	}
	got := strings.Join(MessageKinds(m), ",")
	want := "text,embed,sticker,voice,video,file,reply,bot,webhook"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}
