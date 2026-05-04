// Package ai provides AI/LLM integration using OpenAI-compatible API
package ai

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"ezyapper/internal/ai/tools"
	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/retry"

	"github.com/sashabaranov/go-openai"
)

// Client wraps the OpenAI client with additional functionality
type Client struct {
	client       *openai.Client
	httpClient   *http.Client
	config       *config.AIConfig
	toolRegistry *tools.ToolRegistry
}

// NewClient creates a new AI client
func NewClient(cfg *config.AIConfig, toolRegistry *tools.ToolRegistry) *Client {
	httpTimeout := time.Duration(cfg.HTTPTimeoutSec) * time.Second
	httpClient := &http.Client{Timeout: httpTimeout}

	// Create OpenAI config with custom base URL
	openaiConfig := openai.DefaultConfig(cfg.APIKey)
	openaiConfig.BaseURL = cfg.APIBaseURL
	openaiConfig.HTTPClient = httpClient

	return &Client{
		client:       openai.NewClientWithConfig(openaiConfig),
		httpClient:   httpClient,
		config:       cfg,
		toolRegistry: toolRegistry,
	}
}

// ChatCompletionRequest represents a chat completion request
type ChatCompletionRequest struct {
	SystemPrompt string
	Messages     []openai.ChatCompletionMessage
	Tools        []openai.Tool
	UserContext  string // Dynamic context appended to user message (for prompt caching)
}

// ChatCompletionResponse represents a chat completion response
type ChatCompletionResponse struct {
	Content          string
	ToolCalls        []openai.ToolCall
	ReasoningContent string
	FinishReason     string
	Usage            openai.Usage
}

// processMessages converts image URLs to base64 if VisionBase64 is enabled
func (c *Client) processMessages(ctx context.Context, messages []openai.ChatCompletionMessage) ([]openai.ChatCompletionMessage, error) {
	if !c.config.VisionBase64 {
		return messages, nil
	}

	processed := make([]openai.ChatCompletionMessage, len(messages))
	for i, msg := range messages {
		processed[i] = msg

		if len(msg.MultiContent) == 0 {
			continue
		}

		newParts := make([]openai.ChatMessagePart, len(msg.MultiContent))
		for j, part := range msg.MultiContent {
			newParts[j] = part
			if part.Type != openai.ChatMessagePartTypeImageURL || part.ImageURL == nil {
				continue
			}
			url := part.ImageURL.URL
			if strings.HasPrefix(url, "data:image") {
				continue
			}
			base64Data, err := c.downloadImageToBase64(ctx, url)
			if err != nil {
				return nil, fmt.Errorf("failed to convert image to base64: %w", err)
			}
			// Create a copy to avoid mutating the original message's ImageURL
			imgCopy := *part.ImageURL
			imgCopy.URL = base64Data
			newParts[j].ImageURL = &imgCopy
		}
		processed[i].MultiContent = newParts
	}

	return processed, nil
}

// retryableError checks if an error should trigger a retry
func (c *Client) retryableError(err error) bool {
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

func (c *Client) requestTimeout() time.Duration {
	if c.config != nil && c.config.Timeout > 0 {
		return time.Duration(c.config.Timeout) * time.Second
	}
	if c.httpClient != nil && c.httpClient.Timeout > 0 {
		return c.httpClient.Timeout
	}
	logger.Warnf("[ai] requestTimeout: no timeout configured, this should not happen (check config validation)")
	return 30 * time.Second
}

func (c *Client) closeIdleConnections() {
	if c.httpClient != nil {
		c.httpClient.CloseIdleConnections()
	}
}

// IsTimeoutLikeError checks if an error is timeout-related (deadline exceeded, network timeout).
func IsTimeoutLikeError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline exceeded")
}

