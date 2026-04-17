package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/logger"

	openai "github.com/sashabaranov/go-openai"
)

// DecisionService uses an LLM to decide whether the bot should respond to a message.
// It considers message content, author, conversation context, and images.
type DecisionService struct {
	config     *config.DecisionConfig
	client     *openai.Client
	httpClient *http.Client
}

// DecisionResult contains the LLM's decision about whether to respond.
type DecisionResult struct {
	ShouldRespond bool    `json:"should_respond"`
	Reason        string  `json:"reason"`
	Confidence    float64 `json:"confidence"`
}

// MessageInfo contains metadata about a message for decision making
type MessageInfo struct {
	AuthorName string // Username of the message author
	AuthorID   string // Discord ID of the message author
	Content    string // Message content
	ReplyTo    string // Username of the user being replied to (empty if not a reply)
	ReplyToID  string // Discord ID of the user being replied to (empty if not a reply)
}

// ContextMessage represents a single message in the conversation context
type ContextMessage struct {
	AuthorName string
	AuthorID   string
	Content    string
	IsBot      bool
	Timestamp  time.Time
}

func NewDecisionService(cfg *config.DecisionConfig) (*DecisionService, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("decision.api_key is required when decision is enabled")
	}

	if cfg.APIBaseURL == "" {
		return nil, fmt.Errorf("decision.api_base_url is required when decision is enabled")
	}

	openaiConfig := openai.DefaultConfig(cfg.APIKey)
	openaiConfig.BaseURL = cfg.APIBaseURL

	httpTimeout := time.Duration(cfg.Timeout) * time.Second * 2
	if httpTimeout < 30*time.Second {
		httpTimeout = 30 * time.Second
	}
	httpClient := &http.Client{Timeout: httpTimeout}
	openaiConfig.HTTPClient = httpClient

	return &DecisionService{
		config:     cfg,
		client:     openai.NewClientWithConfig(openaiConfig),
		httpClient: httpClient,
	}, nil
}

func (d *DecisionService) closeIdleConnections() {
	if d.httpClient != nil {
		d.httpClient.CloseIdleConnections()
	}
}

func (d *DecisionService) ShouldRespondWithInfo(ctx context.Context, botName string, msgInfo MessageInfo, imageCount int, recentMessages []ContextMessage) (*DecisionResult, error) {
	if !d.config.Enabled {
		return &DecisionResult{ShouldRespond: false, Reason: "decision service disabled"}, nil
	}

	logger.Debugf("[decision] analyzing message:")
	logger.Debugf("  Author: %s (ID: %s)", msgInfo.AuthorName, msgInfo.AuthorID)
	logger.Debugf("  Content: %q", msgInfo.Content)
	if msgInfo.ReplyTo != "" {
		logger.Debugf("  Reply to: %s (ID: %s)", msgInfo.ReplyTo, msgInfo.ReplyToID)
	}
	logger.Debugf("  Image count: %d", imageCount)
	logger.Debugf("  Recent messages count: %d", len(recentMessages))

	// Append image info to message content if images are present
	content := msgInfo.Content
	if imageCount > 0 {
		content += fmt.Sprintf("\n\n[User attached %d image(s) to this message]", imageCount)
	}

	systemPrompt, userPrompt := d.buildPromptsWithInfo(botName, msgInfo, content, recentMessages)
	logger.Debugf("[decision] built system prompt (length: %d)", len(systemPrompt))
	logger.Debugf("[decision] built user prompt (length: %d)", len(userPrompt))

	req := openai.ChatCompletionRequest{
		Model: d.config.Model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userPrompt},
		},
		MaxTokens:   d.config.MaxTokens,
		Temperature: d.config.Temperature,
	}

	// Apply extra parameters from config
	ApplyExtraParams(&req, d.config.ExtraParams, "[decision]")

	var lastErr error
	for attempt := 0; attempt <= d.config.RetryCount; attempt++ {
		// Check if parent context is explicitly cancelled before starting attempt
		// Don't cancel on DeadlineExceeded - that's what we're trying to recover from with retries
		if err := ctx.Err(); err != nil && err != context.DeadlineExceeded {
			return nil, fmt.Errorf("decision cancelled before attempt %d: %w", attempt+1, err)
		}

		// Create a fresh context with timeout for each attempt, but inherit from parent ctx
		// This ensures the request can be cancelled by the parent context
		attemptCtx, cancel := context.WithTimeout(ctx, time.Duration(d.config.Timeout)*time.Second)

		logger.Debugf("[decision] making LLM request (attempt %d/%d)", attempt+1, d.config.RetryCount+1)
		resp, err := d.client.CreateChatCompletion(attemptCtx, req)
		cancel() // Clean up immediately after request completes

		if err == nil {
			if len(resp.Choices) == 0 {
				return nil, fmt.Errorf("no response from decision llm")
			}
			if attempt > 0 {
				logger.Infof("[decision] succeeded after %d retries", attempt)
			}
			result, parseErr := d.parseResponse(resp.Choices[0].Message.Content)
			if parseErr == nil {
				logger.Debugf("[decision] result: should_respond=%v, reason=%q, confidence=%.2f",
					result.ShouldRespond, result.Reason, result.Confidence)
			}
			return result, parseErr
		}

		lastErr = err
		if isTimeoutLikeError(err) {
			d.closeIdleConnections()
		}

		// Check if the original context was cancelled
		if ctx.Err() == context.Canceled {
			return nil, fmt.Errorf("decision llm call cancelled: %w", err)
		}

		if attempt >= d.config.RetryCount {
			break
		}

		logger.Warnf("[decision] attempt %d/%d failed: %v, will retry", attempt+1, d.config.RetryCount, err)
		backoff := time.Duration(100*(1<<attempt)) * time.Millisecond
		time.Sleep(backoff)
	}

	return nil, fmt.Errorf("decision llm call failed after %d attempts: %w", d.config.RetryCount+1, lastErr)
}

