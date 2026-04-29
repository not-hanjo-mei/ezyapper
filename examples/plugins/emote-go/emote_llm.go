package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type MatchResult struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

type emoteLLMResponse struct {
	Matches []MatchResult `json:"matches"`
	NoMatch bool          `json:"no_match"`
}

type EmoteLLMClient struct {
	model      string
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func NewEmoteLLMClient(model, apiKey, baseURL string, timeout time.Duration) *EmoteLLMClient {
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	return &EmoteLLMClient{
		model:      model,
		apiKey:     apiKey,
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (c *EmoteLLMClient) Match(query string, emotes []EmoteEntry) ([]MatchResult, error) {
	if len(emotes) == 0 || c.model == "" || c.apiKey == "" {
		return nil, nil
	}

	prompt := buildSearchPrompt(query, emotes)

	reqBody := map[string]interface{}{
		"model": c.model,
		"messages": []map[string]interface{}{
			{"role": "user", "content": prompt},
		},
		"max_tokens":  500,
		"temperature": 0.1,
	}

	body, _ := json.Marshal(reqBody)
	req, err := http.NewRequest("POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
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
		return nil, nil
	}

	content := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	content = stripJSONFences(content)

	var result emoteLLMResponse
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, fmt.Errorf("failed to parse emote LLM response: %w", err)
	}
	if result.NoMatch {
		return nil, nil
	}
	return result.Matches, nil
}

func buildSearchPrompt(query string, emotes []EmoteEntry) string {
	var sb strings.Builder
	sb.WriteString("You are a search engine for an emote library.\n\n")
	sb.WriteString(fmt.Sprintf("Find emotes matching this request: %q\n\n", query))
	sb.WriteString("Available emotes:\n")
	for _, e := range emotes {
		sb.WriteString(fmt.Sprintf("- [%s] %s: %s (tags: %s)\n",
			e.ID, e.Name, e.Description, strings.Join(e.Tags, ", ")))
	}
	sb.WriteString("\nReturn a JSON array of up to 5 most relevant matches:\n")
	sb.WriteString(`{"matches":[{"id":"MD5_ID","reason":"why this matches"}],"no_match":false}`)
	sb.WriteString("\n\nIf no emote matches, return {\"matches\":[], \"no_match\":true}\n")
	sb.WriteString("Only return the JSON object, nothing else.")
	return sb.String()
}

func stripJSONFences(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