// CreateChatCompletionWithRetry sends a chat completion request with automatic retry on failures.
func (c *Client) CreateChatCompletionWithRetry(ctx context.Context, req openai.ChatCompletionRequest, operation string) (openai.ChatCompletionResponse, error) {
	return retry.Retry(ctx, c.config.RetryCount, func(ctx context.Context) (openai.ChatCompletionResponse, error) {
		attemptCtx, cancel := context.WithTimeout(ctx, c.requestTimeout())
		defer cancel()
		logger.Debugf("[ai] calling %s API...", operation)
		resp, err := c.client.CreateChatCompletion(attemptCtx, req)
		if err != nil && IsTimeoutLikeError(err) {
			c.closeIdleConnections()
		}
		return resp, err
	},
		retry.WithBaseDelay(1*time.Second),
		retry.WithMaxDelay(30*time.Second),
		retry.WithErrorClassifier(c.retryableError),
	)
}

func (c *Client) createEmbeddingWithRetry(ctx context.Context, req openai.EmbeddingRequest, operation string) (openai.EmbeddingResponse, error) {
	return retry.Retry(ctx, c.config.RetryCount, func(ctx context.Context) (openai.EmbeddingResponse, error) {
		attemptCtx, cancel := context.WithTimeout(ctx, c.requestTimeout())
		defer cancel()
		logger.Debugf("[ai] calling %s API...", operation)
		resp, err := c.client.CreateEmbeddings(attemptCtx, req)
		if err != nil && IsTimeoutLikeError(err) {
			c.closeIdleConnections()
		}
		return resp, err
	},
		retry.WithBaseDelay(1*time.Second),
		retry.WithMaxDelay(30*time.Second),
		retry.WithErrorClassifier(c.retryableError),
	)
}

func (c *Client) buildVisionParts(ctx context.Context, textPrompt string, imageURLs []string) ([]openai.ChatMessagePart, error) {
	parts := make([]openai.ChatMessagePart, 0, len(imageURLs)+1)

	if textPrompt != "" {
		parts = append(parts, openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeText,
			Text: textPrompt,
		})
	}

	for _, url := range imageURLs {
		finalURL := url
		if !strings.HasPrefix(url, "data:image") && c.config.VisionBase64 {
			base64Data, err := c.downloadImageToBase64(ctx, url)
			if err != nil {
				return nil, fmt.Errorf("failed to convert image to base64: %w", err)
			}
			finalURL = base64Data
		}

		parts = append(parts, openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeImageURL,
			ImageURL: &openai.ChatMessageImageURL{
				URL:    finalURL,
				Detail: openai.ImageURLDetailAuto,
			},
		})
	}

	return parts, nil
}

// CreateChatCompletion creates a chat completion
func (c *Client) CreateChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	// Build messages
	messages := make([]openai.ChatCompletionMessage, 0, len(req.Messages)+1)

	// Add system prompt
	if req.SystemPrompt != "" {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: req.SystemPrompt,
		})
	}

	// Process messages (convert images to base64 if needed)
	processedMessages, err := c.processMessages(ctx, req.Messages)
	if err != nil {
		return nil, err
	}

	// Prepend UserContext to the last user message if provided
	// This places dynamic content after the static system prompt, preserving cacheability
	if req.UserContext != "" && len(processedMessages) > 0 {
		// Find the last user message
		for i := len(processedMessages) - 1; i >= 0; i-- {
			if processedMessages[i].Role == openai.ChatMessageRoleUser {
				// Prepend context to the user message
				processedMessages[i].Content = req.UserContext + "\n\n" + processedMessages[i].Content
				break
			}
		}
	}

	// Add conversation messages
	messages = append(messages, processedMessages...)

	// Build request
	chatReq := openai.ChatCompletionRequest{
		Model:       c.config.Model,
		Messages:    messages,
		MaxTokens:   c.config.MaxTokens,
		Temperature: c.config.Temperature,
	}

	// Add tools if provided
	if len(req.Tools) > 0 {
		chatReq.Tools = req.Tools
	}

	// Apply extra parameters from config
	c.applyExtraParams(&chatReq)

	logger.Debugf("[ai] creating chat completion:")
	logger.Debugf("  Model: %s", c.config.Model)
	logger.Debugf("  Messages: %d", len(messages))
	logger.Debugf("  System prompt length: %d", len(req.SystemPrompt))
	logger.Debugf("  Tools: %d", len(chatReq.Tools))

	resp, err := c.CreateChatCompletionWithRetry(ctx, chatReq, "llm completion")
	if err != nil {
		return nil, err
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no response choices returned")
	}

	choice := resp.Choices[0]
	logger.Debugf("[ai] received response:")
	logger.Debugf("  Finish reason: %s", choice.FinishReason)
	logger.Debugf("  Prompt tokens: %d", resp.Usage.PromptTokens)
	logger.Debugf("  Completion tokens: %d", resp.Usage.CompletionTokens)
	logger.Debugf("  Total tokens: %d", resp.Usage.TotalTokens)

	return &ChatCompletionResponse{
		Content:          choice.Message.Content,
		ToolCalls:        choice.Message.ToolCalls,
		ReasoningContent: choice.Message.ReasoningContent,
		FinishReason:     string(choice.FinishReason),
		Usage:            resp.Usage,
	}, nil
}

