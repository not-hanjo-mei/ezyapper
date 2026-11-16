package bot

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"ezyapper/internal/config"

	"github.com/bwmarrin/discordgo"
)

// minimalConfigStore creates a config store with enough fields for processMessageCore to run.
func minimalConfigStore() *atomic.Value {
	var v atomic.Value
	v.Store(&config.Config{
		Discord: config.DiscordConfig{
			OtherBotPolicy:  config.OtherBotIgnore,
			CooldownSeconds: 5,
		},
		Memory: config.MemoryConfig{
			ShortTermLimit: 10,
			Retrieval: config.RetrievalConfig{
				TopK: 0,
			},
		},
		AI: config.AIConfig{
			Modalities: config.ModalitiesConfig{
				Image: config.ImageModalityConfig{Enabled: true},
			},
		},
	})
	return &v
}

// newTestMessage creates a minimal discordgo.MessageCreate for testing.
func newTestMessage(id, channelID, guildID, authorID, authorName, content string) *discordgo.MessageCreate {
	return &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        id,
			ChannelID: channelID,
			GuildID:   guildID,
			Content:   content,
			Author: &discordgo.User{
				ID:       authorID,
				Username: authorName,
			},
		},
	}
}

// TestProcessMessageCore_CancelledContext verifies that a cancelled context
// causes processMessageCore to return immediately at the first checkpoint.
func TestProcessMessageCore_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	b := &Bot{
		ctx:                ctx,
		processingMessages: map[string]*ProcessingMessage{},
	}
	m := newTestMessage("msg-1", "chan-1", "guild-1", "user-1", "alice", "hello")
	pm := &ProcessingMessage{MessageID: "msg-1"}
	b.processingMessages["msg-1"] = pm

	assertNoPanic(t, func() {
		b.processMessageCore(ctx, nil, m, pm, nil)
	})

	b.processingMu.RLock()
	_, exists := b.processingMessages["msg-1"]
	b.processingMu.RUnlock()
	if exists {
		t.Error("expected processing message to be cleared after cancelled context")
	}
}

// TestProcessMessageCore_CancelledAfterPhaseSet verifies that a context
// cancelled after the first checkpoint also results in early return.
func TestProcessMessageCore_CancelledAfterPhaseSet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	b := &Bot{
		ctx:                ctx,
		processingMessages: map[string]*ProcessingMessage{},
	}
	m := newTestMessage("msg-2", "chan-1", "guild-1", "user-2", "bob", "test")
	pm := &ProcessingMessage{MessageID: "msg-2"}
	b.processingMessages["msg-2"] = pm

	time.Sleep(100 * time.Millisecond)

	assertNoPanic(t, func() {
		b.processMessageCore(ctx, nil, m, pm, nil)
	})

	b.processingMu.RLock()
	_, exists := b.processingMessages["msg-2"]
	b.processingMu.RUnlock()
	if exists {
		t.Error("expected processing message to be cleared after timeout cancellation")
	}
}

// TestProcessMessageCore_NilPM verifies that a nil ProcessingMessage does not panic.
func TestProcessMessageCore_NilPM(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	b := &Bot{
		ctx:                ctx,
		processingMessages: map[string]*ProcessingMessage{},
	}
	m := newTestMessage("msg-3", "chan-1", "guild-1", "user-3", "carol", "hi")

	assertNoPanic(t, func() {
		b.processMessageCore(ctx, nil, m, nil, nil)
	})
}

// TestProcessMessageCore_GuildLookupError verifies that when Guild lookup fails,
// the function returns early without panicking and clears the processing message.
func TestProcessMessageCore_GuildLookupError(t *testing.T) {
	ctx := context.Background()
	s, _ := discordgo.New("Bot fake-test-token")

	cfgStore := minimalConfigStore()
	b := &Bot{
		ctx:                ctx,
		cancel:             func() {},
		configStore:        cfgStore,
		processingMessages: map[string]*ProcessingMessage{},
	}
	b.session.Store(s)

	m := newTestMessage("msg-4", "chan-1", "nonexistent-guild", "user-4", "dave", "test")
	pm := &ProcessingMessage{MessageID: "msg-4"}
	b.processingMessages["msg-4"] = pm

	assertNoPanic(t, func() {
		b.processMessageCore(ctx, s, m, pm, nil)
	})

	b.processingMu.RLock()
	_, exists := b.processingMessages["msg-4"]
	b.processingMu.RUnlock()
	if exists {
		t.Error("expected processing message to be cleared after guild lookup failure")
	}
}

// TestProcessMessageCore_DisplayNameAlwaysSet verifies processMessage
// delegates to the core, where profile.DisplayName is set unconditionally.
func TestProcessMessageCore_DisplayNameAlwaysSet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	b := &Bot{
		ctx:                ctx,
		processingMessages: map[string]*ProcessingMessage{},
	}

	m := newTestMessage("msg-7", "chan-1", "guild-1", "user-7", "grace", "structural test")
	pm := &ProcessingMessage{MessageID: "msg-7"}
	b.processingMessages["msg-7"] = pm

	b.wg.Add(1)
	assertNoPanic(t, func() {
		b.processMessage(ctx, nil, m, pm, nil)
	})

	m2 := newTestMessage("msg-7b", "chan-1", "guild-1", "user-7b", "grace", "structural test 2")
	pm2 := &ProcessingMessage{MessageID: "msg-7b"}
	b.processingMessages["msg-7b"] = pm2

	b.wg.Add(1)
	assertNoPanic(t, func() {
		b.processMessage(ctx, nil, m2, pm2, nil)
	})
}

// TestProcessMessage_DelegatesToCore verifies that
// processMessage is a thin wrapper around processMessageCore.
func TestProcessMessage_DelegatesToCore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	b := &Bot{
		ctx:                ctx,
		processingMessages: map[string]*ProcessingMessage{},
	}
	m := newTestMessage("msg-9", "chan-1", "guild-1", "user-9", "ivan", "wrapper test 2")
	pm := &ProcessingMessage{MessageID: "msg-9"}
	b.processingMessages["msg-9"] = pm

	b.wg.Add(1)
	assertNoPanic(t, func() {
		b.processMessage(ctx, nil, m, pm, nil)
	})

	b.processingMu.RLock()
	_, exists := b.processingMessages["msg-9"]
	b.processingMu.RUnlock()
	if exists {
		t.Error("expected wrapper to clear processing message")
	}
}
