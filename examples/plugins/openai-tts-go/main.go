package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ezyapper/internal/plugin"

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
	pluginRoot string
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
		pluginRoot: pluginRoot,
		outputDir:  outputDir,
	}, nil
}

func (p *openAITTSPlugin) Info() (plugin.PluginInfo, error) {
	return plugin.PluginInfo{
		Name:        "openai-tts",
		Version:     "0.0.0",
		Author:      "EZyapper",
		Description: "OpenAI-compatible text-to-speech tool plugin",
		Priority:    20,
	}, nil
}

func (p *openAITTSPlugin) OnMessage(msg plugin.DiscordMessage) (bool, error) {
	return true, nil
}

func (p *openAITTSPlugin) OnResponse(msg plugin.DiscordMessage, response string) error {
	return nil
}

func (p *openAITTSPlugin) Shutdown() error {
	p.httpClient.CloseIdleConnections()
	return nil
}

func (p *openAITTSPlugin) BeforeSend(msg plugin.DiscordMessage, response string) (plugin.BeforeSendResult, error) {
	text := strings.TrimSpace(response)
	if text == "" {
		return plugin.BeforeSendResult{}, nil
	}

	fileName := fmt.Sprintf("discord_%s_%s", msg.ChannelID, msg.ID)
	generatedFile, err := p.synthesizeForBeforeSend(
		text,
		p.cfg.Model,
		p.cfg.Voice,
		p.cfg.Format,
		p.cfg.Speed,
		"",
		fileName,
	)
	if err != nil {
		return plugin.BeforeSendResult{}, fmt.Errorf("before_send tts failed: %w", err)
	}

	return plugin.BeforeSendResult{
		Files: []plugin.LocalFile{generatedFile},
	}, nil
}

func (p *openAITTSPlugin) synthesizeForBeforeSend(
	text string,
	model string,
	voice string,
	format string,
	speed float64,
	instructions string,
	fileName string,
) (plugin.LocalFile, error) {
	rewrittenText, rewrittenInstructions, _, includeInstructions, err := p.maybeRewriteInputAndInstructions(text, instructions)
	if err != nil {
		return plugin.LocalFile{}, err
	}

	if textLen := len([]rune(rewrittenText)); textLen > p.cfg.MaxTextChars {
		return plugin.LocalFile{}, fmt.Errorf("text exceeds max_text_chars (%d > %d)", textLen, p.cfg.MaxTextChars)
	}

	audioData, contentType, _, err := p.requestSpeech(
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

func (p *openAITTSPlugin) ListTools() ([]plugin.ToolSpec, error) {
	return []plugin.ToolSpec{
		{
			Name:        "generate_tts_audio",
			Description: "Generate speech audio from text using an OpenAI-compatible TTS endpoint and save it to a local file",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"text": map[string]interface{}{
						"type":        "string",
						"description": "Input text to synthesize",
					},
					"voice": map[string]interface{}{
						"type":        "string",
						"description": "TTS voice. Optional, defaults to plugin config",
					},
					"model": map[string]interface{}{
						"type":        "string",
						"description": "TTS model. Optional, defaults to plugin config",
					},
					"format": map[string]interface{}{
						"type":        "string",
						"description": "Audio format: mp3, opus, aac, flac, wav, pcm",
					},
					"speed": map[string]interface{}{
						"type":        "number",
						"description": "Playback speed in range (0, 4]",
					},
					"instructions": map[string]interface{}{
						"type":        "string",
						"description": "Optional speaking style instructions",
					},
					"file_name": map[string]interface{}{
						"type":        "string",
						"description": "Optional output file base name (without extension)",
					},
					"overwrite": map[string]interface{}{
						"type":        "boolean",
						"description": "When true, overwrite existing file with same name",
					},
				},
				"required": []string{"text"},
			},
		},
	}, nil
}

func (p *openAITTSPlugin) ExecuteTool(name string, args map[string]interface{}) (string, error) {
	if name != "generate_tts_audio" {
		return "", fmt.Errorf("unknown tool: %s", name)
	}

	text, err := requiredStringArg(args, "text")
	if err != nil {
		return "", err
	}

	if textLen := len([]rune(text)); textLen > p.cfg.MaxTextChars {
		return "", fmt.Errorf("text exceeds max_text_chars (%d > %d)", textLen, p.cfg.MaxTextChars)
	}

	voice := p.cfg.Voice
	if value, ok, err := optionalStringArg(args, "voice"); err != nil {
		return "", err
	} else if ok {
		voice = value
	}

	model := p.cfg.Model
	if value, ok, err := optionalStringArg(args, "model"); err != nil {
		return "", err
	} else if ok {
		model = value
	}

	format := p.cfg.Format
	if value, ok, err := optionalStringArg(args, "format"); err != nil {
		return "", err
	} else if ok {
		format = strings.ToLower(value)
	}

	if _, ok := supportedAudioFormats[format]; !ok {
		return "", fmt.Errorf("unsupported format %q, expected one of: mp3, opus, aac, flac, wav, pcm", format)
	}

	speed := p.cfg.Speed
	if value, ok, err := optionalFloatArg(args, "speed"); err != nil {
		return "", err
	} else if ok {
		speed = value
	}

	if speed <= 0 || speed > 4 {
		return "", fmt.Errorf("speed must be in range (0, 4]")
	}

	instructions := ""
	if value, ok, err := optionalStringArg(args, "instructions"); err != nil {
		return "", err
	} else if ok {
		instructions = value
	}

	fileName := ""
	if value, ok, err := optionalStringArg(args, "file_name"); err != nil {
		return "", err
	} else if ok {
		fileName = value
	}

	overwrite := false
	if value, ok, err := optionalBoolArg(args, "overwrite"); err != nil {
		return "", err
	} else if ok {
		overwrite = value
	}

	result, _, err := p.synthesizeAndSave(text, model, voice, format, speed, instructions, fileName, overwrite)
	if err != nil {
		return "", err
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data), nil
}

