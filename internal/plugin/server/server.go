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
			var args plugin.ResponseArgs
			if err := decodeJSONRPCParams(req.Params, &args); err != nil {
				callErr = fmt.Errorf("invalid params for on_response")
				break
			}

			callErr = impl.OnResponse(args.Message, args.Response)
			result = map[string]any{}
		case "before_send":
			provider, ok := impl.(plugin.BeforeSendProvider)
			if !ok {
				result = plugin.BeforeSendResult{}
				break
			}

			var args plugin.BeforeSendArgs
			if err := decodeJSONRPCParams(req.Params, &args); err != nil {
				callErr = fmt.Errorf("invalid params for before_send")
				break
			}

			result, callErr = provider.BeforeSend(args.Message, args.Response)
		case "list_tools":
			provider, ok := impl.(plugin.ToolProvider)
			if !ok {
				result = []plugin.ToolSpec{}
				break
			}

			result, callErr = provider.ListTools()
		case "execute_tool":
			provider, ok := impl.(plugin.ToolProvider)
			if !ok {
				callErr = fmt.Errorf("plugin does not implement tool provider")
				break
			}

			var args plugin.ExecuteToolArgs
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

		if err := WriteJSONRPCResponse(encoder, req.ID, result, callErr); err != nil {
			return err
		}
	}
}
