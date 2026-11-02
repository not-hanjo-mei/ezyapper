// Package bot provides Discord bot event handlers
package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ezyapper/internal/ai"
	"ezyapper/internal/logger"
	"ezyapper/internal/media"
	"ezyapper/internal/memory"
	"ezyapper/internal/plugin"
	"ezyapper/internal/types"

	"github.com/bwmarrin/discordgo"
	openai "github.com/sashabaranov/go-openai"
)

// ModeContext bundles user/session context for response generation modes.
type ModeContext struct {
	AIClient        *ai.Client
	UserContent     string
	Username        string
	UserID          string
	DisplayName     string
	ReplyToID       string
	ReplyToUsername string
	ReplyToContent  string
	GuildID         string
	ChannelID       string
	MessageID       string
	GuildName       string
	Mentions        []*discordgo.User
	// Current is the message being answered, in canonical form.
	Current types.DiscordMessage
	// InjectedContext holds prompt blocks plugins added in after_decision.
	InjectedContext []plugin.ContextBlock
}

// GenerateContext bundles request and image parameters for response generation.
type GenerateContext struct {
	ImageURLs         []string
	ImageDescriptions []string
}

// extractReplyInfo extracts reply-to username and content from the message reference.
func extractReplyInfo(m *discordgo.MessageCreate) (username string, content string) {
	if m.MessageReference == nil {
		return "", ""
	}
	if m.ReferencedMessage != nil && m.ReferencedMessage.Author != nil {
		username = m.ReferencedMessage.Author.Username
		content = m.ReferencedMessage.Content
		return
	}
	return "(deleted message)", ""
}

// generateResponse generates an AI response for a message.
func (b *Bot) generateResponse(ctx context.Context, mc ModeContext, gc GenerateContext, recentMessages []*types.DiscordMessage, memories []*memory.Record, profile *memory.Profile) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	systemPrompt := b.systemPrompt()

	var botID string
	if b.session != nil && b.session.State != nil && b.session.State.User != nil {
		botID = b.session.State.User.ID
	}

	// Historical media: cached descriptions/transcripts, plus bounded
	// on-demand enrichment of the media message the user replied to.
	history := b.enrichHistory(ctx, b.filterMessages(recentMessages), mc.MessageID, planRecentImageEnrichment(mc.UserContent, mc.ReplyToID))

	current := mc.Current
	current.Content = mc.UserContent
	current.Embeds = nil // already folded into UserContent

	content, parts := b.annotateCurrent(ctx, current, gc)
	current.Content = content

	in := b.basePromptInput(mc, botID)
	in.History = history
	in.Current = &current
	in.MentionedUsers = mc.Mentions
	in.PluginBlocks = mc.InjectedContext
	in.Profile = profile
	in.Memories = b.renderMemories(memories)
	userPrompt := buildUserPrompt(in)

	logger.Debugf("[prompt] system prompt: %d chars (static), user prompt: %d chars", len(systemPrompt), len(userPrompt))

	client := mc.AIClient
	if p := b.mediaProcessor(); p != nil {
		client = p.ReplyClient(mc.AIClient, len(parts) > 0)
	}
	req := ai.ChatCompletionRequest{SystemPrompt: systemPrompt, Messages: []openai.ChatCompletionMessage{}, MediaParts: parts}
	response, err := b.completeTextWithTools(ctx, client, req, userPrompt)
	if err != nil {
		return "", err
	}
	// The prompt shows mentions as <@DisplayName>; turn the ones the model
	// copied back into real pings.
	return mentionsToIDs(response, collectParticipants(in)), nil
}

// systemPrompt returns the static system prompt: the configured instructions
// plus static plugin sections. It is byte-identical across requests.
func (b *Bot) systemPrompt() string {
	var blocks []plugin.ContextBlock
	if b.pluginManager != nil {
		blocks = b.pluginManager.SystemPromptBlocks()
	}
	return SystemPromptFor(b.cfg().AI.SystemPrompt, blocks)
}

