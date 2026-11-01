package bot

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/ratelimit"
	"ezyapper/internal/types"

	"github.com/bwmarrin/discordgo"
)

func TestHandleEditedMessagePhaseReceived(t *testing.T) {
	b := &Bot{}
	cancelCalled := false
	pm := &ProcessingMessage{
		MessageID:       "msg-1",
		Content:         "old",
		OriginalContent: "old",
		Phase:           PhaseReceived,
		CancelFunc: func() {
			cancelCalled = true
		},
	}

	reprocess := b.handleEditedMessage(pm, "new")
	if reprocess {
		t.Fatal("expected no reprocess in PhaseReceived")
	}
	if cancelCalled {
		t.Fatal("expected cancel not to be called in PhaseReceived")
	}
	if pm.GetContent() != "new" {
		t.Fatalf("expected updated content, got %q", pm.GetContent())
	}
	if pm.GetEditCount() != 1 {
		t.Fatalf("expected edit count 1, got %d", pm.GetEditCount())
	}
}

func TestHandleEditedMessagePhaseDeciding(t *testing.T) {
	b := &Bot{}
	cancelCalled := false
	pm := &ProcessingMessage{
		MessageID:       "msg-1",
		Content:         "old",
		OriginalContent: "old",
		Phase:           PhaseDeciding,
		CancelFunc: func() {
			cancelCalled = true
		},
	}

	reprocess := b.handleEditedMessage(pm, "new")
	if !reprocess {
		t.Fatal("expected reprocess in PhaseDeciding")
	}
	if !cancelCalled {
		t.Fatal("expected cancel to be called in PhaseDeciding")
	}
	if pm.GetContent() != "new" {
		t.Fatalf("expected updated content, got %q", pm.GetContent())
	}
}

func TestHandleEditedMessagePhaseSending(t *testing.T) {
	b := &Bot{}
	cancelCalled := false
	pm := &ProcessingMessage{
		MessageID:       "msg-1",
		Content:         "old",
		OriginalContent: "old",
		Phase:           PhaseSending,
		CancelFunc: func() {
			cancelCalled = true
		},
	}

	reprocess := b.handleEditedMessage(pm, "new")
	if reprocess {
		t.Fatal("expected no reprocess in PhaseSending")
	}
	if !cancelCalled {
		t.Fatal("expected cancel to be called in PhaseSending")
	}
	if pm.GetContent() != "new" {
		t.Fatalf("expected updated content, got %q", pm.GetContent())
	}
}

func TestHandleEditedMessageNilMessage(t *testing.T) {
	b := &Bot{}
	if b.handleEditedMessage(nil, "new") {
		t.Fatal("expected false for nil processing message")
	}
}

func TestRemoveProcessingMessageIfMatch_DoesNotDeleteWrongPointer(t *testing.T) {
	b := &Bot{processingMessages: make(map[string]*ProcessingMessage)}

	pm1 := b.registerProcessingMessage("msg-race", "chan-1", "user-1", "original")
	if pm1 == nil {
		t.Fatal("registerProcessingMessage returned nil")
	}

	// Simulate edit: old goroutine's cleanup calls clearProcessingMessage with pm1
	// but a new PM (pm2) has already been registered with the same messageID.
	pm2 := b.registerProcessingMessage("msg-race", "chan-1", "user-1", "edited")
	if pm2 == nil {
		t.Fatal("second registerProcessingMessage returned nil")
	}
	if pm1 == pm2 {
		t.Fatal("expected different pointers for old and new ProcessingMessage")
	}

	// Old goroutine cleanup runs: must NOT delete pm2.
	b.clearProcessingMessage(pm1, "msg-race")

	if _, exists := b.processingMessages["msg-race"]; !exists {
		t.Error("pm2 was incorrectly deleted by old goroutine's clearProcessingMessage")
	}

	// Verify the stored pointer is still pm2.
	p := b.getProcessingMessage("msg-race")
	if p != pm2 {
		t.Error("stored pointer is not pm2 after old goroutine cleanup")
	}

	// New goroutine cleanup runs: should delete pm2.
	b.clearProcessingMessage(pm2, "msg-race")
	if _, exists := b.processingMessages["msg-race"]; exists {
		t.Error("pm2 was not deleted by its own clearProcessingMessage")
	}
}

