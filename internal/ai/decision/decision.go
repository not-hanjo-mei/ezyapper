// Package decision provides LLM-based reply decisions for whether the bot should respond to a message.
package decision

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"ezyapper/internal/ai"
	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/retry"

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

// NewDecisionService creates a new decision service for LLM-based reply decisions.
func NewDecisionService(cfg *config.DecisionConfig) (*DecisionService, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("decision.api_key is required when decision is enabled")
	}

	if cfg.APIBaseURL == "" {
		return nil, fmt.Errorf("decision.api_base_url is required when decision is enabled")
	}

	openaiConfig := openai.DefaultConfig(cfg.APIKey)
	openaiConfig.BaseURL = cfg.APIBaseURL

	httpTimeout := time.Duration(cfg.HTTPTimeoutSec) * time.Second
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

// retryableError checks if an error should trigger a retry.
// Only 429 (rate limit), 5xx (server errors), and connection/timeout errors are retryable.
// 400, 401, 403, 404 and other client errors are not retried.
func retryableError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	// Rate limit errors
	if strings.Contains(errStr, "429") || strings.Contains(errStr, "too many requests") {
		return true
	}
	// Server errors
	if strings.Contains(errStr, "500") || strings.Contains(errStr, "502") || strings.Contains(errStr, "503") || strings.Contains(errStr, "504") {
		return true
	}
	// Connection errors
	if strings.Contains(errStr, "connection refused") || strings.Contains(errStr, "timeout") || strings.Contains(errStr, "context deadline exceeded") || strings.Contains(errStr, "eof") {
		return true
	}
	return false
}

// ShouldRespondWithInfo uses an LLM to decide whether the bot should respond to a message.
func (d *DecisionService) ShouldRespondWithInfo(ctx context.Context, botName string, msgInfo MessageInfo, imageCount int, recentMessages []ContextMessage) (*DecisionResult, error) {
	if !d.config.Enabled {
		return &DecisionResult{ShouldRespond: true, Reason: "decision service disabled, falling back to random"}, nil
	}

	logger.Debugf("[decision] analyzing message:")
	logger.Debugf("  Author: %s (ID: %s)", msgInfo.AuthorName, msgInfo.AuthorID)
	logger.Debugf("  Content: %q", msgInfo.Content)
	if msgInfo.ReplyTo != "" {
		logger.Debugf("  Reply to: %s (ID: %s)", msgInfo.ReplyTo, msgInfo.ReplyToID)
	}
	logger.Debugf("  Image count: %d", imageCount)
	logger.Debugf("  Recent messages count: %d", len(recentMessages))

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

	ai.ApplyExtraParams(&req, d.config.ExtraParams, "[decision]")

	resp, err := retry.Retry(ctx, d.config.RetryCount, func(ctx context.Context) (openai.ChatCompletionResponse, error) {
		attemptCtx, cancel := context.WithTimeout(ctx, time.Duration(d.config.Timeout)*time.Second)
		defer cancel()

		logger.Debugf("[decision] making LLM request")
		resp, err := d.client.CreateChatCompletion(attemptCtx, req)
		if err != nil && ai.IsTimeoutLikeError(err) {
			d.closeIdleConnections()
		}
		return resp, err
	},
		retry.WithBaseDelay(100*time.Millisecond),
		retry.WithErrorClassifier(retryableError),
	)

	if err != nil {
		return nil, fmt.Errorf("decision llm call failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no response from decision llm")
	}

	result, err := d.parseResponse(resp.Choices[0].Message.Content)
	if err != nil {
		return nil, err
	}

	logger.Debugf("[decision] result: should_respond=%v, reason=%q, confidence=%.2f",
		result.ShouldRespond, result.Reason, result.Confidence)
	return result, nil
}

func (d *DecisionService) buildPromptsWithInfo(botName string, msgInfo MessageInfo, content string, recentMessages []ContextMessage) (string, string) {
	// Build user prompt with XML formatting
	var userPrompt strings.Builder

	// Build context section with recent messages
	if len(recentMessages) > 0 {
		userPrompt.WriteString("<context>\n")
		for _, msg := range recentMessages {
			timeStr := msg.Timestamp.UTC().Format(time.RFC3339)
			fmt.Fprintf(&userPrompt, "\"%s\"{UserID=%s,Time=%s}: \"%s\"\n", msg.AuthorName, msg.AuthorID, timeStr, msg.Content)
		}
		userPrompt.WriteString("</context>\n\n")
	}

	// Build current message section
	userPrompt.WriteString("<currentMessage>\n")
	fmt.Fprintf(&userPrompt, "\"%s\"{UserID=%s}: \"%s\"\n", msgInfo.AuthorName, msgInfo.AuthorID, content)
	if msgInfo.ReplyTo != "" {
		fmt.Fprintf(&userPrompt, "Reply to: \"%s\"{UserID=%s}\n", msgInfo.ReplyTo, msgInfo.ReplyToID)
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
		return nil, fmt.Errorf("confidence %f is below 0", result.Confidence)
	}
	if result.Confidence > 1 {
		return nil, fmt.Errorf("confidence %f is above 1", result.Confidence)
	}

	return &result, nil
}