// applyExtraParams applies extra parameters from config to the request
func (c *Client) applyExtraParams(req *openai.ChatCompletionRequest) {
	ApplyExtraParams(req, c.config.ExtraParams, "[ai]")
}

// ApplyExtraParams applies extra parameters to a ChatCompletionRequest using reflection.
// This is a package-level function that can be used by other components.
// prefix is used for logging (e.g., "[decision]", "[vision]")
func ApplyExtraParams(req *openai.ChatCompletionRequest, extraParams map[string]interface{}, logPrefix string) {
	applyExtraParamsToStruct(req, extraParams, logPrefix)
}

// applyExtraParamsToStruct applies extra parameters to any struct using reflection.
// This generic version works with any struct (ChatCompletionRequest, EmbeddingRequest, etc.)
func applyExtraParamsToStruct(req interface{}, extraParams map[string]interface{}, logPrefix string) {
	if len(extraParams) == 0 {
		return
	}

	v := reflect.ValueOf(req)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		logger.Warnf("%s extra params: invalid request type %T", logPrefix, req)
		return
	}
	reqValue := v.Elem()
	reqType := reqValue.Type()

	for key, value := range extraParams {
		fieldIndex := findFieldIndexByJSONTag(reqType, key)
		if fieldIndex < 0 {
			logger.Debugf("%s extra param '%s' not found in request struct", logPrefix, key)
			continue
		}

		field := reqValue.Field(fieldIndex)
		if !field.CanSet() {
			logger.Debugf("%s extra param '%s' cannot be set", logPrefix, key)
			continue
		}

		// Try to set the value - user is responsible for correct types
		if err := setFieldValue(field, value); err != nil {
			logger.Warnf("%s failed to set extra param '%s': %v (check your config)", logPrefix, key, err)
		} else {
			logger.Debugf("%s applied extra param: %s", logPrefix, key)
		}
	}
}

// findFieldIndexByJSONTag finds a struct field index by its json tag name
func findFieldIndexByJSONTag(t reflect.Type, name string) int {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		jsonTag := field.Tag.Get("json")
		// Parse json tag like `json:"top_p,omitempty"`
		if idx := strings.Index(jsonTag, ","); idx != -1 {
			jsonTag = jsonTag[:idx]
		}
		if jsonTag == name {
			return i
		}
		// Also check field name (case insensitive)
		if strings.EqualFold(field.Name, name) {
			return i
		}
	}
	return -1
}

