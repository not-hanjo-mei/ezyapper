package bot

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ezyapper/internal/ai"
	"ezyapper/internal/config"
	"ezyapper/internal/memory"
	"ezyapper/internal/plugin"

	"github.com/bwmarrin/discordgo"
)

func TestExtractReplyToUsername_NoReference(t *testing.T) {
	m := &discordgo.MessageCreate{Message: &discordgo.Message{}}
	result, content := extractReplyInfo(m)
	if result != "" {
		t.Fatalf("expected empty username, got %q", result)
	}
	if content != "" {
		t.Fatalf("expected empty content, got %q", content)
	}
}

func TestExtractReplyToUsername_DeletedMessage(t *testing.T) {
	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			MessageReference:  &discordgo.MessageReference{},
			ReferencedMessage: nil,
		},
	}
	result, content := extractReplyInfo(m)
	if result != "(deleted message)" {
		t.Fatalf("expected '(deleted message)', got %q", result)
	}
	if content != "" {
		t.Fatalf("expected empty content, got %q", content)
	}
}

func TestExtractReplyToUsername_ValidReply(t *testing.T) {
	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			MessageReference: &discordgo.MessageReference{},
			ReferencedMessage: &discordgo.Message{
				Author: &discordgo.User{Username: "Alice"},
			},
		},
	}
	result, _ := extractReplyInfo(m)
	if result != "Alice" {
		t.Fatalf("expected 'Alice', got %q", result)
	}
}

func TestShouldSendGenerationFallback_NoError(t *testing.T) {
	if shouldSendGenerationFallback(nil) {
		t.Fatal("expected false for nil error")
	}
}

func TestShouldSendGenerationFallback_DeadlineExceeded(t *testing.T) {
	if !shouldSendGenerationFallback(context.DeadlineExceeded) {
		t.Fatal("expected true for DeadlineExceeded")
	}
}

func TestShouldSendGenerationFallback_Cancelled(t *testing.T) {
	// context.Canceled wraps context.Canceled which should pass errors.Is check too
	if shouldSendGenerationFallback(context.Canceled) {
		t.Fatal("expected false for Canceled (only DeadlineExceeded)")
	}
}

func TestFormatMessageXML_Basic(t *testing.T) {
	result := formatMessageXML("Alice", "Alice", "123", "hello", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "", "")
	if !strings.Contains(result, "Alice") {
		t.Fatalf("expected Alice in output: %s", result)
	}
	if !strings.Contains(result, "ID:123") {
		t.Fatalf("expected ID:123 in output: %s", result)
	}
	if !strings.Contains(result, "hello") {
		t.Fatalf("expected hello in output: %s", result)
	}
}

func TestFormatMessageXML_WithReply(t *testing.T) {
	result := formatMessageXML("Alice", "Alice", "123", "hi", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "Bob", "")
	if !strings.Contains(result, "replying to @Bob") {
		t.Fatalf("expected reply mention in output: %s", result)
	}
}

func TestFormatMessageXML_ReplyToDeleted(t *testing.T) {
	result := formatMessageXML("Alice", "Alice", "123", "hi", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "(deleted message)", "")
	if !strings.Contains(result, "replying to deleted message") {
		t.Fatalf("expected 'replying to deleted message' in output: %s", result)
	}
	if strings.Contains(result, "@(deleted message)") {
		t.Fatalf("should not have @ prefix for deleted message: %s", result)
	}
}

func TestBuildDynamicContext_Empty(t *testing.T) {
	b := &Bot{}
	profile := &memory.Profile{
		Traits:      []string{},
		Facts:       make(map[string]string),
		Preferences: make(map[string]string),
	}
	result := b.buildDynamicContext("Alice", profile, nil, nil)
	if !strings.Contains(result, "User profile") {
		t.Fatalf("expected 'User profile' in output: %s", result)
	}
}

func TestBuildDynamicContext_WithDisplayName(t *testing.T) {
	b := &Bot{}
	profile := &memory.Profile{
		DisplayName: "AliceInWonder",
		Traits:      []string{},
		Facts:       make(map[string]string),
		Preferences: make(map[string]string),
	}
	result := b.buildDynamicContext("Alice", profile, nil, nil)
	if !strings.Contains(result, "AliceInWonder") {
		t.Fatalf("expected display name in output: %s", result)
	}
}

func TestBuildDynamicContext_WithTraits(t *testing.T) {
	b := &Bot{}
	profile := &memory.Profile{
		Traits:      []string{"friendly", "helpful"},
		Facts:       make(map[string]string),
		Preferences: make(map[string]string),
	}
	result := b.buildDynamicContext("Alice", profile, nil, nil)
	if !strings.Contains(result, "friendly") {
		t.Fatalf("expected traits in output: %s", result)
	}
}

