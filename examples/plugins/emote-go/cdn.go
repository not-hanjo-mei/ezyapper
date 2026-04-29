package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type cachedURL struct {
	refreshed string
	expiresAt time.Time
}

type CDNRefreshClient struct {
	token      string
	httpClient *http.Client
	mu         sync.RWMutex
	cache      map[string]cachedURL
}

func NewCDNRefreshClient(token string) *CDNRefreshClient {
	return &CDNRefreshClient{
		token:      token,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		cache:      make(map[string]cachedURL),
	}
}

// RefreshURL refreshes a Discord CDN URL. Non-Discord URLs are returned unchanged.
// The input URL may contain ?ex=&is=&hm= query parameters; these are stripped before
// caching/refreshing. The returned URL is the refreshed (or original if non-Discord) URL.
func (c *CDNRefreshClient) RefreshURL(raw string) (string, error) {
	if !strings.Contains(raw, "cdn.discordapp.com") && !strings.Contains(raw, "media.discordapp.net") {
		return raw, nil
	}

	bare := raw
	if idx := strings.Index(raw, "?"); idx >= 0 {
		bare = raw[:idx]
	}

	c.mu.RLock()
	cached, ok := c.cache[bare]
	c.mu.RUnlock()
	if ok && time.Now().Before(cached.expiresAt) {
		return cached.refreshed, nil
	}

	refreshed, err := c.callRefreshAPI(bare)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[EMOTE-CDN] refresh failed for %s: %v\n", bare, err)
		return bare, nil
	}

	c.mu.Lock()
	c.cache[bare] = cachedURL{refreshed: refreshed, expiresAt: time.Now().Add(24 * time.Hour)}
	c.mu.Unlock()

	return refreshed, nil
}

func (c *CDNRefreshClient) callRefreshAPI(bareURL string) (string, error) {
	reqBody := map[string]interface{}{
		"attachment_urls": []string{bareURL},
	}
	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", "https://discord.com/api/v9/attachments/refresh-urls", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("refresh-urls returned %d", resp.StatusCode)
	}

	var result struct {
		RefreshedURLs []struct {
			Original  string `json:"original"`
			Refreshed string `json:"refreshed"`
		} `json:"refreshed_urls"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if len(result.RefreshedURLs) == 0 {
		return "", fmt.Errorf("no refreshed URL returned")
	}
	return result.RefreshedURLs[0].Refreshed, nil
}
