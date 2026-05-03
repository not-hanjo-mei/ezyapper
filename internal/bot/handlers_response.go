// Package bot provides Discord bot event handlers
package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ezyapper/internal/ai"
	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/memory"
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
	ReplyToUsername string
	ReplyToContent  string
	GuildID         string
	ChannelID       string
	MessageID       string
	GuildName       string
	Mentions        []*discordgo.User
}

// GenerateContext bundles request and image parameters for response generation.
type GenerateContext struct {
	Request           interface{}
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

// generateResponse generates an AI response for a message
func (b *Bot) generateResponse(ctx context.Context, mc ModeContext, gc GenerateContext, recentMessages []*types.DiscordMessage, memories []*memory.Record, profile *memory.Profile) (string, error) {
	// Check if context is cancelled before starting
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Build static system prompt (cacheable - does not change between requests)
	// This includes persona definition and mention guidelines
	systemPrompt := b.cfg().FormatSystemPrompt(mc.Username, mc.GuildName, mc.GuildID, mc.ChannelID)

	// Build dynamic user context (not cacheable - changes every request)
	// This is placed in the user message to preserve prompt caching of the system prompt
	dynamicContext := b.buildDynamicContext(mc.Username, profile, memories, recentMessages)

	logger.Debugf("[prompt] system prompt length: %d chars (static)", len(systemPrompt))
	logger.Debugf("[prompt] dynamic context length: %d chars", len(dynamicContext))

	// Get current bot ID
	var botID string
	if b.session != nil && b.session.State != nil && b.session.State.User != nil {
		botID = b.session.State.User.ID
	}

	// Build channel mappings from state cache for resolving <#ID> mentions
	channelMappings := []ChannelMapping{}
	if b.session != nil && b.session.State != nil {
		for _, guild := range b.session.State.Guilds {
			for _, ch := range guild.Channels {
				channelMappings = append(channelMappings, ChannelMapping{ID: ch.ID, Name: ch.Name})
			}
		}
	}

	// Build conversation history as formatted text to include in UserContext.
	// Keep historical image enrichment fast by default, but allow limited on-demand
	// enrichment when the user likely references recent images.
	conversationHistory := b.buildConversationHistoryText(
		ctx,
		recentMessages,
		mc.MessageID,
		botID,
		shouldEnrichRecentHistoricalImages(mc.UserContent, mc.ReplyToUsername != ""),
		mc.Mentions,
		channelMappings,
	)

	// Combine dynamic context with conversation history
	fullContext := dynamicContext
	if conversationHistory != "" {
		fullContext = conversationHistory + "\n\n" + dynamicContext
	}

	logger.Debugf("[prompt] system prompt length: %d chars (static)", len(systemPrompt))
	logger.Debugf("[prompt] full context length: %d chars", len(fullContext))

	// Build request - Messages will only contain current user message
	req := ai.ChatCompletionRequest{
		SystemPrompt: systemPrompt,
		Messages:     []openai.ChatCompletionMessage{},
		UserContext:  fullContext,
	}

	var response string
	var err error

	logger.Infof("[vision] using mode: %s", b.cfg().AI.Vision.Mode)

	switch b.cfg().AI.Vision.Mode {
	case config.VisionModeTextOnly:
		logger.Debugf("[vision] text-only mode - skipping image extraction")
		response, err = b.handleTextOnlyMode(ctx, mc, req)

	case config.VisionModeHybrid:
		logger.Debugf("[vision] hybrid mode - describing images with vision model, then using text model with tools")
		// Use cached image descriptions if available (from processMessage)
		response, err = b.handleHybridMode(ctx, mc, req, gc.ImageURLs, gc.ImageDescriptions)

	case config.VisionModeMultimodal:
		logger.Debugf("[vision] multimodal mode - using single model for both vision and tools")
		response, err = b.handleMultimodalMode(ctx, mc, req, gc.ImageURLs)

	default:
		return "", fmt.Errorf("unknown vision mode: %s", b.cfg().AI.Vision.Mode)
	}

	if err != nil {
		return "", err
	}

	return response, nil
}

// completeTextWithTools completes chat with tool support
func (b *Bot) completeTextWithTools(ctx context.Context, aiClient *ai.Client, req ai.ChatCompletionRequest, content string) (string, error) {
	req.Messages = append(req.Messages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: content,
	})
	req.UserContext = "" // Clear so CreateChatCompletion doesn't add it again

	resp, err := aiClient.CreateChatCompletionWithTools(ctx, req, b.createToolHandler())
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// handleTextOnlyMode handles text-only mode (no image processing)
func (b *Bot) handleTextOnlyMode(ctx context.Context, mc ModeContext, req ai.ChatCompletionRequest) (string, error) {
	// Build final content with XML formatting:
	// UserContext contains <context> + dynamic context
	// Current message wrapped in <currentMessage>
	var fullContent strings.Builder
	if req.UserContext != "" {
		fullContent.WriteString(req.UserContext)
		fullContent.WriteString("\n\n")
	}
	fullContent.WriteString("<currentMessage>\n")
	fullContent.WriteString(formatMessageXML(mc.DisplayName, mc.Username, mc.UserID, mc.UserContent, time.Now(), mc.ReplyToUsername, mc.ReplyToContent))
	fullContent.WriteString("\n</currentMessage>")

	return b.completeTextWithTools(ctx, mc.AIClient, req, fullContent.String())
}

// handleHybridMode handles hybrid mode (vision description + text model)
func (b *Bot) handleHybridMode(ctx context.Context, mc ModeContext, req ai.ChatCompletionRequest, imageURLs []string, cachedDescriptions []string) (string, error) {
	// Build full message content with XML formatting
	// UserContext contains <context> + dynamic context
	// Current message wrapped in <currentMessage>
	var fullContent strings.Builder
	if req.UserContext != "" {
		fullContent.WriteString(req.UserContext)
		fullContent.WriteString("\n\n")
	}
	fullContent.WriteString("<currentMessage>\n")

	// Build current message content (may include image descriptions)
	var currentMsgContent strings.Builder
	currentMsgContent.WriteString(mc.UserContent)

	if len(imageURLs) == 0 {
		// No images - format and send
		fullContent.WriteString(formatMessageXML(mc.DisplayName, mc.Username, mc.UserID, currentMsgContent.String(), time.Now(), mc.ReplyToUsername, mc.ReplyToContent))
		fullContent.WriteString("\n</currentMessage>")
		return b.completeTextWithTools(ctx, mc.AIClient, req, fullContent.String())
	}

	descriptions := []string{}
	if len(cachedDescriptions) > 0 {
		// Use cached descriptions to avoid redundant API calls
		descriptions = cachedDescriptions
		logger.Debugf("[hybrid] using cached image descriptions count=%d", len(descriptions))
	} else {
		visionDescriber := b.getVisionDescriber()
		if visionDescriber == nil {
			logger.Warnf("[hybrid] vision describer unavailable, continuing without image descriptions")
			fullContent.WriteString(formatMessageXML(mc.DisplayName, mc.Username, mc.UserID, currentMsgContent.String(), time.Now(), mc.ReplyToUsername, mc.ReplyToContent))
			fullContent.WriteString("\n</currentMessage>")
			return b.completeTextWithTools(ctx, mc.AIClient, req, fullContent.String())
		}

		// No cache available, call vision API
		var err error
		descriptions, err = visionDescriber.DescribeImages(ctx, imageURLs)
		if err != nil {
			logger.Warnf("Failed to describe images: %v", err)
			// Description failed - format without image descriptions
			fullContent.WriteString(formatMessageXML(mc.DisplayName, mc.Username, mc.UserID, currentMsgContent.String(), time.Now(), mc.ReplyToUsername, mc.ReplyToContent))
			fullContent.WriteString("\n</currentMessage>")
			return b.completeTextWithTools(ctx, mc.AIClient, req, fullContent.String())
		}
		logger.Debugf("[hybrid] generated fresh image descriptions count=%d", len(descriptions))
	}

	// Add image descriptions to current message content
	for i, desc := range descriptions {
		if i < b.cfg().AI.Vision.MaxImages {
			currentMsgContent.WriteString(fmt.Sprintf(" [Image %d: %s]", i+1, desc))
		}
	}

	fullContent.WriteString(formatMessageXML(mc.DisplayName, mc.Username, mc.UserID, currentMsgContent.String(), time.Now(), mc.ReplyToUsername, mc.ReplyToContent))
	fullContent.WriteString("\n</currentMessage>")

	return b.completeTextWithTools(ctx, mc.AIClient, req, fullContent.String())
}

// handleMultimodalMode handles multimodal mode (single model for vision + tools)
func (b *Bot) handleMultimodalMode(ctx context.Context, mc ModeContext, req ai.ChatCompletionRequest, imageURLs []string) (string, error) {
	maxImages := b.cfg().AI.Vision.MaxImages
	if maxImages == 0 {
		maxImages = len(imageURLs)
	}

	imagesToProcess := imageURLs
	if len(imageURLs) > maxImages {
		imagesToProcess = imageURLs[:maxImages]
		logger.Warnf("Limiting images to %d (received %d)", maxImages, len(imageURLs))
	}

	// Wrap user content in XML format for multimodal mode
	wrappedContent := "<currentMessage>\n" + formatMessageXML(mc.DisplayName, mc.Username, mc.UserID, mc.UserContent, time.Now(), mc.ReplyToUsername, mc.ReplyToContent) + "\n</currentMessage>"

	resp, err := mc.AIClient.CreateVisionCompletionWithTools(
		ctx,
		req.SystemPrompt,
		req.UserContext,
		wrappedContent,
		imagesToProcess,
		req.Messages,
		b.createToolHandler(),
	)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}
