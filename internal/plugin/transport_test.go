package plugin

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error { return nil }

func TestCallJSONRPCWithTimeout_MarksClientDead(t *testing.T) {
	stdoutR, stdoutW := io.Pipe()
	defer stdoutW.Close()

	stdin := &nopWriteCloser{&bytes.Buffer{}}
	client := newStdioJSONRPCClient(stdin, stdoutR)

	var wg sync.WaitGroup
	var reply string

	err := callJSONRPCWithTimeout(client, &wg, "test_method", nil, &reply, 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout error, got: %v", err)
	}

	if !client.dead.Load() {
		t.Fatal("expected client.dead to be true after timeout")
	}

	time.Sleep(5 * time.Millisecond)

	done := make(chan error, 1)
	go func() {
		done <- client.Call("test2", nil, &reply)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error from dead client, got nil")
		}
		if !strings.Contains(err.Error(), "dead") {
			t.Fatalf("expected 'dead' in error, got: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Call() on dead client should return immediately, but it hung (deadlock)")
	}
}

func TestStdioJSONRPCClient_Call_DeadClientReturnsError(t *testing.T) {
	stdoutR, stdoutW := io.Pipe()
	defer stdoutW.Close()

	stdin := &nopWriteCloser{&bytes.Buffer{}}
	client := newStdioJSONRPCClient(stdin, stdoutR)

	client.dead.Store(true)

	var reply string
	err := client.Call("test", nil, &reply)
	if err == nil {
		t.Fatal("expected error from dead client, got nil")
	}
	if !strings.Contains(err.Error(), "dead") {
		t.Fatalf("expected 'dead' in error, got: %v", err)
	}
}
