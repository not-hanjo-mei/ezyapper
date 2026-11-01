package bot

import (
	"testing"
	"time"

	"ezyapper/internal/types"
)

func TestClearChannelContextHidesOlderHistoryOnlyInThatChannel(t *testing.T) {
	b := &Bot{processingMessages: map[string]*ProcessingMessage{}}
	old := time.Now().Add(-time.Minute)

	cancelledHere, cancelledThere := false, false
	b.processingMessages["a"] = &ProcessingMessage{ChannelID: "c1", CancelFunc: func() { cancelledHere = true }}
	b.processingMessages["b"] = &ProcessingMessage{ChannelID: "c2", CancelFunc: func() { cancelledThere = true }}

	b.clearChannelContext("c1")
	newer := time.Now().Add(time.Second)

	msgs := []*types.DiscordMessage{{ID: "new", Timestamp: newer}, {ID: "old", Timestamp: old}}
	got := b.applyContextCutoff("c1", msgs)
	if len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("expected only the post-clear message, got %v", got)
	}
	if other := b.applyContextCutoff("c2", msgs); len(other) != 2 {
		t.Fatalf("other channels must be unaffected, got %d messages", len(other))
	}
	if !cancelledHere || cancelledThere {
		t.Fatalf("expected only in-channel generation to be cancelled (here=%v there=%v)", cancelledHere, cancelledThere)
	}
}
