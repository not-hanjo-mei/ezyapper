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
	"ezyapper/internal/config/configtest"
	"ezyapper/internal/types"
)

// Consolidation gets audio transcripts and video descriptions as text: the
// ones the reply path cached are reused, missing ones are computed once.
func TestResolveMemory(t *testing.T) {
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
	configtest.FillSuggested(t, &cfg.AI.Modalities)
	p := New(cfg, NewCache(time.Hour, 10))

	voiceMsg := &types.DiscordMessage{ID: "m1", Media: []types.MediaAttachment{voice(files.URL)}}
	videoMsg := &types.DiscordMessage{ID: "m2", Media: []types.MediaAttachment{{ID: "vid1", Kind: types.MediaVideo, URL: files.URL + "/v.mp4", Filename: "v.mp4", ContentType: "video/mp4", Size: 9}}}
	textMsg := &types.DiscordMessage{ID: "m3", Content: "hi"}

	// The reply path already transcribed the voice message.
	_ = annotate(p, voiceMsg, CurrentPolicy())
	if asrCalls != 1 {
		t.Fatalf("asr calls = %d", asrCalls)
	}

	in := []*types.DiscordMessage{voiceMsg, videoMsg, textMsg}
	out := p.Resolve(context.Background(), in, MemoryPolicy())
	if got := out[0].ResolvedMedia[0]; got.Text != "my birthday is in May" || got.TextKind != types.TextTranscript || got.Status != types.MediaOK {
		t.Fatalf("voice = %+v", got)
	}
	if got := out[1].ResolvedMedia[0]; got.Text != "a cat jumps on a sofa" || got.TextKind != types.TextDescription {
		t.Fatalf("video = %+v", got)
	}
	if len(out[2].ResolvedMedia) != 0 || out[2].Content != "hi" {
		t.Fatal("messages without media must carry no media text")
	}
	if asrCalls != 1 || videoCalls != 1 {
		t.Fatalf("asr=%d video=%d, want the cached transcript reused and the video described once", asrCalls, videoCalls)
	}
	if voiceMsg.ResolvedMedia != nil || videoMsg.ResolvedMedia != nil {
		t.Fatal("Resolve must not modify its input")
	}
	// Again: everything from the cache.
	_ = p.Resolve(context.Background(), in, MemoryPolicy())
	if asrCalls != 1 || videoCalls != 1 {
		t.Fatalf("second pass asr=%d video=%d", asrCalls, videoCalls)
	}
}

func TestResolveMemory_DisabledIsNoop(t *testing.T) {
	cfg := baseConfig("http://unused.invalid")
	configtest.FillSuggested(t, &cfg.AI.Modalities)
	p := New(cfg, nil)
	in := []*types.DiscordMessage{{ID: "m", Media: []types.MediaAttachment{{ID: "a", Kind: types.MediaVoice}}}}
	if out := p.Resolve(context.Background(), in, MemoryPolicy()); len(out[0].ResolvedMedia) != 0 {
		t.Fatal("disabled modalities must add no media text")
	}
}

// The reply path and memory consolidation share one cache: an image is
// described once, even across processors.
func TestResolveImages_SharedCacheDescribesOnce(t *testing.T) {
	var replyCalls, consCalls int32
	replyVL := chatServer(t, "a red bike", &replyCalls, nil)
	consVL := chatServer(t, "should not be asked", &consCalls, nil)
	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png"))
	}))
	defer img.Close()

	cache := NewCache(time.Hour, 10)
	mk := func(url string) *Processor {
		cfg := baseConfig("http://unused.invalid")
		cfg.AI.Modalities.Image = config.ImageModalityConfig{Enabled: true, Transport: config.TransportURL, ModalityModel: config.ModalityModel{Model: "vl", APIBaseURL: url}}
		configtest.FillSuggested(t, &cfg.AI.Modalities)
		return New(cfg, cache)
	}
	reply, cons := mk(replyVL.URL), mk(consVL.URL)

	// Discord CDN URLs are keyed by their attachment path, so a re-signed
	// URL (new ex/hm query) still hits.
	u1 := "https://cdn.discordapp.com/attachments/1/42/bike.png?ex=a&hm=b"
	u2 := "https://cdn.discordapp.com/attachments/1/42/bike.png?ex=c&hm=d"
	text := func(p *Processor, u string, pol Policy) string {
		out := p.Resolve(context.Background(), []*types.DiscordMessage{{ID: "m", ImageURLs: []string{u}}}, pol)
		return types.RenderAttachments(out[0], types.StyleQuery)
	}
	if got := text(reply, u1, CurrentPolicy()); got != "a red bike" {
		t.Fatalf("reply: %q", got)
	}
	if got := text(cons, u2, MemoryPolicy()); got != "a red bike" {
		t.Fatalf("consolidation: %q", got)
	}
	if replyCalls != 1 || consCalls != 0 {
		t.Fatalf("reply calls=%d consolidation calls=%d, want 1 and 0", replyCalls, consCalls)
	}

	// A cache miss is described by the processor that missed.
	other := img.URL + "/other.png"
	if got := text(cons, other, MemoryPolicy()); got != "should not be asked" {
		t.Fatalf("consolidation miss: %q", got)
	}
	if consCalls != 1 {
		t.Fatalf("consolidation calls=%d on a miss, want 1", consCalls)
	}
}
