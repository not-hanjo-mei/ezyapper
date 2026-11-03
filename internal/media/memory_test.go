package media

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/types"
)

// Consolidation gets audio transcripts and video descriptions as text: the
// ones the reply path cached are reused, missing ones are computed once.
func TestAnnotateForMemory(t *testing.T) {
	files := cdn(t)
	var asrCalls, videoCalls int32
	asr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&asrCalls, 1)
		_, _ = io.WriteString(w, `{"text":"my birthday is\nin May"}`)
	}))
	defer asr.Close()
	chat := chatServer(t, "a cat jumps on a sofa", &videoCalls, nil)

	cfg := baseConfig("http://unused.invalid")
	cfg.AI.Modalities.Audio = config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeASR, ModalityModel: config.ModalityModel{Model: "asr", APIBaseURL: asr.URL}}
	cfg.AI.Modalities.Video = config.VideoModalityConfig{Enabled: true, Transport: config.TransportURL, ModalityModel: config.ModalityModel{Model: "omni", APIBaseURL: chat.URL}}
	cfg.AI.Modalities.ApplyDefaults()
	p := New(cfg, NewCache(time.Hour, 10))

	voiceMsg := &types.DiscordMessage{ID: "m1", Media: []types.MediaAttachment{voice(files.URL)}}
	videoMsg := &types.DiscordMessage{ID: "m2", Media: []types.MediaAttachment{{ID: "vid1", Kind: types.MediaVideo, URL: files.URL + "/v.mp4", Filename: "v.mp4", ContentType: "video/mp4", Size: 9}}}
	textMsg := &types.DiscordMessage{ID: "m3", Content: "hi"}

	// The reply path already transcribed the voice message.
	_ = p.AnnotateAV(context.Background(), voiceMsg, AnnotateOptions{Current: true})
	if asrCalls != 1 {
		t.Fatalf("asr calls = %d", asrCalls)
	}

	in := []*types.DiscordMessage{voiceMsg, videoMsg, textMsg}
	out := p.AnnotateForMemory(context.Background(), in)
	if got := out[0].Media[0]; got.Text != "my birthday is in May" || !got.Transcript {
		t.Fatalf("voice = %+v", got)
	}
	if got := out[1].Media[0]; got.Text != "a cat jumps on a sofa" || got.Transcript {
		t.Fatalf("video = %+v", got)
	}
	if out[2] != textMsg {
		t.Fatal("messages without media must be passed through")
	}
	if asrCalls != 1 || videoCalls != 1 {
		t.Fatalf("asr=%d video=%d, want the cached transcript reused and the video described once", asrCalls, videoCalls)
	}
	if voiceMsg.Media[0].Text != "" || videoMsg.Media[0].Text != "" {
		t.Fatal("AnnotateForMemory must not modify its input")
	}
	// Again: everything from the cache.
	_ = p.AnnotateForMemory(context.Background(), in)
	if asrCalls != 1 || videoCalls != 1 {
		t.Fatalf("second pass asr=%d video=%d", asrCalls, videoCalls)
	}
}

func TestAnnotateForMemory_DisabledIsNoop(t *testing.T) {
	cfg := baseConfig("http://unused.invalid")
	cfg.AI.Modalities.ApplyDefaults()
	p := New(cfg, nil)
	in := []*types.DiscordMessage{{ID: "m", Media: []types.MediaAttachment{{ID: "a", Kind: types.MediaVoice}}}}
	if out := p.AnnotateForMemory(context.Background(), in); out[0] != in[0] {
		t.Fatal("disabled modalities must leave messages untouched")
	}
	var nilP *Processor
	if out := nilP.AnnotateForMemory(context.Background(), in); len(out) != 1 {
		t.Fatal("nil processor must pass messages through")
	}
}