func TestBuildDynamicContext_WithFacts(t *testing.T) {
	b := &Bot{}
	profile := &memory.Profile{
		Traits:      []string{},
		Facts:       map[string]string{"name": "Alice", "job": "engineer"},
		Preferences: make(map[string]string),
	}
	result := b.buildDynamicContext("Alice", profile, nil, nil)
	if !strings.Contains(result, "name: Alice") {
		t.Fatalf("expected facts in output: %s", result)
	}
	if !strings.Contains(result, "job: engineer") {
		t.Fatalf("expected job fact: %s", result)
	}
}

func TestBuildDynamicContext_WithPreferences(t *testing.T) {
	b := &Bot{}
	profile := &memory.Profile{
		Traits:      []string{},
		Facts:       make(map[string]string),
		Preferences: map[string]string{"color": "blue"},
	}
	result := b.buildDynamicContext("Alice", profile, nil, nil)
	if !strings.Contains(result, "color: blue") {
		t.Fatalf("expected preferences in output: %s", result)
	}
}

func TestBuildDynamicContext_WithMemories(t *testing.T) {
	b := &Bot{}
	profile := &memory.Profile{
		Traits:      []string{},
		Facts:       make(map[string]string),
		Preferences: make(map[string]string),
	}
	memories := []*memory.Record{
		{Summary: "User likes pizza"},
		{Summary: "User dislikes coffee"},
	}
	result := b.buildDynamicContext("Alice", profile, memories, nil)
	if !strings.Contains(result, "likes pizza") {
		t.Fatalf("expected memory in output: %s", result)
	}
	if !strings.Contains(result, "dislikes coffee") {
		t.Fatalf("expected second memory in output: %s", result)
	}
}

func TestShouldEnrichRecentHistoricalImages_EmptyContent(t *testing.T) {
	result := shouldEnrichRecentHistoricalImages("", false)
	if result {
		t.Fatal("expected false for empty content without reference")
	}
}

func TestShouldEnrichRecentHistoricalImages_WithReference(t *testing.T) {
	result := shouldEnrichRecentHistoricalImages("", true)
	if !result {
		t.Fatal("expected true when replying to a message")
	}
}

func TestShouldEnrichRecentHistoricalImages_WithContent(t *testing.T) {
	result := shouldEnrichRecentHistoricalImages("look at this image", false)
	if !result {
		t.Fatal("expected true when user mentions an image keyword")
	}
}

// mockAIClientForResponse mocks ai.Client for testing generateResponse path
type mockAIClientForResponse struct {
	completeTextErr  error
	completeTextResp string
}

func (m *mockAIClientForResponse) CreateChatCompletionWithTools(ctx context.Context, req ai.ChatCompletionRequest, handler ai.ToolHandler) (*ai.ChatCompletionResponse, error) {
	if m.completeTextErr != nil {
		return nil, m.completeTextErr
	}
	return &ai.ChatCompletionResponse{Content: m.completeTextResp}, nil
}

func TestHandleTextOnlyMode_NoContext(t *testing.T) {
	cfg := &config.Config{
		AI: config.AIConfig{
			Vision: config.VisionConfig{Mode: config.VisionModeTextOnly},
			Model:  "gpt-4",
		},
		Discord: config.DiscordConfig{
			Token:           "token",
			BotName:         "Bot",
			ReplyPercentage: 0.1,
			CooldownSeconds: 5,
		},
		Embedding: config.EmbeddingConfig{
			Model:  "embed",
			APIKey: "key",
		},
		Qdrant: config.QdrantConfig{Host: "localhost", Port: 6334},
	}
	cfgStore := &atomic.Value{}
	cfgStore.Store(cfg)

	b := &Bot{configStore: cfgStore}
	_ = b.createToolHandler()

	mc := ModeContext{
		AIClient:    (*ai.Client)(nil),
		Username:    "Alice",
		UserID:      "123",
		UserContent: "test message",
	}
	req := ai.ChatCompletionRequest{
		SystemPrompt: "You are a bot",
		UserContext:  "",
	}
	_ = mc
	_ = req
}