// setFieldValue sets a field value with type conversion
func setFieldValue(field reflect.Value, value interface{}) error {
	if value == nil {
		return nil
	}

	valReflect := reflect.ValueOf(value)

	if valReflect.Type().ConvertibleTo(field.Type()) {
		field.Set(valReflect.Convert(field.Type()))
		return nil
	}

	if field.Kind() == reflect.Ptr {
		elemType := field.Type().Elem()
		newVal := reflect.New(elemType)
		if valReflect.Type().ConvertibleTo(elemType) {
			newVal.Elem().Set(valReflect.Convert(elemType))
			field.Set(newVal)
			return nil
		}
		return fmt.Errorf("cannot convert %T to %v", value, elemType)
	}

	// Handle slice types (e.g., []string for Stop)
	if field.Kind() == reflect.Slice && valReflect.Kind() == reflect.Slice {
		sliceLen := valReflect.Len()
		newSlice := reflect.MakeSlice(field.Type(), sliceLen, sliceLen)
		elemType := field.Type().Elem()
		for i := 0; i < sliceLen; i++ {
			elemVal := valReflect.Index(i)
			if elemVal.Type().ConvertibleTo(elemType) {
				newSlice.Index(i).Set(elemVal.Convert(elemType))
			} else {
				return fmt.Errorf("slice element %d: cannot convert %T to %v", i, elemVal.Interface(), elemType)
			}
		}
		field.Set(newSlice)
		return nil
	}

	// Handle map types (e.g., map[string]int for LogitBias)
	if field.Kind() == reflect.Map && valReflect.Kind() == reflect.Map {
		mapType := field.Type()
		newMap := reflect.MakeMap(mapType)
		keyType := mapType.Key()
		elemType := mapType.Elem()
		for _, key := range valReflect.MapKeys() {
			elemVal := valReflect.MapIndex(key)
			if key.Type().ConvertibleTo(keyType) && elemVal.Type().ConvertibleTo(elemType) {
				newMap.SetMapIndex(key.Convert(keyType), elemVal.Convert(elemType))
			} else {
				return fmt.Errorf("map entry: cannot convert key %T or value %T", key.Interface(), elemVal.Interface())
			}
		}
		field.Set(newMap)
		return nil
	}

	return fmt.Errorf("cannot convert %T to %v", value, field.Type())
}

type imageDownloadOptions struct {
	RequireImageContentType bool
	MaxBytes                int64
	UserAgent               string
}

func (c *Client) fetchImageAsDataURL(ctx context.Context, url string, opts imageDownloadOptions) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	if opts.UserAgent != "" {
		req.Header.Set("User-Agent", opts.UserAgent)
	}

	httpClient := c.httpClient
	if httpClient == nil {
		timeout := 30 * time.Second
		if c.config != nil && c.config.HTTPTimeoutSec > 0 {
			timeout = time.Duration(c.config.HTTPTimeoutSec) * time.Second
		}
		logger.Warnf("[ai] fetchImageAsDataURL: httpClient is nil, this should not happen (check NewClient initialization)")
		httpClient = &http.Client{Timeout: timeout}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download image: status %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/png"
	}

	if opts.RequireImageContentType && !strings.HasPrefix(contentType, "image/") {
		return "", fmt.Errorf("invalid content type: %s", contentType)
	}

	if opts.MaxBytes > 0 && resp.ContentLength > opts.MaxBytes {
		return "", fmt.Errorf("image too large: %d bytes", resp.ContentLength)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read image data: %w", err)
	}

	if opts.MaxBytes > 0 && int64(len(data)) > opts.MaxBytes {
		return "", fmt.Errorf("image too large: %d bytes", len(data))
	}

	base64Data := base64.StdEncoding.EncodeToString(data)
	return fmt.Sprintf("data:%s;base64,%s", contentType, base64Data), nil
}

func (c *Client) downloadImageToBase64(ctx context.Context, url string) (string, error) {
	if err := validateImageURL(url); err != nil {
		return "", fmt.Errorf("invalid image URL: %w", err)
	}
	return c.fetchImageAsDataURL(ctx, url, imageDownloadOptions{
		MaxBytes:                int64(c.config.MaxImageBytes),
		RequireImageContentType: c.config.RequireImageContentType,
		UserAgent:               c.config.UserAgent,
	})
}

func validateImageURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse URL: %w", err)
	}

	if parsed.Scheme != "https" {
		if parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1" {
			return nil
		}
		return fmt.Errorf("only https URLs are allowed for images, got %q", parsed.Scheme)
	}

	host := parsed.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateIP(ip) {
			return fmt.Errorf("image URL resolves to private/internal IP address")
		}
		return nil
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("resolve host %q: %w", host, err)
	}
	for _, ip := range ips {
		if isPrivateIP(ip) {
			return fmt.Errorf("image URL resolves to private/internal IP (%s)", ip.String())
		}
	}
	return nil
}

