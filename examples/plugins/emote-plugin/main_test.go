package main

import (
	"encoding/json"
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
			AllowedFormats:         []string{"png", "jpg", "jpeg", "webp"},
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

func createEmoteFile(t *testing.T, plugin *EmotePlugin, guildID, fileName string) {
	t.Helper()
	imgDir := filepath.Join(plugin.config.DataDir, guildID, "images")
	if err := os.MkdirAll(imgDir, 0755); err != nil {
		t.Fatalf("failed to create images dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(imgDir, fileName), []byte("fake-image-data"), 0644); err != nil {
		t.Fatalf("failed to write emote file: %v", err)
	}
}

func TestListEmotes(t *testing.T) {
	p := newTestPlugin(t)
	guildID := "global"

	entries := []EmoteEntry{
		{ID: "id-1", Name: "happy_cat", Description: "A happy cat", Tags: []string{"cat", "happy"}},
		{ID: "id-2", Name: "sad_dog", Description: "A sad dog", Tags: []string{"dog", "sad"}},
		{ID: "id-3", Name: "angry_bird", Description: "An angry bird", Tags: []string{"bird", "angry"}},
	}
	addTestEmotes(t, p.storage, guildID, entries)

	result, err := p.ExecuteTool("list_emotes", map[string]interface{}{
		"guild_id": guildID,
		"limit":    float64(10),
	})
	if err != nil {
		t.Fatalf("list_emotes failed: %v", err)
	}

	var parsed struct {
		Emotes []struct {
			ID          string   `json:"id"`
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Tags        []string `json:"tags"`
		} `json:"emotes"`
		Total   int    `json:"total"`
		GuildID string `json:"guild_id"`
	}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse list_emotes result: %v\nraw: %s", err, result)
	}

	if parsed.Total != 3 {
		t.Fatalf("expected total=3, got %d", parsed.Total)
	}
	if len(parsed.Emotes) != 3 {
		t.Fatalf("expected 3 emotes, got %d", len(parsed.Emotes))
	}
	if parsed.GuildID != guildID {
		t.Fatalf("expected guild_id=%q, got %q", guildID, parsed.GuildID)
	}
	if parsed.Emotes[0].Name != "happy_cat" {
		t.Fatalf("expected first emote name=happy_cat, got %s", parsed.Emotes[0].Name)
	}
}

func TestListEmotes_Limit(t *testing.T) {
	p := newTestPlugin(t)
	guildID := "global"

	for i := 0; i < 10; i++ {
		entry := EmoteEntry{
			ID:   "id-" + string(rune('a'+i)),
			Name: "emote_" + string(rune('a'+i)),
		}
		if err := p.storage.AddEmote(guildID, entry); err != nil {
			t.Fatalf("AddEmote failed: %v", err)
		}
	}

	result, err := p.ExecuteTool("list_emotes", map[string]interface{}{
		"guild_id": guildID,
		"limit":    float64(3),
	})
	if err != nil {
		t.Fatalf("list_emotes failed: %v", err)
	}

	var parsed struct {
		Emotes []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"emotes"`
		Total int `json:"total"`
	}
	json.Unmarshal([]byte(result), &parsed)

	if parsed.Total != 10 {
		t.Fatalf("expected total=10, got %d", parsed.Total)
	}
	if len(parsed.Emotes) != 3 {
		t.Fatalf("expected 3 emotes with limit=3, got %d", len(parsed.Emotes))
	}
}

func TestListEmotes_DefaultGuild(t *testing.T) {
	p := newTestPlugin(t)

	entry := EmoteEntry{ID: "global-only", Name: "global_emote"}
	if err := p.storage.AddEmote("global", entry); err != nil {
		t.Fatalf("AddEmote failed: %v", err)
	}

	result, err := p.ExecuteTool("list_emotes", map[string]interface{}{})
	if err != nil {
		t.Fatalf("list_emotes failed: %v", err)
	}

	var parsed struct {
		Emotes []struct {
			ID   string `json:"id"`
		} `json:"emotes"`
	}
	json.Unmarshal([]byte(result), &parsed)

	if len(parsed.Emotes) != 1 || parsed.Emotes[0].ID != "global-only" {
		t.Fatalf("expected 1 global emote, got %+v", parsed.Emotes)
	}
}

func TestSearchEmote(t *testing.T) {
	p := newTestPlugin(t)
	guildID := "global"

	entries := []EmoteEntry{
		{ID: "s1", Name: "happy_cat", Description: "cat being happy", Tags: []string{"cat", "joy"}},
		{ID: "s2", Name: "grumpy_cat", Description: "cat being grumpy", Tags: []string{"cat", "grumpy"}},
		{ID: "s3", Name: "sad_dog", Description: "dog looking sad", Tags: []string{"dog", "sad"}},
	}
	addTestEmotes(t, p.storage, guildID, entries)

	result, err := p.ExecuteTool("search_emote", map[string]interface{}{
		"query":    "cat",
		"guild_id": guildID,
		"limit":    float64(10),
	})
	if err != nil {
		t.Fatalf("search_emote failed: %v", err)
	}

	var parsed struct {
		Results []struct {
			ID    string  `json:"id"`
			Name  string  `json:"name"`
			Score float64 `json:"score"`
		} `json:"results"`
		Query        string `json:"query"`
		TotalMatches int    `json:"total_matches"`
	}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse search result: %v", err)
	}

	if parsed.TotalMatches != 2 {
		t.Fatalf("expected 2 matches for 'cat', got %d", parsed.TotalMatches)
	}
	if len(parsed.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(parsed.Results))
	}

	if parsed.Results[0].Score < parsed.Results[1].Score {
		t.Fatal("results should be sorted by descending score")
	}

	result2, err := p.ExecuteTool("search_emote", map[string]interface{}{
		"query":    "sad",
		"guild_id": guildID,
	})
	if err != nil {
		t.Fatalf("search_emote with 'sad' failed: %v", err)
	}

	var parsed2 struct {
		Results []struct {
			Name string `json:"name"`
		} `json:"results"`
	}
	json.Unmarshal([]byte(result2), &parsed2)

	if len(parsed2.Results) != 1 || parsed2.Results[0].Name != "sad_dog" {
		t.Fatalf("expected 1 result 'sad_dog', got %+v", parsed2.Results)
	}
}

func TestSearchEmote_NoMatch(t *testing.T) {
	p := newTestPlugin(t)

	entry := EmoteEntry{ID: "nm1", Name: "happy_cat", Tags: []string{"cat"}}
	addTestEmotes(t, p.storage, "global", []EmoteEntry{entry})

	result, err := p.ExecuteTool("search_emote", map[string]interface{}{
		"query": "nonexistent_xyz",
	})
	if err != nil {
		t.Fatalf("search_emote failed: %v", err)
	}

	var parsed struct {
		Results      []interface{} `json:"results"`
		TotalMatches int           `json:"total_matches"`
	}
	json.Unmarshal([]byte(result), &parsed)

	if parsed.TotalMatches != 0 {
		t.Fatalf("expected 0 matches, got %d", parsed.TotalMatches)
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

func TestGetEmote_ByID(t *testing.T) {
	p := newTestPlugin(t)
	guildID := "global"
	fileName := "550e8400-e29b-41d4-a716-446655440000.png"

	createEmoteFile(t, p, guildID, fileName)

	entry := EmoteEntry{
		ID:          "550e8400-e29b-41d4-a716-446655440000",
		Name:        "test_emote",
		Description: "A test emote",
		Tags:        []string{"test", "sample"},
		FileName:    fileName,
	}
	addTestEmotes(t, p.storage, guildID, []EmoteEntry{entry})

	result, err := p.ExecuteTool("get_emote", map[string]interface{}{
		"id": "550e8400-e29b-41d4-a716-446655440000",
	})
	if err != nil {
		t.Fatalf("get_emote by ID failed: %v", err)
	}

	var parsed EmoteEntry
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse get_emote result: %v", err)
	}

	if parsed.ID != entry.ID {
		t.Fatalf("ID mismatch: got %s, want %s", parsed.ID, entry.ID)
	}
	if parsed.Name != "test_emote" {
		t.Fatalf("Name mismatch: got %s, want %s", parsed.Name, "test_emote")
	}
	if len(parsed.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(parsed.Tags))
	}
}

func TestGetEmote_ByName(t *testing.T) {
	p := newTestPlugin(t)
	guildID := "global"
	fileName := "test-uuid-by-name.png"

	createEmoteFile(t, p, guildID, fileName)

	entry := EmoteEntry{
		ID:          "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		Name:        "cool_frog",
		Description: "A cool frog emote",
		FileName:    fileName,
	}
	addTestEmotes(t, p.storage, guildID, []EmoteEntry{entry})

	result, err := p.ExecuteTool("get_emote", map[string]interface{}{
		"name": "cool_frog",
	})
	if err != nil {
		t.Fatalf("get_emote by name failed: %v", err)
	}

	var parsed EmoteEntry
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse get_emote result: %v", err)
	}

	if parsed.ID != entry.ID {
		t.Fatalf("ID mismatch: got %s, want %s", parsed.ID, entry.ID)
	}
}

func TestGetEmote_NotFound(t *testing.T) {
	p := newTestPlugin(t)

	_, err := p.ExecuteTool("get_emote", map[string]interface{}{
		"id": "nonexistent-id",
	})
	if err == nil {
		t.Fatal("expected error for non-existent emote")
	}
	if !strings.Contains(err.Error(), "emote not found") {
		t.Fatalf("expected 'emote not found' error, got: %v", err)
	}
}

func TestGetEmote_MissingIDAndName(t *testing.T) {
	p := newTestPlugin(t)

	_, err := p.ExecuteTool("get_emote", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error when both id and name are missing")
	}
	if !strings.Contains(err.Error(), "id or name is required") {
		t.Fatalf("expected 'id or name is required' error, got: %v", err)
	}
}

func TestGetEmote_FileNotFound(t *testing.T) {
	p := newTestPlugin(t)
	guildID := "global"

	entry := EmoteEntry{
		ID:       "file-missing-id",
		Name:     "missing_file_emote",
		FileName: "nonexistent.png",
	}
	addTestEmotes(t, p.storage, guildID, []EmoteEntry{entry})

	_, err := p.ExecuteTool("get_emote", map[string]interface{}{
		"id": "file-missing-id",
	})
	if err == nil {
		t.Fatal("expected error when emote file is missing on disk")
	}
	if !strings.Contains(err.Error(), "emote file not found on disk") {
		t.Fatalf("expected 'emote file not found on disk' error, got: %v", err)
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

	expected := []string{"list_emotes", "search_emote", "get_emote"}
	for _, name := range expected {
		if !toolNames[name] {
			t.Fatalf("expected tool %q in ListTools output", name)
		}
	}
}

func TestIsAllowedFormat(t *testing.T) {
	allowed := []string{"png", "jpg", "webp"}

	tests := []struct {
		format   string
		expected bool
	}{
		{"png", true},
		{"jpg", true},
		{"webp", true},
		{"gif", false},
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

func TestMatchScore(t *testing.T) {
	q1 := "cat"
	s1 := matchScore(q1, "happy_cat", "a cat being happy", []string{"cat", "happy"})
	if s1 <= 3.0 {
		t.Fatalf("expected score > 3.0 for 'cat' matching name+desc+tags, got %f", s1)
	}

	s2 := matchScore(q1, "sad_dog", "a dog being sad", []string{"dog", "sad"})
	if s2 != 0 {
		t.Fatalf("expected score 0 for non-matching query, got %f", s2)
	}

	s3 := matchScore(q1, "cat_only", "", nil)
	if s3 != 3.0 {
		t.Fatalf("expected score 3.0 for name-only match, got %f", s3)
	}

	s4 := matchScore(q1, "", "description about cat", nil)
	if s4 != 1.0 {
		t.Fatalf("expected score 1.0 for desc-only match, got %f", s4)
	}

	s5 := matchScore(q1, "", "", []string{"cat", "meow"})
	if s5 != 1.5 {
		t.Fatalf("expected score 1.5 for single-tag match, got %f", s5)
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

	if len(cfg.AllowedFormats) != 4 {
		t.Fatalf("expected 4 allowed formats, got %d", len(cfg.AllowedFormats))
	}
	expectedFormats := []string{"png", "jpg", "jpeg", "webp"}
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
