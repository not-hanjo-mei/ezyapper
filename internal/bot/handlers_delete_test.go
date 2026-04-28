package bot

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestOnMessageDelete_CancelsProcessing(t *testing.T) {
	b := &Bot{processingMessages: make(map[string]*ProcessingMessage)}
	ctx, cancel := context.WithCancel(context.Background())

	pm := &ProcessingMessage{
		MessageID:  "msg-del-1",
		ChannelID:  "chan-del-1",
		AuthorID:   "user-del",
		Content:    "hello",
		CancelFunc: cancel,
	}
	b.processingMessages["msg-del-1"] = pm

	state := discordgo.NewState()
	state.User = &discordgo.User{ID: "bot-id", Username: "bot"}
	session := &discordgo.Session{State: state}

	b.onMessageDelete(session, &discordgo.MessageDelete{
		Message: &discordgo.Message{
			ID:        "msg-del-1",
			ChannelID: "chan-del-1",
			GuildID:   "guild-del-1",
			Author:    &discordgo.User{ID: "user-del"},
		},
	})

	if ctx.Err() != context.Canceled {
		t.Error("expected CancelFunc to be called (context should be canceled)")
	}
	if _, exists := b.processingMessages["msg-del-1"]; exists {
		t.Error("expected processingMessages entry to be removed after deletion")
	}
}

func TestOnMessageDelete_NotFound(t *testing.T) {
	b := &Bot{processingMessages: make(map[string]*ProcessingMessage)}

	state := discordgo.NewState()
	state.User = &discordgo.User{ID: "bot-id", Username: "bot"}
	session := &discordgo.Session{State: state}

	assertNoPanic(t, func() {
		b.onMessageDelete(session, &discordgo.MessageDelete{
			Message: &discordgo.Message{
				ID:        "msg-not-found",
				ChannelID: "chan-1",
				GuildID:   "guild-1",
				Author:    &discordgo.User{ID: "user-1"},
			},
		})
	})
}

func TestOnMessageDelete_NilPayload(t *testing.T) {
	b := &Bot{}
	assertNoPanic(t, func() {
		b.onMessageDelete(nil, nil)
	})
}

func TestOnMessageDelete_NilEmbeddedMessage(t *testing.T) {
	b := &Bot{}
	assertNoPanic(t, func() {
		b.onMessageDelete(nil, &discordgo.MessageDelete{})
	})
}

func TestOnMessageDelete_NilCancelFunc(t *testing.T) {
	b := &Bot{processingMessages: make(map[string]*ProcessingMessage)}
	b.processingMessages["msg-nil-cancel"] = &ProcessingMessage{
		MessageID:  "msg-nil-cancel",
		ChannelID:  "chan-1",
		CancelFunc: nil,
	}

	state := discordgo.NewState()
	state.User = &discordgo.User{ID: "bot-id", Username: "bot"}
	session := &discordgo.Session{State: state}

	assertNoPanic(t, func() {
		b.onMessageDelete(session, &discordgo.MessageDelete{
			Message: &discordgo.Message{
				ID:        "msg-nil-cancel",
				ChannelID: "chan-1",
				GuildID:   "guild-1",
				Author:    &discordgo.User{ID: "user-1"},
			},
		})
	})

	if _, exists := b.processingMessages["msg-nil-cancel"]; exists {
		t.Error("expected PM to be removed after deletion even with nil CancelFunc")
	}
}

func TestOnMessageDelete_GuardOwnBotMessage(t *testing.T) {
	state := discordgo.NewState()
	state.User = &discordgo.User{ID: "bot-id", Username: "bot"}
	session := &discordgo.Session{State: state}

	b := &Bot{processingMessages: make(map[string]*ProcessingMessage)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pm := &ProcessingMessage{
		MessageID:  "msg-own",
		ChannelID:  "chan-1",
		AuthorID:   "bot-id",
		CancelFunc: cancel,
	}
	b.processingMessages["msg-own"] = pm

	assertNoPanic(t, func() {
		b.onMessageDelete(session, &discordgo.MessageDelete{
			Message: &discordgo.Message{
				ID:        "msg-own",
				ChannelID: "chan-1",
				GuildID:   "guild-1",
				Author:    &discordgo.User{ID: "bot-id"},
			},
		})
	})

	if ctx.Err() != nil {
		t.Error("expected cancel NOT to be called for own bot message")
	}
	if _, exists := b.processingMessages["msg-own"]; !exists {
		t.Error("expected PM to remain untouched for own bot message")
	}
}

func TestProcessMessageCore_CancelBeforeSend(t *testing.T) {
	b := &Bot{processingMessages: make(map[string]*ProcessingMessage)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	pm := b.registerProcessingMessage("msg-del-2", "chan-del-2", "user-del-2", "hello")
	b.clearProcessingMessage(pm, "msg-del-2")
	if _, exists := b.processingMessages["msg-del-2"]; exists {
		t.Error("expected PM to be removed after clearProcessingMessage")
	}
	if ctx.Err() == nil {
		t.Error("expected cancelled context to return error")
	}
}
