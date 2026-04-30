package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ezyapper/internal/plugin"
	"ezyapper/internal/types"
)

func newTestPlugin(t *testing.T) *EmotePlugin {
	t.Helper()
	tmpDir := t.TempDir()
	return &EmotePlugin{
		config: Config{
			DataDir:                     tmpDir,
			MaxImageSizeKb:              512,
			AllowedFormats:              []string{"png", "jpg", "jpeg", "webp", "gif"},
			VisionApiKey:                "test-key",
			VisionApiBaseUrl:            "https://api.openai.com/v1",
			VisionModel:                 "gpt-4o-mini",
			VisionTimeoutSeconds:        30,
			VisionPrompt:                "test prompt",
			AutoStealEnabled:            true,
			AdditionalBlacklistChannels: []string{},
			AdditionalWhitelistChannels: []string{},
			AdditionalBlacklistUsers:    []string{},
			RateLimitPerMinute:          100,
			CooldownSeconds:             0,
			LoggingEnabled:              false,
			LoggingLevel:                "info",
			EmoteModel:                  "test-model",
			EmoteApiKey:                 "test-key",
			EmoteApiBaseURL:             "https://api.example.com/v1",
			EmoteMaxTokens:              1024,
			EmoteTemperature:            0.1,
			DiscordToken:                "test-token",
			SearchEmoteMs:               15000,
			SendEmoteMs:                 10000,
		},
		storage: NewStorage(tmpDir),
	}
}

func addTestEmotes(t *testing.T, s *Storage, guildID string, entries []EmoteEntry) {
	t.Helper()
	for _, e := range entries {
		if err := s.AddEmote(guildID, e); err != nil {
			t.Fatalf("AddEmote failed: %v", err)
		}
	}
}

func TestSearchEmote_NoEmoteLLM(t *testing.T) {
	p := newTestPlugin(t)

	_, err := p.ExecuteTool("search_emote", map[string]interface{}{
		"query": "happy",
	})
	if err == nil {
		t.Fatal("expected error when emoteLLM is nil")
	}
	if !strings.Contains(err.Error(), "emote LLM not configured") {
		t.Fatalf("expected 'emote LLM not configured' error, got: %v", err)
	}
}

func TestSearchEmote_NoMatch(t *testing.T) {
	p := newTestPlugin(t)
	p.emoteLLM = &EmoteLLMClient{model: "test-model"}

	entry := EmoteEntry{ID: "nm1", Name: "happy_cat", Tags: []string{"cat"}, URL: ""}
	addTestEmotes(t, p.storage, "global", []EmoteEntry{entry})

	result, err := p.ExecuteTool("search_emote", map[string]interface{}{
		"query":    "sad dog",
		"guild_id": "global",
	})
	if err != nil {
		t.Fatalf("search_emote failed: %v", err)
	}
	if result != "no matching emotes found" {
		t.Fatalf("expected 'no matching emotes found', got: %s", result)
	}
}

func TestSearchEmote_MissingQuery(t *testing.T) {
	p := newTestPlugin(t)

	_, err := p.ExecuteTool("search_emote", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing query")
	}
	if !strings.Contains(err.Error(), "query is required") {
		t.Fatalf("expected 'query is required' error, got: %v", err)
	}
}

func TestSearchEmote_InvalidQueryType(t *testing.T) {
	p := newTestPlugin(t)

	_, err := p.ExecuteTool("search_emote", map[string]interface{}{
		"query": 123,
	})
	if err == nil {
		t.Fatal("expected error for invalid query type")
	}
	if !strings.Contains(err.Error(), "argument query must be a string") {
		t.Fatalf("expected 'argument query must be a string' error, got: %v", err)
	}
}

func TestOnMessage_SkipWhenDisabled(t *testing.T) {
	p := newTestPlugin(t)
	p.config.AutoStealEnabled = false

	msg := types.DiscordMessage{
		ImageURLs: []string{"https://example.com/image.png"},
		GuildID:   "g",
		ChannelID: "c",
		AuthorID:  "u",
	}

	cont, err := p.OnMessage(msg)
	if err != nil {
		t.Fatalf("OnMessage returned error: %v", err)
	}
	if !cont {
		t.Fatal("OnMessage should return true when disabled")
	}
}

func TestOnMessage_SkipEmptyAttachments(t *testing.T) {
	p := newTestPlugin(t)
	p.config.AutoStealEnabled = true

	msg := types.DiscordMessage{
		GuildID:   "g",
		ChannelID: "c",
		AuthorID:  "u",
	}

	cont, err := p.OnMessage(msg)
	if err != nil {
		t.Fatalf("OnMessage returned error: %v", err)
	}
	if !cont {
		t.Fatal("OnMessage should return true for empty attachments")
	}
}