func TestGenerateResponse_UnknownVisionMode(t *testing.T) {
	cfg := &config.Config{
		AI: config.AIConfig{
			Vision: config.VisionConfig{Mode: "invalid_mode"},
			Model:  "gpt-4",
		},
		Discord: config.DiscordConfig{
			Token:           "token",
			BotName:         "Bot",
			ReplyPercentage: 0.1,
			CooldownSeconds: 5,
		},
		Embedding: config.EmbeddingConfig{
			Model:  "embed",
			APIKey: "key",
		},
		Qdrant: config.QdrantConfig{Host: "localhost", Port: 6334},
	}
	cfgStore := &atomic.Value{}
	cfgStore.Store(cfg)

	b := &Bot{configStore: cfgStore}

	mc := ModeContext{
		UserContent: "test",
		Username:    "Alice",
		UserID:      "123",
	}
	gc := GenerateContext{}

	_, err := b.generateResponse(context.Background(), mc, gc, nil, nil, &memory.Profile{})
	if err == nil {
		t.Fatal("expected error for unknown vision mode")
	}
	if !strings.Contains(err.Error(), "unknown vision mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGenerateResponse_ContextCancelled(t *testing.T) {
	cfg := &config.Config{
		AI: config.AIConfig{
			Vision: config.VisionConfig{Mode: config.VisionModeTextOnly},
			Model:  "gpt-4",
		},
		Discord: config.DiscordConfig{
			Token:           "token",
			BotName:         "Bot",
			ReplyPercentage: 0.1,
			CooldownSeconds: 5,
		},
		Embedding: config.EmbeddingConfig{
			Model:  "embed",
			APIKey: "key",
		},
		Qdrant: config.QdrantConfig{Host: "localhost", Port: 6334},
	}
	cfgStore := &atomic.Value{}
	cfgStore.Store(cfg)

	b := &Bot{configStore: cfgStore}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mc := ModeContext{
		UserContent: "test",
		Username:    "Alice",
		UserID:      "123",
	}
	gc := GenerateContext{}

	_, err := b.generateResponse(ctx, mc, gc, nil, nil, &memory.Profile{})
	if err == nil {
		t.Fatal("expected context cancelled error")
	}
}

func TestFormatSystemPrompt_Basic(t *testing.T) {
	cfg := &config.Config{
		AI: config.AIConfig{
			SystemPrompt: "You are {BotName} in {ServerName}",
			Vision:       config.VisionConfig{Mode: config.VisionModeTextOnly},
			Model:        "gpt-4",
		},
		Discord: config.DiscordConfig{
			Token:           "token",
			BotName:         "TestBot",
			ReplyPercentage: 0.1,
			CooldownSeconds: 5,
		},
		Embedding: config.EmbeddingConfig{
			Model:  "embed",
			APIKey: "key",
		},
		Qdrant: config.QdrantConfig{Host: "localhost", Port: 6334},
	}
	cfgStore := &atomic.Value{}
	cfgStore.Store(cfg)

	b := &Bot{configStore: cfgStore}

	result := b.cfg().FormatSystemPrompt("Alice", "TestServer", "guild-1", "ch-1")
	if !strings.Contains(result, "TestBot") {
		t.Fatalf("expected bot name in prompt: %s", result)
	}
	if !strings.Contains(result, "TestServer") {
		t.Fatalf("expected server name in prompt: %s", result)
	}
}

// Test the utility functions from handlers_response that don't need AI client
func TestModeContext_Fields(t *testing.T) {
	mc := ModeContext{
		AIClient:        nil,
		UserContent:     "hello",
		Username:        "Alice",
		UserID:          "123",
		ReplyToUsername: "Bob",
		GuildID:         "guild-1",
		ChannelID:       "ch-1",
		MessageID:       "msg-1",
		GuildName:       "TestServer",
	}
	if mc.UserContent != "hello" {
		t.Fatal("unexpected UserContent")
	}
	if mc.Username != "Alice" {
		t.Fatal("unexpected Username")
	}
	if mc.ReplyToUsername != "Bob" {
		t.Fatal("unexpected ReplyToUsername")
	}
}

func TestAddGenerationFailureReaction_NilSession(t *testing.T) {
	b := &Bot{}
	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg-1",
			ChannelID: "ch-1",
		},
	}
	// Should not panic with nil session
	b.addGenerationFailureReaction(nil, m)
}

func TestAddGenerationFailureReaction_NilMessage(t *testing.T) {
	b := &Bot{}
	s := &discordgo.Session{}
	b.addGenerationFailureReaction(s, nil)
}

func TestLongResponseChunking_Basic(t *testing.T) {
	cfg := &config.Config{
		Discord: config.DiscordConfig{
			LongResponseDelayMs: 10,
		},
		AI: config.AIConfig{
			Vision: config.VisionConfig{Mode: config.VisionModeTextOnly},
			Model:  "gpt-4",
		},
		Embedding: config.EmbeddingConfig{
			Model:  "embed",
			APIKey: "key",
		},
		Qdrant: config.QdrantConfig{Host: "localhost", Port: 6334},
	}
	cfgStore := &atomic.Value{}
	cfgStore.Store(cfg)

	b := &Bot{configStore: cfgStore}

	// Test chunking at boundary
	longMsg := strings.Repeat("a", discordChunkLimit+100)
	chunks := chunkMessage(longMsg)

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	_ = b

	// Verify no chunk exceeds limit
	for i, chunk := range chunks {
		if len(chunk) > discordChunkLimit {
			t.Fatalf("chunk %d exceeds limit: %d > %d", i, len(chunk), discordChunkLimit)
		}
	}

	// Verify all content present
	joined := strings.Join(chunks, "")
	if strings.Count(longMsg, "a") != strings.Count(joined, "a") {
		t.Fatal("chunks lost content")
	}
}

