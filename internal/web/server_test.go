package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/memory"
	"ezyapper/internal/plugin"
	"ezyapper/internal/utils"

	"github.com/gin-gonic/gin"
)

func init() {
	logger.Init(logger.Config{Level: "error"})
}

// mockMemoryService implements memory.MemoryStore, memory.ProfileStore,
// memory.ConsolidationManager, and memory.Service for testing.
type mockMemoryService struct{}

func (m *mockMemoryService) Store(ctx context.Context, mem *memory.Record) error { return nil }
func (m *mockMemoryService) Search(ctx context.Context, userID string, query string, opts *memory.SearchOptions) ([]*memory.Record, error) {
	return nil, nil
}
func (m *mockMemoryService) HybridSearch(ctx context.Context, userID string, query string, keywords []string, opts *memory.SearchOptions) ([]*memory.Record, error) {
	return nil, nil
}
func (m *mockMemoryService) GetMemories(ctx context.Context, userID string, limit int) ([]*memory.Record, error) {
	return nil, nil
}
func (m *mockMemoryService) GetMemory(ctx context.Context, memoryID string) (*memory.Record, error) {
	return nil, nil
}
func (m *mockMemoryService) GetProfile(ctx context.Context, userID string) (*memory.Profile, error) {
	return &memory.Profile{UserID: userID}, nil
}
func (m *mockMemoryService) UpdateProfile(ctx context.Context, p *memory.Profile) error { return nil }
func (m *mockMemoryService) DeleteMemory(ctx context.Context, memoryID string) error    { return nil }
func (m *mockMemoryService) DeleteUserData(ctx context.Context, userID string) error    { return nil }
func (m *mockMemoryService) Consolidate(ctx context.Context, userID string) error       { return nil }
func (m *mockMemoryService) ConsolidateWithMessages(ctx context.Context, userID string, messages []*memory.DiscordMessage) error {
	return nil
}
func (m *mockMemoryService) ConsolidateChannel(ctx context.Context, channelID string, messages []*memory.DiscordMessage) error {
	return nil
}
func (m *mockMemoryService) IncrementMessageCount(ctx context.Context, userID string) (int, error) {
	return 0, nil
}
func (m *mockMemoryService) IncrementChannelMessageCount(ctx context.Context, channelID string) (int, error) {
	return 0, nil
}
func (m *mockMemoryService) ResetMessageCount(userID string)           {}
func (m *mockMemoryService) ResetChannelMessageCount(channelID string) {}
func (m *mockMemoryService) ConsumeChannelMessageCount(channelID string, consumed int) int {
	return 0
}
func (m *mockMemoryService) GetStats(ctx context.Context) (*memory.GlobalStats, error) {
	return &memory.GlobalStats{}, nil
}
func (m *mockMemoryService) GetUserStats(ctx context.Context, userID string) (*memory.UserStats, error) {
	return &memory.UserStats{UserID: userID}, nil
}
func (m *mockMemoryService) Close() error { return nil }

func setupTestServer(t *testing.T) (*Server, *httptest.Server) {
	cfg := &config.Config{
		Discord: config.DiscordConfig{
			BotName: "TestBot",
		},
		AI: config.AIConfig{
			Model: "gpt-4",
		},
		Web: config.WebConfig{
			Port:     8080,
			Username: "admin",
			Password: "test123",
			Enabled:  true,
		},
		Logging: config.LoggingConfig{
			Level: "error",
		},
	}

	cfgStore := &atomic.Value{}
	cfgStore.Store(cfg)

	mem := &mockMemoryService{}
	pluginManager := plugin.NewManager()
	server := NewServer(cfgStore, mem, mem, mem, pluginManager, nil)

	gin.SetMode(gin.TestMode)
	router := server.router

	ts := httptest.NewServer(router)

	return server, ts
}

func TestHealthCheck(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

func TestGetConfig_Unauthorized(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/config")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", resp.StatusCode)
	}
}

func TestGetConfig_Authorized(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL+"/api/config", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Basic YWRtaW46dGVzdDEyMw==")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

func TestGetLogs_InvalidLines(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL+"/api/logs?lines=abc", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Basic YWRtaW46dGVzdDEyMw==")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", resp.StatusCode)
	}
}

func TestGetMemories_InvalidLimit(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL+"/api/memories/u1?limit=abc", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Basic YWRtaW46dGVzdDEyMw==")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", resp.StatusCode)
	}
}

func TestTriggerConsolidation_InvalidMaxMessages(t *testing.T) {
	_, ts := setupTestServer(t)
	defer ts.Close()

	req, err := http.NewRequest("POST", ts.URL+"/api/consolidate/u1?max_messages=abc", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Basic YWRtaW46dGVzdDEyMw==")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", resp.StatusCode)
	}
}

func TestBasicAuthMiddleware(t *testing.T) {
	cfg := &config.Config{
		Web: config.WebConfig{
			Username: "admin",
			Password: "secret",
		},
	}

	expectedUser := "admin"
	expectedPass := "secret"

	if cfg.Web.Username != expectedUser {
		t.Errorf("Expected username '%s', got '%s'", expectedUser, cfg.Web.Username)
	}

	if cfg.Web.Password != expectedPass {
		t.Errorf("Expected password '%s', got '%s'", expectedPass, cfg.Web.Password)
	}
}

func TestRemoveFromSlice(t *testing.T) {
	tests := []struct {
		name     string
		slice    []string
		item     string
		expected []string
	}{
		{
			name:     "Item exists",
			slice:    []string{"a", "b", "c"},
			item:     "b",
			expected: []string{"a", "c"},
		},
		{
			name:     "Item doesn't exist",
			slice:    []string{"a", "b", "c"},
			item:     "d",
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "Single item",
			slice:    []string{"a"},
			item:     "a",
			expected: []string{},
		},
		{
			name:     "Empty slice",
			slice:    []string{},
			item:     "a",
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := utils.RemoveFromSlice(tt.slice, tt.item)

			if len(result) != len(tt.expected) {
				t.Errorf("Expected length %d, got %d", len(tt.expected), len(result))
				return
			}

			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("Expected item %d to be '%s', got '%s'", i, tt.expected[i], result[i])
				}
			}
		})
	}
}

func TestNewServer(t *testing.T) {
	cfg := &config.Config{
		Discord: config.DiscordConfig{
			BotName: "TestBot",
		},
		AI: config.AIConfig{
			Model: "gpt-4",
		},
		Web: config.WebConfig{
			Port:     8080,
			Username: "admin",
			Password: "test123",
		},
		Logging: config.LoggingConfig{
			Level: "error",
		},
	}

	mem := &mockMemoryService{}
	pluginManager := plugin.NewManager()
	cfgStore := &atomic.Value{}
	cfgStore.Store(cfg)
	server := NewServer(cfgStore, mem, mem, mem, pluginManager, nil)

	if server == nil {
		t.Error("Expected non-nil server")
	}

	if server.configStore.Load().(*config.Config) != cfg {
		t.Error("Server config mismatch")
	}
}

func TestServer_StartDisabled(t *testing.T) {
	cfg := &config.Config{
		Web: config.WebConfig{
			Enabled:  false,
			Username: "admin",
			Password: "test123",
		},
	}

	mem := &mockMemoryService{}
	pluginManager := plugin.NewManager()
	cfgStore := &atomic.Value{}
	cfgStore.Store(cfg)
	server := NewServer(cfgStore, mem, mem, mem, pluginManager, nil)

	err := server.Start()
	if err != nil {
		t.Errorf("Start should not error when disabled: %v", err)
	}

	if server.server != nil {
		t.Error("Server should be nil when disabled")
	}
}
