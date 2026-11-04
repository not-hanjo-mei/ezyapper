package bot

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"ezyapper/internal/ai"
	"ezyapper/internal/logger"
	"ezyapper/internal/media"
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
	if b.cancelledBefore(ctx, pm, m.ID, "starting") {
		return
	}

	sessionCtx, cancel := context.WithTimeout(ctx, calculateSessionTimeout(b.cfg()))
	defer cancel()
	ctx = sessionCtx

	if pm != nil {
		pm.SetPhase(PhaseGenerating)
	}

	msg, imageURLs, imageDescriptions := b.extractMessageImages(ctx, m, withImages)

	if b.cancelledBefore(ctx, pm, m.ID, "guild lookup") {
		return
	}

	guild, err := b.GetGuild(m.GuildID)
	if err != nil {
		logger.Warnf("[processing] failed to get guild %s: %v", m.GuildID, err)
		b.clearProcessingMessage(pm, m.ID)
		return
	}
	guildName := guild.Name

	if b.cancelledBefore(ctx, pm, m.ID, "fetching recent messages") {
		return
	}

	recentMessages = b.fetchRecentMessagesIfEmpty(ctx, m.ChannelID, recentMessages)

	if withImages {
		for i, recentMsg := range recentMessages {
			if recentMsg.ID == m.ID {
				recentMessages[i] = msg
				break
			}
		}
	}

	if b.cancelledBefore(ctx, pm, m.ID, "memory search") {
		return
	}

	memories := b.searchMemories(ctx, m, recentMessages, b.currentMediaText(ctx, m, imageDescriptions))

	if b.cancelledBefore(ctx, pm, m.ID, "profile fetch") {
		return
	}

	profile, displayName := b.fetchProfileAndDisplayName(ctx, m)

	s.ChannelTyping(m.ChannelID)
	typingCtx, cancelTyping := context.WithCancel(ctx)
	b.wg.Go(func() {
		maintainTyping(typingCtx, s, m.ChannelID, b.cfg().Discord.TypingIndicatorIntervalSec)
	})
	defer cancelTyping()

	mc, gc := b.buildModeContexts(m, pm, displayName, guildName, imageURLs, imageDescriptions)

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

	if b.cancelledBefore(ctx, pm, m.ID, "sending") {
		return
	}

	outcome, err := b.sendResponse(ctx, s, m, response)
	if err != nil {
		logger.Errorf("[processing] failed to send response for message %s: %v", m.ID, err)
		b.clearProcessingMessage(pm, m.ID)
		return
	}

	b.clearProcessingMessage(pm, m.ID)

	if outcome.Skipped {
		return
	}

	if b.pluginManager != nil {
		b.pluginManager.AfterSend(ctx, types.FromDiscordgo(m), outcome.Text, outcome.MessageIDs)
	}

	b.SetCooldown(m.Author.ID, time.Duration(b.cfg().Discord.CooldownSeconds)*time.Second)
}

func (b *Bot) cancelledBefore(ctx context.Context, pm *ProcessingMessage, msgID, phase string) bool {
	if err := ctx.Err(); err != nil {
		logger.Infof("[processing] Message %s cancelled before %s", msgID, phase)
		b.clearProcessingMessage(pm, msgID)
		return true
	}
	return false
}

func (b *Bot) extractMessageImages(ctx context.Context, m *discordgo.MessageCreate, withImages bool) (msg *types.DiscordMessage, imageURLs []string, imageDescriptions []string) {
	imageURLs = make([]string, 0)
	imageDescriptions = make([]string, 0)

	if withImages {
		img := b.cfg().AI.Modalities.Image
		imageURLs = extractImageURLs(m.Message, img.MaxItems)

		describer := b.imageDescriber()
		if img.Enabled && len(imageURLs) > 0 && describer != nil {
			descriptions, err := describer.DescribeImages(ctx, imageURLs)
			if err != nil {
				logger.Warnf("[process] failed to describe images for message %s: %v", m.ID, err)
			} else {
				// DescribeImages cached them by image key.
				imageDescriptions = descriptions
			}
		}

		msg = &types.DiscordMessage{
			ID:                m.ID,
			ChannelID:         m.ChannelID,
			GuildID:           m.GuildID,
			AuthorID:          m.Author.ID,
			Username:          m.Author.Username,
			Content:           m.Content,
			Embeds:            types.SummarizeEmbeds(m.Embeds),
			Kinds:             types.MessageKinds(m.Message),
			ImageURLs:         imageURLs,
			ImageDescriptions: imageDescriptions,
			Media:             types.ExtractMedia(m.Message),
			Stickers:          types.ExtractStickers(m.Message),
			Timestamp:         m.Timestamp,
			IsBot:             m.Author.Bot,
		}

		if m.MessageReference != nil {
			msg.ReplyToID = m.MessageReference.MessageID
			if m.ReferencedMessage != nil && m.ReferencedMessage.Author != nil {
				msg.ReplyToUsername = m.ReferencedMessage.Author.Username
				msg.ReplyToAuthorID = m.ReferencedMessage.Author.ID
				msg.ReplyToContent = types.QuoteText(m.ReferencedMessage)
			} else {
				msg.ReplyToUsername = "(deleted message)"
			}
		}
	}

	return msg, imageURLs, imageDescriptions
}

