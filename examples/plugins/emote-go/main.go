package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ezyapper/internal/plugin"
)

// EmotePlugin is the main plugin struct.
type EmotePlugin struct {
	config  Config
	storage *Storage
	vision  *VisionClient
}

// Info returns plugin metadata.
func (p *EmotePlugin) Info() (plugin.Info, error) {
	return plugin.Info{
		Name:        "emote-plugin",
		Version:     "0.0.1",
		Author:      "EZyapper",
		Description: "Auto-steals emotes from images and provides searchable emote library",
		Priority:    10,
	}, nil
}

// OnMessage is called for every Discord message. Records attachment URLs as emote entries.
func (p *EmotePlugin) OnMessage(msg plugin.DiscordMessage) (bool, error) {
	if !p.config.AutoStealEnabled {
		return true, nil
	}
	if p.storage == nil {
		return true, nil
	}
	if len(msg.AttachmentURLs) == 0 {
		return true, nil
	}

	guildID := msg.GuildID
	if guildID == "" {
		guildID = "global"
	}

	for _, url := range msg.AttachmentURLs {
		if !p.storage.CheckBlacklist(
			guildID, msg.ChannelID, msg.AuthorID,
			p.config.AdditionalBlacklistChannels,
			p.config.AdditionalWhitelistChannels,
			p.config.AdditionalBlacklistUsers,
		) {
			continue
		}

		entry := EmoteEntry{
			ID:        md5Hash(url),
			URL:       url,
			CreatedAt: time.Now().Format(time.RFC3339),
		}
		_ = p.storage.AddEmote(guildID, entry)
	}

	return true, nil
}

// OnResponse is called after the bot generates a response.
func (p *EmotePlugin) OnResponse(msg plugin.DiscordMessage, response string) error {
	return nil
}

// Shutdown is called when the plugin is being stopped.
func (p *EmotePlugin) Shutdown() error {
	return nil
}

// ListTools returns the tool specs exposed by this plugin.
func (p *EmotePlugin) ListTools() ([]plugin.ToolSpec, error) {
	return []plugin.ToolSpec{
		{
			Name:        "list_emotes",
			Description: "List available emotes with optional filtering",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"guild_id": map[string]interface{}{
						"type":        "string",
						"description": "Guild ID to filter emotes (empty = global emotes)",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of emotes to return (default 20, max 50)",
					},
				},
			},
		},
		{
			Name:        "search_emote",
			Description: "Search for emotes by name, description, or tags",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Search query — matches against name, description, and tags",
					},
					"guild_id": map[string]interface{}{
						"type":        "string",
						"description": "Guild ID to filter emotes",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum results (default 10, max 50)",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "get_emote",
			Description: "Get a specific emote by ID or name",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Emote ID (UUID) to look up",
					},
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Emote name to look up (alternative to id)",
					},
				},
			},
		},
	}, nil
}

