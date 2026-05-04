package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ezyapper/internal/types"
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

func decodeJSONRPCParams(raw any, target any) error {
	if raw == nil {
		return nil
	}

	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}

	if string(encoded) == "null" {
		return nil
	}

	return json.Unmarshal(encoded, target)
}

func writeJSONRPCResponse(enc *json.Encoder, id int64, result any, err error) error {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
	}

	if err != nil {
		resp.Error = &jsonRPCError{Code: -32000, Message: err.Error()}
	} else {
		if result == nil {
			result = map[string]any{}
		}

		resultBytes, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			resp.Error = &jsonRPCError{Code: -32603, Message: fmt.Sprintf("failed to marshal jsonrpc response: %v", marshalErr)}
		} else {
			resp.Result = json.RawMessage(resultBytes)
		}
	}

	if encodeErr := enc.Encode(resp); encodeErr != nil {
		return fmt.Errorf("failed to write jsonrpc response: %w", encodeErr)
	}

	return nil
}

// Serve starts a plugin and connects to the host process.
// This should be called from the plugin's main function.
func Serve(impl Interface) error {
	if impl == nil {
		return fmt.Errorf("plugin implementation is nil")
	}

	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)

	for {
		var req jsonRPCRequest
		if err := decoder.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("failed to read jsonrpc request: %w", err)
		}

		if strings.TrimSpace(req.Method) == "" {
			if err := writeJSONRPCResponse(encoder, req.ID, nil, fmt.Errorf("jsonrpc -32600: invalid request")); err != nil {
				return err
			}
			continue
		}

		var result any
		var callErr error

		switch req.Method {
		case "info":
			result, callErr = impl.Info()
		case "on_message":
			var msg types.DiscordMessage
			if err := decodeJSONRPCParams(req.Params, &msg); err != nil {
				callErr = fmt.Errorf("invalid params for on_message")
				break
			}

			var shouldContinue bool
			shouldContinue, callErr = impl.OnMessage(msg)
			result = shouldContinue
		case "on_response":
			var args ResponseArgs
			if err := decodeJSONRPCParams(req.Params, &args); err != nil {
				callErr = fmt.Errorf("invalid params for on_response")
				break
			}

			callErr = impl.OnResponse(args.Message, args.Response)
			result = map[string]any{}
		case "before_send":
			provider, ok := impl.(BeforeSendProvider)
			if !ok {
				result = BeforeSendResult{}
				break
			}

			var args BeforeSendArgs
			if err := decodeJSONRPCParams(req.Params, &args); err != nil {
				callErr = fmt.Errorf("invalid params for before_send")
				break
			}

			result, callErr = provider.BeforeSend(args.Message, args.Response)
		case "list_tools":
			provider, ok := impl.(ToolProvider)
			if !ok {
				result = []ToolSpec{}
				break
			}

			result, callErr = provider.ListTools()
		case "execute_tool":
			provider, ok := impl.(ToolProvider)
			if !ok {
				callErr = fmt.Errorf("plugin does not implement tool provider")
				break
			}

			var args ExecuteToolArgs
			if err := decodeJSONRPCParams(req.Params, &args); err != nil {
				callErr = fmt.Errorf("invalid params for execute_tool")
				break
			}
			if args.Arguments == nil {
				args.Arguments = map[string]any{}
			}

			result, callErr = provider.ExecuteTool(args.Name, args.Arguments)
		case "shutdown":
			callErr = impl.Shutdown()
			result = map[string]any{}
		default:
			callErr = fmt.Errorf("jsonrpc -32601: method not found")
		}

		if err := writeJSONRPCResponse(encoder, req.ID, result, callErr); err != nil {
			return err
		}
	}
}
