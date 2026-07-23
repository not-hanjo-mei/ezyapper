// Package ai provides AI/LLM integration using OpenAI-compatible API
package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"ezyapper/internal/ai/llmjson"
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
	httpTimeout := time.Duration(cfg.Timeout) * time.Second
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
	if !c.config.Vision.Base64 {
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

// visionMaxTokens returns the vision-specific MaxTokens, falling back to AI config.
func (c *Client) visionMaxTokens() int {
	if c.config.Vision.MaxTokens > 0 {
		return c.config.Vision.MaxTokens
	}
	return c.config.MaxTokens
}

// visionTemperature returns the vision-specific temperature, falling back to AI config.
func (c *Client) visionTemperature() float32 {
	if c.config.Vision.Temperature > 0 {
		return c.config.Vision.Temperature
	}
	return c.config.Temperature
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

func (c *Client) CreateChatCompletionOnce(ctx context.Context, req openai.ChatCompletionRequest, operation string) (openai.ChatCompletionResponse, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, time.Duration(c.config.Timeout)*time.Second)
	defer cancel()
	logger.Debugf("[ai] calling %s API (once)...", operation)
	resp, err := c.client.CreateChatCompletion(attemptCtx, req)
	if err != nil && IsTimeoutLikeError(err) {
		c.closeIdleConnections()
	}
	return resp, err
}

// CreateChatCompletionWithRetry sends a chat completion request with automatic retry on failures.
func (c *Client) CreateChatCompletionWithRetry(ctx context.Context, req openai.ChatCompletionRequest, operation string) (openai.ChatCompletionResponse, error) {
	return retry.Retry(ctx, c.config.RetryCount, func(ctx context.Context) (openai.ChatCompletionResponse, error) {
		return c.CreateChatCompletionOnce(ctx, req, operation)
	},
		retry.WithBaseDelay(1*time.Second),
		retry.WithMaxDelay(30*time.Second),
		retry.WithErrorClassifier(IsRetryableError),
	)
}

func (c *Client) createEmbeddingWithRetry(ctx context.Context, req openai.EmbeddingRequest, operation string) (openai.EmbeddingResponse, error) {
	return retry.Retry(ctx, c.config.RetryCount, func(ctx context.Context) (openai.EmbeddingResponse, error) {
		attemptCtx, cancel := context.WithTimeout(ctx, time.Duration(c.config.Timeout)*time.Second)
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
		retry.WithErrorClassifier(IsRetryableError),
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
		if !strings.HasPrefix(url, "data:image") && c.config.Vision.Base64 {
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

func (c *Client) buildChatRequest(ctx context.Context, req ChatCompletionRequest) (openai.ChatCompletionRequest, error) {
	messages := make([]openai.ChatCompletionMessage, 0, len(req.Messages)+1)

	if req.SystemPrompt != "" {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: req.SystemPrompt,
		})
	}

	processedMessages, err := c.processMessages(ctx, req.Messages)
	if err != nil {
		return openai.ChatCompletionRequest{}, err
	}

	if req.UserContext != "" && len(processedMessages) > 0 {
		for i := len(processedMessages) - 1; i >= 0; i-- {
			if processedMessages[i].Role == openai.ChatMessageRoleUser {
				processedMessages[i].Content = req.UserContext + "\n\n" + processedMessages[i].Content
				break
			}
		}
	}

	messages = append(messages, processedMessages...)

	chatReq := openai.ChatCompletionRequest{
		Model:       c.config.Model,
		Messages:    messages,
		MaxTokens:   c.config.MaxTokens,
		Temperature: c.config.Temperature,
	}

	if len(req.Tools) > 0 {
		chatReq.Tools = req.Tools
	}

	c.applyExtraParams(&chatReq)
	return chatReq, nil
}

func chatResponseFromOpenAI(resp openai.ChatCompletionResponse) (*ChatCompletionResponse, error) {
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

func (c *Client) CreateChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	chatReq, err := c.buildChatRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	logger.Debugf("[ai] creating chat completion:")
	logger.Debugf("  Model: %s", c.config.Model)
	logger.Debugf("  Messages: %d", len(chatReq.Messages))
	logger.Debugf("  System prompt length: %d", len(req.SystemPrompt))
	logger.Debugf("  Tools: %d", len(chatReq.Tools))

	resp, err := c.CreateChatCompletionWithRetry(ctx, chatReq, "llm completion")
	if err != nil {
		return nil, err
	}
	return chatResponseFromOpenAI(resp)
}

func (c *Client) createChatCompletionOnce(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	chatReq, err := c.buildChatRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	resp, err := c.CreateChatCompletionOnce(ctx, chatReq, "llm completion")
	if err != nil {
		return nil, err
	}
	return chatResponseFromOpenAI(resp)
}

func (c *Client) CreateChatCompletionSingle(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	return c.createChatCompletionOnce(ctx, req)
}

// applyExtraParams applies extra parameters from config to the request
func (c *Client) applyExtraParams(req *openai.ChatCompletionRequest) {
	ApplyExtraParams(req, c.config.ExtraParams, "[ai]")
}

// ApplyExtraParams applies extra parameters to a ChatCompletionRequest using reflection.
// This is a package-level function that can be used by other components.
// prefix is used for logging (e.g., "[decision]", "[vision]")
func ApplyExtraParams(req *openai.ChatCompletionRequest, extraParams map[string]any, logPrefix string) {
	applyExtraParamsToStruct(req, extraParams, logPrefix)
}

// applyExtraParamsToStruct applies extra parameters to any struct using reflection.
// This generic version works with any struct (ChatCompletionRequest, EmbeddingRequest, etc.)
func applyExtraParamsToStruct(req any, extraParams map[string]any, logPrefix string) {
	if len(extraParams) == 0 {
		return
	}

	v := reflect.ValueOf(req)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		logger.Warnf("%s extra params: invalid request type %T", logPrefix, req)
		return
	}
	reqValue := v.Elem()
	reqType := reqValue.Type()

	for key, value := range extraParams {
		fieldIndex := findFieldIndexByJSONTag(reqType, key)
		if fieldIndex < 0 {
			logger.Warnf("%s extra param '%s' not found in request struct", logPrefix, key)
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
func setFieldValue(field reflect.Value, value any) error {
	if value == nil {
		return nil
	}

	valReflect := reflect.ValueOf(value)

	if valReflect.Type().ConvertibleTo(field.Type()) {
		field.Set(valReflect.Convert(field.Type()))
		return nil
	}

	if field.Kind() == reflect.Pointer {
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
}

func (c *Client) fetchImageAsDataURL(ctx context.Context, url string, opts imageDownloadOptions) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	httpClient := c.httpClient
	if httpClient == nil {
		timeout := 30 * time.Second
		if c.config != nil && c.config.Timeout > 0 {
			timeout = time.Duration(c.config.Timeout) * time.Second
		}
		logger.Warnf("[ai] fetchImageAsDataURL: httpClient is nil, this should not happen (check NewClient initialization)")
		httpClient = &http.Client{Timeout: timeout}
	}
	// Create a copy with redirects disabled to prevent SSRF via redirect chains
	imageClient := *httpClient
	imageClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	resp, err := imageClient.Do(req)
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
		Model:       c.config.Vision.Model,
		Messages:    messages,
		MaxTokens:   c.visionMaxTokens(),
		Temperature: c.visionTemperature(),
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

type toolLoopResponse struct {
	content          string
	reasoningContent string
	toolCalls        []openai.ToolCall
	finishReason     string
	usage            openai.Usage
}

type toolLoopRequester func(ctx context.Context, messages []openai.ChatCompletionMessage) (*toolLoopResponse, error)

func validateToolCallsArgs(registry *tools.ToolRegistry, toolCalls []openai.ToolCall) error {
	if registry == nil {
		return nil
	}
	for _, tc := range toolCalls {
		var args map[string]any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return llmjson.ParseError(fmt.Sprintf("tool %q arguments", tc.Function.Name), err)
		}
		if args == nil {
			args = map[string]any{}
		}
		tool, ok := registry.Get(tc.Function.Name)
		if !ok {
			continue
		}
		if err := llmjson.ValidateArgsAgainstParameters(args, tool.Parameters); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) requestCompletionSlot(ctx context.Context, baseMessages []openai.ChatCompletionMessage, request toolLoopRequester) (*toolLoopResponse, error) {
	messages := make([]openai.ChatCompletionMessage, len(baseMessages))
	copy(messages, baseMessages)
	var feedback string
	var lastErr error

	for attempt := 0; attempt <= c.config.RetryCount; attempt++ {
		msgs := make([]openai.ChatCompletionMessage, len(messages))
		copy(msgs, messages)
		if feedback != "" {
			msgs = append(msgs, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleUser,
				Content: feedback,
			})
		}

		resp, err := request(ctx, msgs)
		if err != nil {
			lastErr = err
			if !IsRetryableError(err) {
				return nil, err
			}
			logger.Warnf("[ai] completion slot network failure attempt %d/%d: %v", attempt+1, c.config.RetryCount+1, err)
			continue
		}
		if len(resp.toolCalls) == 0 {
			return resp, nil
		}
		if err := validateToolCallsArgs(c.toolRegistry, resp.toolCalls); err != nil {
			lastErr = err
			feedback = "Your previous tool_calls failed argument validation: " + err.Error() +
				". Re-issue valid tool_calls only with correct JSON arguments matching each tool schema."
			logger.Warnf("[ai] tool arg schema failure attempt %d/%d: %v", attempt+1, c.config.RetryCount+1, err)
			continue
		}
		return resp, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("completion slot exhausted")
	}
	return nil, fmt.Errorf("completion slot exhausted after %d attempts: %w", c.config.RetryCount+1, lastErr)
}

func (c *Client) runToolLoop(ctx context.Context, initialMessages []openai.ChatCompletionMessage, maxIterations int, toolHandler ToolHandler, request toolLoopRequester, toolError func(error) string, warnLabel string) (*toolLoopResponse, error) {
	messages := make([]openai.ChatCompletionMessage, len(initialMessages))
	copy(messages, initialMessages)

	resp, err := c.requestCompletionSlot(ctx, messages, request)
	if err != nil {
		return nil, err
	}

	for i := 0; i < maxIterations && len(resp.toolCalls) > 0; i++ {
		toolNames := make([]string, len(resp.toolCalls))
		for j, tc := range resp.toolCalls {
			toolNames[j] = tc.Function.Name
		}
		logger.Debugf("[ai] tool loop iteration %d/%d: executing %d tool calls: %s", i+1, maxIterations, len(resp.toolCalls), strings.Join(toolNames, ", "))

		messages = append(messages, openai.ChatCompletionMessage{
			Role:             openai.ChatMessageRoleAssistant,
			Content:          resp.content,
			ReasoningContent: resp.reasoningContent,
			ToolCalls:        resp.toolCalls,
		})

		for _, toolCall := range resp.toolCalls {
			start := time.Now()
			result, err := toolHandler(ctx, toolCall)
			elapsed := time.Since(start)
			if err != nil {
				logger.Errorf("[ai] tool call failed for %s after %v: %v", toolCall.Function.Name, elapsed, err)
				result = toolError(err)
				if strings.TrimSpace(result) == "" {
					result = "Error: tool execution failed"
				}
			} else {
				logger.Debugf("[ai] tool %s returned %d bytes in %v", toolCall.Function.Name, len(result), elapsed)
			}

			messages = append(messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    result,
				ToolCallID: toolCall.ID,
			})
		}

		resp, err = c.requestCompletionSlot(ctx, messages, request)
		if err != nil {
			return nil, err
		}
	}

	if len(resp.toolCalls) > 0 {
		logger.Warnf("[ai] %s loop exhausted with %d unprocessed tool calls", warnLabel, len(resp.toolCalls))
		resp.toolCalls = nil
	}

	return resp, nil
}

func (c *Client) executeToolLoop(ctx context.Context, messages []openai.ChatCompletionMessage, request toolLoopRequester, toolHandler ToolHandler, warnLabel string) (*ChatCompletionResponse, error) {
	final, err := c.runToolLoop(ctx, messages, c.config.MaxToolIterations, toolHandler, request, func(err error) string {
		return fmt.Sprintf("Error: %v", err)
	}, warnLabel)
	if err != nil {
		return nil, err
	}
	return &ChatCompletionResponse{
		Content:          final.content,
		ToolCalls:        final.toolCalls,
		ReasoningContent: final.reasoningContent,
		FinishReason:     final.finishReason,
		Usage:            final.usage,
	}, nil
}

func (c *Client) CreateChatCompletionWithTools(ctx context.Context, req ChatCompletionRequest, toolHandler ToolHandler) (*ChatCompletionResponse, error) {
	toolDefs := c.toolRegistry.GetTools()
	req.Tools = toolDefs

	request := func(ctx context.Context, messages []openai.ChatCompletionMessage) (*toolLoopResponse, error) {
		reqCopy := req
		reqCopy.Messages = messages
		reqCopy.Tools = toolDefs
		next, err := c.createChatCompletionOnce(ctx, reqCopy)
		if err != nil {
			return nil, err
		}
		return &toolLoopResponse{
			content:          next.Content,
			reasoningContent: next.ReasoningContent,
			toolCalls:        next.ToolCalls,
			finishReason:     next.FinishReason,
			usage:            next.Usage,
		}, nil
	}

	return c.executeToolLoop(ctx, req.Messages, request, toolHandler, "tool")
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

	chatReq := openai.ChatCompletionRequest{
		Model:       c.config.Vision.Model,
		Messages:    messages,
		MaxTokens:   c.visionMaxTokens(),
		Temperature: c.visionTemperature(),
		Tools:       tools,
	}

	request := func(ctx context.Context, msgs []openai.ChatCompletionMessage) (*toolLoopResponse, error) {
		reqCopy := chatReq
		reqCopy.Messages = msgs
		reqCopy.Tools = tools
		next, err := c.CreateChatCompletionOnce(ctx, reqCopy, "vision+tools")
		if err != nil {
			return nil, fmt.Errorf("tool iteration failed: %w", err)
		}
		if len(next.Choices) == 0 {
			return nil, fmt.Errorf("no response choices returned after tool call")
		}
		return &toolLoopResponse{
			content:          next.Choices[0].Message.Content,
			reasoningContent: next.Choices[0].Message.ReasoningContent,
			toolCalls:        next.Choices[0].Message.ToolCalls,
			finishReason:     string(next.Choices[0].FinishReason),
			usage:            next.Usage,
		}, nil
	}

	return c.executeToolLoop(ctx, messages, request, toolHandler, "vision tool")
}

// ToolHandler is a function that handles tool calls
type ToolHandler func(ctx context.Context, toolCall openai.ToolCall) (string, error)

// DownloadImage downloads an image and returns it as base64 data URL
func (c *Client) DownloadImage(ctx context.Context, url string) (string, error) {
	return c.fetchImageAsDataURL(ctx, url, imageDownloadOptions{
		RequireImageContentType: c.config.RequireImageContentType,
		MaxBytes:                int64(c.config.MaxImageBytes),
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
