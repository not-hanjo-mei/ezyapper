package bot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ezyapper/internal/ai/decision"
	"ezyapper/internal/config"
	"ezyapper/internal/types"

	"github.com/bwmarrin/discordgo"
)

// fakeDecider records what the bot passes to the Decider interface.
type fakeDecider struct {
	result *decision.DecisionResult
	err    error

	gotMsg    decision.MessageInfo
	gotImages int
	gotRecent []decision.ContextMessage
	hadDL     bool
}

func (f *fakeDecider) ShouldRespondWithInfo(ctx context.Context, _ string, msg decision.MessageInfo, images int, recent []decision.ContextMessage) (*decision.DecisionResult, error) {
	f.gotMsg, f.gotImages, f.gotRecent = msg, images, recent
	_, f.hadDL = ctx.Deadline()
	return f.result, f.err
}

func newDecisionTestBot(t *testing.T, provider config.DecisionProvider, replyPercentage float64, d decision.Decider) *Bot {
	t.Helper()
	b, _ := newEditTestBot(t)
	cfg := b.cfg()
	cfg.Discord.MaxResponsesPerMin = 100
	cfg.Discord.CooldownSeconds = 0
	cfg.Discord.ReplyPercentage = replyPercentage
	cfg.Decision.Enabled = true
	cfg.Decision.Provider = provider
	cfg.Decision.Timeout = 1
	cfg.Decision.SystemOne.Timeout = 1
	b.decider = d
	return b
}

func plainMessage() *discordgo.MessageCreate {
	ref := &discordgo.Message{ID: "p1", Author: &discordgo.User{ID: "u2", Username: "bob"}}
	return &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "m1", ChannelID: "c1", Content: "cool, thanks bob",
		Timestamp:         time.Date(2026, 11, 1, 8, 2, 0, 0, time.UTC),
		Author:            &discordgo.User{ID: "u1", Username: "alice"},
		ReferencedMessage: ref,
	}}
}

func TestShouldRespond_SystemOneDeciderWiring(t *testing.T) {
	fd := &fakeDecider{result: &decision.DecisionResult{ShouldRespond: true, Reason: "p(yes)=0.91 threshold=0.70", Confidence: 0.91}}
	b := newDecisionTestBot(t, config.DecisionProviderSystemOne, 0, fd)

	recent := []*types.DiscordMessage{
		{ID: "m1", Username: "alice", Content: "cool, thanks bob"}, // current message, skipped
		{ID: "p1", Username: "bob", AuthorID: "u2", Content: "almost done", ReplyToUsername: "alice",
			Timestamp: time.Date(2026, 11, 1, 8, 1, 0, 0, time.UTC)},
	}
	ok, reason := b.ShouldRespond(context.Background(), plainMessage(), recent)
	if !ok || reason != "system_one decision: p(yes)=0.91 threshold=0.70 (confidence: 0.91)" {
		t.Fatalf("ok=%v reason=%q", ok, reason)
	}
	if !fd.hadDL {
		t.Fatal("decider must get a deadline")
	}
	if fd.gotMsg.AuthorName != "alice" || fd.gotMsg.ReplyTo != "bob" || fd.gotMsg.IsBot || !fd.gotMsg.Timestamp.Equal(plainMessage().Timestamp) {
		t.Fatalf("message info = %+v", fd.gotMsg)
	}
	if len(fd.gotRecent) != 1 || fd.gotRecent[0].AuthorName != "bob" || fd.gotRecent[0].ReplyTo != "alice" {
		t.Fatalf("recent = %+v", fd.gotRecent)
	}
}

func TestShouldRespond_DeciderNoKeepsReason(t *testing.T) {
	fd := &fakeDecider{result: &decision.DecisionResult{ShouldRespond: false, Reason: "p(yes)=0.04 threshold=0.70", Confidence: 0.96}}
	b := newDecisionTestBot(t, config.DecisionProviderSystemOne, 1, fd)
	ok, reason := b.ShouldRespond(context.Background(), plainMessage(), nil)
	if ok || reason != "system_one decision: p(yes)=0.04 threshold=0.70" {
		t.Fatalf("ok=%v reason=%q", ok, reason)
	}
}

func TestShouldRespond_DeciderErrorFallsBackToRandom(t *testing.T) {
	for _, tc := range []struct {
		provider config.DecisionProvider
		pct      float64
		want     bool
	}{
		{config.DecisionProviderSystemOne, 1, true},
		{config.DecisionProviderSystemOne, 0, false},
		{"", 1, true}, // absent provider reports as llm
	} {
		fd := &fakeDecider{err: errors.New("boom")}
		b := newDecisionTestBot(t, tc.provider, tc.pct, fd)
		ok, reason := b.ShouldRespond(context.Background(), plainMessage(), nil)
		wantProvider := string(b.cfg().Decision.Provider)
		if ok != tc.want || reason != wantProvider+" decision failed, fallback" {
			t.Fatalf("provider=%q pct=%v: ok=%v reason=%q", tc.provider, tc.pct, ok, reason)
		}
		if !strings.HasPrefix(reason, wantProvider) {
			t.Fatalf("reason %q must name provider %q", reason, wantProvider)
		}
	}
}
