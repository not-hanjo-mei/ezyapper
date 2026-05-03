package plugin

import (
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"ezyapper/internal/logger"
	"ezyapper/internal/types"
)

func isMethodNotFoundPluginError(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "method not found") ||
		strings.Contains(msg, "jsonrpc -32601")
}

func callJSONRPCWithTimeout(
	client *stdioJSONRPCClient,
	wg *sync.WaitGroup,
	method string,
	params interface{},
	reply interface{},
	timeout time.Duration,
) error {
	if client == nil {
		return fmt.Errorf("jsonrpc client is nil for method %s", method)
	}

	done := make(chan error, 1)
	if wg != nil {
		wg.Add(1)
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("[plugin] panic recovered: %v\n%s", r, debug.Stack())
			}
		}()
		if wg != nil {
			defer wg.Done()
		}
		done <- client.Call(method, params, reply)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		client.dead.Store(true)
		return fmt.Errorf("jsonrpc call timeout (%dms): %s", timeout/time.Millisecond, method)
	}
}

func callPluginOnMessageWithTimeout(
	plugin *Client,
	msg types.DiscordMessage,
	reply *bool,
	timeout time.Duration,
) error {
	if plugin == nil {
		return fmt.Errorf("plugin is nil")
	}

	if plugin.jsonrpc != nil {
		return callJSONRPCWithTimeout(plugin.jsonrpc, plugin.wg, "on_message", msg, reply, timeout)
	}

	return fmt.Errorf("plugin %s has no jsonrpc transport", plugin.Name)
}

func callPluginOnResponseWithTimeout(
	plugin *Client,
	args ResponseArgs,
	reply *struct{},
	timeout time.Duration,
) error {
	if plugin == nil {
		return fmt.Errorf("plugin is nil")
	}

	if plugin.jsonrpc != nil {
		return callJSONRPCWithTimeout(plugin.jsonrpc, plugin.wg, "on_response", args, reply, timeout)
	}

	return fmt.Errorf("plugin %s has no jsonrpc transport", plugin.Name)
}

func callPluginBeforeSendWithTimeout(
	plugin *Client,
	args BeforeSendArgs,
	reply *BeforeSendResult,
	timeout time.Duration,
) error {
	if plugin == nil {
		return fmt.Errorf("plugin is nil")
	}

	if plugin.jsonrpc != nil {
		return callJSONRPCWithTimeout(plugin.jsonrpc, plugin.wg, "before_send", args, reply, timeout)
	}

	return fmt.Errorf("plugin %s has no jsonrpc transport", plugin.Name)
}