// basePromptInput fills server/channel info and channel names from the
// session state cache.
func (b *Bot) basePromptInput(mc ModeContext, botID string) promptInput {
	in := promptInput{GuildID: mc.GuildID, GuildName: mc.GuildName, ChannelID: mc.ChannelID, BotID: botID, ChannelNames: map[string]string{}}
	if b.session == nil || b.session.State == nil {
		return in
	}
	if g, err := b.session.State.Guild(mc.GuildID); err == nil && g != nil {
		if in.GuildName == "" {
			in.GuildName = g.Name
		}
		in.GuildDescription = g.Description
		for _, ch := range g.Channels {
			in.ChannelNames[ch.ID] = ch.Name
		}
	}
	if ch, err := b.session.State.Channel(mc.ChannelID); err == nil && ch != nil {
		in.ChannelName = ch.Name
		in.ChannelTopic = ch.Topic
	}
	return in
}

// renderMemories renders retrieved memories as <key_facts>/<recent_memories>.
func (b *Bot) renderMemories(memories []*memory.Record) string {
	if len(memories) == 0 {
		return ""
	}
	cfg := b.cfg().Memory
	logger.Debugf("[memory] adding %d memories to the prompt", len(memories))
	return memory.FormatMemoriesForContext(memories, cfg.MemoryStrengthMultiplier, time.Now(), cfg.Retrieval.MaxMentionedMemories, cfg.Retrieval.MaxChannelMemories)
}

// annotateCurrent applies the enabled modalities to the message being
// answered. It returns the content with description/transcript lines added
// and the media parts to send in direct modes.
func (b *Bot) annotateCurrent(ctx context.Context, current types.DiscordMessage, gc GenerateContext) (string, []ai.MediaPart) {
	content := current.Content
	p := b.mediaProcessor()
	if p == nil {
		return content, nil
	}
	var parts []ai.MediaPart
	switch {
	case p.ImageDescribeEnabled():
		content += b.describeCurrentImages(ctx, gc)
	case p.ImageDirectEnabled():
		// A marker per attached image tells the model which message the
		// media parts belong to (they follow the whole prompt text).
		for i, u := range gc.ImageURLs {
			part, err := p.ImagePart(ctx, u)
			if err != nil {
				logger.Warnf("[media] image not sent: %v", err)
				content += fmt.Sprintf("\n[Image %d: not processed: %v]", i+1, err)
				continue
			}
			parts = append(parts, part)
			content += fmt.Sprintf("\n[Image %d: attached]", i+1)
		}
	}
	content += p.AnnotateStickers(ctx, &current, true)
	ann := p.AnnotateAV(ctx, &current, media.AnnotateOptions{Current: true})
	content += ann.Text
	parts = append(parts, ann.Parts...)
	if len(parts) > 0 {
		logger.Infof("[media] sending %d media part(s) directly with the reply request", len(parts))
	}
	return content, parts
}

// describeCurrentImages returns " [Image N: description]" suffixes for the
// current message's images (describe mode), using cached descriptions first.
func (b *Bot) describeCurrentImages(ctx context.Context, gc GenerateContext) string {
	if len(gc.ImageURLs) == 0 {
		return ""
	}
	descriptions := gc.ImageDescriptions
	if len(descriptions) == 0 {
		describer := b.imageDescriber()
		if describer == nil {
			logger.Warnf("[media] image describer unavailable, continuing without image descriptions")
			return ""
		}
		var err error
		descriptions, err = describer.DescribeImages(ctx, gc.ImageURLs)
		if err != nil {
			logger.Warnf("[response] Failed to describe images: %v", err)
			return ""
		}
	}
	var sb strings.Builder
	for i, d := range descriptions {
		fmt.Fprintf(&sb, " [Image %d: %s]", i+1, d)
	}
	return sb.String()
}

// completeTextWithTools completes chat with tool support.
func (b *Bot) completeTextWithTools(ctx context.Context, aiClient *ai.Client, req ai.ChatCompletionRequest, content string) (string, error) {
	req.Messages = append(req.Messages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: content,
	})
	req.UserContext = ""

	resp, err := aiClient.CreateChatCompletionWithTools(ctx, req, b.createToolHandler())
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}
