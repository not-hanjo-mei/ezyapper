package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"ezyapper/internal/plugin"
	"ezyapper/internal/types"

	"gopkg.in/yaml.v3"
)

type kimiConfig struct {
	BaseURL        string
	APIKey         string
	TimeoutSeconds int
	Formulas       []string
	ToolTimeoutMs  int
}

type fileConfig struct {
	BaseURL        *string  `yaml:"base_url"`
	APIKey         *string  `yaml:"api_key"`
	TimeoutSeconds *int     `yaml:"timeout_seconds"`
	Formulas       []string `yaml:"formulas"`
	ToolTimeouts   *struct {
		DefaultMs *int `yaml:"default_ms"`
	} `yaml:"tool_timeouts"`
}

type formulaToolsResponse struct {
	Tools []formulaTool `json:"tools"`
}

type formulaTool struct {
	Type     string          `json:"type"`
	Function formulaFunction `json:"function"`
}

type formulaFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type fiberRequest struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type fiberResponse struct {
	Status  string       `json:"status"`
	Error   string       `json:"error"`
	Context fiberContext `json:"context"`
}

type fiberContext struct {
	Output          interface{} `json:"output"`
	EncryptedOutput interface{} `json:"encrypted_output"`
	Error           interface{} `json:"error"`
}

type kimiToolsPlugin struct {
	cfg       kimiConfig
	http      *http.Client
	tools     []plugin.ToolSpec
	toolToURI map[string]string
	mu        sync.RWMutex
}

func newKimiToolsPlugin(cfg kimiConfig) (*kimiToolsPlugin, error) {
	p := &kimiToolsPlugin{
		cfg: cfg,
		http: &http.Client{
			Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second,
		},
		toolToURI: make(map[string]string),
	}

	if err := p.loadTools(context.Background()); err != nil {
		return nil, err
	}

	return p, nil
}

func (p *kimiToolsPlugin) Info() (plugin.Info, error) {
	return plugin.Info{
		Name:        "kimi-tools",
		Version:     "0.0.0",
		Author:      "EZyapper",
		Description: "Integrates Kimi official Formula tools",
		Priority:    20,
	}, nil
}

func (p *kimiToolsPlugin) OnMessage(msg types.DiscordMessage) (bool, error) {
	return true, nil
}

func (p *kimiToolsPlugin) OnResponse(msg types.DiscordMessage, response string) error {
	return nil
}

func (p *kimiToolsPlugin) Shutdown() error {
	p.http.CloseIdleConnections()
	return nil
}

func (p *kimiToolsPlugin) ListTools() ([]plugin.ToolSpec, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]plugin.ToolSpec, len(p.tools))
	copy(result, p.tools)
	return result, nil
}

func (p *kimiToolsPlugin) ExecuteTool(name string, args map[string]interface{}) (string, error) {
	p.mu.RLock()
	formulaURI, ok := p.toolToURI[name]
	p.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("unknown kimi formula tool: %s", name)
	}

	return p.callFiber(formulaURI, name, args)
}

func (p *kimiToolsPlugin) loadTools(ctx context.Context) error {
	allTools := make([]plugin.ToolSpec, 0)
	toolToURI := make(map[string]string)

	for _, formulaURI := range p.cfg.Formulas {
		tools, err := p.fetchFormulaTools(ctx, formulaURI)
		if err != nil {
			return fmt.Errorf("failed to load tools from %s: %w", formulaURI, err)
		}

		for _, t := range tools {
			if _, exists := toolToURI[t.Name]; exists {
				return fmt.Errorf("duplicate tool name %q across formulas", t.Name)
			}
			toolToURI[t.Name] = formulaURI
			allTools = append(allTools, t)
		}
	}

	sort.Slice(allTools, func(i, j int) bool {
		return allTools[i].Name < allTools[j].Name
	})

	p.mu.Lock()
	p.tools = allTools
	p.toolToURI = toolToURI
	p.mu.Unlock()

	return nil
}

func (p *kimiToolsPlugin) fetchFormulaTools(ctx context.Context, formulaURI string) ([]plugin.ToolSpec, error) {
	endpoint := joinURL(p.cfg.BaseURL, fmt.Sprintf("formulas/%s/tools", formulaURI))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create tools request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to request formula tools: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read formula tools response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("formula tools request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var data formulaToolsResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("failed to parse formula tools response: %w", err)
	}

	tools := make([]plugin.ToolSpec, 0, len(data.Tools))
	for _, t := range data.Tools {
		if t.Type != "function" {
			continue
		}

		if strings.TrimSpace(t.Function.Name) == "" {
			return nil, fmt.Errorf("received function tool with empty name from %s", formulaURI)
		}

		params := t.Function.Parameters
		if params == nil {
			params = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
		}

		tools = append(tools, plugin.ToolSpec{
			Name:        t.Function.Name,
			Description: strings.TrimSpace(t.Function.Description),
			Parameters:  params,
			TimeoutMs:   p.cfg.ToolTimeoutMs,
		})
	}

	if len(tools) == 0 {
		return nil, fmt.Errorf("formula %s returned no function tools", formulaURI)
	}

	return tools, nil
}

