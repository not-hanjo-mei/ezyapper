package bot

import (
	"sync"

	"ezyapper/internal/types"
)

// contextWindow implements the anchored history window.
//
// A plain sliding window ("the newest N messages") changes its first message
// on every request, so the <context> section never shares a prefix with the
// previous request and provider prompt caches miss every time. The anchored
// window instead keeps its oldest message (the anchor) fixed and only grows;
// once it would hold more than limit+flush messages it drops the oldest
// block at once, re-anchoring at the oldest of the newest `limit` messages.
// The window therefore holds between limit and limit+flush messages, and
// between flushes every request extends the previous one.
type contextWindow struct {
	mu      sync.Mutex
	anchors map[string]string // channel ID -> oldest message ID in the window
}

func newContextWindow() *contextWindow {
	return &contextWindow{anchors: make(map[string]string)}
}

// fetchSize is how many messages to fetch so apply can tell whether the
// window overflowed.
func fetchSize(limit, flush int) int {
	if flush <= 0 {
		return limit
	}
	return limit + flush + 1
}

// apply returns the window for channelID from msgs (newest first).
func (w *contextWindow) apply(channelID string, msgs []*types.DiscordMessage, limit, flush int) []*types.DiscordMessage {
	if w == nil {
		w = &contextWindow{anchors: map[string]string{}}
	}
	if limit <= 0 {
		return nil
	}
	if flush <= 0 {
		if len(msgs) > limit {
			return msgs[:limit]
		}
		return msgs
	}
	if len(msgs) == 0 {
		return msgs
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	reanchor := func() []*types.DiscordMessage {
		n := min(limit, len(msgs))
		w.anchors[channelID] = msgs[n-1].ID
		return msgs[:n]
	}

	anchor, ok := w.anchors[channelID]
	if !ok {
		return reanchor()
	}
	visible := 0
	for _, m := range msgs {
		if m == nil || snowflakeLess(m.ID, anchor) {
			break
		}
		visible++
	}
	if visible > limit+flush {
		return reanchor()
	}
	if visible == 0 {
		// Everything since the anchor is gone (deleted or cleared): start over.
		return reanchor()
	}
	return msgs[:visible]
}

// reset forgets the anchor of a channel (e.g. after /clear-context).
func (w *contextWindow) reset(channelID string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	delete(w.anchors, channelID)
	w.mu.Unlock()
}
