package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// VisionResult is the parsed response from the Vision model.
type VisionResult struct {
	IsEmote     bool     `json:"is_emote"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

// VisionClient wraps the OpenAI-compatible Vision API.
type VisionClient struct {
	apiKey     string
	baseURL    string
	model      string
	prompt     string
	timeout    time.Duration
	httpClient *http.Client
}

func NewVisionClient(apiKey, baseURL, model, prompt string, timeout time.Duration) *VisionClient {
	return &VisionClient{
		apiKey:     apiKey,
		baseURL:    baseURL,
		model:      model,
		prompt:     prompt,
		timeout:    timeout,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// AnalyzeImage sends imageBytes to the Vision model and returns the parsed result.
func (v *VisionClient) AnalyzeImage(imageBytes []byte) (*VisionResult, error) {
	b64 := base64.StdEncoding.EncodeToString(imageBytes)

	reqBody := map[string]interface{}{
		"model": v.model,
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
					{
						"type": "text",
						"text": v.prompt,
					},
					{
						"type": "image_url",
						"image_url": map[string]string{
							"url": "data:image/png;base64," + b64,
						},
					},
				},
			},
		},
		"max_tokens":  200,
		"temperature": 0.1,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal vision request: %w", err)
	}
	req, err := http.NewRequest("POST", v.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+v.apiKey)

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, err
	}
	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in vision response")
	}

	content := chatResp.Choices[0].Message.Content
	content = stripMarkdownFences(content)

	var result VisionResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, fmt.Errorf("failed to parse vision result: %w, content: %s", err, content)
	}
	return &result, nil
}

func stripMarkdownFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
	}
	return strings.TrimSpace(s)
}
