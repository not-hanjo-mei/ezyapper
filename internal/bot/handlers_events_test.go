package bot

import (
	"testing"
	"time"

	"ezyapper/internal/types"

	"github.com/bwmarrin/discordgo"
)

func assertNoPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	fn()
}

func TestOnMessageCreateGuards(t *testing.T) {
	b := &Bot{}

	assertNoPanic(t, func() {
		b.onMessageCreate(nil, nil)
	})

	assertNoPanic(t, func() {
		b.onMessageCreate(nil, &discordgo.MessageCreate{
			Message: &discordgo.Message{
				ID:        "msg-1",
				ChannelID: "channel-1",
				GuildID:   "guild-1",
				Content:   "hello",
			},
		})
	})
}

func TestOnMessageUpdateGuards(t *testing.T) {
	b := &Bot{}

	assertNoPanic(t, func() {
		b.onMessageUpdate(nil, nil)
	})

	msg := &discordgo.MessageUpdate{
		Message: &discordgo.Message{
			ID:        "msg-1",
			ChannelID: "channel-1",
			GuildID:   "guild-1",
			Content:   "edited",
			Author: &discordgo.User{
				ID:       "user-1",
				Username: "alice",
			},
		},
	}

	assertNoPanic(t, func() {
		b.onMessageUpdate(&discordgo.Session{}, msg)
	})

	state := discordgo.NewState()
	state.User = &discordgo.User{ID: "bot-1", Username: "bot"}
	session := &discordgo.Session{State: state}

	assertNoPanic(t, func() {
		b.onMessageUpdate(session, &discordgo.MessageUpdate{
			Message: &discordgo.Message{
				ID:        "msg-2",
				ChannelID: "channel-1",
				GuildID:   "guild-1",
				Content:   "edited",
			},
		})
	})
}

func TestFromDiscordgo_ImageURLsPopulated(t *testing.T) {
	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg-img-1",
			ChannelID: "chan-1",
			GuildID:   "guild-1",
			Content:   "check this image",
			Author: &discordgo.User{
				ID:         "user-1",
				Username:   "alice",
				GlobalName: "Alice Display",
			},
			Timestamp: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			Attachments: []*discordgo.MessageAttachment{
				{
					URL:         "https://cdn.discord.com/attachments/x/y/image.png",
					ContentType: "image/png",
				},
			},
		},
	}

	msg := types.FromDiscordgo(m)

	if len(msg.ImageURLs) != 1 {
		t.Errorf("expected 1 image URL, got %d", len(msg.ImageURLs))
	}
	if msg.ImageURLs[0] != "https://cdn.discord.com/attachments/x/y/image.png" {
		t.Errorf("unexpected image URL: %s", msg.ImageURLs[0])
	}

	if msg.DisplayName != "Alice Display" {
		t.Errorf("expected DisplayName 'Alice Display', got %q", msg.DisplayName)
	}
}

func TestFromDiscordgo_NoImages(t *testing.T) {
	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg-txt-1",
			ChannelID: "chan-1",
			GuildID:   "guild-1",
			Content:   "plain text message",
			Author: &discordgo.User{
				ID:       "user-1",
				Username: "bob",
			},
		},
	}

	msg := types.FromDiscordgo(m)

	if len(msg.ImageURLs) != 0 {
		t.Errorf("expected 0 image URLs, got %d", len(msg.ImageURLs))
	}
}
