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
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"

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

// lockedEncoder serialises writes to the plugin's stdout, which the request
// loop and NotifyToolsChanged (called from a config watcher goroutine)
// share. Interleaved writes would corrupt the frame stream.
type lockedEncoder struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func (l *lockedEncoder) Encode(v any) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.enc.Encode(v)
}

// active is the encoder of the running ServeIO loop (one per plugin
// process), used by NotifyToolsChanged.
var active atomic.Pointer[lockedEncoder]

type jsonRPCNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
}

// ErrNotServing is returned by NotifyToolsChanged when no Serve loop runs.
var ErrNotServing = errors.New("plugin server is not running")

// NotifyToolsChanged tells the host that this plugin's tool list changed,
// so the host re-queries list_tools and updates the tools offered to the
// model. Call it after a config reload that may add, remove or rename
// tools. The host picks the notification up at its next exchange with the
// plugin (normally the next Discord message, via on_message). It is safe to
// call from any goroutine.
func NotifyToolsChanged() error {
	enc := active.Load()
	if enc == nil {
		return ErrNotServing
	}
	return enc.Encode(jsonRPCNotification{JSONRPC: "2.0", Method: plugin.NotificationToolsChanged})
}

func WriteJSONRPCResponse(enc interface{ Encode(any) error }, id int64, result any, err error) error {
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
		return nil, fmt.Errorf("jsonrpc -32602: invalid params for on_message: %w", err)
	}
	return impl.OnMessage(msg)
}

func handleAfterDecision(impl plugin.Interface, params any) (any, error) {
	provider, ok := impl.(plugin.AfterDecisionProvider)
	if !ok {
		return plugin.AfterDecisionResult{}, nil
	}
	var args plugin.AfterDecisionArgs
	if err := decodeJSONRPCParams(params, &args); err != nil {
		return nil, fmt.Errorf("jsonrpc -32602: invalid params for after_decision: %w", err)
	}
	return provider.AfterDecision(args)
}

func handleAfterSend(impl plugin.Interface, params any) (any, error) {
	provider, ok := impl.(plugin.AfterSendProvider)
	if !ok {
		return map[string]any{}, nil
	}
	var args plugin.AfterSendArgs
	if err := decodeJSONRPCParams(params, &args); err != nil {
		return nil, fmt.Errorf("jsonrpc -32602: invalid params for after_send: %w", err)
	}
	if err := provider.AfterSend(args); err != nil {
		return nil, err
	}
	return map[string]any{}, nil
}

func handleSystemPrompt(impl plugin.Interface, _ any) (any, error) {
	provider, ok := impl.(plugin.SystemPromptProvider)
	if !ok {
		return plugin.ContextBlock{}, nil
	}
	return provider.SystemPrompt()
}

func handleBeforeSend(impl plugin.Interface, params any) (any, error) {
	provider, ok := impl.(plugin.BeforeSendProvider)
	if !ok {
		return plugin.BeforeSendResult{}, nil
	}
	var args plugin.BeforeSendArgs
	if err := decodeJSONRPCParams(params, &args); err != nil {
		return nil, fmt.Errorf("jsonrpc -32602: invalid params for before_send: %w", err)
	}
	return provider.BeforeSend(args)
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
		return nil, fmt.Errorf("jsonrpc -32602: invalid params for execute_tool: %w", err)
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
	"info":           handleInfo,
	"on_message":     handleOnMessage,
	"after_decision": handleAfterDecision,
	"after_send":     handleAfterSend,
	"system_prompt":  handleSystemPrompt,
	"before_send":    handleBeforeSend,
	"list_tools":     handleListTools,
	"execute_tool":   handleExecuteTool,
	"shutdown":       handleShutdown,
}

// Serve starts a plugin server loop over stdio, reading JSON-RPC requests from the
// host bot process and dispatching them to impl.
//
// This function is intended to be called from a plugin binary's main function, NOT
// from the bot process itself. The bot only spawns plugins as subprocesses and
// communicates with them over stdio; it never calls Serve directly.
func Serve(impl plugin.Interface) error {
	return ServeIO(impl, os.Stdin, os.Stdout, os.Stderr)
}

// ServeIO runs the JSON-RPC loop on the given streams. A malformed frame is
// answered with a -32700 parse error and skipped; only EOF or a broken pipe
// ends the loop. Panics inside a handler are recovered and reported as a
// JSON-RPC error so one bad request cannot take the plugin process down.
// Diagnostics go to logw (the host captures plugin stderr into its log).
func ServeIO(impl plugin.Interface, r io.Reader, w io.Writer, logw io.Writer) error {
	if impl == nil {
		return fmt.Errorf("plugin implementation is nil")
	}
	if logw == nil {
		logw = io.Discard
	}

	decoder := plugin.NewFrameDecoder(r)
	encoder := &lockedEncoder{enc: json.NewEncoder(w)}
	active.Store(encoder)
	defer active.CompareAndSwap(encoder, nil)

	for {
		var req jsonRPCRequest
		if err := decoder.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			if plugin.IsMalformedFrame(err) {
				fmt.Fprintf(logw, "[plugin-server] %v\n", err)
				if werr := WriteJSONRPCResponse(encoder, 0, nil, fmt.Errorf("jsonrpc -32700: parse error")); werr != nil {
					return werr
				}
				continue
			}
			return fmt.Errorf("failed to read jsonrpc request: %w", err)
		}

		if strings.TrimSpace(req.Method) == "" {
			if err := WriteJSONRPCResponse(encoder, req.ID, nil, fmt.Errorf("jsonrpc -32600: invalid request")); err != nil {
				return err
			}
			continue
		}

		result, callErr := dispatch(impl, req, logw)
		if callErr != nil {
			fmt.Fprintf(logw, "[plugin-server] method %s (id=%d) failed: %v\n", req.Method, req.ID, callErr)
		}

		if err := WriteJSONRPCResponse(encoder, req.ID, result, callErr); err != nil {
			return err
		}
	}
}

func dispatch(impl plugin.Interface, req jsonRPCRequest, logw io.Writer) (result any, err error) {
	handler, ok := rpcHandlers[req.Method]
	if !ok {
		return nil, fmt.Errorf("jsonrpc -32601: method not found")
	}
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(logw, "[plugin-server] panic in %s: %v\n%s\n", req.Method, r, debug.Stack())
			result = nil
			err = fmt.Errorf("plugin panic in %s: %v", req.Method, r)
		}
	}()
	return handler(impl, req.Params)
}
