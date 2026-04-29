package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ezyapper/internal/plugin"
)

func newTestPlugin(t *testing.T) *EmotePlugin {
	t.Helper()
	tmpDir := t.TempDir()
	return &EmotePlugin{
		config: Config{
			DataDir:                tmpDir,
			AutoStealEnabled:       true,
			MaxImageSizeKb:         512,
			AllowedFormats:         []string{"png", "jpg", "jpeg", "webp", "gif"},
			RateLimitPerMinute:     100,
			CooldownSeconds:        0,
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
	p.emoteLLM = &EmoteLLMClient{model: "test-model"} // empty apiKey → returns nil, nil (no matches)

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

func TestOnMessage_SkipWhenDisabled(t *testing.T) {
	p := newTestPlugin(t)
	p.config.AutoStealEnabled = false

	msg := plugin.DiscordMessage{
		AttachmentURLs: []string{"https://example.com/image.png"},
		GuildID:        "g",
		ChannelID:      "c",
		AuthorID:       "u",
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

	msg := plugin.DiscordMessage{
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
	msg := plugin.DiscordMessage{
		AttachmentURLs: []string{"https://example.com/image.png"},
		GuildID:        "g",
		ChannelID:      "c",
		AuthorID:       "u",
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

func TestOnResponse(t *testing.T) {
	p := newTestPlugin(t)
	msg := plugin.DiscordMessage{Content: "hello"}
	if err := p.OnResponse(msg, "response text"); err != nil {
		t.Fatalf("OnResponse should return nil: %v", err)
	}
}

func TestShutdown(t *testing.T) {
	p := newTestPlugin(t)
	if err := p.Shutdown(); err != nil {
		t.Fatalf("Shutdown should return nil: %v", err)
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

	toolNames := make(map[string]bool)
	for _, ts := range tools {
		toolNames[ts.Name] = true
	}

	expected := []string{"search_emote", "send_emote"}
	for _, name := range expected {
		if !toolNames[name] {
			t.Fatalf("expected tool %q in ListTools output", name)
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

func TestLoadConfig_Defaults(t *testing.T) {
	cfg, err := loadConfig("")
	if err != nil {
		t.Fatalf("loadConfig with empty path failed: %v", err)
	}

	if cfg.DataDir != "data" {
		t.Fatalf("expected DataDir='data', got %q", cfg.DataDir)
	}
	if cfg.MaxImageSizeKb != 512 {
		t.Fatalf("expected MaxImageSizeKb=512, got %d", cfg.MaxImageSizeKb)
	}
	if cfg.AutoStealEnabled != true {
		t.Fatal("expected AutoStealEnabled=true")
	}
	if cfg.RateLimitPerMinute != 5 {
		t.Fatalf("expected RateLimitPerMinute=5, got %d", cfg.RateLimitPerMinute)
	}
	if cfg.CooldownSeconds != 2 {
		t.Fatalf("expected CooldownSeconds=2, got %d", cfg.CooldownSeconds)
	}
	if cfg.VisionModel != "gpt-4o-mini" {
		t.Fatalf("expected VisionModel='gpt-4o-mini', got %q", cfg.VisionModel)
	}
	if cfg.VisionTimeoutSeconds != 30 {
		t.Fatalf("expected VisionTimeoutSeconds=30, got %d", cfg.VisionTimeoutSeconds)
	}
	if cfg.LoggingEnabled != true {
		t.Fatal("expected LoggingEnabled=true")
	}
	if cfg.LoggingLevel != "info" {
		t.Fatalf("expected LoggingLevel='info', got %q", cfg.LoggingLevel)
	}

	if len(cfg.AllowedFormats) != 5 {
		t.Fatalf("expected 4 allowed formats, got %d", len(cfg.AllowedFormats))
	}
	expectedFormats := []string{"png", "jpg", "jpeg", "webp", "gif"}
	for i, f := range cfg.AllowedFormats {
		if f != expectedFormats[i] {
			t.Fatalf("AllowedFormats[%d] = %q, want %q", i, f, expectedFormats[i])
		}
	}
}

func TestLoadConfig_NonexistentFile(t *testing.T) {
	cfg, err := loadConfig(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err != nil {
		t.Fatalf("loadConfig with nonexistent file should not error: %v", err)
	}
	if cfg.DataDir != "data" {
		t.Fatalf("expected default DataDir, got %q", cfg.DataDir)
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
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := loadConfig(configPath)
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

	_, err := loadConfig(configPath)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadConfig_EmptyYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(""), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig with empty YAML should not error: %v", err)
	}
	if cfg.DataDir != "data" {
		t.Fatalf("expected default DataDir for empty YAML, got %q", cfg.DataDir)
	}
}

func TestLoadConfig_PartialOverride(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	yamlContent := `
storage:
  data_dir: "partial-data"
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	if cfg.DataDir != "partial-data" {
		t.Fatalf("DataDir should be overridden: got %q", cfg.DataDir)
	}
	if cfg.MaxImageSizeKb != 512 {
		t.Fatalf("MaxImageSizeKb should remain default: got %d", cfg.MaxImageSizeKb)
	}
	if cfg.AutoStealEnabled != true {
		t.Fatal("AutoStealEnabled should remain default: true")
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

func TestOnResponse_SendsQueued(t *testing.T) {
	p := newTestPlugin(t)
	p.discord = &DiscordSession{}
	p.sendQueue = map[string]string{"chan-123": "https://example.com/test.png"}
	msg := plugin.DiscordMessage{ChannelID: "chan-123"}
	if err := p.OnResponse(msg, "response text"); err != nil {
		t.Fatalf("OnResponse should return nil: %v", err)
	}
	if _, ok := p.sendQueue["chan-123"]; ok {
		t.Fatal("expected queue entry to be removed after send")
	}
}

func TestOnResponse_NoQueueEntry(t *testing.T) {
	p := newTestPlugin(t)
	msg := plugin.DiscordMessage{ChannelID: "no-queue"}
	if err := p.OnResponse(msg, "response text"); err != nil {
		t.Fatalf("OnResponse should return nil: %v", err)
	}
}