func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsPrivate()
}

// CreateVisionCompletion creates a vision completion for image analysis
func (c *Client) CreateVisionCompletion(ctx context.Context, systemPrompt, textPrompt string, imageURLs []string) (string, error) {
	parts, err := c.buildVisionParts(ctx, textPrompt, imageURLs)
	if err != nil {
		return "", err
	}

	// Build messages
	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: systemPrompt,
		},
		{
			Role:         openai.ChatMessageRoleUser,
			MultiContent: parts,
		},
	}

	// Make API call with vision model and retry logic
	visionReq := openai.ChatCompletionRequest{
		Model:       c.config.VisionModel,
		Messages:    messages,
		MaxTokens:   c.config.MaxTokens,
		Temperature: c.config.Temperature,
	}

	resp, err := c.CreateChatCompletionWithRetry(ctx, visionReq, "vision completion")
	if err != nil {
		return "", err
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no response choices returned")
	}

	return resp.Choices[0].Message.Content, nil
}

// CreateChatCompletionWithTools creates a chat completion with tool support
func (c *Client) CreateChatCompletionWithTools(ctx context.Context, req ChatCompletionRequest, toolHandler ToolHandler) (*ChatCompletionResponse, error) {
	// Get available tools
	tools := c.toolRegistry.GetTools()

	// Add tools to request
	req.Tools = tools

	// Make initial request
	resp, err := c.CreateChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}

	// Process tool calls iteratively
	messages := make([]openai.ChatCompletionMessage, len(req.Messages))
	copy(messages, req.Messages)
	maxIterations := c.config.MaxToolIterations // Prevent infinite loops

	for i := 0; i < maxIterations && len(resp.ToolCalls) > 0; i++ {
		// Add assistant message with tool calls
		messages = append(messages, openai.ChatCompletionMessage{
			Role:             openai.ChatMessageRoleAssistant,
			Content:          resp.Content,
			ReasoningContent: resp.ReasoningContent,
			ToolCalls:        resp.ToolCalls,
		})

		// Process each tool call
		for _, toolCall := range resp.ToolCalls {
			// Execute tool
			result, err := toolHandler(ctx, toolCall)
			if err != nil {
				result = fmt.Sprintf("Error: %v", err)
			}

			// Add tool result message
			messages = append(messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    result,
				ToolCallID: toolCall.ID,
			})
		}

		// Make follow-up request
		req.Messages = messages
		req.Tools = tools

		resp, err = c.CreateChatCompletion(ctx, req)
		if err != nil {
			return nil, err
		}
	}

	// If we exited the loop with unprocessed tool calls, strip them
	if len(resp.ToolCalls) > 0 {
		logger.Warnf("[ai] tool loop exhausted with %d unprocessed tool calls", len(resp.ToolCalls))
		resp.ToolCalls = nil
	}

	return resp, nil
}

