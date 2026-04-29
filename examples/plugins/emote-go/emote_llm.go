package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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

	reqBody := map[string]interface{}{
		"model": c.model,
		"messages": []map[string]interface{}{
			{"role": "system", "content": buildSystemPrompt()},
			{"role": "user", "content": buildUserPrompt(query, emotes)},
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

	// Log raw response for debugging
	fmt.Fprintf(os.Stderr, "[EMOTE-LLM] raw response: %s\n", content)

	content = stripJSONFences(content)

	var result emoteLLMResponse
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		fmt.Fprintf(os.Stderr, "[EMOTE-LLM] parse error: %v, content=%q\n", err, content)
		return nil, fmt.Errorf("failed to parse emote LLM response: %w", err)
	}
	if result.NoMatch {
		return nil, nil
	}
	return result.Matches, nil
}

func buildSystemPrompt() string {
	return "You are a search engine for an emote library.\n" +
		"Given a user's intent and a list of available emotes, find the best matches.\n\n" +
		"Return JSON: {\"matches\":[{\"id\":\"MD5\",\"reason\":\"why this matches\"}],\"no_match\":false}\n" +
		"If no emote matches, return {\"matches\":[],\"no_match\":true}\n" +
		"Only return the JSON object, nothing else."
}

func buildUserPrompt(query string, emotes []EmoteEntry) string {
	var sb strings.Builder
	sb.WriteString("<request>")
	sb.WriteString(query)
	sb.WriteString("</request>\n\n")
	sb.WriteString("<emotes>\n")
	for _, e := range emotes {
		sb.WriteString(fmt.Sprintf("  <emote id=\"%s\">\n", e.ID))
		sb.WriteString(fmt.Sprintf("    <name>%s</name>\n", e.Name))
		sb.WriteString(fmt.Sprintf("    <desc>%s</desc>\n", e.Description))
		sb.WriteString(fmt.Sprintf("    <tags>%s</tags>\n", strings.Join(e.Tags, ", ")))
		sb.WriteString("  </emote>\n")
	}
	sb.WriteString("</emotes>")
	return sb.String()
}

func stripJSONFences(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
