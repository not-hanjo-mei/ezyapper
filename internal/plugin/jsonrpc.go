package plugin

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type stdioJSONRPCClient struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	enc    *json.Encoder
	dec    *json.Decoder
	mu     sync.Mutex
	nextID int64
	dead   atomic.Bool
}

func newStdioJSONRPCClient(stdin io.WriteCloser, stdout io.ReadCloser) *stdioJSONRPCClient {
	return &stdioJSONRPCClient{
		stdin:  stdin,
		stdout: stdout,
		enc:    json.NewEncoder(stdin),
		dec:    json.NewDecoder(stdout),
		nextID: 1,
	}
}

func (c *stdioJSONRPCClient) Close() {
	if c == nil {
		return
	}

	if c.stdin != nil {
		c.stdin.Close()
	}
	if c.stdout != nil {
		c.stdout.Close()
	}
}

func (c *stdioJSONRPCClient) Call(method string, params any, reply any) error {
	if c == nil {
		return fmt.Errorf("jsonrpc client is nil")
	}

	if c.dead.Load() {
		return fmt.Errorf("jsonrpc client is dead: transport timed out and connection is stale")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if params == nil {
		params = map[string]any{}
	}

	id := c.nextID
	c.nextID++

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	if err := c.enc.Encode(req); err != nil {
		return fmt.Errorf("failed to write jsonrpc request: %w", err)
	}

	for {
		if c.dead.Load() {
			return fmt.Errorf("jsonrpc client is dead: transport timed out and connection is stale")
		}

		var resp jsonRPCResponse
		if err := c.dec.Decode(&resp); err != nil {
			return fmt.Errorf("failed to read jsonrpc response: %w", err)
		}

		if resp.ID != id {
			continue
		}

		if resp.Error != nil {
			return fmt.Errorf("jsonrpc %d: %s", resp.Error.Code, strings.TrimSpace(resp.Error.Message))
		}

		if reply == nil || len(resp.Result) == 0 || string(resp.Result) == "null" {
			return nil
		}

		if err := json.Unmarshal(resp.Result, reply); err != nil {
			return fmt.Errorf("failed to decode jsonrpc result for %s: %w", method, err)
		}

		return nil
	}
}

func listPluginToolsJSONRPC(client *stdioJSONRPCClient, wg *sync.WaitGroup, timeout time.Duration) ([]ToolSpec, error) {
	tools := []ToolSpec{}
	err := callJSONRPCWithTimeout(client, wg, "list_tools", map[string]any{}, &tools, timeout)
	if err != nil {
		return nil, err
	}

	return tools, nil
}
