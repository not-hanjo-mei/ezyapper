package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ezyapper/internal/plugin"
	pluginserver "ezyapper/internal/plugin/server"
	"ezyapper/internal/types"

	"gopkg.in/yaml.v3"
)

var supportedAudioFormats = map[string]string{
	"mp3":  "mp3",
	"opus": "opus",
	"aac":  "aac",
	"flac": "flac",
	"wav":  "wav",
	"pcm":  "pcm",
}

type ttsConfig struct {
	BaseURL                    string
	APIKey                     string
	Model                      string
	Voice                      string
	Format                     string
	Speed                      float64
	TimeoutSeconds             int
	OutputDir                  string
	MaxTextChars               int
	UploadMemoryThresholdBytes int
	Rewriter                   ttsRewriterConfig
	Headers                    map[string]string
}

type ttsRewriterConfig struct {
	Enabled        bool
	BaseURL        string
	APIKey         string
	Model          string
	TimeoutSeconds int
	RetryCount     int
	MaxTokens      int
	Temperature    float64
	Prompt         string
}

type ttsConfigFile struct {
	BaseURL                    *string           `yaml:"api_base_url"`
	APIKey                     *string           `yaml:"api_key"`
	Model                      *string           `yaml:"model"`
	Voice                      *string           `yaml:"voice"`
	Format                     *string           `yaml:"format"`
	Speed                      *float64          `yaml:"speed"`
	TimeoutSeconds             *int              `yaml:"timeout_seconds"`
	OutputDir                  *string           `yaml:"output_dir"`
	MaxTextChars               *int              `yaml:"max_text_chars"`
	UploadMemoryThreshold      *string           `yaml:"upload_memory_threshold"`
	UploadMemoryThresholdBytes *int              `yaml:"upload_memory_threshold_bytes"`
	Rewriter                   ttsRewriterFile   `yaml:"rewriter"`
	Headers                    map[string]string `yaml:"headers"`
}

type ttsRewriterFile struct {
	Enabled        *bool    `yaml:"enabled"`
	APIBaseURL     *string  `yaml:"api_base_url"`
	APIKey         *string  `yaml:"api_key"`
	Model          *string  `yaml:"model"`
	TimeoutSeconds *int     `yaml:"timeout_seconds"`
	RetryCount     *int     `yaml:"retry_count"`
	MaxTokens      *int     `yaml:"max_tokens"`
	Temperature    *float64 `yaml:"temperature"`
	Prompt         *string  `yaml:"prompt"`
}

type speechRequest struct {
	Model          string  `json:"model"`
	Input          string  `json:"input"`
	Voice          string  `json:"voice"`
	ResponseFormat string  `json:"response_format,omitempty"`
	Speed          float64 `json:"speed,omitempty"`
	Instructions   *string `json:"instructions,omitempty"`
}

type rewriteRequestPayload struct {
	Input        string `json:"input"`
	Instructions string `json:"instructions"`
}

type chatCompletionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionRequest struct {
	Model       string                  `json:"model"`
	Messages    []chatCompletionMessage `json:"messages"`
	Temperature float64                 `json:"temperature,omitempty"`
	MaxTokens   int                     `json:"max_tokens,omitempty"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message chatCompletionMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type rewriteResult struct {
	Input        *string `json:"input"`
	Instructions *string `json:"instructions"`
}

type openAITTSPlugin struct {
	cfg        ttsConfig
	httpClient *http.Client
	outputDir  string
}

func pluginRuntimePath() string {
	if dir := strings.TrimSpace(os.Getenv("EZYAPPER_PLUGIN_PATH")); dir != "" {
		return dir
	}

	return "."
}

func pluginConfigPath() string {
	if cfg := strings.TrimSpace(os.Getenv("EZYAPPER_PLUGIN_CONFIG")); cfg != "" {
		return cfg
	}

	return filepath.Join(pluginRuntimePath(), "config.yaml")
}

func loadConfig() (ttsConfig, error) {
	configPath := pluginConfigPath()

	content, err := os.ReadFile(configPath)
	if err != nil {
		return ttsConfig{}, fmt.Errorf("failed to read plugin config file %s: %w", configPath, err)
	}
	if len(bytes.TrimSpace(content)) == 0 {
		return ttsConfig{}, fmt.Errorf("plugin config file %s is empty", configPath)
	}

	var raw ttsConfigFile
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		return ttsConfig{}, fmt.Errorf("invalid plugin config file %s: %w", configPath, err)
	}

	cfg := ttsConfig{
		Headers: map[string]string{},
	}

	var errs []string

	if raw.BaseURL == nil {
		errs = append(errs, "api_base_url is required")
	} else {
		cfg.BaseURL = strings.TrimRight(strings.TrimSpace(*raw.BaseURL), "/")
		if cfg.BaseURL == "" {
			errs = append(errs, "api_base_url is required")
		}
	}

	if raw.APIKey == nil {
		errs = append(errs, "api_key is required")
	} else {
		cfg.APIKey = strings.TrimSpace(*raw.APIKey)
		if cfg.APIKey == "" {
			errs = append(errs, "api_key is required")
		}
	}

	if raw.Model == nil {
		errs = append(errs, "model is required")
	} else {
		cfg.Model = strings.TrimSpace(*raw.Model)
		if cfg.Model == "" {
			errs = append(errs, "model is required")
		}
	}

	if raw.Voice == nil {
		errs = append(errs, "voice is required")
	} else {
		cfg.Voice = strings.TrimSpace(*raw.Voice)
		if cfg.Voice == "" {
			errs = append(errs, "voice is required")
		}
	}

	if raw.Format == nil {
		errs = append(errs, "format is required")
	} else {
		cfg.Format = strings.ToLower(strings.TrimSpace(*raw.Format))
		if cfg.Format == "" {
			errs = append(errs, "format is required")
		}
	}

	if raw.Speed == nil {
		errs = append(errs, "speed is required")
	} else {
		cfg.Speed = *raw.Speed
	}

	if raw.TimeoutSeconds == nil {
		errs = append(errs, "timeout_seconds is required")
	} else {
		cfg.TimeoutSeconds = *raw.TimeoutSeconds
	}

	if raw.MaxTextChars == nil {
		errs = append(errs, "max_text_chars is required")
	} else {
		cfg.MaxTextChars = *raw.MaxTextChars
	}

	if raw.UploadMemoryThreshold != nil {
		sizeBytes, parseErr := parseSizeToBytes(*raw.UploadMemoryThreshold)
		if parseErr != nil {
			errs = append(errs, fmt.Sprintf("upload_memory_threshold is invalid: %v", parseErr))
		} else {
			cfg.UploadMemoryThresholdBytes = sizeBytes
		}
	} else if raw.UploadMemoryThresholdBytes != nil {
		cfg.UploadMemoryThresholdBytes = *raw.UploadMemoryThresholdBytes
	} else {
		errs = append(errs, "upload_memory_threshold is required (for example: 1MB)")
	}

	if raw.OutputDir == nil {
		errs = append(errs, "output_dir is required")
	} else {
		cfg.OutputDir = strings.TrimSpace(*raw.OutputDir)
		if cfg.OutputDir == "" {
			errs = append(errs, "output_dir is required")
		}
	}

	if raw.Rewriter.Enabled == nil {
		errs = append(errs, "rewriter.enabled is required (explicit true or false)")
	} else {
		cfg.Rewriter.Enabled = *raw.Rewriter.Enabled
	}

	if raw.Headers != nil {
		cfg.Headers = make(map[string]string, len(raw.Headers))
		for key, value := range raw.Headers {
			trimmedKey := strings.TrimSpace(key)
			if trimmedKey == "" {
				continue
			}
			cfg.Headers[trimmedKey] = value
		}
	}

	if cfg.TimeoutSeconds <= 0 {
		errs = append(errs, "timeout_seconds must be a positive integer")
	}
	if cfg.MaxTextChars <= 0 {
		errs = append(errs, "max_text_chars must be a positive integer")
	}
	if cfg.UploadMemoryThresholdBytes <= 0 {
		errs = append(errs, "upload_memory_threshold must resolve to a positive size")
	}
	if cfg.Speed <= 0 || cfg.Speed > 4 {
		errs = append(errs, "speed must be in range (0, 4]")
	}
	if _, ok := supportedAudioFormats[cfg.Format]; !ok && cfg.Format != "" {
		errs = append(errs, "format must be one of: mp3, opus, aac, flac, wav, pcm")
	}

	for key := range cfg.Headers {
		if strings.EqualFold(key, "Authorization") {
			errs = append(errs, "headers must not override Authorization")
			break
		}
	}

	if cfg.Rewriter.Enabled {
		if raw.Rewriter.APIBaseURL == nil {
			errs = append(errs, "rewriter.api_base_url is required when rewriter is enabled")
		} else {
			cfg.Rewriter.BaseURL = strings.TrimRight(strings.TrimSpace(*raw.Rewriter.APIBaseURL), "/")
			if cfg.Rewriter.BaseURL == "" {
				errs = append(errs, "rewriter.api_base_url is required when rewriter is enabled")
			}
		}

		if raw.Rewriter.APIKey == nil {
			errs = append(errs, "rewriter.api_key is required when rewriter is enabled")
		} else {
			cfg.Rewriter.APIKey = strings.TrimSpace(*raw.Rewriter.APIKey)
			if cfg.Rewriter.APIKey == "" {
				errs = append(errs, "rewriter.api_key is required when rewriter is enabled")
			}
		}

		if raw.Rewriter.Model == nil {
			errs = append(errs, "rewriter.model is required when rewriter is enabled")
		} else {
			cfg.Rewriter.Model = strings.TrimSpace(*raw.Rewriter.Model)
			if cfg.Rewriter.Model == "" {
				errs = append(errs, "rewriter.model is required when rewriter is enabled")
			}
		}

		if raw.Rewriter.TimeoutSeconds == nil {
			errs = append(errs, "rewriter.timeout_seconds is required when rewriter is enabled")
		} else {
			cfg.Rewriter.TimeoutSeconds = *raw.Rewriter.TimeoutSeconds
		}

		if raw.Rewriter.RetryCount == nil {
			errs = append(errs, "rewriter.retry_count is required when rewriter is enabled")
		} else {
			cfg.Rewriter.RetryCount = *raw.Rewriter.RetryCount
		}

		if raw.Rewriter.MaxTokens == nil {
			errs = append(errs, "rewriter.max_tokens is required when rewriter is enabled")
		} else {
			cfg.Rewriter.MaxTokens = *raw.Rewriter.MaxTokens
		}

		if raw.Rewriter.Temperature == nil {
			errs = append(errs, "rewriter.temperature is required when rewriter is enabled")
		} else {
			cfg.Rewriter.Temperature = *raw.Rewriter.Temperature
		}

		if raw.Rewriter.Prompt == nil {
			errs = append(errs, "rewriter.prompt is required when rewriter is enabled")
		} else {
			cfg.Rewriter.Prompt = strings.TrimSpace(*raw.Rewriter.Prompt)
			if cfg.Rewriter.Prompt == "" {
				errs = append(errs, "rewriter.prompt is required when rewriter is enabled")
			}
		}

		if cfg.Rewriter.TimeoutSeconds <= 0 {
			errs = append(errs, "rewriter.timeout_seconds must be a positive integer")
		}
		if cfg.Rewriter.RetryCount < 0 {
			errs = append(errs, "rewriter.retry_count must be >= 0")
		}
		if cfg.Rewriter.MaxTokens <= 0 {
			errs = append(errs, "rewriter.max_tokens must be a positive integer")
		}
		if cfg.Rewriter.Temperature < 0 || cfg.Rewriter.Temperature > 2 {
			errs = append(errs, "rewriter.temperature must be between 0 and 2")
		}
	}

	if len(errs) > 0 {
		return ttsConfig{}, fmt.Errorf("configuration errors in %s: %s", configPath, strings.Join(errs, "; "))
	}

	return cfg, nil
}

func newOpenAITTSPlugin(cfg ttsConfig) (*openAITTSPlugin, error) {
	pluginRoot, err := filepath.Abs(pluginRuntimePath())
	if err != nil {
		return nil, fmt.Errorf("failed to resolve plugin path: %w", err)
	}

	outputDir := cfg.OutputDir
	if !filepath.IsAbs(outputDir) {
		outputDir = filepath.Join(pluginRoot, outputDir)
	}

	outputDir, err = filepath.Abs(filepath.Clean(outputDir))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve output dir: %w", err)
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create output dir %s: %w", outputDir, err)
	}

	return &openAITTSPlugin{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second,
		},
		outputDir: outputDir,
	}, nil
}

func (p *openAITTSPlugin) Info() (plugin.Info, error) {
	return plugin.Info{
		Name:        "openai-tts",
		Version:     "0.0.0",
		Author:      "EZyapper",
		Description: "OpenAI-compatible text-to-speech tool plugin",
		Priority:    20,
	}, nil
}

func (p *openAITTSPlugin) OnMessage(msg types.DiscordMessage) (bool, error) {
	return true, nil
}

func (p *openAITTSPlugin) Shutdown() error {
	p.httpClient.CloseIdleConnections()
	return nil
}

// beforeSendSafetyMargin is reserved from the host's before_send deadline so
// the reply (possibly without audio) reaches the host before it gives up on
// the plugin and restarts it.
const beforeSendSafetyMargin = 2 * time.Second

func (p *openAITTSPlugin) BeforeSend(args plugin.BeforeSendArgs) (plugin.BeforeSendResult, error) {
	msg, response := args.Message, args.Response
	ctx := context.Background()
	if args.DeadlineUnixMs > 0 {
		deadline := time.UnixMilli(args.DeadlineUnixMs).Add(-beforeSendSafetyMargin)
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}
	text := strings.TrimSpace(response)
	if text == "" {
		return plugin.BeforeSendResult{}, nil
	}

	fileName := fmt.Sprintf("discord_%s_%s", msg.ChannelID, msg.ID)
	generatedFile, err := p.synthesizeForBeforeSend(
		ctx,
		text,
		p.cfg.Model,
		p.cfg.Voice,
		p.cfg.Format,
		p.cfg.Speed,
		"",
		fileName,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[OPENAI-TTS] before_send tts skipped: %v\n", err)
		return plugin.BeforeSendResult{}, nil
	}

	return plugin.BeforeSendResult{
		Files: []plugin.LocalFile{generatedFile},
	}, nil
}

func (p *openAITTSPlugin) synthesizeForBeforeSend(
	ctx context.Context,
	text string,
	model string,
	voice string,
	format string,
	speed float64,
	instructions string,
	fileName string,
) (plugin.LocalFile, error) {
	rewrittenText, rewrittenInstructions, _, includeInstructions, err := p.maybeRewriteInputAndInstructions(ctx, text, instructions)
	if err != nil {
		return plugin.LocalFile{}, err
	}

	if textLen := len([]rune(rewrittenText)); textLen > p.cfg.MaxTextChars {
		return plugin.LocalFile{}, fmt.Errorf("text exceeds max_text_chars (%d > %d)", textLen, p.cfg.MaxTextChars)
	}

	audioData, contentType, _, err := p.requestSpeech(
		ctx,
		model,
		voice,
		format,
		speed,
		rewrittenText,
		rewrittenInstructions,
		includeInstructions,
	)
	if err != nil {
		return plugin.LocalFile{}, err
	}

	uploadName := buildAudioFileName(fileName, format)
	if len(audioData) <= p.cfg.UploadMemoryThresholdBytes {
		return plugin.LocalFile{
			Name:        uploadName,
			ContentType: contentType,
			Data:        audioData,
		}, nil
	}

	tempPath, err := p.writeTempUploadFile(uploadName, audioData)
	if err != nil {
		return plugin.LocalFile{}, err
	}

	return plugin.LocalFile{
		Path:              tempPath,
		Name:              filepath.Base(tempPath),
		ContentType:       contentType,
		DeleteAfterUpload: true,
	}, nil
}

func (p *openAITTSPlugin) maybeRewriteInputAndInstructions(
	parent context.Context,
	text string,
	instructions string,
) (string, string, bool, bool, error) {
	originalText := strings.TrimSpace(text)
	originalInstructions := strings.TrimSpace(instructions)

	if !p.cfg.Rewriter.Enabled {
		return originalText, originalInstructions, false, originalInstructions != "", nil
	}

	var lastErr error
	for attempt := 0; attempt <= p.cfg.Rewriter.RetryCount; attempt++ {
		if parent.Err() != nil {
			lastErr = parent.Err()
			break
		}
		rewrittenText, rewrittenInstructions, includeInstructions, err := p.rewriteInputAndInstructionsOnce(parent, originalText, originalInstructions)
		if err == nil {
			rewritten := rewrittenText != originalText || rewrittenInstructions != originalInstructions
			if includeInstructions != (originalInstructions != "") {
				rewritten = true
			}
			return rewrittenText, rewrittenInstructions, rewritten, includeInstructions, nil
		}

		lastErr = err
		if attempt < p.cfg.Rewriter.RetryCount {
			time.Sleep(time.Duration(attempt+1) * 300 * time.Millisecond)
		}
	}

	fmt.Fprintf(os.Stderr, "[OPENAI-TTS] rewriter failed after %d attempts, using original text: %v\n", p.cfg.Rewriter.RetryCount+1, lastErr)
	return originalText, originalInstructions, false, originalInstructions != "", nil
}

func (p *openAITTSPlugin) rewriteInputAndInstructionsOnce(
	parent context.Context,
	text string,
	instructions string,
) (string, string, bool, error) {
	payload := rewriteRequestPayload{
		Input:        text,
		Instructions: instructions,
	}

	payloadText, err := json.Marshal(payload)
	if err != nil {
		return "", "", false, fmt.Errorf("failed to encode rewriter payload: %w", err)
	}

	requestBody := chatCompletionRequest{
		Model: p.cfg.Rewriter.Model,
		Messages: []chatCompletionMessage{
			{Role: "system", Content: p.cfg.Rewriter.Prompt},
			{Role: "user", Content: string(payloadText)},
		},
		Temperature: p.cfg.Rewriter.Temperature,
		MaxTokens:   p.cfg.Rewriter.MaxTokens,
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return "", "", false, fmt.Errorf("failed to encode rewriter request: %w", err)
	}

	endpoint := joinURL(p.cfg.Rewriter.BaseURL, "chat/completions")
	ctx, cancel := context.WithTimeout(parent, time.Duration(p.cfg.Rewriter.TimeoutSeconds)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", "", false, fmt.Errorf("failed to create rewriter request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.cfg.Rewriter.APIKey)
	req.Header.Set("Content-Type", "application/json")
	for key, value := range p.cfg.Headers {
		req.Header.Set(key, value)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", "", false, fmt.Errorf("rewriter request failed: %w", err)
	}
	defer resp.Body.Close()

	responseBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", false, fmt.Errorf("failed to read rewriter response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", false, fmt.Errorf(
			"rewriter request failed: status=%d body=%s",
			resp.StatusCode,
			truncateString(strings.TrimSpace(string(responseBytes)), 1000),
		)
	}

	var completion chatCompletionResponse
	if err := json.Unmarshal(responseBytes, &completion); err != nil {
		return "", "", false, fmt.Errorf("failed to parse rewriter response: %w", err)
	}

	if completion.Error != nil && strings.TrimSpace(completion.Error.Message) != "" {
		return "", "", false, fmt.Errorf("rewriter API error: %s", completion.Error.Message)
	}

	if len(completion.Choices) == 0 {
		return "", "", false, fmt.Errorf("rewriter returned no choices")
	}

	content := strings.TrimSpace(completion.Choices[0].Message.Content)
	if content == "" {
		return "", "", false, fmt.Errorf("rewriter returned empty content")
	}

	jsonContent, err := extractJSONObject(content)
	if err != nil {
		return "", "", false, fmt.Errorf("rewriter output must be valid json object: %w", err)
	}

	var rewritten rewriteResult
	decoder := json.NewDecoder(strings.NewReader(jsonContent))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&rewritten); err != nil {
		return "", "", false, fmt.Errorf("failed to parse rewriter output json: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", "", false, fmt.Errorf("rewriter output must contain exactly one json object")
	}

	if rewritten.Input == nil {
		return "", "", false, fmt.Errorf("rewriter output schema mismatch: required key input")
	}

	rewrittenInput := strings.TrimSpace(*rewritten.Input)
	if rewrittenInput == "" {
		return "", "", false, fmt.Errorf("rewriter output input cannot be empty")
	}

	rewrittenInstructions := ""
	includeInstructions := false
	if rewritten.Instructions != nil {
		rewrittenInstructions = strings.TrimSpace(*rewritten.Instructions)
		includeInstructions = true
	}

	return rewrittenInput, rewrittenInstructions, includeInstructions, nil
}

func extractJSONObject(content string) (string, error) {
	trimmed := strings.TrimSpace(content)

	if strings.HasPrefix(trimmed, "```") {
		firstLineEnd := strings.Index(trimmed, "\n")
		lastFence := strings.LastIndex(trimmed, "```")
		if firstLineEnd > 0 && lastFence > firstLineEnd {
			trimmed = strings.TrimSpace(trimmed[firstLineEnd:lastFence])
		}
	}

	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end <= start {
		return "", fmt.Errorf("json object not found")
	}

	return strings.TrimSpace(trimmed[start : end+1]), nil
}

