package bot

import (
	"context"
	"sync"
	"time"

	"ezyapper/internal/ai"
	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/memory"
	"ezyapper/internal/types"

	"github.com/bwmarrin/discordgo"
)

func (b *Bot) processMessageWithoutImages(ctx context.Context, s *discordgo.Session, m *discordgo.MessageCreate, pm *ProcessingMessage, recentMessages []*types.DiscordMessage) {
	defer b.wg.Done()
	defer recoverHandler()
	b.processMessageCore(ctx, s, m, pm, false, recentMessages)
}

func (b *Bot) processMessage(ctx context.Context, s *discordgo.Session, m *discordgo.MessageCreate, pm *ProcessingMessage, recentMessages []*types.DiscordMessage) {
	defer b.wg.Done()
	defer recoverHandler()
	b.processMessageCore(ctx, s, m, pm, true, recentMessages)
}

func (b *Bot) processMessageCore(ctx context.Context, s *discordgo.Session, m *discordgo.MessageCreate, pm *ProcessingMessage, withImages bool, recentMessages []*types.DiscordMessage) {
	if err := ctx.Err(); err != nil {
		logger.Infof("[processing] Message %s cancelled before starting", m.ID)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	sessionCtx, cancel := context.WithTimeout(ctx, calculateSessionTimeout(b.cfg()))
	defer cancel()
	ctx = sessionCtx

	if pm != nil {
		pm.SetPhase(PhaseGenerating)
	}

	imageURLs := make([]string, 0)
	imageDescriptions := make([]string, 0)
	var msg *types.DiscordMessage

	if withImages {
		imageURLs = extractImageURLs(m.Message, b.cfg().AI.Vision.MaxImages)

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
		logger.Warnf("[processing] failed to get guild %s: %v", m.GuildID, err)
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
			logger.Warnf("[processing] Failed to fetch recent messages: %v", fetchErr)
		}
	}

	if withImages {
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

	memories := []*memory.Record{}
	if b.cfg().Memory.Retrieval.TopK > 0 {
		query, _ := memory.BuildSearchQuery(m.Content, recentMessages, b.cfg().Discord.OwnBotID)

		var wg sync.WaitGroup
		var userMemories, mentionedMemories, channelMemories []*memory.Record

		// 1. User memories (always)
		wg.Go(func() {
			mems, searchErr := b.memoryStore.Search(ctx, m.Author.ID, query, nil)
			if searchErr != nil {
				logger.Warnf("[processing] Failed to search user memories: %v", searchErr)
			}
			userMemories = mems
		})

		// 2. Mentioned memories (if mentions exist)
		if len(m.Mentions) > 0 {
			wg.Go(func() {
				maxMentioned := b.cfg().Memory.Retrieval.MaxMentionedMemories
				if maxMentioned <= 0 {
					maxMentioned = b.cfg().Memory.Retrieval.TopK
				}
				opts := &memory.SearchOptions{TopK: maxMentioned}
				for _, mention := range m.Mentions {
					if mention.ID == m.Author.ID {
						continue
					}
					mems, searchErr := b.memoryStore.SearchByMentionedUser(ctx, m.Author.ID, mention.ID, opts)
					if searchErr != nil {
						logger.Warnf("[processing] Failed to search mentioned user memories for %s: %v", mention.ID, searchErr)
						continue
					}
					mentionedMemories = append(mentionedMemories, mems...)
				}
			})
		}

		// 3. Channel memories (if enabled in config)
		if b.cfg().Memory.Retrieval.IncludeChannelMemories {
			wg.Go(func() {
				maxChannel := b.cfg().Memory.Retrieval.MaxChannelMemories
				if maxChannel <= 0 {
					maxChannel = b.cfg().Memory.Retrieval.TopK
				}
				opts := &memory.SearchOptions{TopK: maxChannel}
				mems, searchErr := b.memoryStore.SearchByChannel(ctx, m.ChannelID, opts)
				if searchErr != nil {
					logger.Warnf("[processing] Failed to search channel memories: %v", searchErr)
				}
				channelMemories = mems
			})
		}

		wg.Wait()

		// Merge all three result sets
		memories = make([]*memory.Record, 0, len(userMemories)+len(mentionedMemories)+len(channelMemories))
		memories = append(memories, userMemories...)
		memories = append(memories, mentionedMemories...)
		memories = append(memories, channelMemories...)

		// Deduplicate by memory ID
		if len(memories) > 1 {
			seen := make(map[string]bool, len(memories))
			deduped := make([]*memory.Record, 0, len(memories))
			for _, mem := range memories {
				if mem.ID == "" || seen[mem.ID] {
					continue
				}
				seen[mem.ID] = true
				deduped = append(deduped, mem)
			}
			memories = deduped
		}

		// Apply PostProcessResults after merging ALL result sets
		if len(memories) > 0 {
			multiplier := b.cfg().Memory.MemoryStrengthMultiplier
			mws := memory.ScoringWeights{
				Importance: b.cfg().Memory.Scoring.ImportanceWeight,
				Recency:    b.cfg().Memory.Scoring.RecencyWeight,
				Access:     b.cfg().Memory.Scoring.AccessWeight,
				Confidence: b.cfg().Memory.Scoring.ConfidenceWeight,
			}
			memories = memory.PostProcessResults(memories, multiplier, time.Now(), mws)
		}

		if len(memories) > 0 {
			logger.Debugf("[memory] parallel search: user=%d mentioned=%d channel=%d deduped=%d query=%q",
				len(userMemories), len(mentionedMemories), len(channelMemories), len(memories), m.Content)
		}
	}

	if err := ctx.Err(); err != nil {
		logger.Infof("[processing] Message %s cancelled before profile fetch", m.ID)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	profile, err := b.profileStore.GetProfile(ctx, m.Author.ID)
	if err != nil {
		logger.Warnf("[processing] Failed to get profile: %v", err)
		profile = nil
	}
	displayName := m.Author.GlobalName
	if displayName == "" {
		displayName = m.Author.Username
	}
	if profile != nil {
		profile.DisplayName = displayName
	}

	s.ChannelTyping(m.ChannelID)
	typingCtx, cancelTyping := context.WithCancel(ctx)
	b.wg.Go(func() {
		maintainTyping(typingCtx, s, m.ChannelID, b.cfg().Discord.TypingIndicatorIntervalSec)
	})
	defer cancelTyping()

	replyToUsername, replyToContent := extractReplyInfo(m)

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
			logger.Errorf("[processing] Failed to generate response: %v", err)
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
		logger.Errorf("[processing] failed to send response for message %s: %v", m.ID, err)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	b.clearProcessingMessage(pm, m.ID)

	if b.pluginManager != nil {
		dm := types.FromDiscordgo(m)
		if err := b.pluginManager.OnResponse(ctx, dm, response); err != nil {
			logger.Warnf("[processing] Plugin error in OnResponse: %v", err)
		}
	}

	b.SetCooldown(m.Author.ID, time.Duration(b.cfg().Discord.CooldownSeconds)*time.Second)
}

func recoverHandler() {
	if r := recover(); r != nil {
		logger.Errorf("[processing] panic recovered: %v", r)
	}
}