func TestOnMessage_SkipNilStorage(t *testing.T) {
	p := &EmotePlugin{
		config: Config{AutoStealEnabled: true},
	}
	msg := types.DiscordMessage{
		ImageURLs: []string{"https://example.com/image.png"},
		GuildID:   "g",
		ChannelID: "c",
		AuthorID:  "u",
	}

	cont, err := p.OnMessage(msg)
	if err != nil {
		t.Fatalf("OnMessage returned error: %v", err)
	}
	if !cont {
		t.Fatal("OnMessage should return true when storage is nil")
	}
}

func TestExecuteTool_Unknown(t *testing.T) {
	p := newTestPlugin(t)

	_, err := p.ExecuteTool("nonexistent_tool", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("expected 'unknown tool' error, got: %v", err)
	}
}

func TestOnResponse_NoQueueEntry(t *testing.T) {
	p := newTestPlugin(t)
	msg := types.DiscordMessage{ChannelID: "no-queue"}
	if err := p.OnResponse(msg, "response text"); err != nil {
		t.Fatalf("OnResponse should return nil: %v", err)
	}
}

func TestInfo(t *testing.T) {
	p := newTestPlugin(t)
	info, err := p.Info()
	if err != nil {
		t.Fatalf("Info failed: %v", err)
	}
	if info.Name != "emote-plugin" {
		t.Fatalf("expected name=emote-plugin, got %s", info.Name)
	}
	if info.Version != "0.0.1" {
		t.Fatalf("expected version=0.0.1, got %s", info.Version)
	}
	if info.Priority != 10 {
		t.Fatalf("expected priority=10, got %d", info.Priority)
	}
}

func TestListTools(t *testing.T) {
	p := newTestPlugin(t)
	tools, err := p.ListTools()
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	toolMap := make(map[string]plugin.ToolSpec)
	for _, ts := range tools {
		toolMap[ts.Name] = ts
	}

	expected := []struct {
		name              string
		expectedTimeoutMs int
	}{
		{"search_emote", 15000},
		{"send_emote", 10000},
	}
	for _, e := range expected {
		ts, ok := toolMap[e.name]
		if !ok {
			t.Fatalf("expected tool %q in ListTools output", e.name)
		}
		if ts.TimeoutMs != e.expectedTimeoutMs {
			t.Fatalf("tool %q: expected TimeoutMs=%d, got %d", e.name, e.expectedTimeoutMs, ts.TimeoutMs)
		}
	}
}

func TestIsAllowedFormat(t *testing.T) {
	allowed := []string{"png", "jpg", "webp", "gif"}

	tests := []struct {
		format   string
		expected bool
	}{
		{"png", true},
		{"jpg", true},
		{"webp", true},
		{"gif", true},
		{"bmp", false},
		{"PNG", false},
		{"", false},
	}

	for _, tc := range tests {
		result := isAllowedFormat(tc.format, allowed)
		if result != tc.expected {
			t.Errorf("isAllowedFormat(%q, %v) = %v, want %v", tc.format, allowed, result, tc.expected)
		}
	}
}

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://example.com/image.png", "png"},
		{"https://example.com/photo.JPG", "jpg"},
		{"https://example.com/sticker.webp", "webp"},
		{"https://example.com/emoji.gif", "gif"},
		{"/path/to/image.jpeg", "jpeg"},
		{"https://example.com/noextension", "png"},
		{"", "png"},
		{"https://example.com/double.dot.png", "png"},
	}

	for _, tc := range tests {
		result := detectFormat(tc.url)
		if result != tc.expected {
			t.Errorf("detectFormat(%q) = %q, want %q", tc.url, result, tc.expected)
		}
	}
}