func (p *openAITTSPlugin) requestSpeech(
	parent context.Context,
	model string,
	voice string,
	format string,
	speed float64,
	text string,
	instructions string,
	includeInstructions bool,
) ([]byte, string, string, error) {
	payload := speechRequest{
		Model:          model,
		Input:          text,
		Voice:          voice,
		ResponseFormat: format,
		Speed:          speed,
	}
	if includeInstructions {
		normalized := strings.TrimSpace(instructions)
		payload.Instructions = &normalized
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to encode request payload: %w", err)
	}

	endpoint := joinURL(p.cfg.BaseURL, "audio/speech")
	ctx, cancel := context.WithTimeout(parent, time.Duration(p.cfg.TimeoutSeconds)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, "", endpoint, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	for key, value := range p.cfg.Headers {
		req.Header.Set(key, value)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, "", endpoint, fmt.Errorf("tts request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", endpoint, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", endpoint, fmt.Errorf(
			"tts request failed: status=%d body=%s",
			resp.StatusCode,
			truncateString(strings.TrimSpace(string(body)), 1000),
		)
	}

	if len(body) == 0 {
		return nil, "", endpoint, fmt.Errorf("tts request succeeded but returned empty audio content")
	}

	return body, strings.TrimSpace(resp.Header.Get("Content-Type")), endpoint, nil
}

func buildAudioFileName(fileName string, format string) string {
	baseName := sanitizeBaseName(fileName)
	if baseName == "" {
		baseName = "tts_" + time.Now().UTC().Format("20060102_150405")
	}

	ext := supportedAudioFormats[format]
	return baseName + "." + ext
}

func (p *openAITTSPlugin) writeTempUploadFile(fileName string, audioData []byte) (string, error) {
	base := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	base = sanitizeBaseName(base)
	if base == "" {
		base = "tts"
	}

	ext := filepath.Ext(fileName)
	pattern := base + "_*" + ext

	tempFile, err := os.CreateTemp(p.outputDir, pattern)
	if err != nil {
		return "", fmt.Errorf("failed to create temp upload file: %w", err)
	}

	if _, err := tempFile.Write(audioData); err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tempFile.Name())
		return "", fmt.Errorf("failed to write temp upload file: %w", err)
	}

	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempFile.Name())
		return "", fmt.Errorf("failed to close temp upload file: %w", err)
	}

	return tempFile.Name(), nil
}

