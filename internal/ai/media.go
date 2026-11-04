package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ezyapper/internal/httpcapture"
	"ezyapper/internal/logger"
	"ezyapper/internal/retry"

	"github.com/sashabaranov/go-openai"
)

// MediaPartKind is the content-part type of a media part in a chat request.
type MediaPartKind string

const (
	// PartImageURL is the OpenAI image_url part (http URL or data URI).
	PartImageURL MediaPartKind = "image_url"
	// PartAudioURL is the audio_url part (http URL or data URI). go-openai
	// has no type for it, which is why media parts are spliced in as JSON.
	PartAudioURL MediaPartKind = "audio_url"
	// PartVideoURL is the video_url part (http URL or data URI).
	PartVideoURL MediaPartKind = "video_url"
	// PartInputAudio is the OpenAI input_audio part (base64 data + format,
	// mp3 or wav only).
	PartInputAudio MediaPartKind = "input_audio"
)

// MediaPart is one media content part of a user message.
type MediaPart struct {
	Kind MediaPartKind
	// URL is an http(s) URL or a data URI (image_url/audio_url/video_url).
	URL string
	// Data and Format are used by input_audio (base64 payload, "mp3"/"wav").
	Data   string
	Format string
}

// JSON returns the part in OpenAI content-part form.
func (p MediaPart) JSON() map[string]any {
	switch p.Kind {
	case PartInputAudio:
		return map[string]any{"type": string(PartInputAudio), "input_audio": map[string]any{"data": p.Data, "format": p.Format}}
	case PartImageURL:
		return map[string]any{"type": string(PartImageURL), "image_url": map[string]any{"url": p.URL, "detail": "auto"}}
	default:
		return map[string]any{"type": string(p.Kind), string(p.Kind): map[string]any{"url": p.URL}}
	}
}

// DataURI builds a base64 data URI. Content-type parameters (such as
// "; codecs=opus") are dropped.
func DataURI(contentType string, data []byte) string {
	if i := strings.Index(contentType, ";"); i >= 0 {
		contentType = contentType[:i]
	}
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// mediaTargetIndex returns the index of the user message media parts are
// attached to: the last user message before the first assistant message, so
// a tool loop (assistant/tool turns, validation feedback) keeps the media on
// the message that carried it.
func mediaTargetIndex(msgs []any) int {
	target := -1
	for i, raw := range msgs {
		m, _ := raw.(map[string]any)
		role, _ := m["role"].(string)
		if role == openai.ChatMessageRoleAssistant {
			break
		}
		if role == openai.ChatMessageRoleUser {
			target = i
		}
	}
	return target
}

// spliceMediaParts marshals req (go-openai's own encoding) and appends parts
// to the content of the target user message.
func spliceMediaParts(req openai.ChatCompletionRequest, parts []MediaPart) ([]byte, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	if len(parts) == 0 {
		return body, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("re-read request: %w", err)
	}
	msgs, _ := doc["messages"].([]any)
	idx := mediaTargetIndex(msgs)
	if idx < 0 {
		return nil, fmt.Errorf("no user message to attach media to")
	}
	msg := msgs[idx].(map[string]any)
	var content []any
	switch c := msg["content"].(type) {
	case string:
		if c != "" {
			content = append(content, map[string]any{"type": "text", "text": c})
		}
	case []any:
		content = c
	}
	for _, p := range parts {
		content = append(content, p.JSON())
	}
	msg["content"] = content
	return json.Marshal(doc)
}

// createChatCompletionWithMediaOnce posts a chat completion whose user
// message carries media parts go-openai cannot express (audio_url,
// video_url, input_audio). Errors keep the HTTP status in their text so
// IsRetryableError classifies them like go-openai errors.
func (c *Client) createChatCompletionWithMediaOnce(ctx context.Context, req openai.ChatCompletionRequest, parts []MediaPart, operation string) (openai.ChatCompletionResponse, error) {
	var out openai.ChatCompletionResponse
	body, err := spliceMediaParts(req, parts)
	if err != nil {
		return out, err
	}
	attemptCtx, cancel := context.WithTimeout(ctx, time.Duration(c.config.Timeout)*time.Second)
	defer cancel()
	endpoint := strings.TrimRight(c.config.APIBaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return out, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.config.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}
	logger.Debugf("[ai] calling %s API with %d media part(s)...", operation, len(parts))
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if IsTimeoutLikeError(err) {
			c.closeIdleConnections()
		}
		return out, fmt.Errorf("%s request failed: %w", operation, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return out, fmt.Errorf("%s: read response: %w", operation, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(respBody))
		if len(msg) > 500 {
			msg = msg[:500] + "..."
		}
		return out, fmt.Errorf("%s: status code %d: %s", operation, resp.StatusCode, msg)
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return out, fmt.Errorf("%s: decode response: %w", operation, err)
	}
	return out, nil
}

func (c *Client) createChatCompletionWithMediaRetry(ctx context.Context, req openai.ChatCompletionRequest, parts []MediaPart, operation string) (openai.ChatCompletionResponse, error) {
	return retry.Retry(ctx, c.config.RetryCount, func(ctx context.Context) (openai.ChatCompletionResponse, error) {
		return c.createChatCompletionWithMediaOnce(ctx, req, parts, operation)
	},
		retry.WithBaseDelay(1*time.Second),
		retry.WithMaxDelay(30*time.Second),
		retry.WithErrorClassifier(IsRetryableError),
	)
}

// DescribeMedia asks the client's model to turn media into text: prompt plus
// the parts in one user message, no system prompt, with retries. An answer
// that only has reasoning (common for reasoning models that ran out of
// max_tokens) is reported as an error rather than an empty description.
func (c *Client) DescribeMedia(ctx context.Context, prompt string, parts ...MediaPart) (string, error) {
	req := openai.ChatCompletionRequest{
		Model:       c.config.Model,
		Messages:    []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: prompt}},
		MaxTokens:   c.config.MaxTokens,
		Temperature: c.config.Temperature,
	}
	ApplyExtraParams(&req, c.config.ExtraParams, "[media]")
	resp, err := c.createChatCompletionWithMediaRetry(ctx, req, parts, "media describe")
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no response choices returned")
	}
	choice := resp.Choices[0]
	content := strings.TrimSpace(choice.Message.Content)
	if content == "" {
		if choice.Message.ReasoningContent != "" || choice.FinishReason == openai.FinishReasonLength {
			return "", fmt.Errorf("model returned no content (finish_reason=%s); raise max_tokens for this modality", choice.FinishReason)
		}
		return "", fmt.Errorf("model returned empty content")
	}
	return content, nil
}