// ExecuteTool dispatches tool calls by name.
func (p *EmotePlugin) ExecuteTool(name string, args map[string]interface{}) (string, error) {
	switch name {
	case "list_emotes":
		guildID, _ := args["guild_id"].(string)
		if guildID == "" {
			guildID = "global"
		}
		limit := 20
		if l, ok := args["limit"].(float64); ok {
			limit = int(l)
		}
		if limit > 50 {
			limit = 50
		}

		mf, err := p.storage.LoadMetadata(guildID)
		if err != nil {
			return "", fmt.Errorf("failed to load emotes: %w", err)
		}

		end := limit
		if end > len(mf.Emotes) {
			end = len(mf.Emotes)
		}

		type EmoteSummary struct {
			ID          string   `json:"id"`
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Tags        []string `json:"tags"`
		}
		summaries := make([]EmoteSummary, 0, end)
		for i := 0; i < end; i++ {
			e := mf.Emotes[i]
			summaries = append(summaries, EmoteSummary{
				ID: e.ID, Name: e.Name, Description: e.Description, Tags: e.Tags,
			})
		}
		result := map[string]interface{}{
			"emotes":   summaries,
			"total":    len(mf.Emotes),
			"guild_id": guildID,
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		return string(data), nil

	case "search_emote":
		query, _ := args["query"].(string)
		if query == "" {
			return "", fmt.Errorf("query is required")
		}
		query = strings.ToLower(query)
		guildID, _ := args["guild_id"].(string)
		if guildID == "" {
			guildID = "global"
		}
		limit := 10
		if l, ok := args["limit"].(float64); ok {
			limit = int(l)
		}
		if limit > 50 {
			limit = 50
		}

		mf, err := p.storage.LoadMetadata(guildID)
		if err != nil {
			return "", fmt.Errorf("failed to search emotes: %w", err)
		}

		type SearchResult struct {
			ID          string   `json:"id"`
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Tags        []string `json:"tags"`
			Score       float64  `json:"score"`
		}

		var results []SearchResult
		for _, e := range mf.Emotes {
			score := matchScore(query, e.Name, e.Description, e.Tags)
			if score > 0 {
				results = append(results, SearchResult{
					ID: e.ID, Name: e.Name, Description: e.Description,
					Tags: e.Tags, Score: score,
				})
			}
		}

		sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })

		if len(results) > limit {
			results = results[:limit]
		}

		resp := map[string]interface{}{
			"results":       results,
			"query":         query,
			"total_matches": len(results),
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return string(data), nil

	case "get_emote":
		id, _ := args["id"].(string)
		name, _ := args["name"].(string)

		if id == "" && name == "" {
			return "", fmt.Errorf("id or name is required")
		}

		guildID := "global"
		mf, err := p.storage.LoadMetadata(guildID)
		if err != nil {
			return "", fmt.Errorf("failed to get emote: %w", err)
		}

		var entry *EmoteEntry
		for i := range mf.Emotes {
			e := &mf.Emotes[i]
			if id != "" && e.ID == id {
				entry = e
				break
			}
			if name != "" && e.Name == name {
				entry = e
				break
			}
		}

		if entry == nil {
			return "", fmt.Errorf("emote not found: id=%s name=%s", id, name)
		}

		// Skip file check for URL-only emotes (no local file).
		if entry.URL == "" {
			filePath := filepath.Join(p.config.DataDir, guildID, "images", entry.FileName)
			if _, err := os.Stat(filePath); os.IsNotExist(err) {
				return "", fmt.Errorf("emote file not found on disk: %s", entry.FileName)
			}
		}

		data, _ := json.MarshalIndent(entry, "", "  ")
		return string(data), nil

	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

// matchScore computes a relevance score for an emote entry against a query.
func matchScore(query, name, description string, tags []string) float64 {
	var score float64
	if strings.Contains(strings.ToLower(name), query) {
		score += 3.0
	}
	if strings.Contains(strings.ToLower(description), query) {
		score += 1.0
	}
	for _, t := range tags {
		if strings.Contains(strings.ToLower(t), query) {
			score += 1.5
		}
	}
	return score
}

func detectFormat(url string) string {
	ext := filepath.Ext(url)
	if ext == "" {
		return "png"
	}
	return strings.ToLower(strings.TrimPrefix(ext, "."))
}

func isAllowedFormat(format string, allowed []string) bool {
	for _, a := range allowed {
		if a == format {
			return true
		}
	}
	return false
}

func main() {
	configPath := strings.TrimSpace(os.Getenv("EZYAPPER_PLUGIN_CONFIG"))
	config, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[EMOTE] Error loading config: %v\n", err)
		os.Exit(1)
	}

	if err := validateConfig(&config); err != nil {
		fmt.Fprintf(os.Stderr, "[EMOTE] Config validation error: %v\n", err)
		os.Exit(1)
	}

	p := &EmotePlugin{config: config}
	p.storage = NewStorage(config.DataDir)
	if config.VisionApiKey != "" {
		p.vision = NewVisionClient(
			config.VisionApiKey,
			config.VisionApiBaseUrl,
			config.VisionModel,
			config.VisionPrompt,
			time.Duration(config.VisionTimeoutSeconds)*time.Second,
		)
	}
	if err := plugin.Serve(p); err != nil {
		fmt.Fprintf(os.Stderr, "[EMOTE] Error: %v\n", err)
		os.Exit(1)
	}
}
