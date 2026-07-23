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

type VisionResult struct {
	IsEmote     bool     `json:"is_emote"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

type VisionClient struct {
	apiKey     string
	baseURL    string
	model      string
	prompt     string
	timeout    time.Duration
	retryCount int
	httpClient *http.Client
}

func NewVisionClient(apiKey, baseURL, model, prompt string, timeout time.Duration, retryCount int) *VisionClient {
	return &VisionClient{
		apiKey:     apiKey,
		baseURL:    baseURL,
		model:      model,
		prompt:     prompt,
		timeout:    timeout,
		retryCount: retryCount,
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (v *VisionClient) AnalyzeImage(imageBytes []byte) (*VisionResult, error) {
	b64 := base64.StdEncoding.EncodeToString(imageBytes)
	var feedback string
	var lastErr error

	for attempt := 0; attempt <= v.retryCount; attempt++ {
		text := v.prompt
		if feedback != "" {
			text = v.prompt + "\n\n" + feedback
		}
		reqBody := map[string]interface{}{
			"model": v.model,
			"messages": []map[string]interface{}{
				{
					"role": "user",
					"content": []map[string]interface{}{
						{"type": "text", "text": text},
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
			lastErr = err
			continue
		}

		var chatResp struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&chatResp)
		resp.Body.Close()
		if decodeErr != nil {
			lastErr = decodeErr
			feedback = "Previous response was not valid chat JSON. Return only the vision result JSON object."
			continue
		}
		if len(chatResp.Choices) == 0 {
			lastErr = fmt.Errorf("no choices in vision response")
			feedback = "Previous response had no choices. Return only the vision result JSON object."
			continue
		}

		content := stripMarkdownFences(chatResp.Choices[0].Message.Content)
		if err := requireVisionKeys(content); err != nil {
			lastErr = err
			feedback = "Previous response failed validation: " + err.Error()
			continue
		}
		var result VisionResult
		if err := json.Unmarshal([]byte(content), &result); err != nil {
			lastErr = fmt.Errorf("failed to parse vision result: %w", err)
			feedback = "Previous response failed JSON parse: " + err.Error()
			continue
		}
		if err := validateVisionResult(&result); err != nil {
			lastErr = err
			feedback = "Previous response failed validation: " + err.Error() +
				`. Return {"is_emote":false} or {"is_emote":true,"name":"...","description":"...","tags":[...]}`
			continue
		}
		return &result, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("vision attempts exhausted")
	}
	return nil, fmt.Errorf("vision exhausted after %d attempts: %w", v.retryCount+1, lastErr)
}

func requireVisionKeys(content string) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &m); err != nil {
		return fmt.Errorf("not a json object: %w", err)
	}
	if _, ok := m["is_emote"]; !ok {
		return fmt.Errorf("missing required field is_emote")
	}
	return nil
}

func validateVisionResult(r *VisionResult) error {
	if !r.IsEmote {
		return nil
	}
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("name required when is_emote is true")
	}
	if strings.TrimSpace(r.Description) == "" {
		return fmt.Errorf("description required when is_emote is true")
	}
	return nil
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
