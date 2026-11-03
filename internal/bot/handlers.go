// Package bot provides Discord bot event handlers
package bot

import (
	"context"
	"time"

	"ezyapper/internal/logger"
	"ezyapper/internal/types"

	"github.com/bwmarrin/discordgo"
)

// registerHandlers attaches the bot's event handlers to s. It runs for the
// startup session and again for the session a token hot reload opens.
//
// While a reconnect is in progress two sessions are connected for a moment
// (the new one is opened before the old one is closed, so a bad token never
// takes the bot offline). Message and guild events are therefore only
// handled when they come from the current session; otherwise the same
// message could be answered twice. Ready is always handled: it registers the
// slash command and presence on the session that sent it.
func (b *Bot) registerHandlers(s *discordgo.Session) {
	s.AddHandler(b.onReady)
	s.AddHandler(func(s *discordgo.Session, e *discordgo.MessageCreate) {
		if b.isCurrentSession(s) {
			b.onMessageCreate(s, e)
		}
	})
	s.AddHandler(func(s *discordgo.Session, e *discordgo.MessageUpdate) {
		if b.isCurrentSession(s) {
			b.onMessageUpdate(s, e)
		}
	})
	s.AddHandler(func(s *discordgo.Session, e *discordgo.MessageDelete) {
		if b.isCurrentSession(s) {
			b.onMessageDelete(s, e)
		}
	})
	s.AddHandler(func(s *discordgo.Session, e *discordgo.GuildCreate) {
		if b.isCurrentSession(s) {
			b.onGuildJoin(s, e)
		}
	})
	s.AddHandler(func(s *discordgo.Session, e *discordgo.GuildDelete) {
		if b.isCurrentSession(s) {
			b.onGuildLeave(s, e)
		}
	})
	s.AddHandler(func(s *discordgo.Session, e *discordgo.InteractionCreate) {
		if b.isCurrentSession(s) {
			b.onInteractionCreate(s, e)
		}
	})
}

// isCurrentSession reports whether s is the live session.
func (b *Bot) isCurrentSession(s *discordgo.Session) bool {
	return s != nil && s == b.sess()
}

func (b *Bot) onReady(s *discordgo.Session, r *discordgo.Ready) {
	logger.Infof("[bot] Bot is ready! Serving %d guilds", len(r.Guilds))
	b.registerClearCommand(s)

	s.UpdateStatusComplex(discordgo.UpdateStatusData{
		Status: "online",
		Activities: []*discordgo.Activity{
			{
				Name: "logging mankind's extinction process",
				Type: discordgo.ActivityTypeGame,
			},
		},
	})
}

