package bot

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"ezyapper/internal/logger"

	"ezyapper/internal/types"

	"github.com/bwmarrin/discordgo"
)

const discordAPIMaxLimit = 100

// maxRateLimitRetries bounds how many times one page is retried after a 429.
const maxRateLimitRetries = 3

// maxRateLimitWait caps a single Retry-After wait so a bogus header cannot
// stall history fetching for minutes.
const maxRateLimitWait = 10 * time.Second

// channelMessagesAPI is the slice of *discordgo.Session the fetcher needs.
// It exists so pagination can be tested without a live Discord connection.
type channelMessagesAPI interface {
	ChannelMessages(channelID string, limit int, beforeID, afterID, aroundID string, options ...discordgo.RequestOption) ([]*discordgo.Message, error)
}

// DiscordMessageFetcher implements memory.MessageFetcher using the Discord session.
type DiscordMessageFetcher struct {
	session channelMessagesAPI
	// sleep is replaceable in tests.
	sleep func(ctx context.Context, d time.Duration) error
}

// NewDiscordMessageFetcher creates a new Discord-backed message fetcher.
func NewDiscordMessageFetcher(session *discordgo.Session) *DiscordMessageFetcher {
	return &DiscordMessageFetcher{session: session, sleep: sleepCtx}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// FetchMessages implements memory.MessageFetcher by paginating the Discord API
// and converting results to the canonical types.DiscordMessage.
//
// Messages are returned newest first, deduplicated by message ID. If a page
// fails after earlier pages succeeded, the messages fetched so far are kept
// (logged as a warning) rather than discarding the whole history; an error is
// returned only when nothing could be fetched.
func (f *DiscordMessageFetcher) FetchMessages(ctx context.Context, channelID string, limit int) ([]types.DiscordMessage, error) {
	messages, err := f.fetchPaginated(ctx, channelID, limit)
	if err != nil {
		if len(messages) == 0 {
			return nil, err
		}
		logger.Warnf("[history] partial fetch for channel %s: kept %d of %d requested messages: %v", channelID, len(messages), limit, err)
	}
	return f.convertMessages(messages), nil
}

// fetchPaginated walks the channel backwards in pages of at most 100
// messages until totalLimit messages are collected or the channel runs out.
// A limit of 250 issues exactly three requests: 100, 100, 50.
//
// It always returns the messages collected so far, together with the error
// that stopped it, if any.
func (f *DiscordMessageFetcher) fetchPaginated(ctx context.Context, channelID string, totalLimit int) ([]*discordgo.Message, error) {
	all := []*discordgo.Message{}
	seen := make(map[string]struct{}, totalLimit)
	var beforeID string
	remaining := totalLimit

	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return all, err
		}

		batchSize := min(remaining, discordAPIMaxLimit)

		batch, err := f.fetchPage(ctx, channelID, batchSize, beforeID)
		if err != nil {
			return all, err
		}

		added := 0
		for _, msg := range batch {
			if msg == nil || msg.ID == "" {
				continue
			}
			// Network jitter (retried requests, overlapping pages) can
			// return the same message twice. The ID is unique, so it is
			// the dedup key: a message never appears twice in context.
			if _, dup := seen[msg.ID]; dup {
				continue
			}
			seen[msg.ID] = struct{}{}
			all = append(all, msg)
			added++
		}
		remaining -= added

		if len(batch) < batchSize {
			break
		}
		next := oldestID(batch)
		if next == "" || (beforeID != "" && !snowflakeLess(next, beforeID)) {
			// The cursor is not moving backwards; stop instead of looping.
			logger.Warnf("[history] pagination stalled for channel %s at before=%s; stopping", channelID, beforeID)
			break
		}
		beforeID = next
	}

	return all, nil
}