func parseSizeToBytes(input string) (int, error) {
	normalized := strings.ToUpper(strings.TrimSpace(input))
	normalized = strings.ReplaceAll(normalized, " ", "")
	if normalized == "" {
		return 0, fmt.Errorf("value cannot be empty")
	}

	index := 0
	for index < len(normalized) {
		char := normalized[index]
		if (char >= '0' && char <= '9') || char == '.' {
			index++
			continue
		}
		break
	}

	if index == 0 {
		return 0, fmt.Errorf("missing numeric value")
	}

	numberPart := normalized[:index]
	unitPart := "B"
	if index < len(normalized) {
		unitPart = normalized[index:]
	}

	value, err := strconv.ParseFloat(numberPart, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid numeric value")
	}
	if value <= 0 {
		return 0, fmt.Errorf("size must be positive")
	}

	multipliers := map[string]float64{
		"B":   1,
		"K":   1024,
		"KB":  1024,
		"KIB": 1024,
		"M":   1024 * 1024,
		"MB":  1024 * 1024,
		"MIB": 1024 * 1024,
		"G":   1024 * 1024 * 1024,
		"GB":  1024 * 1024 * 1024,
		"GIB": 1024 * 1024 * 1024,
	}

	multiplier, ok := multipliers[unitPart]
	if !ok {
		return 0, fmt.Errorf("unsupported unit %q (use B/KB/MB/GB)", unitPart)
	}

	bytesValue := int(value * multiplier)
	if bytesValue <= 0 {
		return 0, fmt.Errorf("size must resolve to at least 1 byte")
	}

	return bytesValue, nil
}

func sanitizeBaseName(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}

	base := filepath.Base(input)
	base = strings.TrimSuffix(base, filepath.Ext(base))

	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-':
			b.WriteRune(r)
		case r == ' ' || r == '.':
			b.WriteByte('_')
		}
	}

	cleaned := strings.Trim(b.String(), "_-")
	if len(cleaned) > 64 {
		runes := []rune(cleaned)
		if len(runes) > 64 {
			cleaned = string(runes[:64])
		}
	}

	return cleaned
}

func joinURL(baseURL string, path string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(path, "/")
}

func truncateString(text string, max int) string {
	if len(text) <= max {
		return text
	}

	return text[:max] + "..."
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[OPENAI-TTS] Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	p, err := newOpenAITTSPlugin(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[OPENAI-TTS] Failed to initialize plugin: %v\n", err)
		os.Exit(1)
	}

	r := &reloadingPlugin{}
	r.cur.Store(p)
	w := r.watch()
	defer w.Close()

	if err := pluginserver.Serve(r); err != nil {
		fmt.Fprintf(os.Stderr, "[OPENAI-TTS] Error: %v\n", err)
		os.Exit(1)
	}
}
