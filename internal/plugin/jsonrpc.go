package plugin

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ezyapper/internal/logger"
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
	dec    *FrameDecoder
	// name labels log lines; it is set once the plugin reports its info.
	name   atomic.Value
	mu     sync.Mutex
	nextID int64
	dead   atomic.Bool
	// deadReason records why the transport was marked dead, for logs.
	deadReason atomic.Value
}

// markDead flags the transport as unusable and records why.
func (c *stdioJSONRPCClient) markDead(reason string) {
	if c == nil {
		return
	}
	if c.dead.CompareAndSwap(false, true) {
		c.deadReason.Store(reason)
	}
}

// DeadReason returns why the transport was marked dead, or "" if alive.
func (c *stdioJSONRPCClient) DeadReason() string {
	if c == nil || !c.dead.Load() {
		return ""
	}
	if v, ok := c.deadReason.Load().(string); ok && v != "" {
		return v
	}
	return "unknown reason"
}

func newStdioJSONRPCClient(stdin io.WriteCloser, stdout io.ReadCloser) *stdioJSONRPCClient {
	return &stdioJSONRPCClient{
		stdin:  stdin,
		stdout: stdout,
		enc:    json.NewEncoder(stdin),
		dec:    NewFrameDecoder(stdout),
		nextID: 1,
	}
}

// label returns the plugin name for log lines, or "unknown" before info.
func (c *stdioJSONRPCClient) label() string {
	if v, ok := c.name.Load().(string); ok && v != "" {
		return v
	}
	return "unknown"
}

// SetName sets the plugin name used in log lines.
func (c *stdioJSONRPCClient) SetName(name string) {
	if c != nil {
		c.name.Store(name)
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
		return fmt.Errorf("jsonrpc client is dead: %s", c.DeadReason())
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
		c.markDead(fmt.Sprintf("write failed during %s: %v", method, err))
		return fmt.Errorf("jsonrpc client is dead: failed to write jsonrpc request for %s: %w", method, err)
	}

	for {
		if c.dead.Load() {
			return fmt.Errorf("jsonrpc client is dead: %s", c.DeadReason())
		}

		var resp jsonRPCResponse
		if err := c.dec.Decode(&resp); err != nil {
			if IsMalformedFrame(err) {
				// A stray or corrupt line on the plugin's stdout must not
				// kill the transport. Log it and keep waiting for our reply.
				logger.Warnf("[plugin] %s: skipping %v (method=%s id=%d)", c.label(), err, method, id)
				continue
			}
			// The stream itself is broken (EOF or closed pipe): the plugin
			// process most likely exited. Mark the transport dead so the
			// manager restarts it instead of failing every later call.
			c.markDead(fmt.Sprintf("read failed during %s: %v", method, err))
			return fmt.Errorf("jsonrpc client is dead: failed to read jsonrpc response for %s: %w", method, err)
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