// fetchPage requests one page, waiting out rate limits a bounded number of times.
func (f *DiscordMessageFetcher) fetchPage(ctx context.Context, channelID string, limit int, beforeID string) ([]*discordgo.Message, error) {
	sleep := f.sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	for attempt := 0; ; attempt++ {
		batch, err := f.session.ChannelMessages(channelID, limit, beforeID, "", "")
		if err == nil {
			return batch, nil
		}
		wait, limited := rateLimitWait(err)
		if !limited || attempt >= maxRateLimitRetries {
			return nil, err
		}
		logger.Warnf("[history] rate limited fetching channel %s (before=%s), retrying in %v (attempt %d/%d)", channelID, beforeID, wait, attempt+1, maxRateLimitRetries)
		if sleepErr := sleep(ctx, wait); sleepErr != nil {
			return nil, sleepErr
		}
	}
}

// rateLimitWait reports whether err is a Discord rate limit and how long to wait.
func rateLimitWait(err error) (time.Duration, bool) {
	var rl *discordgo.RateLimitError
	if errors.As(err, &rl) && rl.RateLimit != nil {
		return clampWait(rl.RetryAfter), true
	}
	var rest *discordgo.RESTError
	if errors.As(err, &rest) && rest.Response != nil && rest.Response.StatusCode == http.StatusTooManyRequests {
		wait := time.Second
		if h := rest.Response.Header.Get("Retry-After"); h != "" {
			if secs, perr := strconv.ParseFloat(h, 64); perr == nil {
				wait = time.Duration(secs * float64(time.Second))
			}
		}
		return clampWait(wait), true
	}
	return 0, false
}

func clampWait(d time.Duration) time.Duration {
	if d <= 0 {
		return 500 * time.Millisecond
	}
	return min(d, maxRateLimitWait)
}

// oldestID returns the smallest snowflake in the batch. Discord returns pages
// newest first, but the cursor must not depend on that ordering.
func oldestID(batch []*discordgo.Message) string {
	oldest := ""
	for _, m := range batch {
		if m == nil || m.ID == "" {
			continue
		}
		if oldest == "" || snowflakeLess(m.ID, oldest) {
			oldest = m.ID
		}
	}
	return oldest
}

// snowflakeLess compares two decimal snowflake IDs numerically.
func snowflakeLess(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

func (f *DiscordMessageFetcher) convertMessages(messages []*discordgo.Message) []types.DiscordMessage {
	result := make([]types.DiscordMessage, len(messages))
	for i, msg := range messages {
		result[i] = f.convertMessage(msg)
	}
	return result
}

func (f *DiscordMessageFetcher) convertMessage(msg *discordgo.Message) types.DiscordMessage {
	d := types.DiscordMessage{
		ID:        msg.ID,
		ChannelID: msg.ChannelID,
		GuildID:   msg.GuildID,
		Content:   msg.Content,
		ImageURLs: extractImageURLs(msg, 0),
		Media:     types.ExtractMedia(msg),
		Embeds:    types.SummarizeEmbeds(msg.Embeds),
		Kinds:     types.MessageKinds(msg),
		Timestamp: msg.Timestamp,
	}

	if msg.Author != nil {
		d.AuthorID = msg.Author.ID
		d.Username = msg.Author.Username
		d.DisplayName = msg.Author.GlobalName
		d.IsBot = msg.Author.Bot
	}
	if msg.Member != nil && msg.Member.Nick != "" {
		d.DisplayName = msg.Member.Nick
	}
	if d.DisplayName == "" {
		d.DisplayName = d.Username
	}

	if msg.MessageReference != nil {
		d.ReplyToID = msg.MessageReference.MessageID
		if msg.ReferencedMessage != nil && msg.ReferencedMessage.Author != nil {
			d.ReplyToUsername = msg.ReferencedMessage.Author.Username
			d.ReplyToAuthorID = msg.ReferencedMessage.Author.ID
			d.ReplyToContent = msg.ReferencedMessage.Content
		} else {
			d.ReplyToUsername = "(deleted message)"
		}
	}

	return d
}

func extractImageURLs(msg *discordgo.Message, maxImages int) []string {
	return types.ExtractImageURLs(msg, maxImages)
}
