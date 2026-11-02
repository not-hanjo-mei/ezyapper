package bot

import (
	"strconv"
	"testing"

	"ezyapper/internal/types"
)

// channelHistory returns messages with IDs 1..n, newest first.
func channelHistory(n int) []*types.DiscordMessage {
	out := make([]*types.DiscordMessage, 0, n)
	for i := n; i >= 1; i-- {
		out = append(out, &types.DiscordMessage{ID: strconv.Itoa(1000 + i)})
	}
	return out
}

func windowOldest(w []*types.DiscordMessage) string { return w[len(w)-1].ID }

func TestContextWindowAnchoredGrowthAndFlush(t *testing.T) {
	w := newContextWindow()
	limit, flush := 5, 3
	fetch := fetchSize(limit, flush)

	got := w.apply("c", channelHistory(20)[:fetch], limit, flush)
	if len(got) != 5 || windowOldest(got) != "1016" {
		t.Fatalf("cold start: len=%d oldest=%s", len(got), windowOldest(got))
	}
	// Three more messages: window grows, anchor unchanged.
	for n := 21; n <= 23; n++ {
		got = w.apply("c", channelHistory(n)[:fetch], limit, flush)
		if windowOldest(got) != "1016" || len(got) != 5+(n-20) {
			t.Fatalf("n=%d: len=%d oldest=%s", n, len(got), windowOldest(got))
		}
	}
	// One more exceeds limit+flush: flush down to limit and re-anchor.
	got = w.apply("c", channelHistory(24)[:fetch], limit, flush)
	if len(got) != 5 || windowOldest(got) != "1020" {
		t.Fatalf("flush: len=%d oldest=%s", len(got), windowOldest(got))
	}
}

func TestContextWindowDisabledSlides(t *testing.T) {
	w := newContextWindow()
	got := w.apply("c", channelHistory(10), 4, 0)
	if len(got) != 4 || windowOldest(got) != "1007" {
		t.Fatalf("len=%d oldest=%s", len(got), windowOldest(got))
	}
}

func TestContextWindowResetAndSnowflakeOrder(t *testing.T) {
	if !snowflakeLess("999", "1000") || snowflakeLess("1000", "999") {
		t.Fatal("snowflakes must compare numerically")
	}
	w := newContextWindow()
	w.apply("c", channelHistory(20)[:9], 5, 3)
	w.reset("c")
	got := w.apply("c", channelHistory(22)[:9], 5, 3)
	if windowOldest(got) != "1018" {
		t.Fatalf("after reset expected fresh anchor, got %s", windowOldest(got))
	}
}