func (d *DecisionService) buildPromptsWithInfo(botName string, msgInfo MessageInfo, content string, recentMessages []ContextMessage) (string, string) {
	// Build user prompt with XML formatting
	var userPrompt strings.Builder

	// Build context section with recent messages
	if len(recentMessages) > 0 {
		userPrompt.WriteString("<context>\n")
		for _, msg := range recentMessages {
			timeStr := msg.Timestamp.UTC().Format(time.RFC3339)
			userPrompt.WriteString(fmt.Sprintf("\"%s\"{UserID=%s,Time=%s}: \"%s\"\n", msg.AuthorName, msg.AuthorID, timeStr, msg.Content))
		}
		userPrompt.WriteString("</context>\n\n")
	}

	// Build current message section
	userPrompt.WriteString("<currentMessage>\n")
	userPrompt.WriteString(fmt.Sprintf("\"%s\"{UserID=%s}: \"%s\"\n", msgInfo.AuthorName, msgInfo.AuthorID, content))
	if msgInfo.ReplyTo != "" {
		userPrompt.WriteString(fmt.Sprintf("Reply to: \"%s\"{UserID=%s}\n", msgInfo.ReplyTo, msgInfo.ReplyToID))
	}
	userPrompt.WriteString("</currentMessage>")

	// Build system prompt with role and rules (use config.system_prompt as template)
	systemPrompt := strings.ReplaceAll(d.config.SystemPrompt, "{BotName}", botName)

	return systemPrompt, userPrompt.String()
}

func (d *DecisionService) parseResponse(content string) (*DecisionResult, error) {
	content = strings.TrimSpace(content)

	jsonStart := strings.Index(content, "{")
	jsonEnd := strings.LastIndex(content, "}")
	if jsonStart == -1 || jsonEnd == -1 || jsonEnd <= jsonStart {
		return nil, fmt.Errorf("no valid json found in response: %s", content)
	}

	jsonStr := content[jsonStart : jsonEnd+1]

	var result DecisionResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse json: %w", err)
	}

	if result.Confidence < 0 {
		result.Confidence = 0
	}
	if result.Confidence > 1 {
		result.Confidence = 1
	}

	return &result, nil
}