func (p *openAITTSPlugin) synthesizeAndSave(
	text string,
	model string,
	voice string,
	format string,
	speed float64,
	instructions string,
	fileName string,
	overwrite bool,
) (map[string]interface{}, plugin.LocalFile, error) {
	rewrittenText, rewrittenInstructions, rewritten, includeInstructions, err := p.maybeRewriteInputAndInstructions(text, instructions)
	if err != nil {
		return nil, plugin.LocalFile{}, err
	}

	if textLen := len([]rune(rewrittenText)); textLen > p.cfg.MaxTextChars {
		return nil, plugin.LocalFile{}, fmt.Errorf("text exceeds max_text_chars (%d > %d)", textLen, p.cfg.MaxTextChars)
	}

	outPath, err := p.resolveOutputPath(fileName, format, overwrite)
	if err != nil {
		return nil, plugin.LocalFile{}, err
	}

	audioData, contentType, endpoint, err := p.requestSpeech(
		model,
		voice,
		format,
		speed,
		rewrittenText,
		rewrittenInstructions,
		includeInstructions,
	)
	if err != nil {
		return nil, plugin.LocalFile{}, err
	}

	if err := os.WriteFile(outPath, audioData, 0o644); err != nil {
		return nil, plugin.LocalFile{}, fmt.Errorf("failed to write audio file: %w", err)
	}

	relPath := filepath.ToSlash(outPath)
	if rel, err := filepath.Rel(p.pluginRoot, outPath); err == nil {
		relPath = filepath.ToSlash(rel)
	}

	result := map[string]interface{}{
		"status":        "ok",
		"file_path":     outPath,
		"relative_path": relPath,
		"bytes":         len(audioData),
		"content_type":  contentType,
		"model":         model,
		"voice":         voice,
		"format":        format,
		"speed":         speed,
		"text_chars":    len([]rune(rewrittenText)),
		"rewritten":     rewritten,
		"endpoint":      endpoint,
	}

	generatedFile := plugin.LocalFile{
		Path:        outPath,
		Name:        filepath.Base(outPath),
		ContentType: contentType,
	}

	return result, generatedFile, nil
}

func (p *openAITTSPlugin) maybeRewriteInputAndInstructions(
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
		rewrittenText, rewrittenInstructions, includeInstructions, err := p.rewriteInputAndInstructionsOnce(originalText, originalInstructions)
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

	return "", "", false, false, fmt.Errorf("rewriter failed: %w", lastErr)
}

func (p *openAITTSPlugin) rewriteInputAndInstructionsOnce(
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(p.cfg.Rewriter.TimeoutSeconds)*time.Second)
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(p.cfg.TimeoutSeconds)*time.Second)
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

func (p *openAITTSPlugin) resolveOutputPath(fileName string, format string, overwrite bool) (string, error) {
	baseName := sanitizeBaseName(fileName)
	if baseName == "" {
		baseName = "tts_" + time.Now().UTC().Format("20060102_150405")
	}

	ext := supportedAudioFormats[format]
	candidate := filepath.Join(p.outputDir, baseName+"."+ext)

	if overwrite {
		return candidate, nil
	}

	if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
		return candidate, nil
	}

	for i := 1; i <= 9999; i++ {
		name := fmt.Sprintf("%s_%d.%s", baseName, i, ext)
		path := filepath.Join(p.outputDir, name)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
	}

	return "", fmt.Errorf("failed to allocate output file name for %s", baseName)
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

func requiredStringArg(args map[string]interface{}, key string) (string, error) {
	value, exists := args[key]
	if !exists {
		return "", fmt.Errorf("missing required argument: %s", key)
	}

	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("argument %s must be a string", key)
	}

	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", fmt.Errorf("argument %s cannot be empty", key)
	}

	return trimmed, nil
}

func optionalStringArg(args map[string]interface{}, key string) (string, bool, error) {
	value, exists := args[key]
	if !exists {
		return "", false, nil
	}

	text, ok := value.(string)
	if !ok {
		return "", false, fmt.Errorf("argument %s must be a string", key)
	}

	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false, fmt.Errorf("argument %s cannot be empty when provided", key)
	}

	return trimmed, true, nil
}

func optionalFloatArg(args map[string]interface{}, key string) (float64, bool, error) {
	value, exists := args[key]
	if !exists {
		return 0, false, nil
	}

	number, ok := anyToFloat(value)
	if !ok {
		return 0, false, fmt.Errorf("argument %s must be a number", key)
	}

	return number, true, nil
}

func optionalBoolArg(args map[string]interface{}, key string) (bool, bool, error) {
	value, exists := args[key]
	if !exists {
		return false, false, nil
	}

	b, ok := value.(bool)
	if !ok {
		return false, false, fmt.Errorf("argument %s must be a boolean", key)
	}

	return b, true, nil
}

func anyToFloat(value interface{}) (float64, bool) {
	switch n := value.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	default:
		return 0, false
	}
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
		cleaned = cleaned[:64]
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

	if err := plugin.Serve(p); err != nil {
		fmt.Fprintf(os.Stderr, "[OPENAI-TTS] Error: %v\n", err)
		os.Exit(1)
	}
}
