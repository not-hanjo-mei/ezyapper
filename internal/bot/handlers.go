// Package bot provides Discord bot event handlers
package bot

import (
	"context"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/memory"

	"github.com/bwmarrin/discordgo"
)

// registerHandlers registers all Discord event handlers
func (b *Bot) registerHandlers() {
	b.session.AddHandler(b.onReady)
	b.session.AddHandler(b.onMessageCreate)
	b.session.AddHandler(b.onMessageUpdate)
	b.session.AddHandler(b.onMessageReactionAdd)
	b.session.AddHandler(b.onGuildJoin)
	b.session.AddHandler(b.onGuildLeave)
}

// onReady handles the ready event
func (b *Bot) onReady(s *discordgo.Session, r *discordgo.Ready) {
	logger.Infof("Bot is ready! Serving %d guilds", len(r.Guilds))

	// Set status
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

// onMessageCreate handles new message events
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

	// Log detailed raw message information
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
		var mentionList []string
		for _, user := range m.Mentions {
			mentionList = append(mentionList, user.Username)
		}
		logger.Debugf("    Mentioned users: %v", mentionList)
	}

	// Log immediately when message is received
	logger.Infof("[message] received: content=%q author=%s channel=%s guild=%s",
		m.Content, m.Author.Username, m.ChannelID, m.GuildID)

	// Create DiscordMessage early so bot's own messages also enter channel buffer
	msg := &memory.DiscordMessage{
		ID:        m.ID,
		ChannelID: m.ChannelID,
		GuildID:   m.GuildID,
		AuthorID:  m.Author.ID,
		Username:  m.Author.Username,
		Content:   m.Content,
		Timestamp: m.Timestamp,
		IsBot:     m.Author.Bot,
	}

	if m.MessageReference != nil {
		msg.ReplyToID = m.MessageReference.MessageID
		if m.ReferencedMessage != nil && m.ReferencedMessage.Author != nil {
			msg.ReplyToUsername = m.ReferencedMessage.Author.Username
			content := m.ReferencedMessage.Content
			if len(content) > 100 {
				content = content[:100]
			}
			msg.ReplyToContent = content
		} else {
			msg.ReplyToUsername = "(deleted message)"
		}
	}

	// Always add to channel buffer for complete conversation context in consolidation
	// This ensures bot's own messages are included in batch consolidation
	b.addMessageToChannelBuffer(m.ChannelID, msg)

	// Run plugin OnMessage hooks
	if b.pluginManager != nil {
		continueProcessing, err := b.pluginManager.OnMessage(ctx, m)
		if err != nil {
			logger.Warnf("Plugin error in OnMessage: %v", err)
		}
		if !continueProcessing {
			logger.Debugf("Message processing blocked by plugin")
			return
		}
	}

	// Register message for processing tracking
	pm := b.registerProcessingMessage(m.ID, m.ChannelID, m.Author.ID, m.Content)

	// Create a cancellable context for this message processing
	messageCtx, cancel := context.WithCancel(ctx)
	pm.CancelFunc = cancel

	// Fetch recent messages once for both decision and response paths
	recentMessages, fetchErr := b.discordClient.FetchRecentMessages(ctx, m.ChannelID, b.cfg().Memory.ShortTermLimit)
	if fetchErr != nil {
		logger.Warnf("Failed to fetch recent messages for decision: %v", fetchErr)
	}

	// Feed fetched messages into channel buffer so consolidation has warm context
	for _, rm := range recentMessages {
		b.addMessageToChannelBuffer(m.ChannelID, rm)
	}

	// Determine if we should respond
	pm.SetPhase(PhaseDeciding)
	shouldRespond, reason := b.ShouldRespond(messageCtx, m, recentMessages)
	logger.Infof("Message from %s: shouldRespond=%v, reason=%s", m.Author.Username, shouldRespond, reason)

	if !shouldRespond {
		b.removeProcessingMessage(m.ID)
		cancel()
		return
	}

	if b.cfg().AI.Vision.Mode == config.VisionModeTextOnly {
		go b.processMessageWithoutImages(messageCtx, s, m, pm, recentMessages)
		return
	}

	// Process message in goroutine to not block
	go b.processMessage(messageCtx, s, m, pm, recentMessages)
}
