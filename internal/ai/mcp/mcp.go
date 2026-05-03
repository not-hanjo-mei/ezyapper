// Package mcp provides the Model Context Protocol client for connecting to
// external tool servers (stdio, SSE) and calling tools across multiple providers.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"sync"

	"ezyapper/internal/config"
	"ezyapper/internal/logger"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	openai "github.com/sashabaranov/go-openai"
)

// MCPManager manages connections to MCP (Model Context Protocol) servers.
// It handles tool discovery and execution across multiple external tool providers.
type MCPManager struct {
	mu       sync.RWMutex
	sessions map[string]*mcp.ClientSession
	servers  []config.MCPServer
}

// NewMCPManager creates a new MCP manager for the given server configurations.
func NewMCPManager(servers []config.MCPServer) *MCPManager {
	return &MCPManager{
		sessions: make(map[string]*mcp.ClientSession),
		servers:  servers,
	}
}

// Connect establishes connections to all configured MCP servers.
func (m *MCPManager) Connect(ctx context.Context) error {
	var errs []error
	for _, server := range m.servers {
		if err := m.connectServer(ctx, server); err != nil {
			logger.Warnf("Failed to connect to MCP server '%s': %v", server.Name, err)
			errs = append(errs, fmt.Errorf("%s: %w", server.Name, err))
			continue
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("mcp connect errors: %w", errors.Join(errs...))
	}
	return nil
}

func (m *MCPManager) connectServer(ctx context.Context, server config.MCPServer) error {
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "ezyapper",
		Version: "1.0.0",
	}, nil)

	var transport mcp.Transport
	switch server.Type {
	case "stdio":
		cmd := exec.Command(server.Command, server.Args...)
		if len(server.Env) > 0 {
			cmd.Env = append(cmd.Env, os.Environ()...)
			for k, v := range server.Env {
				cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
			}
		}
		transport = &mcp.CommandTransport{Command: cmd}
	case "sse":
		transport = &mcp.SSEClientTransport{Endpoint: server.URL}
	default:
		return fmt.Errorf("unsupported transport type: %s", server.Type)
	}

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	m.mu.Lock()
	m.sessions[server.Name] = session
	m.mu.Unlock()
	logger.Infof("Connected to MCP server '%s'", server.Name)
	return nil
}

// GetAllTools lists all tools from all connected MCP servers.
func (m *MCPManager) GetAllTools(ctx context.Context) ([]MCPTool, error) {
	m.mu.RLock()
	sessions := make(map[string]*mcp.ClientSession, len(m.sessions))
	maps.Copy(sessions, m.sessions)
	m.mu.RUnlock()

	var allTools []MCPTool
	for name, session := range sessions {
		tools, err := m.getServerTools(ctx, name, session)
		if err != nil {
			logger.Warnf("Failed to get tools from MCP server '%s': %v", name, err)
			continue
		}
		allTools = append(allTools, tools...)
	}
	return allTools, nil
}

func (m *MCPManager) getServerTools(ctx context.Context, serverName string, session *mcp.ClientSession) ([]MCPTool, error) {
	result, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return nil, err
	}
	tools := make([]MCPTool, 0, len(result.Tools))
	for _, tool := range result.Tools {
		tools = append(tools, MCPTool{
			ServerName: serverName,
			Tool:       tool,
		})
	}
	return tools, nil
}

// CallTool invokes a tool on a specific MCP server with the given arguments.
func (m *MCPManager) CallTool(ctx context.Context, serverName, toolName string, arguments map[string]interface{}) (string, error) {
	m.mu.RLock()
	session, exists := m.sessions[serverName]
	m.mu.RUnlock()
	if !exists {
		return "", fmt.Errorf("mcp server '%s' not connected", serverName)
	}
	params := &mcp.CallToolParams{
		Name:      toolName,
		Arguments: arguments,
	}
	result, err := session.CallTool(ctx, params)
	if err != nil {
		return "", fmt.Errorf("tool call failed: %w", err)
	}
	if result.IsError {
		return "", fmt.Errorf("tool returned error")
	}
	var output string
	for _, content := range result.Content {
		if textContent, ok := content.(*mcp.TextContent); ok {
			output += textContent.Text
		}
	}
	return output, nil
}

// MCPTool wraps an MCP tool with its server origin for tracking.
type MCPTool struct {
	ServerName string
	Tool       *mcp.Tool
}

// ToOpenAITools converts MCP tools to OpenAI-compatible format for use in LLM tool calling.
func ToOpenAITools(tools []MCPTool) []openai.Tool {
	openaiTools := make([]openai.Tool, 0, len(tools))
	for _, tool := range tools {
		schemaJSON, err := json.Marshal(tool.Tool.InputSchema)
		if err != nil {
			logger.Warnf("[mcp] failed to marshal tool schema for '%s': %v", tool.Tool.Name, err)
			continue
		}
		var schema map[string]interface{}
		if err := json.Unmarshal(schemaJSON, &schema); err != nil {
			logger.Warnf("[mcp] failed to unmarshal tool schema for '%s': %v", tool.Tool.Name, err)
			continue
		}
		openaiTools = append(openaiTools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        fmt.Sprintf("%s_%s", tool.ServerName, tool.Tool.Name),
				Description: fmt.Sprintf("[%s] %s", tool.ServerName, tool.Tool.Description),
				Parameters:  schema,
			},
		})
	}
	return openaiTools
}

// Close shuts down all MCP server connections.
func (m *MCPManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, session := range m.sessions {
		if err := session.Close(); err != nil {
			logger.Warnf("Error closing MCP session '%s': %v", name, err)
		}
	}
	return nil
}
