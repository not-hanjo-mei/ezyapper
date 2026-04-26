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
	"ezyapper/internal/utils"

	"github.com/bwmarrin/discordgo"
	openai "github.com/sashabaranov/go-openai"
)

// generateResponse generates an AI response for a message
func (b *Bot) generateResponse(ctx context.Context, m *discordgo.MessageCreate, guildName string, imageURLs []string, imageDescriptions []string, recentMessages []*memory.DiscordMessage, memories []*memory.Memory, profile *memory.Profile) (string, error) {
	// Check if context is cancelled before starting
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Create AI client for chat completion
	aiClient := ai.NewClient(&b.cfg().AI, b.toolRegistry)

	// Build static system prompt (cacheable - does not change between requests)
	// This includes persona definition and mention guidelines
	systemPrompt := b.cfg().FormatSystemPrompt(m.Author.Username, guildName, m.GuildID, m.ChannelID)

	// Build dynamic user context (not cacheable - changes every request)
	// This is placed in the user message to preserve prompt caching of the system prompt
	dynamicContext := b.buildDynamicContext(m.Author.Username, profile, memories, recentMessages)

	logger.Debugf("[prompt] system prompt length: %d chars (static)", len(systemPrompt))
	logger.Debugf("[prompt] dynamic context length: %d chars", len(dynamicContext))

	// Get current bot ID
	botID := ""
	if b.session != nil && b.session.State != nil && b.session.State.User != nil {
		botID = b.session.State.User.ID
	}

	// Build channel mappings from state cache for resolving <#ID> mentions
	var channelMappings []utils.ChannelMapping
	if b.session != nil && b.session.State != nil {
		for _, guild := range b.session.State.Guilds {
			for _, ch := range guild.Channels {
				channelMappings = append(channelMappings, utils.ChannelMapping{ID: ch.ID, Name: ch.Name})
			}
		}
	}

	// Build conversation history as formatted text to include in UserContext.
	// Keep historical image enrichment fast by default, but allow limited on-demand
	// enrichment when the user likely references recent images.
	conversationHistory := b.buildConversationHistoryText(
		ctx,
		recentMessages,
		m.ID,
		botID,
		shouldEnrichRecentHistoricalImages(m),
		m.Mentions,
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

	// Extract reply info for current message so the LLM sees "who replied to whom"
	replyToUsername := ""
	if m.MessageReference != nil {
		if m.ReferencedMessage != nil && m.ReferencedMessage.Author != nil {
			replyToUsername = m.ReferencedMessage.Author.Username
		} else {
			replyToUsername = "(deleted message)"
		}
	}

	var response string
	var err error

	logger.Infof("[vision] using mode: %s", b.cfg().AI.Vision.Mode)

	switch b.cfg().AI.Vision.Mode {
	case config.VisionModeTextOnly:
		logger.Debugf("[vision] text-only mode - skipping image extraction")
		response, err = b.handleTextOnlyMode(ctx, aiClient, req, m.Author.Username, m.Author.ID, m.Content, replyToUsername)

	case config.VisionModeHybrid:
		logger.Debugf("[vision] hybrid mode - describing images with vision model, then using text model with tools")
		// Use cached image descriptions if available (from processMessage)
		response, err = b.handleHybridMode(ctx, aiClient, req, m.Author.Username, m.Author.ID, m.Content, imageURLs, imageDescriptions, replyToUsername)

	case config.VisionModeMultimodal:
		logger.Debugf("[vision] multimodal mode - using single model for both vision and tools")
		response, err = b.handleMultimodalMode(ctx, aiClient, req, m.Author.Username, m.Author.ID, m.Content, imageURLs, replyToUsername)

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
func (b *Bot) handleTextOnlyMode(ctx context.Context, aiClient *ai.Client, req ai.ChatCompletionRequest, username, userID, userContent string, replyToUsername string) (string, error) {
	// Build final content with XML formatting:
	// UserContext contains <context> + dynamic context
	// Current message wrapped in <currentMessage>
	var fullContent strings.Builder
	if req.UserContext != "" {
		fullContent.WriteString(req.UserContext)
		fullContent.WriteString("\n\n")
	}
	fullContent.WriteString("<currentMessage>\n")
	fullContent.WriteString(formatMessageXML(username, userID, userContent, time.Now(), replyToUsername))
	fullContent.WriteString("\n</currentMessage>")

	return b.completeTextWithTools(ctx, aiClient, req, fullContent.String())
}

// handleHybridMode handles hybrid mode (vision description + text model)
func (b *Bot) handleHybridMode(ctx context.Context, aiClient *ai.Client, req ai.ChatCompletionRequest, username, userID, userContent string, imageURLs []string, cachedDescriptions []string, replyToUsername string) (string, error) {
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
	currentMsgContent.WriteString(userContent)

	if len(imageURLs) == 0 {
		// No images - format and send
		fullContent.WriteString(formatMessageXML(username, userID, currentMsgContent.String(), time.Now(), replyToUsername))
		fullContent.WriteString("\n</currentMessage>")
		return b.completeTextWithTools(ctx, aiClient, req, fullContent.String())
	}

	var descriptions []string
	if len(cachedDescriptions) > 0 {
		// Use cached descriptions to avoid redundant API calls
		descriptions = cachedDescriptions
		logger.Debugf("[hybrid] using cached image descriptions count=%d", len(descriptions))
	} else {
		visionDescriber := b.getVisionDescriber()
		if visionDescriber == nil {
			logger.Warnf("[hybrid] vision describer unavailable, continuing without image descriptions")
			fullContent.WriteString(formatMessageXML(username, userID, currentMsgContent.String(), time.Now(), replyToUsername))
			fullContent.WriteString("\n</currentMessage>")
			return b.completeTextWithTools(ctx, aiClient, req, fullContent.String())
		}

		// No cache available, call vision API
		var err error
		descriptions, err = visionDescriber.DescribeImages(ctx, imageURLs)
		if err != nil {
			logger.Warnf("Failed to describe images: %v", err)
			// Description failed - format without image descriptions
			fullContent.WriteString(formatMessageXML(username, userID, currentMsgContent.String(), time.Now(), replyToUsername))
			fullContent.WriteString("\n</currentMessage>")
			return b.completeTextWithTools(ctx, aiClient, req, fullContent.String())
		}
		logger.Debugf("[hybrid] generated fresh image descriptions count=%d", len(descriptions))
	}

	// Add image descriptions to current message content
	for i, desc := range descriptions {
		if i < b.cfg().AI.Vision.MaxImages {
			currentMsgContent.WriteString(fmt.Sprintf(" [Image %d: %s]", i+1, desc))
		}
	}

	fullContent.WriteString(formatMessageXML(username, userID, currentMsgContent.String(), time.Now(), replyToUsername))
	fullContent.WriteString("\n</currentMessage>")

	return b.completeTextWithTools(ctx, aiClient, req, fullContent.String())
}

// handleMultimodalMode handles multimodal mode (single model for vision + tools)
func (b *Bot) handleMultimodalMode(ctx context.Context, aiClient *ai.Client, req ai.ChatCompletionRequest, username, userID, userContent string, imageURLs []string, replyToUsername string) (string, error) {
	maxImages := b.cfg().AI.Vision.MaxImages
	if maxImages == 0 {
		maxImages = len(imageURLs)
	}

	imagesToProcess := imageURLs
	if len(imageURLs) > maxImages {
		imagesToProcess = imageURLs[:maxImages]
		logger.Infof("Limiting images to %d (received %d)", maxImages, len(imageURLs))
	}

	// Wrap user content in XML format for multimodal mode
	wrappedContent := "<currentMessage>\n" + formatMessageXML(username, userID, userContent, time.Now(), replyToUsername) + "\n</currentMessage>"

	resp, err := aiClient.CreateVisionCompletionWithTools(
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