// CaptureLabelDownload labels media downloads in HTTP captures. They go
// through an AI client's HTTP client but are not model calls.
const CaptureLabelDownload = "download"

// FetchMedia downloads a media URL (https only, no private addresses, no
// redirects) and returns its bytes and content type. maxBytes <= 0 means no
// limit. A content type not starting with typePrefix (e.g. "audio/") is
// rejected when typePrefix is not empty and the server sent one.
func (c *Client) FetchMedia(ctx context.Context, rawURL string, maxBytes int64, typePrefix string) ([]byte, string, error) {
	if err := validateMediaURL(rawURL); err != nil {
		return nil, "", fmt.Errorf("invalid media URL: %w", err)
	}
	req, err := http.NewRequestWithContext(httpcapture.WithLabel(ctx, CaptureLabelDownload), http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	client := *c.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download media: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("download media: status %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if typePrefix != "" && ct != "" && !strings.HasPrefix(ct, typePrefix) && !strings.HasPrefix(ct, "application/octet-stream") {
		return nil, "", fmt.Errorf("unexpected content type %q (want %s*)", ct, typePrefix)
	}
	if maxBytes > 0 && resp.ContentLength > maxBytes {
		return nil, "", fmt.Errorf("media too large: %d bytes (limit %d)", resp.ContentLength, maxBytes)
	}
	reader := io.Reader(resp.Body)
	if maxBytes > 0 {
		reader = io.LimitReader(resp.Body, maxBytes+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", fmt.Errorf("read media: %w", err)
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return nil, "", fmt.Errorf("media too large: more than %d bytes", maxBytes)
	}
	return data, ct, nil
}

// HTTPClient returns the client's HTTP client (timeouts, capture and
// User-Agent applied), for callers that talk to related endpoints.
func (c *Client) HTTPClient() *http.Client { return c.httpClient }