func TestLoadConfig_FromYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	yamlContent := `
storage:
  data_dir: "/custom/data"
  max_image_size_kb: 256
  allowed_formats: ["png", "gif"]

vision:
  api_key: "sk-test"
  api_base_url: "https://custom.api.com/v1"
  model: "gpt-4o"
  timeout_seconds: 60
  prompt: "Custom prompt"

auto_steal:
  enabled: false
  additional_blacklist_channels: ["ch-bad"]
  additional_whitelist_channels: ["ch-good"]
  additional_blacklist_users: ["u-bad"]
  rate_limit_per_minute: 10
  cooldown_seconds: 5

logging:
  enabled: false
  level: "debug"

emote:
  model: "test-model"
  api_key: "test-key"
  api_base_url: "https://api.example.com/v1"
  max_tokens: 2048
  temperature: 0.5

discord:
  token: "test-token"

tool_timeouts:
  search_emote_ms: 20000
  send_emote_ms: 15000
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Set env var to point to our test config
	oldConfig := os.Getenv("EZYAPPER_PLUGIN_CONFIG")
	os.Setenv("EZYAPPER_PLUGIN_CONFIG", configPath)
	defer os.Setenv("EZYAPPER_PLUGIN_CONFIG", oldConfig)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	if cfg.DataDir != "/custom/data" {
		t.Fatalf("DataDir mismatch: got %q", cfg.DataDir)
	}
	if cfg.MaxImageSizeKb != 256 {
		t.Fatalf("MaxImageSizeKb mismatch: got %d", cfg.MaxImageSizeKb)
	}
	if cfg.AutoStealEnabled != false {
		t.Fatal("expected AutoStealEnabled=false")
	}
	if cfg.VisionApiKey != "sk-test" {
		t.Fatalf("VisionApiKey mismatch: got %q", cfg.VisionApiKey)
	}
	if cfg.VisionModel != "gpt-4o" {
		t.Fatalf("VisionModel mismatch: got %q", cfg.VisionModel)
	}
	if cfg.VisionTimeoutSeconds != 60 {
		t.Fatalf("VisionTimeoutSeconds mismatch: got %d", cfg.VisionTimeoutSeconds)
	}
	if cfg.RateLimitPerMinute != 10 {
		t.Fatalf("RateLimitPerMinute mismatch: got %d", cfg.RateLimitPerMinute)
	}
	if cfg.CooldownSeconds != 5 {
		t.Fatalf("CooldownSeconds mismatch: got %d", cfg.CooldownSeconds)
	}
	if cfg.LoggingEnabled != false {
		t.Fatal("expected LoggingEnabled=false")
	}
	if cfg.LoggingLevel != "debug" {
		t.Fatalf("LoggingLevel mismatch: got %q", cfg.LoggingLevel)
	}
	if cfg.EmoteModel != "test-model" {
		t.Fatalf("EmoteModel mismatch: got %q", cfg.EmoteModel)
	}
	if cfg.EmoteMaxTokens != 2048 {
		t.Fatalf("EmoteMaxTokens mismatch: got %d", cfg.EmoteMaxTokens)
	}
	if cfg.EmoteTemperature != 0.5 {
		t.Fatalf("EmoteTemperature mismatch: got %f", cfg.EmoteTemperature)
	}
	if cfg.DiscordToken != "test-token" {
		t.Fatalf("DiscordToken mismatch: got %q", cfg.DiscordToken)
	}
	if cfg.SearchEmoteMs != 20000 {
		t.Fatalf("SearchEmoteMs mismatch: got %d", cfg.SearchEmoteMs)
	}
	if cfg.SendEmoteMs != 15000 {
		t.Fatalf("SendEmoteMs mismatch: got %d", cfg.SendEmoteMs)
	}
	if len(cfg.AllowedFormats) != 2 || cfg.AllowedFormats[0] != "png" || cfg.AllowedFormats[1] != "gif" {
		t.Fatalf("AllowedFormats mismatch: %v", cfg.AllowedFormats)
	}
	if len(cfg.AdditionalBlacklistChannels) != 1 || cfg.AdditionalBlacklistChannels[0] != "ch-bad" {
		t.Fatalf("AdditionalBlacklistChannels mismatch: %v", cfg.AdditionalBlacklistChannels)
	}
	if len(cfg.AdditionalWhitelistChannels) != 1 || cfg.AdditionalWhitelistChannels[0] != "ch-good" {
		t.Fatalf("AdditionalWhitelistChannels mismatch: %v", cfg.AdditionalWhitelistChannels)
	}
	if len(cfg.AdditionalBlacklistUsers) != 1 || cfg.AdditionalBlacklistUsers[0] != "u-bad" {
		t.Fatalf("AdditionalBlacklistUsers mismatch: %v", cfg.AdditionalBlacklistUsers)
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("not: valid: yaml: [[["), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	oldConfig := os.Getenv("EZYAPPER_PLUGIN_CONFIG")
	os.Setenv("EZYAPPER_PLUGIN_CONFIG", configPath)
	defer os.Setenv("EZYAPPER_PLUGIN_CONFIG", oldConfig)

	_, err := loadConfig()
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadConfig_MissingRequired(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	// Write config with missing required fields
	yamlContent := `
storage:
  data_dir: "/data"
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	oldConfig := os.Getenv("EZYAPPER_PLUGIN_CONFIG")
	os.Setenv("EZYAPPER_PLUGIN_CONFIG", configPath)
	defer os.Setenv("EZYAPPER_PLUGIN_CONFIG", oldConfig)

	_, err := loadConfig()
	if err == nil {
		t.Fatal("expected error for missing required fields")
	}
	if !strings.Contains(err.Error(), "vision.api_key is required") {
		t.Fatalf("expected error about missing vision.api_key, got: %v", err)
	}
}