func (p *kimiToolsPlugin) callFiber(formulaURI, toolName string, args map[string]interface{}) (string, error) {
	if args == nil {
		args = map[string]interface{}{}
	}

	encodedArgs, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("failed to encode tool arguments: %w", err)
	}

	payload := fiberRequest{
		Name:      toolName,
		Arguments: string(encodedArgs),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to encode fiber request: %w", err)
	}

	endpoint := joinURL(p.cfg.BaseURL, fmt.Sprintf("formulas/%s/fibers", formulaURI))

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, bytes.NewReader(payloadBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create fiber request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("fiber request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read fiber response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fiber request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var data fiberResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("failed to parse fiber response: %w", err)
	}

	if strings.EqualFold(data.Status, "succeeded") {
		if out, ok := stringifyToolOutput(data.Context.Output); ok {
			return out, nil
		}
		if out, ok := stringifyToolOutput(data.Context.EncryptedOutput); ok {
			return out, nil
		}
		return "", fmt.Errorf("fiber succeeded but returned no output")
	}

	if msg, ok := stringifyToolOutput(data.Error); ok {
		return "", fmt.Errorf("fiber failed: %s", msg)
	}
	if msg, ok := stringifyToolOutput(data.Context.Error); ok {
		return "", fmt.Errorf("fiber failed: %s", msg)
	}
	if msg, ok := stringifyToolOutput(data.Context.Output); ok {
		return "", fmt.Errorf("fiber failed: %s", msg)
	}

	return "", fmt.Errorf("fiber failed with status %q", data.Status)
}

func stringifyToolOutput(value interface{}) (string, bool) {
	if value == nil {
		return "", false
	}

	if str, ok := value.(string); ok {
		return str, true
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return "", false
	}

	return string(encoded), true
}

func joinURL(baseURL, path string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(path, "/")
}

func pluginConfigPath() string {
	if cfg := strings.TrimSpace(os.Getenv("EZYAPPER_PLUGIN_CONFIG")); cfg != "" {
		return cfg
	}

	if dir := strings.TrimSpace(os.Getenv("EZYAPPER_PLUGIN_PATH")); dir != "" {
		return filepath.Join(dir, "config.yaml")
	}

	return "config.yaml"
}

func normalizeFormulaURI(uri string) string {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return ""
	}

	if !strings.Contains(uri, "/") {
		uri = "moonshot/" + uri
	}

	if !strings.Contains(uri, ":") {
		uri += ":latest"
	}

	return uri
}

func loadConfig() (kimiConfig, error) {
	var cfg kimiConfig
	path := pluginConfigPath()

	content, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("failed to read plugin config file %s: %w", path, err)
	}

	var raw fileConfig
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		return cfg, fmt.Errorf("invalid plugin config file %s: %w", path, err)
	}

	var errs []string

	if raw.BaseURL == nil || strings.TrimSpace(*raw.BaseURL) == "" {
		errs = append(errs, "base_url is required")
	} else {
		cfg.BaseURL = strings.TrimRight(strings.TrimSpace(*raw.BaseURL), "/")
	}

	if raw.APIKey == nil || strings.TrimSpace(*raw.APIKey) == "" {
		errs = append(errs, "api_key is required")
	} else {
		cfg.APIKey = strings.TrimSpace(*raw.APIKey)
	}

	if raw.TimeoutSeconds == nil {
		errs = append(errs, "timeout_seconds is required")
	} else if *raw.TimeoutSeconds <= 0 {
		errs = append(errs, "timeout_seconds must be a positive integer")
	} else {
		cfg.TimeoutSeconds = *raw.TimeoutSeconds
	}

	if len(raw.Formulas) == 0 {
		errs = append(errs, "formulas must contain at least one formula URI")
	} else {
		seen := make(map[string]struct{})
		for _, item := range raw.Formulas {
			normalized := normalizeFormulaURI(item)
			if normalized == "" {
				errs = append(errs, "formulas contains an empty value")
				continue
			}
			if _, exists := seen[normalized]; exists {
				continue
			}
			seen[normalized] = struct{}{}
			cfg.Formulas = append(cfg.Formulas, normalized)
		}
	}

	if raw.ToolTimeouts != nil && raw.ToolTimeouts.DefaultMs != nil {
		cfg.ToolTimeoutMs = *raw.ToolTimeouts.DefaultMs
	}
	if cfg.ToolTimeoutMs <= 0 {
		errs = append(errs, "tool_timeouts.default_ms is required and must be > 0")
	}

	if len(errs) > 0 {
		return cfg, fmt.Errorf("configuration errors in %s: %s", path, strings.Join(errs, "; "))
	}

	return cfg, nil
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[KIMI-TOOLS] Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	if cfg.ToolTimeoutMs <= 0 {
		fmt.Fprintf(os.Stderr, "[KIMI-TOOLS] tool_timeouts.default_ms is required and must be > 0\n")
		os.Exit(1)
	}

	p, err := newKimiToolsPlugin(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[KIMI-TOOLS] Failed to initialize plugin: %v\n", err)
		os.Exit(1)
	}

	if err := plugin.Serve(p); err != nil {
		fmt.Fprintf(os.Stderr, "[KIMI-TOOLS] Error: %v\n", err)
		os.Exit(1)
	}
}