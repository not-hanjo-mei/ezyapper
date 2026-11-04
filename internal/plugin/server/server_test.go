package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"ezyapper/internal/plugin"
	"ezyapper/internal/types"
)

type fakePlugin struct{ panicOnMessage bool }

func (f *fakePlugin) Info() (plugin.Info, error) {
	return plugin.Info{Name: "fake", Version: "1.0.0"}, nil
}

func (f *fakePlugin) OnMessage(msg types.DiscordMessage) (bool, error) {
	if f.panicOnMessage {
		panic("boom")
	}
	return true, nil
}

func (f *fakePlugin) OnResponse(msg types.DiscordMessage, response string) error { return nil }
func (f *fakePlugin) Shutdown() error                                            { return nil }

func runServe(t *testing.T, impl plugin.Interface, input string) ([]jsonRPCResponse, string) {
	t.Helper()
	var out, logs bytes.Buffer
	if err := ServeIO(impl, strings.NewReader(input), &out, &logs); err != nil {
		t.Fatalf("ServeIO returned error: %v", err)
	}
	var resps []jsonRPCResponse
	sc := bufio.NewScanner(&out)
	for sc.Scan() {
		var r jsonRPCResponse
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("server wrote invalid frame %q: %v", sc.Text(), err)
		}
		resps = append(resps, r)
	}
	return resps, logs.String()
}

// Reproduction for the "plugin exits on malformed JSON-RPC frame" report:
// one bad frame followed by a normal request. The process must survive,
// answer the bad frame with a parse error, and still serve the next request.
func TestServeSurvivesMalformedFrame(t *testing.T) {
	input := "{not json\n" + `{"jsonrpc":"2.0","id":7,"method":"info"}` + "\n"
	resps, logs := runServe(t, &fakePlugin{}, input)
	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(resps))
	}
	if resps[0].Error == nil || !strings.Contains(resps[0].Error.Message, "-32700") {
		t.Fatalf("expected parse error for bad frame, got %+v", resps[0])
	}
	if resps[1].ID != 7 || resps[1].Error != nil {
		t.Fatalf("expected successful info response with id 7, got %+v", resps[1])
	}
	if !strings.Contains(logs, "malformed jsonrpc frame") {
		t.Fatalf("expected malformed frame to be logged, logs=%q", logs)
	}
}

func TestServeSurvivesHandlerPanic(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"on_message","params":{}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"info"}` + "\n"
	resps, logs := runServe(t, &fakePlugin{panicOnMessage: true}, input)
	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(resps))
	}
	if resps[0].Error == nil || !strings.Contains(resps[0].Error.Message, "panic") {
		t.Fatalf("expected panic error, got %+v", resps[0])
	}
	if resps[1].ID != 2 || resps[1].Error != nil {
		t.Fatalf("expected info to succeed after panic, got %+v", resps[1])
	}
	if !strings.Contains(logs, "panic in on_message") {
		t.Fatalf("expected panic to be logged, logs=%q", logs)
	}
}

func TestServeInvalidParamsKeepsCause(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":3,"method":"on_message","params":"oops"}` + "\n"
	resps, _ := runServe(t, &fakePlugin{}, input)
	if len(resps) != 1 || resps[0].Error == nil {
		t.Fatalf("expected an error response, got %+v", resps)
	}
	if !strings.Contains(resps[0].Error.Message, "-32602") || !strings.Contains(resps[0].Error.Message, "cannot unmarshal") {
		t.Fatalf("expected invalid params error with cause, got %q", resps[0].Error.Message)
	}
}

func TestServeEOFEndsCleanly(t *testing.T) {
	var out bytes.Buffer
	if err := ServeIO(&fakePlugin{}, strings.NewReader(""), &out, io.Discard); err != nil {
		t.Fatalf("expected nil on EOF, got %v", err)
	}
}

func TestNotifyToolsChanged(t *testing.T) {
	if err := NotifyToolsChanged(); err != ErrNotServing {
		t.Fatalf("expected ErrNotServing outside Serve, got %v", err)
	}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- ServeIO(&notifyTestPlugin{}, inR, outW, io.Discard) }()

	// Wait until the loop is running.
	deadline := time.Now().Add(2 * time.Second)
	for active.Load() == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	go func() { _ = NotifyToolsChanged() }()

	var frame map[string]any
	if err := json.NewDecoder(outR).Decode(&frame); err != nil {
		t.Fatal(err)
	}
	if frame["method"] != plugin.NotificationToolsChanged {
		t.Fatalf("unexpected frame %v", frame)
	}
	if _, hasID := frame["id"]; hasID {
		t.Fatalf("a notification must not carry an id: %v", frame)
	}

	inW.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := NotifyToolsChanged(); err != ErrNotServing {
		t.Fatalf("expected ErrNotServing after Serve returned, got %v", err)
	}
}

type notifyTestPlugin struct{}

func (notifyTestPlugin) Info() (plugin.Info, error)                    { return plugin.Info{Name: "n"}, nil }
func (notifyTestPlugin) OnMessage(types.DiscordMessage) (bool, error)  { return true, nil }
func (notifyTestPlugin) OnResponse(types.DiscordMessage, string) error { return nil }
func (notifyTestPlugin) Shutdown() error                               { return nil }