func TestLoadConfig_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "nonexistent.yaml")

	oldConfig := os.Getenv("EZYAPPER_PLUGIN_CONFIG")
	os.Setenv("EZYAPPER_PLUGIN_CONFIG", configPath)
	defer os.Setenv("EZYAPPER_PLUGIN_CONFIG", oldConfig)

	_, err := loadConfig()
	if err == nil {
		t.Fatal("expected error when config file does not exist")
	}
}

func TestSearchEmote_MergesGlobalAndGuild(t *testing.T) {
	p := newTestPlugin(t)
	p.emoteLLM = &EmoteLLMClient{model: "test-model"}
	addTestEmotes(t, p.storage, "global", []EmoteEntry{
		{ID: "id-global", Name: "global_emote", URL: ""},
	})
	addTestEmotes(t, p.storage, "guild123", []EmoteEntry{
		{ID: "id-guild", Name: "guild_emote", URL: ""},
	})
	result, err := p.ExecuteTool("search_emote", map[string]interface{}{
		"query":    "test",
		"guild_id": "guild123",
	})
	if err != nil {
		t.Fatalf("search_emote failed: %v", err)
	}
	if result != "no matching emotes found" {
		t.Fatalf("expected 'no matching emotes found' with empty API key, got: %s", result)
	}
}

func TestSendEmote_NotFound(t *testing.T) {
	p := newTestPlugin(t)
	_, err := p.ExecuteTool("send_emote", map[string]interface{}{
		"id": "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for non-existent emote")
	}
	if !strings.Contains(err.Error(), "emote not found") {
		t.Fatalf("expected 'emote not found' error, got: %v", err)
	}
}

func TestSendEmote_MissingID(t *testing.T) {
	p := newTestPlugin(t)
	_, err := p.ExecuteTool("send_emote", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing id")
	}
	if !strings.Contains(err.Error(), "id is required") {
		t.Fatalf("expected 'id is required' error, got: %v", err)
	}
}

func TestSendEmote_InvalidIDType(t *testing.T) {
	p := newTestPlugin(t)
	_, err := p.ExecuteTool("send_emote", map[string]interface{}{
		"id": 123,
	})
	if err == nil {
		t.Fatal("expected error for invalid id type")
	}
	if !strings.Contains(err.Error(), "argument id must be a string") {
		t.Fatalf("expected 'argument id must be a string' error, got: %v", err)
	}
}

func TestSendEmote_Success(t *testing.T) {
	p := newTestPlugin(t)
	p.cdnRefresh = NewCDNRefreshClient("")
	guildID := "global"
	addTestEmotes(t, p.storage, guildID, []EmoteEntry{
		{ID: "md5abc", Name: "test_emote", URL: "https://example.com/test.png"},
	})
	result, err := p.ExecuteTool("send_emote", map[string]interface{}{
		"id": "md5abc",
	})
	if err != nil {
		t.Fatalf("send_emote failed: %v", err)
	}
	if !strings.Contains(result, "sent test_emote") {
		t.Fatalf("expected 'sent test_emote', got: %s", result)
	}
}

func TestSendEmote_NoURLorFile(t *testing.T) {
	p := newTestPlugin(t)
	p.cdnRefresh = NewCDNRefreshClient("")
	guildID := "global"
	addTestEmotes(t, p.storage, guildID, []EmoteEntry{
		{ID: "md5abc", Name: "test_emote", URL: "", FileName: ""},
	})
	_, err := p.ExecuteTool("send_emote", map[string]interface{}{
		"id": "md5abc",
	})
	if err == nil {
		t.Fatal("expected error for emote without URL or file")
	}
	if !strings.Contains(err.Error(), "emote has neither URL nor file_name") {
		t.Fatalf("expected 'emote has neither URL nor file_name' error, got: %v", err)
	}
}

func TestGetStringArg(t *testing.T) {
	tests := []struct {
		name      string
		args      map[string]interface{}
		key       string
		wantVal   string
		wantOK    bool
		wantError bool
	}{
		{
			name:    "missing key",
			args:    map[string]interface{}{},
			key:     "test",
			wantVal: "",
			wantOK:  false,
		},
		{
			name:    "valid string",
			args:    map[string]interface{}{"test": "value"},
			key:     "test",
			wantVal: "value",
			wantOK:  true,
		},
		{
			name:      "invalid type",
			args:      map[string]interface{}{"test": 123},
			key:       "test",
			wantError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			val, ok, err := getStringArg(tc.args, tc.key)
			if tc.wantError {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if val != tc.wantVal {
				t.Fatalf("expected val=%q, got %q", tc.wantVal, val)
			}
			if ok != tc.wantOK {
				t.Fatalf("expected ok=%v, got %v", tc.wantOK, ok)
			}
		})
	}
}
