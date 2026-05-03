// Package bot provides Discord bot event handlers
package bot

import (
	"context"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/types"

	"github.com/bwmarrin/discordgo"
)

func (b *Bot) registerHandlers() {
	b.session.AddHandler(b.onReady)
	b.session.AddHandler(b.onMessageCreate)
	b.session.AddHandler(b.onMessageUpdate)
	b.session.AddHandler(b.onMessageReactionAdd)
	b.session.AddHandler(b.onMessageDelete)
	b.session.AddHandler(b.onGuildJoin)
	b.session.AddHandler(b.onGuildLeave)
}

func (b *Bot) onReady(s *discordgo.Session, r *discordgo.Ready) {
	logger.Infof("Bot is ready! Serving %d guilds", len(r.Guilds))

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
		mentionList := []string{}
		for _, user := range m.Mentions {
			mentionList = append(mentionList, user.Username)
		}
		logger.Debugf("    Mentioned users: %v", mentionList)
	}

	logger.Infof("[message] received: content=%q author=%s channel=%s guild=%s",
		m.Content, m.Author.Username, m.ChannelID, m.GuildID)

	// Create DiscordMessage early so bot's own messages also enter channel buffer.
	// Use FromDiscordgo for canonical field population (DisplayName, ImageURLs, etc.).
	// ReplyTo fields from FromDiscordgo are overridden below with config-based truncation.
	msg := types.FromDiscordgo(m)

	if m.MessageReference != nil {
		msg.ReplyToID = m.MessageReference.MessageID
		if m.ReferencedMessage != nil && m.ReferencedMessage.Author != nil {
			msg.ReplyToUsername = m.ReferencedMessage.Author.Username
			content := m.ReferencedMessage.Content
			if len(content) > b.cfg().Discord.ReplyTruncationLength {
				logger.Warnf("reply content truncated from %d to %d chars", len(content), b.cfg().Discord.ReplyTruncationLength)
				content = content[:b.cfg().Discord.ReplyTruncationLength]
			}
			msg.ReplyToContent = content
		} else {
			msg.ReplyToUsername = "(deleted message)"
		}
	}

	// Always add to channel buffer for complete conversation context in consolidation
	// This ensures bot's own messages are included in batch consolidation
	b.addMessageToChannelBuffer(m.ChannelID, &msg)

	// Run plugin OnMessage hooks
	if b.pluginManager != nil {
		continueProcessing, err := b.pluginManager.OnMessage(ctx, types.FromDiscordgo(m))
		if err != nil {
			logger.Warnf("Plugin error in OnMessage: %v", err)
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
	recentMessages, fetchErr := b.discordClient.FetchRecentMessages(ctx, m.ChannelID, b.cfg().Memory.ShortTermLimit)
	if fetchErr != nil {
		logger.Warnf("Failed to fetch recent messages for decision: %v", fetchErr)
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

	pm.SetPhase(PhaseDeciding)
	shouldRespond, reason := b.ShouldRespond(messageCtx, m, recentMessages)
	logger.Infof("Message from %s: shouldRespond=%v, reason=%s", m.Author.Username, shouldRespond, reason)

	if !shouldRespond {
		b.removeProcessingMessageIfMatch(m.ID, pm)
		cancel()
		return
	}

	if b.cfg().AI.Vision.Mode == config.VisionModeTextOnly {
		b.wg.Add(1)
		go b.processMessageWithoutImages(messageCtx, s, m, pm, recentMessages)
		return
	}

	b.wg.Add(1)
	go b.processMessage(messageCtx, s, m, pm, recentMessages)
}
