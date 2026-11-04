package bot

import (
	"sync/atomic"
	"testing"

	"ezyapper/internal/config"
	"ezyapper/internal/types"
)

func bufferBot(interval int) *Bot {
	var store atomic.Value
	store.Store(&config.Config{Memory: config.MemoryConfig{ConsolidationInterval: interval}})
	return &Bot{configStore: &store, channelMessageBuffer: map[string][]*types.DiscordMessage{}}
}

func ids(msgs []*types.DiscordMessage) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.ID
	}
	return out
}

// A batch is the oldest interval messages; newer ones stay for the next
// batch instead of being dropped by the consolidator's truncation.
func TestTakeChannelMessageBatch_OldestFirstRestKept(t *testing.T) {
	b := bufferBot(3)
	for _, id := range []string{"105", "101", "103", "102", "104"} { // out of order (warm-up)
		b.addMessageToChannelBuffer("c", &types.DiscordMessage{ID: id})
	}
	first := b.takeChannelMessageBatch("c", 3)
	if got := ids(first); len(got) != 3 || got[0] != "101" || got[1] != "102" || got[2] != "103" {
		t.Fatalf("first batch = %v", got)
	}
	second := b.takeChannelMessageBatch("c", 3)
	if got := ids(second); len(got) != 2 || got[0] != "104" || got[1] != "105" {
		t.Fatalf("second batch = %v", got)
	}
}

// After a batch, the cold-start warm-up re-adds recent history; messages
// already consolidated must not be buffered (and consolidated) again.
func TestChannelBuffer_SkipsConsolidatedMessages(t *testing.T) {
	b := bufferBot(3)
	for _, id := range []string{"101", "102", "103"} {
		b.addMessageToChannelBuffer("c", &types.DiscordMessage{ID: id})
	}
	_ = b.takeChannelMessageBatch("c", 3)
	for _, id := range []string{"102", "103", "104"} { // warm-up after the batch
		b.addMessageToChannelBuffer("c", &types.DiscordMessage{ID: id})
	}
	if got := ids(b.takeChannelMessageBatch("c", 3)); len(got) != 1 || got[0] != "104" {
		t.Fatalf("batch after warm-up = %v, want only the new message", got)
	}
	// Other channels are independent.
	b.addMessageToChannelBuffer("d", &types.DiscordMessage{ID: "50"})
	if got := ids(b.takeChannelMessageBatch("d", 3)); len(got) != 1 {
		t.Fatalf("other channel = %v", got)
	}
}

func TestSelectUnconsolidated(t *testing.T) {
	b := bufferBot(2)
	_ = b.selectUnconsolidated("c", []*types.DiscordMessage{{ID: "11"}, {ID: "10"}}, 2)
	got := ids(b.selectUnconsolidated("c", []*types.DiscordMessage{{ID: "13"}, {ID: "12"}, {ID: "11"}, {ID: "14"}}, 2))
	if len(got) != 2 || got[0] != "12" || got[1] != "13" {
		t.Fatalf("selected = %v", got)
	}
}
