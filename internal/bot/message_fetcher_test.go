package bot

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

type pageCall struct {
	limit    int
	beforeID string
}

// fakeChannel simulates a channel of n messages with IDs 1..n (newest = n).
type fakeChannel struct {
	n      int
	calls  []pageCall
	failAt map[int]error // call index -> error to return
	// dupFirstOfPage re-sends the previous page's last message at the head
	// of each later page, as a retried/overlapping request would.
	dupFirstOfPage bool
	lastSent       *discordgo.Message
}

func (f *fakeChannel) ChannelMessages(channelID string, limit int, beforeID, afterID, aroundID string, options ...discordgo.RequestOption) ([]*discordgo.Message, error) {
	idx := len(f.calls)
	f.calls = append(f.calls, pageCall{limit: limit, beforeID: beforeID})
	if err, ok := f.failAt[idx]; ok {
		return nil, err
	}
	start := f.n
	if beforeID != "" {
		b, _ := strconv.Atoi(beforeID)
		start = b - 1
	}
	var out []*discordgo.Message
	if f.dupFirstOfPage && f.lastSent != nil && beforeID != "" && limit > 1 {
		out = append(out, f.lastSent)
	}
	for id := start; id >= 1 && len(out) < limit; id-- {
		out = append(out, &discordgo.Message{ID: strconv.Itoa(id), ChannelID: channelID})
	}
	if len(out) > 0 {
		f.lastSent = out[len(out)-1]
	}
	return out, nil
}

func newTestFetcher(api channelMessagesAPI) *DiscordMessageFetcher {
	return &DiscordMessageFetcher{session: api, sleep: func(context.Context, time.Duration) error { return nil }}
}

func TestFetchPaginated_LimitSemantics250(t *testing.T) {
	ch := &fakeChannel{n: 1000}
	msgs, err := newTestFetcher(ch).fetchPaginated(context.Background(), "c", 250)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 250 {
		t.Fatalf("expected 250 messages, got %d", len(msgs))
	}
	want := []int{100, 100, 50}
	if len(ch.calls) != len(want) {
		t.Fatalf("expected %d requests, got %d: %+v", len(want), len(ch.calls), ch.calls)
	}
	for i, w := range want {
		if ch.calls[i].limit != w {
			t.Fatalf("request %d: expected limit %d, got %d", i, w, ch.calls[i].limit)
		}
	}
}

func TestFetchPaginated_CrossPageOrderingNewestFirst(t *testing.T) {
	ch := &fakeChannel{n: 1000}
	msgs, err := newTestFetcher(ch).fetchPaginated(context.Background(), "c", 250)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range msgs {
		if want := strconv.Itoa(1000 - i); m.ID != want {
			t.Fatalf("position %d: expected id %s, got %s", i, want, m.ID)
		}
	}
	if ch.calls[1].beforeID != "901" || ch.calls[2].beforeID != "801" {
		t.Fatalf("cursor did not advance to the oldest id of each page: %+v", ch.calls)
	}
}

func TestFetchPaginated_ChannelShorterThanLimit(t *testing.T) {
	ch := &fakeChannel{n: 130}
	msgs, err := newTestFetcher(ch).fetchPaginated(context.Background(), "c", 250)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 130 || len(ch.calls) != 2 {
		t.Fatalf("expected 130 messages in 2 requests, got %d in %d", len(msgs), len(ch.calls))
	}
}

func TestFetchPaginated_MidPaginationFailureKeepsEarlierPages(t *testing.T) {
	boom := errors.New("connection reset")
	ch := &fakeChannel{n: 1000, failAt: map[int]error{1: boom}}
	f := newTestFetcher(ch)

	msgs, err := f.fetchPaginated(context.Background(), "c", 250)
	if !errors.Is(err, boom) {
		t.Fatalf("expected the page error to be returned, got %v", err)
	}
	if len(msgs) != 100 {
		t.Fatalf("expected the first page to be kept, got %d messages", len(msgs))
	}

	f = newTestFetcher(&fakeChannel{n: 1000, failAt: map[int]error{1: boom}})
	converted, err := f.FetchMessages(context.Background(), "c", 250)
	if err != nil || len(converted) != 100 {
		t.Fatalf("FetchMessages should return the partial history without error, got %d, %v", len(converted), err)
	}
}

func TestFetchPaginated_FirstPageFailureIsAnError(t *testing.T) {
	boom := errors.New("forbidden")
	ch := &fakeChannel{n: 1000, failAt: map[int]error{0: boom}}
	if _, err := newTestFetcher(ch).FetchMessages(context.Background(), "c", 50); !errors.Is(err, boom) {
		t.Fatalf("expected error when nothing was fetched, got %v", err)
	}
}

func rateLimited(after time.Duration) error {
	return &discordgo.RateLimitError{RateLimit: &discordgo.RateLimit{TooManyRequests: &discordgo.TooManyRequests{RetryAfter: after}}}
}

func TestFetchPaginated_RateLimitIsRetried(t *testing.T) {
	ch := &fakeChannel{n: 1000, failAt: map[int]error{1: rateLimited(2 * time.Second)}}
	var waited []time.Duration
	f := &DiscordMessageFetcher{session: ch, sleep: func(_ context.Context, d time.Duration) error {
		waited = append(waited, d)
		return nil
	}}
	msgs, err := f.fetchPaginated(context.Background(), "c", 150)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 150 {
		t.Fatalf("expected 150 messages after retry, got %d", len(msgs))
	}
	if len(waited) != 1 || waited[0] != 2*time.Second {
		t.Fatalf("expected one 2s wait, got %v", waited)
	}
	if ch.calls[1].beforeID != ch.calls[2].beforeID {
		t.Fatalf("retry must request the same page: %+v", ch.calls)
	}
}

func TestFetchPaginated_RateLimitGivesUpAfterMaxRetries(t *testing.T) {
	fail := map[int]error{}
	for i := 0; i <= maxRateLimitRetries; i++ {
		fail[i] = rateLimited(time.Millisecond)
	}
	ch := &fakeChannel{n: 1000, failAt: fail}
	_, err := newTestFetcher(ch).fetchPaginated(context.Background(), "c", 50)
	var rl *discordgo.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("expected rate limit error after %d retries, got %v", maxRateLimitRetries, err)
	}
	if len(ch.calls) != maxRateLimitRetries+1 {
		t.Fatalf("expected %d attempts, got %d", maxRateLimitRetries+1, len(ch.calls))
	}
}

func TestFetchPaginated_DeduplicatesByMessageID(t *testing.T) {
	ch := &fakeChannel{n: 1000, dupFirstOfPage: true}
	msgs, err := newTestFetcher(ch).fetchPaginated(context.Background(), "c", 250)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, m := range msgs {
		if seen[m.ID] {
			t.Fatalf("duplicate message %s in result", m.ID)
		}
		seen[m.ID] = true
	}
	if len(msgs) != 250 {
		t.Fatalf("expected 250 unique messages, got %d", len(msgs))
	}
}

func TestFetchPaginated_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := newTestFetcher(&fakeChannel{n: 10}).fetchPaginated(ctx, "c", 10)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestSnowflakeLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{{"9", "10", true}, {"10", "9", false}, {"123", "124", true}, {"124", "124", false}}
	for _, c := range cases {
		if got := snowflakeLess(c.a, c.b); got != c.want {
			t.Errorf("snowflakeLess(%s,%s)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}
