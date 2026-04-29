package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ezyapper/internal/plugin"
)

// EmotePlugin is the main plugin struct.
type EmotePlugin struct {
	config        Config
	storage       *Storage
	vision        *VisionClient
	emoteLLM      *EmoteLLMClient
	cdnRefresh    *CDNRefreshClient
	discord       *DiscordSession
	sendQueue     map[string]string
	lastChannelID string // set in OnMessage, used by send_emote
	mu            sync.Mutex
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
	p.lastChannelID = msg.ChannelID

	if !p.config.AutoStealEnabled || p.storage == nil || len(msg.AttachmentURLs) == 0 || msg.IsBot {
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

		// Store bare URL (strip Discord query params for Discord CDN)
		bare := url
		if strings.Contains(url, "cdn.discordapp.com") && strings.Contains(url, "?") {
			bare = url[:strings.Index(url, "?")]
		}

		entry := EmoteEntry{
			ID:        md5Hash(bare),
			URL:       bare,
			CreatedAt: time.Now().Format(time.RFC3339),
		}
		_ = p.storage.AddEmote(guildID, entry)
	}

	return true, nil
}

// OnResponse is called after the bot generates a response.
func (p *EmotePlugin) OnResponse(msg plugin.DiscordMessage, response string) error {
	p.mu.Lock()
	content, ok := p.sendQueue[msg.ChannelID]
	if ok {
		delete(p.sendQueue, msg.ChannelID)
	}
	p.mu.Unlock()

	if !ok {
		return nil
	}

	// Connect Discord if needed (lazy)
	if p.config.DiscordToken != "" && p.discord.session == nil {
		_ = p.discord.Connect(p.config.DiscordToken)
	}

	if strings.HasPrefix(content, "__file__:") {
		filePath := filepath.Join(p.config.DataDir, "global", "images", strings.TrimPrefix(content, "__file__:"))
		_ = p.discord.SendFile(msg.ChannelID, filePath)
	} else {
		_ = p.discord.SendMessage(msg.ChannelID, content)
	}
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
			Name:        "search_emote",
			Description: "Search for emotes by describing what you want",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "What kind of emote the user wants (describe the emotion/situation)",
					},
					"guild_id": map[string]interface{}{
						"type":        "string",
						"description": "Guild ID to search (searches global + this guild)",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "send_emote",
			Description: "Send an emote to the channel",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Emote ID (MD5 hash) to send",
					},
					"guild_id": map[string]interface{}{
						"type":        "string",
						"description": "Guild ID context",
					},
				},
				"required": []string{"id"},
			},
		},
	}, nil
}

// ExecuteTool dispatches tool calls by name.
func (p *EmotePlugin) ExecuteTool(name string, args map[string]interface{}) (string, error) {
	switch name {
	case "search_emote":
		query, ok := args["query"].(string)
		if !ok || query == "" {
			return "", fmt.Errorf("query is required")
		}
		guildID, _ := args["guild_id"].(string)
		if guildID == "" {
			guildID = "global"
		}

		// Load emotes from global + guild
		global, err := p.storage.LoadMetadata("global")
		if err != nil {
			return "", fmt.Errorf("failed to load global emotes: %w", err)
		}
		guild, err := p.storage.LoadMetadata(guildID)
		if err != nil {
			return "", fmt.Errorf("failed to load guild emotes: %w", err)
		}

		// Merge and dedup by ID
		allEmotes := global.Emotes
		seen := make(map[string]bool)
		for _, e := range global.Emotes {
			seen[e.ID] = true
		}
		for _, e := range guild.Emotes {
			if !seen[e.ID] {
				allEmotes = append(allEmotes, e)
				seen[e.ID] = true
			}
		}

		if p.emoteLLM == nil {
			return "", fmt.Errorf("emote LLM not configured")
		}

		matches, err := p.emoteLLM.Match(query, allEmotes)
		if err != nil {
			return "", fmt.Errorf("emote search failed: %w", err)
		}
		if len(matches) == 0 {
			return "no matching emotes found", nil
		}

		// Build response with emote details
		type SearchResult struct {
			ID          string   `json:"id"`
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Tags        []string `json:"tags"`
			Reason      string   `json:"reason"`
		}
		results := make([]SearchResult, 0, len(matches))
		for _, m := range matches {
			for _, e := range allEmotes {
				if e.ID == m.ID {
					results = append(results, SearchResult{
						ID: e.ID, Name: e.Name, Description: e.Description,
						Tags: e.Tags, Reason: m.Reason,
					})
					break
				}
			}
		}

		data, _ := json.MarshalIndent(map[string]interface{}{"results": results}, "", "  ")
		return string(data), nil

	case "send_emote":
		id, ok := args["id"].(string)
		if !ok || id == "" {
			return "", fmt.Errorf("id is required")
		}
		guildID, _ := args["guild_id"].(string)
		if guildID == "" {
			guildID = "global"
		}

		// Search metadata for the emote (try guild first, then global)
		var entry *EmoteEntry
		for _, gid := range []string{guildID, "global"} {
			mf, err := p.storage.LoadMetadata(gid)
			if err != nil {
				continue
			}
			for i := range mf.Emotes {
				if mf.Emotes[i].ID == id {
					e := mf.Emotes[i]
					entry = &e
					break
				}
			}
			if entry != nil {
				break
			}
		}
		if entry == nil {
			return "", fmt.Errorf("emote not found: %s", id)
		}

		// Resolve the output: CDN refresh if URL, or local file path
		var sendContent string
		if entry.URL != "" {
			refreshed, err := p.cdnRefresh.RefreshURL(entry.URL)
			if err != nil {
				refreshed = entry.URL // fallback
			}
			sendContent = refreshed
		} else if entry.FileName != "" {
			sendContent = "__file__:" + entry.FileName
		} else {
			return "", fmt.Errorf("emote has neither URL nor file_name: %s", id)
		}

		// Queue for OnResponse
		p.mu.Lock()
		if p.lastChannelID != "" {
			p.sendQueue[p.lastChannelID] = sendContent
		}
		p.mu.Unlock()

		return fmt.Sprintf("sent %s", entry.Name), nil

	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
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
	p.emoteLLM = NewEmoteLLMClient(
		config.EmoteModel,
		config.EmoteApiKey,
		config.EmoteApiBaseURL,
		15*time.Second,
	)
	p.cdnRefresh = NewCDNRefreshClient(config.DiscordToken)
	p.discord = &DiscordSession{}
	p.sendQueue = make(map[string]string)
	if err := plugin.Serve(p); err != nil {
		fmt.Fprintf(os.Stderr, "[EMOTE] Error: %v\n", err)
		os.Exit(1)
	}
}
