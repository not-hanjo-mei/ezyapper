package bot

import (
	"context"
	"time"

	"ezyapper/internal/ai"
	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/memory"
	"ezyapper/internal/types"

	"github.com/bwmarrin/discordgo"
)

// processMessageWithoutImages processes a message without image handling (text-only mode).
func (b *Bot) processMessageWithoutImages(ctx context.Context, s *discordgo.Session, m *discordgo.MessageCreate, pm *ProcessingMessage, recentMessages []*types.DiscordMessage) {
	defer b.wg.Done()
	b.processMessageCore(ctx, s, m, pm, false, recentMessages)
}

// processMessage processes a message and generates a response (with image handling).
func (b *Bot) processMessage(ctx context.Context, s *discordgo.Session, m *discordgo.MessageCreate, pm *ProcessingMessage, recentMessages []*types.DiscordMessage) {
	defer b.wg.Done()
	b.processMessageCore(ctx, s, m, pm, true, recentMessages)
}

// processMessageCore is the shared processing pipeline used by both text-only and
// image-capable paths. When withImages is false, image extraction and the
// image-enriched DiscordMessage struct are skipped.
func (b *Bot) processMessageCore(ctx context.Context, s *discordgo.Session, m *discordgo.MessageCreate, pm *ProcessingMessage, withImages bool, recentMessages []*types.DiscordMessage) {
	if err := ctx.Err(); err != nil {
		logger.Infof("[processing] Message %s cancelled before starting", m.ID)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	if pm != nil {
		pm.SetPhase(PhaseGenerating)
	}

	var imageURLs []string
	var imageDescriptions []string
	var msg *types.DiscordMessage

	if withImages {
		imageURLs = extractImageURLs(m.Message)

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

		msg = &types.DiscordMessage{
			ID:                m.ID,
			ChannelID:         m.ChannelID,
			GuildID:           m.GuildID,
			AuthorID:          m.Author.ID,
			Username:          m.Author.Username,
			Content:           m.Content,
			ImageURLs:         imageURLs,
			ImageDescriptions: imageDescriptions,
			Timestamp:         m.Timestamp,
			IsBot:             m.Author.Bot,
		}

		if m.MessageReference != nil {
			msg.ReplyToID = m.MessageReference.MessageID
			if m.ReferencedMessage != nil && m.ReferencedMessage.Author != nil {
				msg.ReplyToUsername = m.ReferencedMessage.Author.Username
				content := m.ReferencedMessage.Content
				if len(content) > b.cfg().Discord.ReplyTruncationLength {
					logger.Warnf("[processing] reply content truncated from %d to %d chars", len(content), b.cfg().Discord.ReplyTruncationLength)
					content = content[:b.cfg().Discord.ReplyTruncationLength]
				}
				msg.ReplyToContent = content
			} else {
				msg.ReplyToUsername = "(deleted message)"
			}
		}
	}

	if err := ctx.Err(); err != nil {
		logger.Infof("[processing] Message %s cancelled before guild lookup", m.ID)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	guild, err := b.GetGuild(m.GuildID)
	if err != nil {
		logger.Errorf("Failed to get guild: %v", err)
		b.clearProcessingMessage(pm, m.ID)
		return
	}
	guildName := guild.Name

	if err := ctx.Err(); err != nil {
		logger.Infof("[processing] Message %s cancelled before fetching recent messages", m.ID)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	if len(recentMessages) == 0 {
		var fetchErr error
		recentMessages, fetchErr = b.discordClient.FetchRecentMessages(ctx, m.ChannelID, b.cfg().Memory.ShortTermLimit)
		if fetchErr != nil {
			logger.Warnf("Failed to fetch recent messages: %v", fetchErr)
		}
	}

	if withImages && msg != nil {
		for i, recentMsg := range recentMessages {
			if recentMsg.ID == m.ID {
				recentMessages[i] = msg
				break
			}
		}
	}

	if err := ctx.Err(); err != nil {
		logger.Infof("[processing] Message %s cancelled before memory search", m.ID)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	var memories []*memory.Record
	if b.cfg().Memory.Retrieval.TopK > 0 {
		memories, err = b.memoryStore.Search(ctx, m.Author.ID, m.Content, nil)
		if err != nil {
			logger.Warnf("Failed to search memories: %v", err)
		} else if len(memories) > 0 {
			logger.Debugf("[memory] search found %d memories for user=%s query=%q", len(memories), m.Author.ID, m.Content)
		}
	}

	if err := ctx.Err(); err != nil {
		logger.Infof("[processing] Message %s cancelled before profile fetch", m.ID)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	profile, err := b.profileStore.GetProfile(ctx, m.Author.ID)
	if err != nil {
		logger.Warnf("Failed to get profile: %v", err)
		profile = &memory.Profile{UserID: m.Author.ID}
	}
	displayName := m.Author.GlobalName
	if displayName == "" {
		displayName = m.Author.Username
	}
	profile.DisplayName = displayName

	s.ChannelTyping(m.ChannelID)
	typingCtx, cancelTyping := context.WithCancel(ctx)
	go maintainTyping(typingCtx, s, m.ChannelID, b.cfg().Discord.TypingIndicatorIntervalSec)
	defer cancelTyping()

	replyToUsername, replyToContent := extractReplyInfo(m)
	if len(replyToContent) > b.cfg().Discord.ReplyTruncationLength {
		logger.Warnf("[processing] reply content truncated from %d to %d chars", len(replyToContent), b.cfg().Discord.ReplyTruncationLength)
		replyToContent = replyToContent[:b.cfg().Discord.ReplyTruncationLength]
	}

	mc := ModeContext{
		AIClient:        ai.NewClient(&b.cfg().AI, b.toolRegistry),
		UserContent:     m.Content,
		Username:        m.Author.Username,
		UserID:          m.Author.ID,
		DisplayName:     displayName,
		ReplyToUsername: replyToUsername,
		ReplyToContent:  replyToContent,
		GuildID:         m.GuildID,
		ChannelID:       m.ChannelID,
		MessageID:       m.ID,
		GuildName:       guildName,
		Mentions:        m.Mentions,
	}
	gc := GenerateContext{
		ImageURLs:         imageURLs,
		ImageDescriptions: imageDescriptions,
	}

	response, err := b.generateResponse(ctx, mc, gc, recentMessages, memories, profile)
	if err != nil {
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

	if pm != nil {
		pm.SetPhase(PhaseSending)
	}

	if err := ctx.Err(); err != nil {
		logger.Infof("[processing] Message %s cancelled before sending", m.ID)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	if err := b.sendResponse(ctx, s, m, response); err != nil {
		logger.Errorf("Failed to send response: %v", err)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	b.clearProcessingMessage(pm, m.ID)

	if b.pluginManager != nil {
		dm := types.FromDiscordgo(m)
		if err := b.pluginManager.OnResponse(ctx, dm, response); err != nil {
			logger.Warnf("Plugin error in OnResponse: %v", err)
		}
	}

	b.SetCooldown(m.Author.ID, time.Duration(b.cfg().Discord.CooldownSeconds)*time.Second)

	count, err := b.consolidation.IncrementChannelMessageCount(ctx, m.ChannelID)
	if err != nil {
		logger.Warnf("Failed to increment channel message count: %v", err)
	} else {
		b.triggerChannelConsolidation(ctx, m.ChannelID, count)
	}
}
