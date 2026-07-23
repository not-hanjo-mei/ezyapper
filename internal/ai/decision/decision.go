// Package decision provides LLM-based reply decisions for whether the bot should respond to a message.
package decision

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"ezyapper/internal/ai"
	"ezyapper/internal/ai/llmjson"
	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/retry"

	openai "github.com/sashabaranov/go-openai"
)

type DecisionService struct {
	config     *config.DecisionConfig
	client     *openai.Client
	httpClient *http.Client
}

type DecisionResult struct {
	ShouldRespond bool    `json:"should_respond"`
	Reason        string  `json:"reason"`
	Confidence    float64 `json:"confidence"`
}

type MessageInfo struct {
	AuthorName string
	AuthorID   string
	Content    string
	ReplyTo    string
	ReplyToID  string
}

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

	httpTimeout := time.Duration(cfg.Timeout) * time.Second
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

	baseMessages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
		{Role: openai.ChatMessageRoleUser, Content: userPrompt},
	}
	var feedback string

	result, err := retry.Retry(ctx, d.config.RetryCount, func(ctx context.Context) (*DecisionResult, error) {
		messages := make([]openai.ChatCompletionMessage, len(baseMessages))
		copy(messages, baseMessages)
		if feedback != "" {
			messages = append(messages, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleUser,
				Content: feedback,
			})
		}

		req := openai.ChatCompletionRequest{
			Model:       d.config.Model,
			Messages:    messages,
			MaxTokens:   d.config.MaxTokens,
			Temperature: d.config.Temperature,
		}
		ai.ApplyExtraParams(&req, d.config.ExtraParams, "[decision]")

		attemptCtx, cancel := context.WithTimeout(ctx, time.Duration(d.config.Timeout)*time.Second)
		defer cancel()

		logger.Debugf("[decision] making LLM request")
		resp, err := d.client.CreateChatCompletion(attemptCtx, req)
		if err != nil {
			if ai.IsTimeoutLikeError(err) {
				d.closeIdleConnections()
			}
			return nil, err
		}
		if len(resp.Choices) == 0 {
			return nil, llmjson.ParseError("no response from decision llm", nil)
		}

		parsed, err := d.parseResponse(resp.Choices[0].Message.Content)
		if err != nil {
			feedback = "Your previous response failed validation: " + err.Error() +
				`. Return ONLY valid JSON: {"should_respond":true|false,"reason":"...","confidence":0.0-1.0}`
			logger.Warnf("[decision] schema/parse failed, will retry if budget remains: %v", err)
			return nil, err
		}
		return parsed, nil
	},
		retry.WithBaseDelay(100*time.Millisecond),
		retry.WithErrorClassifier(func(err error) bool {
			return ai.IsRetryableError(err) || llmjson.IsOutputError(err)
		}),
	)

	if err != nil {
		return nil, fmt.Errorf("decision llm call failed: %w", err)
	}

	logger.Debugf("[decision] result: should_respond=%v, reason=%q, confidence=%.2f",
		result.ShouldRespond, result.Reason, result.Confidence)
	return result, nil
}

func (d *DecisionService) buildPromptsWithInfo(botName string, msgInfo MessageInfo, content string, recentMessages []ContextMessage) (string, string) {
	var userPrompt strings.Builder

	if len(recentMessages) > 0 {
		userPrompt.WriteString("<context>\n")
		for _, msg := range recentMessages {
			timeStr := msg.Timestamp.UTC().Format(time.RFC3339)
			fmt.Fprintf(&userPrompt, "\"%s\"{UserID=%s,Time=%s}: \"%s\"\n", msg.AuthorName, msg.AuthorID, timeStr, msg.Content)
		}
		userPrompt.WriteString("</context>\n\n")
	}

	userPrompt.WriteString("<currentMessage>\n")
	fmt.Fprintf(&userPrompt, "\"%s\"{UserID=%s}: \"%s\"\n", msgInfo.AuthorName, msgInfo.AuthorID, content)
	if msgInfo.ReplyTo != "" {
		fmt.Fprintf(&userPrompt, "Reply to: \"%s\"{UserID=%s}\n", msgInfo.ReplyTo, msgInfo.ReplyToID)
	}
	userPrompt.WriteString("</currentMessage>")

	systemPrompt := strings.ReplaceAll(d.config.SystemPrompt, "{BotName}", botName)

	return systemPrompt, userPrompt.String()
}

func (d *DecisionService) parseResponse(content string) (*DecisionResult, error) {
	if err := llmjson.RequiredKeysPresent(content, []string{"should_respond", "reason", "confidence"}); err != nil {
		return nil, err
	}
	return llmjson.Decode(content, validateDecisionResult)
}

func validateDecisionResult(r *DecisionResult) error {
	if strings.TrimSpace(r.Reason) == "" {
		return llmjson.SchemaError("reason must be non-empty", nil)
	}
	if r.Confidence < 0 || r.Confidence > 1 {
		return llmjson.SchemaError(fmt.Sprintf("confidence %v out of range [0,1]", r.Confidence), nil)
	}
	return nil
}