// CreateVisionCompletionWithTools creates a chat completion with vision and tool support (multimodal mode)
func (c *Client) CreateVisionCompletionWithTools(ctx context.Context, systemPrompt, userContext, textPrompt string, imageURLs []string, history []openai.ChatCompletionMessage, toolHandler ToolHandler) (*ChatCompletionResponse, error) {
	// Get available tools
	tools := c.toolRegistry.GetTools()

	// Build text content with UserContext prepended
	var textContent strings.Builder
	if userContext != "" {
		textContent.WriteString(userContext)
		textContent.WriteString("\n\n")
	}
	if textPrompt != "" {
		textContent.WriteString(textPrompt)
	}

	parts, err := c.buildVisionParts(ctx, textContent.String(), imageURLs)
	if err != nil {
		return nil, err
	}

	processedHistory, err := c.processMessages(ctx, history)
	if err != nil {
		return nil, fmt.Errorf("failed to process history messages: %w", err)
	}

	messages := make([]openai.ChatCompletionMessage, 0, len(processedHistory)+2)
	messages = append(messages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleSystem,
		Content: systemPrompt,
	})
	messages = append(messages, processedHistory...)
	messages = append(messages, openai.ChatCompletionMessage{
		Role:         openai.ChatMessageRoleUser,
		MultiContent: parts,
	})

	// Make initial request with tools and retry logic
	chatReq := openai.ChatCompletionRequest{
		Model:       c.config.VisionModel,
		Messages:    messages,
		MaxTokens:   c.config.MaxTokens,
		Temperature: c.config.Temperature,
		Tools:       tools,
	}

	resp, err := c.CreateChatCompletionWithRetry(ctx, chatReq, "vision+tools completion")
	if err != nil {
		return nil, err
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no response choices returned")
	}

	// Process tool calls iteratively
	maxIterations := c.config.MaxToolIterations
	currentMessages := messages

	for i := 0; i < maxIterations && len(resp.Choices[0].Message.ToolCalls) > 0; i++ {
		// Add assistant message with tool calls
		currentMessages = append(currentMessages, openai.ChatCompletionMessage{
			Role:             openai.ChatMessageRoleAssistant,
			Content:          resp.Choices[0].Message.Content,
			ReasoningContent: resp.Choices[0].Message.ReasoningContent,
			ToolCalls:        resp.Choices[0].Message.ToolCalls,
		})

		// Process each tool call
		for _, toolCall := range resp.Choices[0].Message.ToolCalls {
			// Execute tool
			result, err := toolHandler(ctx, toolCall)
			if err != nil {
				result = fmt.Sprintf("Error: %v", err)
			}

			// Add tool result message
			currentMessages = append(currentMessages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    result,
				ToolCallID: toolCall.ID,
			})
		}

		// Make follow-up request with retry logic
		chatReq.Messages = currentMessages
		chatReq.Tools = tools

		resp, err = c.CreateChatCompletionWithRetry(ctx, chatReq, "vision+tools follow-up")
		if err != nil {
			return nil, fmt.Errorf("tool iteration failed: %w", err)
		}

		if len(resp.Choices) == 0 {
			return nil, fmt.Errorf("no response choices returned after tool call")
		}
	}

	// Strip unprocessed tool calls if loop exhausted
	if len(resp.Choices[0].Message.ToolCalls) > 0 {
		logger.Warnf("[ai] vision tool loop exhausted with %d unprocessed tool calls", len(resp.Choices[0].Message.ToolCalls))
		resp.Choices[0].Message.ToolCalls = nil
	}

	return &ChatCompletionResponse{
		Content:          resp.Choices[0].Message.Content,
		ToolCalls:        resp.Choices[0].Message.ToolCalls,
		ReasoningContent: resp.Choices[0].Message.ReasoningContent,
		FinishReason:     string(resp.Choices[0].FinishReason),
		Usage:            resp.Usage,
	}, nil
}

// ToolHandler is a function that handles tool calls
type ToolHandler func(ctx context.Context, toolCall openai.ToolCall) (string, error)

// DownloadImage downloads an image and returns it as base64 data URL
func (c *Client) DownloadImage(ctx context.Context, url string) (string, error) {
	return c.fetchImageAsDataURL(ctx, url, imageDownloadOptions{
		RequireImageContentType: c.config.RequireImageContentType,
		MaxBytes:                int64(c.config.MaxImageBytes),
		UserAgent:               c.config.UserAgent,
	})
}

// CreateEmbedding creates an embedding for the given text
func (c *Client) CreateEmbedding(ctx context.Context, text string, model string) ([]float32, error) {
	if model == "" {
		return nil, fmt.Errorf("embedding model is required")
	}

	req := openai.EmbeddingRequest{
		Input: []string{text},
		Model: openai.EmbeddingModel(model),
	}

	// Apply extra parameters from config
	applyExtraParamsToStruct(&req, c.config.ExtraParams, "[ai]")

	resp, err := c.createEmbeddingWithRetry(ctx, req, "create embedding")
	if err != nil {
		return nil, err
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embedding data returned")
	}

	// Convert []float64 to []float32
	embedding := make([]float32, len(resp.Data[0].Embedding))
	for i, v := range resp.Data[0].Embedding {
		embedding[i] = float32(v)
	}

	return embedding, nil
}