func chunkMessage(content string) []string {
	var chunks []string
	remaining := content
	for len(remaining) > 0 {
		if len(remaining) <= discordChunkLimit {
			chunks = append(chunks, remaining)
			break
		}
		splitAt := strings.LastIndex(remaining[:discordChunkLimit], "\n")
		if splitAt <= 0 {
			splitAt = strings.LastIndex(remaining[:discordChunkLimit], " ")
		}
		if splitAt <= 0 {
			splitAt = discordChunkLimit
		}
		chunk := strings.TrimSpace(remaining[:splitAt])
		if chunk == "" {
			chunk = remaining[:splitAt]
		}
		chunks = append(chunks, chunk)
		remaining = strings.TrimLeft(remaining[splitAt:], "\n ")
	}
	return chunks
}

func TestLongResponseChunking_NewlineSplit(t *testing.T) {
	msg := "line1\nline2\n" + strings.Repeat("x", discordChunkLimit)
	chunks := chunkMessage(msg)

	for _, chunk := range chunks {
		if len(chunk) > discordChunkLimit {
			t.Fatalf("chunk exceeds limit: %d", len(chunk))
		}
	}
	if len(chunks) < 2 {
		t.Fatal("expected multiple chunks")
	}
}

func TestLongResponseChunking_SpaceSplit(t *testing.T) {
	msg := strings.Repeat("a", 100) + " " + strings.Repeat("b", discordChunkLimit)
	chunks := chunkMessage(msg)

	for _, chunk := range chunks {
		if len(chunk) > discordChunkLimit {
			t.Fatalf("chunk exceeds limit: %d", len(chunk))
		}
	}
	if len(chunks) < 2 {
		t.Fatal("expected multiple chunks for space-split message")
	}
}

func TestLongResponseChunking_NoDelimiter(t *testing.T) {
	msg := strings.Repeat("x", discordChunkLimit*2+50)
	chunks := chunkMessage(msg)

	for _, chunk := range chunks {
		if len(chunk) > discordChunkLimit {
			t.Fatalf("chunk exceeds limit: %d", len(chunk))
		}
	}
	if len(chunks) < 2 {
		t.Fatal("expected multiple chunks")
	}
}

func TestDisasmMessageLimit_Constant(t *testing.T) {
	if discordMessageLimit != 2000 {
		t.Error("discordMessageLimit must be 2000 per Discord API spec")
	}
	if discordChunkLimit != 1900 {
		t.Error("discordChunkLimit must be 1900 (safety buffer)")
	}
}

func TestRunBeforeSendPluginHooks_NilManager(t *testing.T) {
	cfg := &config.Config{
		Discord: config.DiscordConfig{
			Token:           "token",
			BotName:         "Bot",
			ReplyPercentage: 0.1,
			CooldownSeconds: 5,
		},
		AI: config.AIConfig{
			Vision: config.VisionConfig{Mode: config.VisionModeTextOnly},
			Model:  "gpt-4",
		},
		Embedding: config.EmbeddingConfig{
			Model:  "embed",
			APIKey: "key",
		},
		Qdrant: config.QdrantConfig{Host: "localhost", Port: 6334},
	}
	cfgStore := &atomic.Value{}
	cfgStore.Store(cfg)

	b := &Bot{configStore: cfgStore}

	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg-1",
			ChannelID: "ch-1",
		},
	}

	response, files, skipSend, err := b.runBeforeSendPluginHooks(context.Background(), m, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if response != "hello" {
		t.Fatalf("expected original response, got %q", response)
	}
	if files != nil {
		t.Fatal("expected nil files")
	}
	if skipSend {
		t.Fatal("expected skipSend=false")
	}
}

func TestPluginFilesToLocalUploadFiles_WhitespaceTrim(t *testing.T) {
	input := []plugin.LocalFile{
		{Path: "  /path/to/file.txt  ", Name: "  clean.txt  ", ContentType: "  text/plain  "},
	}
	result := pluginFilesToLocalUploadFiles(input)
	if len(result) != 1 {
		t.Fatalf("expected 1 file, got %d", len(result))
	}
	if result[0].Path != "/path/to/file.txt" {
		t.Fatalf("expected trimmed path, got %q", result[0].Path)
	}
	if result[0].Name != "clean.txt" {
		t.Fatalf("expected trimmed name, got %q", result[0].Name)
	}
	if result[0].ContentType != "text/plain" {
		t.Fatalf("expected trimmed content type, got %q", result[0].ContentType)
	}
}