func TestRemoveProcessingMessageIfMatch_NilPointer(t *testing.T) {
	b := &Bot{processingMessages: make(map[string]*ProcessingMessage)}

	b.registerProcessingMessage("msg-nil", "chan-1", "user-1", "content")

	// clearProcessingMessage with nil pm should be a no-op.
	b.clearProcessingMessage(nil, "msg-nil")

	if _, exists := b.processingMessages["msg-nil"]; !exists {
		t.Error("PM should still exist when clearProcessingMessage called with nil pm")
	}
}

func newEditTestBot(t *testing.T) (*Bot, *discordgo.Session) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Discord.MaxResponsesPerMin = 1
	cfg.Discord.CooldownSeconds = 60
	cfg.Discord.RateLimit.ResetPeriodSeconds = 60
	cfg.Discord.ReplyPercentage = 1
	cfgStore := &atomic.Value{}
	cfgStore.Store(cfg)
	state := discordgo.NewState()
	state.User = &discordgo.User{ID: "bot-1", Username: "bot"}
	session := &discordgo.Session{State: state}
	b := &Bot{
		configStore:          cfgStore,
		session:              session,
		rateLimiter:          ratelimit.NewLimiter(1, 60*time.Second, 60*time.Second),
		processingMessages:   map[string]*ProcessingMessage{},
		channelMessageBuffer: map[string][]*types.DiscordMessage{},
	}
	return b, session
}

// Regression: re-deciding an edited message charged the rate limiter a second
// time, so near max_responses_per_minute the edit was dropped as "rate limited".
func TestShouldRespondReEvaluationSkipsRateLimit(t *testing.T) {
	b, _ := newEditTestBot(t)
	m := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "m1", ChannelID: "c1", Content: "hi <@bot-1>",
		Author:   &discordgo.User{ID: "u1", Username: "alice"},
		Mentions: []*discordgo.User{{ID: "bot-1"}},
	}}

	if ok, reason := b.ShouldRespond(context.Background(), m, nil); !ok {
		t.Fatalf("first decision should respond, got %q", reason)
	}
	if ok, reason := b.ShouldRespond(context.Background(), m, nil); ok || reason != "rate limited" {
		t.Fatalf("a second normal decision over the per-minute limit should be rate limited, got ok=%v %q", ok, reason)
	}
	if ok, reason := b.shouldRespond(context.Background(), m, nil, respondOptions{reEvaluation: true}); !ok {
		t.Fatalf("re-evaluation of an edit must not be rate limited, got %q", reason)
	}
}

// Regression: a MESSAGE_UPDATE caused by a link unfurl (same content) used to
// cancel the in-flight generation and start a new one.
func TestOnMessageUpdateIgnoresUnchangedContent(t *testing.T) {
	b, session := newEditTestBot(t)
	cancelled := false
	pm := b.registerProcessingMessage("m1", "c1", "u1", "look https://example.com")
	pm.SetPhase(PhaseGenerating)
	pm.CancelFunc = func() { cancelled = true }

	b.onMessageUpdate(session, &discordgo.MessageUpdate{Message: &discordgo.Message{
		ID: "m1", ChannelID: "c1", Content: "look https://example.com",
		Author: &discordgo.User{ID: "u1", Username: "alice"},
	}})

	if cancelled {
		t.Fatal("an unfurl-only update must not cancel generation")
	}
	if b.getProcessingMessage("m1") != pm {
		t.Fatal("the in-flight processing message must be kept")
	}
}

func TestOnMessageUpdateSyncsChannelBuffer(t *testing.T) {
	b, session := newEditTestBot(t)
	b.channelMessageBuffer["c1"] = []*types.DiscordMessage{{ID: "m1", Content: "old"}}

	b.onMessageUpdate(session, &discordgo.MessageUpdate{Message: &discordgo.Message{
		ID: "m1", ChannelID: "c1", Content: "new",
		Author: &discordgo.User{ID: "u1", Username: "alice"},
	}})

	if got := b.channelMessageBuffer["c1"][0].Content; got != "new" {
		t.Fatalf("expected buffered content to be updated, got %q", got)
	}
}
