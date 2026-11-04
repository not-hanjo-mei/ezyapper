package media

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/types"
)

func TestCacheDoCoalescesConcurrentCalls(t *testing.T) {
	c := NewCache(time.Hour, 10)
	var calls int32
	release := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]string, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := c.Do("k", func() (string, error) {
				atomic.AddInt32(&calls, 1)
				<-release
				return "v", nil
			})
			if err != nil {
				t.Error(err)
			}
			results[i] = v
		}(i)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if calls != 1 {
		t.Fatalf("fn called %d times, want 1", calls)
	}
	for _, r := range results {
		if r != "v" {
			t.Fatalf("results = %v", results)
		}
	}
	if v, ok := c.Get("k"); !ok || v != "v" {
		t.Fatal("result must be cached")
	}
}

func TestCacheDoDoesNotCacheErrors(t *testing.T) {
	c := NewCache(time.Hour, 10)
	if _, err := c.Do("k", func() (string, error) { return "", errors.New("boom") }); err == nil {
		t.Fatal("want the error")
	}
	if _, ok := c.Get("k"); ok {
		t.Fatal("an error must not be cached")
	}
	var nilCache *Cache
	if v, err := nilCache.Do("k", func() (string, error) { return "x", nil }); err != nil || v != "x" {
		t.Fatalf("nil cache: %q %v", v, err)
	}
}

// The reply path and consolidation transcribing the same voice message at
// the same moment make one ASR call.
func TestTranscribeConcurrentSameAttachmentOnce(t *testing.T) {
	files := cdn(t)
	var asrCalls int32
	asr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&asrCalls, 1)
		time.Sleep(100 * time.Millisecond)
		_, _ = io.WriteString(w, `{"text":"hello"}`)
	}))
	defer asr.Close()
	cache := NewCache(time.Hour, 10)
	cfg := baseConfig("http://unused.invalid")
	cfg.AI.Modalities.Audio = config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeASR, ModalityModel: config.ModalityModel{Model: "asr", APIBaseURL: asr.URL}}
	cfg.AI.Modalities.ApplyDefaults()
	reply, cons := New(cfg, cache), New(cfg, cache)

	msg := &types.DiscordMessage{ID: "m1", Media: []types.MediaAttachment{voice(files.URL)}}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = reply.AnnotateAV(context.Background(), msg, AnnotateOptions{Current: true})
	}()
	go func() {
		defer wg.Done()
		_ = cons.AnnotateForMemory(context.Background(), []*types.DiscordMessage{msg})
	}()
	wg.Wait()
	if asrCalls != 1 {
		t.Fatalf("asr calls = %d, want 1", asrCalls)
	}
}
