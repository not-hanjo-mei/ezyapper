// Package server provides the JSON-RPC server implementation for plugins.
// This package is intended to be called from plugin binary main functions,
// NOT from the bot process itself. The bot only spawns plugins as subprocesses
// and communicates over stdio.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"ezyapper/internal/plugin"
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
	Code    int    `json:"code"`
	Message string `json:"message"`
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

func WriteJSONRPCResponse(enc *json.Encoder, id int64, result any, err error) error {
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

// rpcHandler dispatches one JSON-RPC method. A nil result with a nil error
// is serialised as an empty object by WriteJSONRPCResponse; a decode failure
// returns (nil, err) so the JSON-RPC error is reported unchanged.
type rpcHandler func(impl plugin.Interface, params any) (result any, err error)

func handleInfo(impl plugin.Interface, _ any) (any, error) {
	return impl.Info()
}

func handleOnMessage(impl plugin.Interface, params any) (any, error) {
	var msg types.DiscordMessage
	if err := decodeJSONRPCParams(params, &msg); err != nil {
		return nil, fmt.Errorf("invalid params for on_message")
	}
	return impl.OnMessage(msg)
}

func handleOnResponse(impl plugin.Interface, params any) (any, error) {
	var args plugin.ResponseArgs
	if err := decodeJSONRPCParams(params, &args); err != nil {
		return nil, fmt.Errorf("invalid params for on_response")
	}
	if err := impl.OnResponse(args.Message, args.Response); err != nil {
		return nil, err
	}
	return map[string]any{}, nil
}

func handleBeforeSend(impl plugin.Interface, params any) (any, error) {
	provider, ok := impl.(plugin.BeforeSendProvider)
	if !ok {
		return plugin.BeforeSendResult{}, nil
	}
	var args plugin.BeforeSendArgs
	if err := decodeJSONRPCParams(params, &args); err != nil {
		return nil, fmt.Errorf("invalid params for before_send")
	}
	return provider.BeforeSend(args.Message, args.Response)
}

func handleListTools(impl plugin.Interface, _ any) (any, error) {
	provider, ok := impl.(plugin.ToolProvider)
	if !ok {
		return []plugin.ToolSpec{}, nil
	}
	return provider.ListTools()
}

func handleExecuteTool(impl plugin.Interface, params any) (any, error) {
	provider, ok := impl.(plugin.ToolProvider)
	if !ok {
		return nil, fmt.Errorf("plugin does not implement tool provider")
	}
	var args plugin.ExecuteToolArgs
	if err := decodeJSONRPCParams(params, &args); err != nil {
		return nil, fmt.Errorf("invalid params for execute_tool")
	}
	if args.Arguments == nil {
		args.Arguments = map[string]any{}
	}
	return provider.ExecuteTool(args.Name, args.Arguments)
}

func handleShutdown(impl plugin.Interface, _ any) (any, error) {
	if err := impl.Shutdown(); err != nil {
		return nil, err
	}
	return map[string]any{}, nil
}

var rpcHandlers = map[string]rpcHandler{
	"info":         handleInfo,
	"on_message":   handleOnMessage,
	"on_response":  handleOnResponse,
	"before_send":  handleBeforeSend,
	"list_tools":   handleListTools,
	"execute_tool": handleExecuteTool,
	"shutdown":     handleShutdown,
}

// Serve starts a plugin server loop over stdio, reading JSON-RPC requests from the
// host bot process and dispatching them to impl.
//
// This function is intended to be called from a plugin binary's main function, NOT
// from the bot process itself. The bot only spawns plugins as subprocesses and
// communicates with them over stdio; it never calls Serve directly.
func Serve(impl plugin.Interface) error {
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
			if err := WriteJSONRPCResponse(encoder, req.ID, nil, fmt.Errorf("jsonrpc -32600: invalid request")); err != nil {
				return err
			}
			continue
		}

		var result any
		var callErr error
		handler, ok := rpcHandlers[req.Method]
		if !ok {
			callErr = fmt.Errorf("jsonrpc -32601: method not found")
		} else {
			result, callErr = handler(impl, req.Params)
		}

		if err := WriteJSONRPCResponse(encoder, req.ID, result, callErr); err != nil {
			return err
		}
	}
}
