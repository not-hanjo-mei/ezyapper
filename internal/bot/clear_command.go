package bot

import (
	"context"
	"slices"
	"sync"
	"time"

	"ezyapper/internal/logger"
	"ezyapper/internal/types"

	"github.com/bwmarrin/discordgo"
)

// clearCommandName is the slash command that clears a channel's context.
const clearCommandName = "clear-context"

// contextCutoffs remembers, per channel, the moment its context was cleared.
// History is re-fetched from Discord for every reply, so "clearing" the
// in-memory context means hiding everything older than the cutoff. The
// cutoffs live in memory only: Discord history, persisted stores and long
// term memory are untouched, and a restart forgets them (by design).
type contextCutoffs struct {
	mu sync.RWMutex
	at map[string]time.Time
}

func (c *contextCutoffs) set(channelID string, t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.at == nil {
		c.at = make(map[string]time.Time)
	}
	c.at[channelID] = t
}

func (c *contextCutoffs) get(channelID string) (time.Time, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	t, ok := c.at[channelID]
	return t, ok
}

// applyContextCutoff drops messages sent at or before the channel's cutoff.
func (b *Bot) applyContextCutoff(channelID string, msgs []*types.DiscordMessage) []*types.DiscordMessage {
	cutoff, ok := b.cutoffs.get(channelID)
	if !ok {
		return msgs
	}
	out := make([]*types.DiscordMessage, 0, len(msgs))
	for _, m := range msgs {
		if m == nil || m.Timestamp.IsZero() || m.Timestamp.After(cutoff) {
			out = append(out, m)
		}
	}
	return out
}

// registerClearCommand creates the global slash command when enabled.
// It is idempotent: Discord upserts global commands by name.
func (b *Bot) registerClearCommand(s *discordgo.Session) {
	cc := b.cfg().Discord.ClearCommand
	if !cc.Enabled || s == nil || s.State == nil || s.State.User == nil {
		return
	}
	dmAllowed := false
	cmd := &discordgo.ApplicationCommand{
		Name:         clearCommandName,
		Description:  "Make the bot forget this channel's conversation context (memory is kept)",
		DMPermission: &dmAllowed,
	}
	if _, err := s.ApplicationCommandCreate(s.State.User.ID, "", cmd); err != nil {
		logger.Warnf("[clear] failed to register /%s: %v", clearCommandName, err)
		return
	}
	logger.Infof("[clear] registered /%s (allowed users: %d)", clearCommandName, len(cc.AllowedUserIDs))
}

// onInteractionCreate handles the clear-context slash command.
func (b *Bot) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Interaction == nil || i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	if i.ApplicationCommandData().Name != clearCommandName {
		return
	}
	reply := func(text string) {
		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: text, Flags: discordgo.MessageFlagsEphemeral},
		})
		if err != nil {
			logger.Warnf("[clear] failed to answer interaction: %v", err)
		}
	}

	userID := interactionUserID(i)
	cc := b.cfg().Discord.ClearCommand
	if !cc.Enabled {
		reply("This command is disabled.")
		return
	}
	if userID == "" || !slices.Contains(cc.AllowedUserIDs, userID) {
		logger.Warnf("[clear] user %s is not allowed to clear context in channel %s", userID, i.ChannelID)
		reply("You are not allowed to use this command.")
		return
	}

	b.clearChannelContext(i.ChannelID)
	logger.Infof("[clear] context cleared for channel %s by user %s", i.ChannelID, userID)
	reply("Context cleared for this channel. Long-term memory is unchanged.")
}

// clearChannelContext hides all earlier history of the channel from the
// model and cancels replies still being generated there.
func (b *Bot) clearChannelContext(channelID string) {
	b.cutoffs.set(channelID, time.Now())
	b.contextWindow.reset(channelID)

	b.processingMu.Lock()
	var cancels []func()
	for _, pm := range b.processingMessages {
		if pm != nil && pm.ChannelID == channelID && pm.CancelFunc != nil {
			cancels = append(cancels, pm.CancelFunc)
		}
	}
	b.processingMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func interactionUserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

// fetchContextMessages fetches recent channel history for context and hides
// messages older than a /clear-context cutoff.
func (b *Bot) fetchContextMessages(ctx context.Context, channelID string, limit int) ([]*types.DiscordMessage, error) {
	flush := b.cfg().Memory.ContextFlushWindow
	msgs, err := b.discordClient.FetchRecentMessages(ctx, channelID, fetchSize(limit, flush))
	if err != nil {
		return nil, err
	}
	msgs = b.applyContextCutoff(channelID, msgs)
	return b.contextWindow.apply(channelID, msgs, limit, flush), nil
}
