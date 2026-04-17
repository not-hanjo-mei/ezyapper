package bot

import (
	"testing"

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

func TestOnMessageReactionAddGuard(t *testing.T) {
	b := &Bot{}

	assertNoPanic(t, func() {
		b.onMessageReactionAdd(nil, nil)
	})
}