func (b *Bot) fetchRecentMessagesIfEmpty(ctx context.Context, channelID string, recentMessages []*types.DiscordMessage) []*types.DiscordMessage {
	if len(recentMessages) == 0 {
		var fetchErr error
		recentMessages, fetchErr = b.fetchContextMessages(ctx, channelID, b.cfg().Memory.ShortTermLimit)
		if fetchErr != nil {
			logger.Warnf("[processing] Failed to fetch recent messages: %v", fetchErr)
		}
	}
	return recentMessages
}

// currentMediaText returns the text the enabled modalities turn the media of
// the message being answered into: image descriptions, sticker text, audio
// transcripts and video descriptions. Results are cached by attachment (or
// sticker) ID, so the reply prompt built later reuses them.
func (b *Bot) currentMediaText(ctx context.Context, m *discordgo.MessageCreate, imageDescriptions []string) string {
	var sb strings.Builder
	for i, d := range imageDescriptions {
		fmt.Fprintf(&sb, "\n[Image %d: %s]", i+1, d)
	}
	if p := b.mediaProcessor(); p != nil && m != nil && m.Message != nil && m.Author != nil {
		cur := types.FromDiscordgo(m)
		sb.WriteString(p.AnnotateStickers(ctx, &cur, true))
		sb.WriteString(p.AnnotateAV(ctx, &cur, media.AnnotateOptions{Current: true}).Text)
	}
	return sb.String()
}

// memorySearchQuery builds the memory search query for the message being
// answered: its content and recent context, plus the text of its media. A
// voice message has no content, so without its transcript the query could be
// empty, and embedding "" fails (400). An empty result means: skip the search.
func memorySearchQuery(content, mediaText string, recentMessages []*types.DiscordMessage, ownBotID string) string {
	query, _ := memory.BuildSearchQuery(content, recentMessages, ownBotID)
	if t := strings.TrimSpace(mediaText); t != "" {
		query = strings.TrimSpace(query + "\n" + t)
	}
	return strings.TrimSpace(query)
}

func (b *Bot) searchMemories(ctx context.Context, m *discordgo.MessageCreate, recentMessages []*types.DiscordMessage, mediaText string) []*memory.Record {
	memories := []*memory.Record{}
	if b.cfg().Memory.Retrieval.TopK > 0 {
		query := memorySearchQuery(m.Content, mediaText, recentMessages, b.cfg().Discord.OwnBotID)
		if query == "" {
			logger.Debugf("[processing] message %s has no text to search memories with, user memory search skipped", m.ID)
		}

		var wg sync.WaitGroup
		var userMemories, mentionedMemories, channelMemories []*memory.Record

		// 1. User memories (always)
		wg.Go(func() {
			if query == "" {
				return
			}
			mems, searchErr := b.memoryStore.Search(ctx, m.Author.ID, query, nil)
			if searchErr != nil {
				logger.Warnf("[processing] Failed to search user memories: %v", searchErr)
			}
			userMemories = mems
		})

		// 2. Mentioned memories (if mentions exist)
		if len(m.Mentions) > 0 {
			wg.Go(func() {
				maxMentioned := b.cfg().Memory.Retrieval.MentionedLimit()
				if maxMentioned <= 0 {
					return // max_mentioned_memories: 0 = none
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
				maxChannel := b.cfg().Memory.Retrieval.ChannelLimit()
				if maxChannel <= 0 {
					return // max_channel_memories: 0 = none
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
	return memories
}

func (b *Bot) fetchProfileAndDisplayName(ctx context.Context, m *discordgo.MessageCreate) (profile *memory.Profile, displayName string) {
	profile, err := b.profileStore.GetProfile(ctx, m.Author.ID)
	if err != nil {
		logger.Warnf("[processing] Failed to get profile: %v", err)
		profile = nil
	}
	displayName = m.Author.GlobalName
	if m.Member != nil && m.Member.Nick != "" {
		displayName = m.Member.Nick
	}
	if displayName == "" {
		displayName = m.Author.Username
	}
	if profile != nil {
		profile.DisplayName = displayName
	}
	return profile, displayName
}

func (b *Bot) buildModeContexts(m *discordgo.MessageCreate, pm *ProcessingMessage, displayName, guildName string, imageURLs, imageDescriptions []string) (ModeContext, GenerateContext) {
	replyToUsername, replyToContent := extractReplyInfo(m)
	replyToID := ""
	if m.MessageReference != nil {
		replyToID = m.MessageReference.MessageID
	}
	mc := ModeContext{
		AIClient:        ai.NewClient(&b.cfg().AI, b.toolRegistry),
		UserContent:     types.JoinContentAndEmbeds(m.Content, types.SummarizeEmbeds(m.Embeds)),
		Username:        m.Author.Username,
		UserID:          m.Author.ID,
		DisplayName:     displayName,
		ReplyToID:       replyToID,
		ReplyToUsername: replyToUsername,
		ReplyToContent:  replyToContent,
		GuildID:         m.GuildID,
		ChannelID:       m.ChannelID,
		MessageID:       m.ID,
		GuildName:       guildName,
		Mentions:        m.Mentions,
		Current:         types.FromDiscordgo(m),
	}
	if pm != nil {
		pm.mu.RLock()
		mc.InjectedContext = pm.InjectedContext
		pm.mu.RUnlock()
	}
	gc := GenerateContext{
		ImageURLs:         imageURLs,
		ImageDescriptions: imageDescriptions,
	}
	return mc, gc
}

func recoverHandler() {
	if r := recover(); r != nil {
		logger.Errorf("[processing] panic recovered: %v", r)
	}
}
