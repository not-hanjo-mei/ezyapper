package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path"
	"strings"
	"time"

	"ezyapper/internal/ai"
	"ezyapper/internal/httpcapture"
	"ezyapper/internal/retry"
)

// Transcriber calls an OpenAI-compatible speech-to-text endpoint
// (POST {base}/audio/transcriptions, multipart file + model).
type Transcriber struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	model      string
	language   string
	prompt     string
	timeout    time.Duration
	retries    int
}

// TranscriberConfig configures a Transcriber.
type TranscriberConfig struct {
	BaseURL, APIKey, Model, Language, Prompt string
	Timeout                                  time.Duration
	RetryCount                               int
}

// NewTranscriber creates a transcriber.
// cfg.Timeout comes from audio.timeout or core.ai.timeout (required > 0).
func NewTranscriber(cfg TranscriberConfig) *Transcriber {
	return &Transcriber{
		httpClient: httpcapture.Wrap(&http.Client{Timeout: cfg.Timeout}, "asr:"+cfg.Model),
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:     cfg.APIKey,
		model:      cfg.Model,
		language:   cfg.Language,
		prompt:     cfg.Prompt,
		timeout:    cfg.Timeout,
		retries:    cfg.RetryCount,
	}
}

// Transcribe uploads the audio and returns the transcript text.
func (t *Transcriber) Transcribe(ctx context.Context, filename, contentType string, data []byte) (string, error) {
	filename = audioFilename(filename, contentType)
	return retry.Retry(ctx, t.retries, func(ctx context.Context) (string, error) {
		return t.transcribeOnce(ctx, filename, contentType, data)
	},
		retry.WithBaseDelay(1*time.Second),
		retry.WithMaxDelay(30*time.Second),
		retry.WithErrorClassifier(ai.IsRetryableError),
	)
}

func (t *Transcriber) transcribeOnce(ctx context.Context, filename, contentType string, data []byte) (string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="file"; filename=%q`, filename)}
	if ct := baseContentType(contentType); ct != "" {
		h["Content-Type"] = []string{ct}
	} else {
		h["Content-Type"] = []string{"application/octet-stream"}
	}
	fw, err := w.CreatePart(h)
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(data); err != nil {
		return "", err
	}
	fields := [][2]string{{"model", t.model}, {"response_format", "json"}}
	if t.language != "" {
		fields = append(fields, [2]string{"language", t.language})
	}
	if t.prompt != "" {
		fields = append(fields, [2]string{"prompt", t.prompt})
	}
	for _, f := range fields {
		if err := w.WriteField(f[0], f[1]); err != nil {
			return "", err
		}
	}
	if err := w.Close(); err != nil {
		return "", err
	}

	attemptCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, t.baseURL+"/audio/transcriptions", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	if t.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.apiKey)
	}
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("transcription request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("read transcription response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 500 {
			msg = msg[:500] + "..."
		}
		return "", fmt.Errorf("transcription: status code %d: %s", resp.StatusCode, msg)
	}
	var parsed struct {
		Text *string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parsed); err == nil && parsed.Text != nil {
		return strings.TrimSpace(*parsed.Text), nil
	}
	// response_format=text servers answer with the bare transcript.
	if !strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		return strings.TrimSpace(string(raw)), nil
	}
	return "", fmt.Errorf("transcription response has no text field")
}

func baseContentType(ct string) string {
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	return strings.TrimSpace(ct)
}

// audioFilename makes sure the upload has an extension: transcription
// endpoints pick the decoder from it.
func audioFilename(name, contentType string) string {
	if name == "" {
		name = "audio"
	}
	if path.Ext(name) != "" {
		return name
	}
	switch baseContentType(contentType) {
	case "audio/ogg", "audio/opus":
		return name + ".ogg"
	case "audio/mpeg", "audio/mp3":
		return name + ".mp3"
	case "audio/wav", "audio/x-wav", "audio/wave":
		return name + ".wav"
	case "audio/mp4", "audio/m4a", "audio/x-m4a":
		return name + ".m4a"
	case "audio/webm":
		return name + ".webm"
	case "audio/flac", "audio/x-flac":
		return name + ".flac"
	}
	if exts, _ := mime.ExtensionsByType(baseContentType(contentType)); len(exts) > 0 {
		return name + exts[0]
	}
	return name
}
