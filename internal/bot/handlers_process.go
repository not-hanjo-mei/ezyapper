// Package bot provides Discord bot event handlers
package bot

import (
	"context"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/memory"
	"ezyapper/internal/utils"

	"github.com/bwmarrin/discordgo"
)

// processMessageWithoutImages processes a message without image handling (text-only mode)
func (b *Bot) processMessageWithoutImages(ctx context.Context, s *discordgo.Session, m *discordgo.MessageCreate, pm *ProcessingMessage) {
	// Check if already cancelled before starting
	if err := ctx.Err(); err != nil {
		logger.Infof("[processing] Message %s cancelled before starting", m.ID)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	// Update phase to generating
	if pm != nil {
		pm.SetPhase(PhaseGenerating)
	}

	// Check cancellation before expensive operations
	if err := ctx.Err(); err != nil {
		logger.Infof("[processing] Message %s cancelled before guild lookup", m.ID)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	// Get guild info
	guild, err := b.GetGuild(m.GuildID)
	if err != nil {
		logger.Errorf("Failed to get guild: %v", err)
		b.clearProcessingMessage(pm, m.ID)
		return
	}
	guildName := guild.Name

	// Check cancellation before fetching messages
	if err := ctx.Err(); err != nil {
		logger.Infof("[processing] Message %s cancelled before fetching recent messages", m.ID)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	// Fetch recent messages from Discord for short-term context
	recentMessages, err := b.discordClient.FetchRecentMessages(m.ChannelID, b.cfg().Memory.ShortTermLimit)
	if err != nil {
		logger.Warnf("Failed to fetch recent messages: %v", err)
	}

	// Check cancellation before memory search
	if err := ctx.Err(); err != nil {
		logger.Infof("[processing] Message %s cancelled before memory search", m.ID)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	var memories []*memory.Record
	if b.cfg().Memory.Retrieval.TopK > 0 {
		memories, err = b.memory.Search(ctx, m.Author.ID, m.Content, nil)
		if err != nil {
			logger.Warnf("Failed to search memories: %v", err)
		} else if len(memories) > 0 {
			logger.Debugf("[memory] search found %d memories for user=%s query=%q", len(memories), m.Author.ID, m.Content)
		}
	}

	// Check cancellation before profile fetch
	if err := ctx.Err(); err != nil {
		logger.Infof("[processing] Message %s cancelled before profile fetch", m.ID)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	// Get user profile
	profile, err := b.memory.GetProfile(ctx, m.Author.ID)
	if err != nil {
		logger.Warnf("Failed to get profile: %v", err)
		profile = &memory.Profile{UserID: m.Author.ID}
	}

	// Start typing indicator before LLM processing
	s.ChannelTyping(m.ChannelID)
	// Maintain typing for long LLM responses
	typingCtx, cancelTyping := context.WithCancel(ctx)
	go maintainTyping(typingCtx, s, m.ChannelID)
	defer cancelTyping()

	// Generate response (no images)
	response, err := b.generateResponse(ctx, m, guildName, []string{}, nil, recentMessages, memories, profile)
	if err != nil {
		// Check if error is due to context cancellation
		if ctx.Err() == context.Canceled {
			logger.Infof("[processing] Message %s generation cancelled", m.ID)
		} else {
			logger.Errorf("Failed to generate response: %v", err)
			if shouldSendGenerationFallback(err) {
				b.addGenerationFailureReaction(s, m)
			}
		}
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	if response == "" {
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	// Update phase to sending
	if pm != nil {
		pm.SetPhase(PhaseSending)
	}

	// Send response
	if err := b.sendResponse(ctx, s, m, response); err != nil {
		logger.Errorf("Failed to send response: %v", err)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	// Remove from processing after sending
	b.clearProcessingMessage(pm, m.ID)

	// Run plugin OnResponse hooks
	if b.pluginManager != nil {
		if err := b.pluginManager.OnResponse(ctx, m, response); err != nil {
			logger.Warnf("Plugin error in OnResponse: %v", err)
		}
	}

	// Set cooldown
	b.SetCooldown(m.Author.ID, time.Duration(b.cfg().Discord.CooldownSeconds)*time.Second)

	// Increment channel message count and check for batch consolidation
	count, err := b.memory.IncrementChannelMessageCount(ctx, m.ChannelID)
	if err != nil {
		logger.Warnf("Failed to increment channel message count: %v", err)
	} else {
		b.triggerChannelConsolidation(ctx, m.ChannelID, count)
	}
}

// processMessage processes a message and generates a response
func (b *Bot) processMessage(ctx context.Context, s *discordgo.Session, m *discordgo.MessageCreate, pm *ProcessingMessage) {
	// Check if already cancelled before starting
	if err := ctx.Err(); err != nil {
		logger.Infof("[processing] Message %s cancelled before starting", m.ID)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	// Update phase to generating
	if pm != nil {
		pm.SetPhase(PhaseGenerating)
	}

	// Extract image URLs for the current message
	imageURLs := utils.ExtractImageURLs(m.Message)

	// For Hybrid mode, describe images now and cache descriptions to avoid redundant API calls later
	var imageDescriptions []string
	visionDescriber := b.getVisionDescriber()
	if b.cfg().AI.Vision.Mode == config.VisionModeHybrid && len(imageURLs) > 0 && visionDescriber != nil {
		descriptions, err := visionDescriber.DescribeImages(ctx, imageURLs)
		if err != nil {
			logger.Warnf("[process] failed to describe images for message %s: %v", m.ID, err)
		} else {
			imageDescriptions = descriptions
			b.setHistoricalImageDescriptions(m.ID, imageURLs, descriptions)
			logger.Debugf("[process] cached image descriptions for message %s count=%d", m.ID, len(descriptions))
		}
	}

	msg := &memory.DiscordMessage{
		ID:                m.ID,
		ChannelID:         m.ChannelID,
		GuildID:           m.GuildID,
		AuthorID:          m.Author.ID,
		Username:          m.Author.Username,
		Content:           m.Content,
		ImageURLs:         imageURLs,
		ImageDescriptions: imageDescriptions, // Cache descriptions for consolidation
		Timestamp:         m.Timestamp,
		IsBot:             m.Author.Bot,
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

	// Check cancellation before expensive operations
	if err := ctx.Err(); err != nil {
		logger.Infof("[processing] Message %s cancelled before guild lookup", m.ID)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	// Get guild info
	guild, err := b.GetGuild(m.GuildID)
	if err != nil {
		logger.Errorf("Failed to get guild: %v", err)
		b.clearProcessingMessage(pm, m.ID)
		return
	}
	guildName := guild.Name

	// Check cancellation before fetching messages
	if err := ctx.Err(); err != nil {
		logger.Infof("[processing] Message %s cancelled before fetching recent messages", m.ID)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	// Fetch recent messages from Discord for short-term context
	recentMessages, err := b.discordClient.FetchRecentMessages(m.ChannelID, b.cfg().Memory.ShortTermLimit)
	if err != nil {
		logger.Warnf("Failed to fetch recent messages: %v", err)
	}

	// Update the current message in recent messages with image info
	for i, recentMsg := range recentMessages {
		if recentMsg.ID == m.ID {
			recentMessages[i] = msg
			break
		}
	}

	// Check cancellation before memory search
	if err := ctx.Err(); err != nil {
		logger.Infof("[processing] Message %s cancelled before memory search", m.ID)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	var memories []*memory.Record
	if b.cfg().Memory.Retrieval.TopK > 0 {
		memories, err = b.memory.Search(ctx, m.Author.ID, m.Content, nil)
		if err != nil {
			logger.Warnf("Failed to search memories: %v", err)
		} else if len(memories) > 0 {
			logger.Debugf("[memory] search found %d memories for user=%s query=%q", len(memories), m.Author.ID, m.Content)
		}
	}

	// Check cancellation before profile fetch
	if err := ctx.Err(); err != nil {
		logger.Infof("[processing] Message %s cancelled before profile fetch", m.ID)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	// Get user profile
	profile, err := b.memory.GetProfile(ctx, m.Author.ID)
	if err != nil {
		logger.Warnf("Failed to get profile: %v", err)
		profile = &memory.Profile{UserID: m.Author.ID}
	}
	profile.DisplayName = m.Author.Username

	// Start typing indicator before LLM processing
	s.ChannelTyping(m.ChannelID)
	// Maintain typing for long LLM responses
	typingCtx, cancelTyping := context.WithCancel(ctx)
	go maintainTyping(typingCtx, s, m.ChannelID)
	defer cancelTyping()

	// Generate response
	response, err := b.generateResponse(ctx, m, guildName, imageURLs, imageDescriptions, recentMessages, memories, profile)
	if err != nil {
		// Check if error is due to context cancellation
		if ctx.Err() == context.Canceled {
			logger.Infof("[processing] Message %s generation cancelled", m.ID)
		} else {
			logger.Errorf("Failed to generate response: %v", err)
			if shouldSendGenerationFallback(err) {
				b.addGenerationFailureReaction(s, m)
			}
		}
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	if response == "" {
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	// Update phase to sending
	if pm != nil {
		pm.SetPhase(PhaseSending)
	}

	// Send response
	if err := b.sendResponse(ctx, s, m, response); err != nil {
		logger.Errorf("Failed to send response: %v", err)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	// Remove from processing after sending
	b.clearProcessingMessage(pm, m.ID)

	// Run plugin OnResponse hooks
	if b.pluginManager != nil {
		if err := b.pluginManager.OnResponse(ctx, m, response); err != nil {
			logger.Warnf("Plugin error in OnResponse: %v", err)
		}
	}

	// Set cooldown
	b.SetCooldown(m.Author.ID, time.Duration(b.cfg().Discord.CooldownSeconds)*time.Second)

	// Increment channel message count and check for batch consolidation
	count, err := b.memory.IncrementChannelMessageCount(ctx, m.ChannelID)
	if err != nil {
		logger.Warnf("Failed to increment channel message count: %v", err)
	} else {
		b.triggerChannelConsolidation(ctx, m.ChannelID, count)
	}
}
