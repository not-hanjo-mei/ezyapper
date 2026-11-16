// Package bot provides Discord bot event handlers
package bot

import (
	"context"
	"slices"
	"time"

	"ezyapper/internal/ai"
	"ezyapper/internal/discordvis"
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
	// InjectedContext holds prompt blocks plugins added in after_decision.
	InjectedContext []plugin.ContextBlock
}

// GenerateContext carries what the process path already resolved for the
// reply.
type GenerateContext struct {
	// Current is the message being answered with its media resolved
	// (media.CurrentPolicy), done once per reply.
	Current *types.DiscordMessage
}

// extractReplyInfo extracts reply-to username and content from the message reference.
func extractReplyInfo(m *discordgo.MessageCreate) (username string, content string) {
	if m.MessageReference == nil {
		return "", ""
	}
	if m.ReferencedMessage != nil && m.ReferencedMessage.Author != nil {
		username = m.ReferencedMessage.Author.Username
		content = types.QuoteText(m.ReferencedMessage)
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
	if b.sess() != nil && b.sess().State != nil && b.sess().State.User != nil {
		botID = b.sess().State.User.ID
	}

	// Content stays the raw text; embeds and media go on the attachments
	// line (renderMessageLine).
	current := types.DiscordMessage{ID: mc.MessageID, ChannelID: mc.ChannelID, GuildID: mc.GuildID, AuthorID: mc.UserID, Username: mc.Username, DisplayName: mc.DisplayName, Content: mc.UserContent}
	if gc.Current != nil {
		current = *gc.Current
	}

	// Keep the media this prompt shows cached while it stays in context.
	b.pinMediaWindow(mc.ChannelID, append(slices.Clone(recentMessages), &current)...)

	// Historical media: cached descriptions/transcripts, plus bounded
	// on-demand processing of the media message the user asked about.
	history := b.mediaProcessor().Resolve(ctx, withoutMessage(b.filterMessages(recentMessages), mc.MessageID), media.HistoryPolicy(mc.UserContent, mc.ReplyToID))

	in := b.basePromptInput(mc, botID)
	in.History = history
	in.Current = &current
	in.MentionedUsers = mc.Mentions
	in.PluginBlocks = mc.InjectedContext
	in.Profile = profile
	in.Memories = b.renderMemories(memories)
	userPrompt := buildUserPrompt(in)

	logger.Debugf("[prompt] system prompt: %d chars (static), user prompt: %d chars", len(systemPrompt), len(userPrompt))

	// The reply request is text only: media was turned into text above.
	req := ai.ChatCompletionRequest{SystemPrompt: systemPrompt, Messages: []openai.ChatCompletionMessage{}}
	response, err := b.completeTextWithTools(ctx, mc.AIClient, req, userPrompt)
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
	if b.sess() == nil || b.sess().State == nil {
		return in
	}
	perms := discordvis.StatePermissions(b.sess().State, botID)
	if g, err := b.sess().State.Guild(mc.GuildID); err == nil && g != nil {
		if in.GuildName == "" {
			in.GuildName = g.Name
		}
		in.GuildDescription = g.Description
		addVisibleChannelNames(in.ChannelNames, g.Channels, perms)
		addVisibleChannelNames(in.ChannelNames, g.Threads, perms)
	}
	if ch, err := b.sess().State.Channel(mc.ChannelID); err == nil && ch != nil {
		// The bot normally sees the channel it answers in; if Discord sent it
		// obfuscated, show neither the placeholder name nor the topic.
		if discordvis.Visible(ch, perms) {
			in.ChannelName = ch.Name
			in.ChannelTopic = ch.Topic
		} else {
			in.ChannelName = discordvis.UnknownChannelName
		}
	}
	return in
}

// addVisibleChannelNames records the names of the channels the bot can view.
// Hidden channels are left out, so their <#id> mentions render as
// #unknown-channel.
func addVisibleChannelNames(dst map[string]string, channels []*discordgo.Channel, perms discordvis.PermissionFunc) {
	for _, ch := range channels {
		if name, ok := discordvis.Name(ch, perms); ok {
			dst[ch.ID] = name
		}
	}
}

// renderMemories renders retrieved memories as <key_facts>/<recent_memories>.
func (b *Bot) renderMemories(memories []*memory.Record) string {
	if len(memories) == 0 {
		return ""
	}
	cfg := b.cfg().Memory
	logger.Debugf("[memory] adding %d memories to the prompt", len(memories))
	return memory.FormatMemoriesForContext(memories, cfg.MemoryStrengthMultiplier, time.Now(), cfg.Retrieval.MentionedLimit(), cfg.Retrieval.ChannelLimit())
}

// withoutMessage returns msgs without the message id (the current message
// is not history).
func withoutMessage(msgs []*types.DiscordMessage, id string) []*types.DiscordMessage {
	return slices.DeleteFunc(slices.Clone(msgs), func(m *types.DiscordMessage) bool { return m == nil || m.ID == id })
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
