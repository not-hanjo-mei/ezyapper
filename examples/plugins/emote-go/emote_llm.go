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
	model       string
	apiKey      string
	baseURL     string
	maxTokens   int
	temperature float64
	retryCount  int
	httpClient  *http.Client
}

func NewEmoteLLMClient(model, apiKey, baseURL string, maxTokens int, temperature float64, timeout time.Duration, retryCount int) *EmoteLLMClient {
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	return &EmoteLLMClient{
		model:       model,
		apiKey:      apiKey,
		baseURL:     strings.TrimRight(baseURL, "/"),
		maxTokens:   maxTokens,
		temperature: temperature,
		retryCount:  retryCount,
		httpClient:  &http.Client{Timeout: timeout},
	}
}

func (c *EmoteLLMClient) Match(query string, emotes []EmoteEntry) ([]MatchResult, error) {
	if len(emotes) == 0 || c.model == "" || c.apiKey == "" {
		return nil, nil
	}

	allowed := make(map[string]struct{}, len(emotes))
	for _, e := range emotes {
		allowed[e.ID] = struct{}{}
	}

	baseUser := buildUserPrompt(query, emotes)
	var feedback string
	var lastErr error

	for attempt := 0; attempt <= c.retryCount; attempt++ {
		userContent := baseUser
		if feedback != "" {
			userContent = baseUser + "\n\n" + feedback
		}

		reqBody := map[string]interface{}{
			"model": c.model,
			"messages": []map[string]interface{}{
				{"role": "system", "content": buildSystemPrompt()},
				{"role": "user", "content": userContent},
			},
			"max_tokens":  c.maxTokens,
			"temperature": c.temperature,
		}

		body, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal emote LLM request: %w", err)
		}
		req, err := http.NewRequest("POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			fmt.Fprintf(os.Stderr, "[EMOTE-LLM] network error attempt %d/%d: %v\n", attempt+1, c.retryCount+1, err)
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
			feedback = "Previous response was not valid chat JSON. Return only the emote match JSON object."
			continue
		}
		if len(chatResp.Choices) == 0 {
			lastErr = fmt.Errorf("no choices in emote LLM response")
			feedback = "Previous response had no choices. Return only the emote match JSON object."
			continue
		}

		content := stripJSONFences(strings.TrimSpace(chatResp.Choices[0].Message.Content))
		var result emoteLLMResponse
		if err := json.Unmarshal([]byte(content), &result); err != nil {
			lastErr = fmt.Errorf("failed to parse emote LLM response: %w", err)
			feedback = "Previous response failed JSON parse: " + err.Error() +
				`. Return ONLY {"matches":[{"id":"...","reason":"..."}],"no_match":false} or {"matches":[],"no_match":true}`
			fmt.Fprintf(os.Stderr, "[EMOTE-LLM] parse error attempt %d/%d: %v content=%q\n", attempt+1, c.retryCount+1, err, content)
			continue
		}
		if err := validateEmoteLLMResponse(&result, allowed); err != nil {
			lastErr = err
			feedback = "Previous response failed validation: " + err.Error() +
				`. Return ONLY valid match ids from the provided emote list.`
			fmt.Fprintf(os.Stderr, "[EMOTE-LLM] schema error attempt %d/%d: %v\n", attempt+1, c.retryCount+1, err)
			continue
		}
		if result.NoMatch {
			return nil, nil
		}
		return result.Matches, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("emote LLM attempts exhausted")
	}
	return nil, fmt.Errorf("emote LLM exhausted after %d attempts: %w", c.retryCount+1, lastErr)
}

func validateEmoteLLMResponse(r *emoteLLMResponse, allowed map[string]struct{}) error {
	if r.NoMatch {
		if len(r.Matches) != 0 {
			return fmt.Errorf("no_match true requires empty matches")
		}
		return nil
	}
	if len(r.Matches) == 0 {
		return fmt.Errorf("no_match false requires at least one match")
	}
	for _, m := range r.Matches {
		if strings.TrimSpace(m.ID) == "" {
			return fmt.Errorf("match id must be non-empty")
		}
		if _, ok := allowed[m.ID]; !ok {
			return fmt.Errorf("unknown emote id %q", m.ID)
		}
	}
	return nil
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