func (b *Bot) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	ctx := b.ctx

	if m == nil || m.Message == nil {
		logger.Warnf("[message] received nil MESSAGE_CREATE event, skipping")
		return
	}

	if m.Author == nil {
		logger.Warnf("[message] received MESSAGE_CREATE without author: id=%s channel=%s guild=%s, skipping", m.ID, m.ChannelID, m.GuildID)
		return
	}

	logger.Debugf("[message] RAW MESSAGE RECEIVED:")
	logger.Debugf("  MessageID: %s", m.ID)
	logger.Debugf("  Content: %q", m.Content)
	logger.Debugf("  Author: %s (ID: %s, Bot: %v)", m.Author.Username, m.Author.ID, m.Author.Bot)
	logger.Debugf("  Channel: %s", m.ChannelID)
	logger.Debugf("  Guild: %s", m.GuildID)
	logger.Debugf("  Timestamp: %s", m.Timestamp.Format(time.RFC3339))
	logger.Debugf("  Mentions: %d users", len(m.Mentions))
	logger.Debugf("  Attachments: %d", len(m.Attachments))
	logger.Debugf("  Embeds: %d", len(m.Embeds))
	logger.Debugf("  MentionRoles: %d", len(m.MentionRoles))
	logger.Debugf("  MentionEveryone: %v", m.MentionEveryone)
	if ref := m.Reference(); ref != nil {
		logger.Debugf("  Reference MessageID: %s", ref.MessageID)
	} else {
		logger.Debugf("  Reference: none")
	}
	if len(m.Attachments) > 0 {
		for i, att := range m.Attachments {
			logger.Debugf("    Attachment[%d]: %s (Type: %s, Size: %d, URL: %s)", i, att.Filename, att.ContentType, att.Size, att.URL)
		}
	}
	if len(m.Embeds) > 0 {
		for i, emb := range m.Embeds {
			logger.Debugf("    Embed[%d]: Type=%s, Title=%q, Description=%q", i, emb.Type, emb.Title, emb.Description)
		}
	}
	if len(m.Mentions) > 0 {
		mentionList := make([]string, 0, len(m.Mentions))
		for _, user := range m.Mentions {
			mentionList = append(mentionList, user.Username)
		}
		logger.Debugf("    Mentioned users: %v", mentionList)
	}

	logger.Infof("[message] received: content=%q author=%s channel=%s guild=%s",
		m.Content, m.Author.Username, m.ChannelID, m.GuildID)

	// Create DiscordMessage early so bot's own messages also enter channel buffer.
	// Use FromDiscordgo for canonical field population (DisplayName, ImageURLs, etc.).
	msg := types.FromDiscordgo(m)

	if m.MessageReference != nil {
		msg.ReplyToID = m.MessageReference.MessageID
		if m.ReferencedMessage != nil && m.ReferencedMessage.Author != nil {
			msg.ReplyToUsername = m.ReferencedMessage.Author.Username
			msg.ReplyToAuthorID = m.ReferencedMessage.Author.ID
			msg.ReplyToContent = m.ReferencedMessage.Content
		} else {
			msg.ReplyToUsername = "(deleted message)"
		}
	}

	// Regex message filters: an ignored message is not answered, not buffered
	// for consolidation and not counted toward it.
	if ignored, rule := b.isFilteredOut(&msg); ignored {
		logger.Infof("[filter] ignoring message %s from %s in channel %s (rule: %s)", m.ID, m.Author.Username, m.ChannelID, rule)
		return
	}

	// Always add to channel buffer for complete conversation context in consolidation
	// This ensures bot's own messages are included in batch consolidation
	b.addMessageToChannelBuffer(m.ChannelID, &msg)

	// Increment channel message counter on every message the bot sees (including its own).
	// Triggers consolidation when total messages reach the configured interval threshold.
	count, err := b.consolidation.IncrementChannelMessageCount(ctx, m.ChannelID)
	if err != nil {
		logger.Warnf("[message] Failed to increment channel message count: %v", err)
	} else {
		b.triggerChannelConsolidation(ctx, m.ChannelID, count)
	}

	// Run plugin OnMessage hooks
	if b.pluginManager != nil {
		continueProcessing, err := b.pluginManager.OnMessage(ctx, types.FromDiscordgo(m))
		if err != nil {
			logger.Warnf("[bot] Plugin error in OnMessage: %v", err)
		}
		if !continueProcessing {
			logger.Debugf("Message processing blocked by plugin")
			return
		}
	}

	pm := b.registerProcessingMessage(m.ID, m.ChannelID, m.Author.ID, m.Content)

	messageCtx, cancel := context.WithCancel(ctx)
	pm.CancelFunc = cancel

	// Fetch recent messages once for both decision and response paths
	recentMessages, fetchErr := b.fetchContextMessages(ctx, m.ChannelID, b.cfg().Memory.ShortTermLimit)
	if fetchErr != nil {
		logger.Warnf("[bot] Failed to fetch recent messages for decision: %v", fetchErr)
	}

	// Warm channel buffer on cold start.
	b.channelBufferMu.Lock()
	bufferEmpty := len(b.channelMessageBuffer[m.ChannelID]) == 0
	b.channelBufferMu.Unlock()
	if bufferEmpty {
		for _, rm := range recentMessages {
			b.addMessageToChannelBuffer(m.ChannelID, rm)
		}
	}

	// An edit that arrived while we were fetching context (PhaseReceived)
	// only updated pm; decide on the edited text, not the original.
	if pm.GetEditCount() > 0 {
		if edited := pm.GetContent(); edited != m.Content {
			logger.Infof("[edit] Message %s was edited before the decision, using edited content", m.ID)
			m.Content = edited
		}
	}

	pm.SetPhase(PhaseDeciding)
	shouldRespond, reason := b.ShouldRespond(messageCtx, m, recentMessages)
	logger.Infof("[bot] Message from %s: shouldRespond=%v, reason=%s", m.Author.Username, shouldRespond, reason)

	if !shouldRespond {
		b.removeProcessingMessageIfMatch(m.ID, pm)
		cancel()
		return
	}

	if !b.runAfterDecision(messageCtx, m, reason, pm) {
		b.removeProcessingMessageIfMatch(m.ID, pm)
		cancel()
		return
	}

	if !b.cfg().AI.Modalities.Image.Enabled {
		b.wg.Add(1)
		go b.processMessageWithoutImages(messageCtx, s, m, pm, recentMessages)
		return
	}

	b.wg.Add(1)
	go b.processMessage(messageCtx, s, m, pm, recentMessages)
}

// runAfterDecision runs the plugins' after_decision hook once the bot has
// decided to reply. It stores injected prompt context on pm and returns false
// when a plugin cancelled the reply.
func (b *Bot) runAfterDecision(ctx context.Context, m *discordgo.MessageCreate, reason string, pm *ProcessingMessage) bool {
	if b.pluginManager == nil {
		return true
	}
	cancelled, blocks := b.pluginManager.AfterDecision(ctx, types.FromDiscordgo(m), reason)
	if cancelled {
		logger.Infof("[bot] reply to message %s cancelled by a plugin after decision", m.ID)
		return false
	}
	if pm != nil {
		pm.mu.Lock()
		pm.InjectedContext = blocks
		pm.mu.Unlock()
	}
	return true
}
