package media

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ezyapper/internal/ai"
	"ezyapper/internal/config"
	"ezyapper/internal/types"
)

// cdn serves fake media files.
func cdn(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".ogg"):
			w.Header().Set("Content-Type", "audio/ogg")
			_, _ = w.Write([]byte("OggS-voice"))
		case strings.HasSuffix(r.URL.Path, ".mp4"):
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("mp4-bytes"))
		case strings.HasSuffix(r.URL.Path, ".png"):
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("png-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// chatServer answers chat completions with content and counts calls.
func chatServer(t *testing.T, content string, calls *int32, bodies *[]map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		raw, _ := io.ReadAll(r.Body)
		var doc map[string]any
		_ = json.Unmarshal(raw, &doc)
		if bodies != nil {
			*bodies = append(*bodies, doc)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func baseConfig(url string) *config.Config {
	cfg := &config.Config{}
	cfg.AI.APIBaseURL = url
	cfg.AI.APIKey = "core-key"
	cfg.AI.Model = "base"
	cfg.AI.MaxTokens = 100
	cfg.AI.Timeout = 5
	cfg.AI.RetryCount = 1
	cfg.AI.MaxImageBytes = 1 << 20
	return cfg
}

func voice(srvURL string) types.MediaAttachment {
	return types.MediaAttachment{ID: "att-voice", Kind: types.MediaVoice, URL: srvURL + "/voice-message.ogg", Filename: "voice-message.ogg", ContentType: "audio/ogg", Size: 10, DurationSec: 3}
}

func TestTranscriberMultipart(t *testing.T) {
	var got struct {
		path, auth, model, format, filename, fileCT, data string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		got.auth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
		}
		got.model = r.FormValue("model")
		got.format = r.FormValue("response_format")
		f, h, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("file part: %v", err)
		}
		b, _ := io.ReadAll(f)
		got.filename, got.fileCT, got.data = h.Filename, h.Header.Get("Content-Type"), string(b)
		_, _ = io.WriteString(w, `{"text":"  hello there  "}`)
	}))
	defer srv.Close()

	tr := NewTranscriber(TranscriberConfig{BaseURL: srv.URL + "/v1/", APIKey: "asr-key", Model: "IFM/Jais-ASR", Timeout: 5 * time.Second})
	text, err := tr.Transcribe(context.Background(), "voice-message", "audio/ogg; codecs=opus", []byte("OggS"))
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello there" {
		t.Fatalf("text = %q", text)
	}
	if got.path != "/v1/audio/transcriptions" || got.auth != "Bearer asr-key" || got.model != "IFM/Jais-ASR" || got.format != "json" {
		t.Fatalf("request = %+v", got)
	}
	if got.filename != "voice-message.ogg" || got.fileCT != "audio/ogg" || got.data != "OggS" {
		t.Fatalf("file part = %+v", got)
	}
}

func TestTranscriberPlainTextResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "plain transcript\n")
	}))
	defer srv.Close()
	tr := NewTranscriber(TranscriberConfig{BaseURL: srv.URL, Model: "m"})
	if text, err := tr.Transcribe(context.Background(), "a.mp3", "audio/mpeg", []byte("x")); err != nil || text != "plain transcript" {
		t.Fatalf("got %q %v", text, err)
	}
}

func TestAudioFilename(t *testing.T) {
	cases := map[[2]string]string{
		{"voice-message.ogg", "audio/ogg"}: "voice-message.ogg",
		{"clip", "audio/mpeg"}:             "clip.mp3",
		{"", "audio/ogg; codecs=opus"}:     "audio.ogg",
		{"x", "audio/wav"}:                 "x.wav",
	}
	for in, want := range cases {
		if got := audioFilename(in[0], in[1]); got != want {
			t.Errorf("audioFilename(%q,%q)=%q want %q", in[0], in[1], got, want)
		}
	}
}

func TestAnnotateASRCurrentHistoryAndCache(t *testing.T) {
	files := cdn(t)
	var asrCalls int32
	asr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&asrCalls, 1)
		_, _ = io.WriteString(w, `{"text":"see you\nat noon"}`)
	}))
	defer asr.Close()

	cfg := baseConfig("http://unused.invalid")
	cfg.AI.Modalities.Audio = config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeASR, ModalityModel: config.ModalityModel{Model: "asr", APIBaseURL: asr.URL}}
	cfg.AI.Modalities.ApplyDefaults()
	p := New(cfg, nil, NewCache(time.Hour, 10))

	msg := &types.DiscordMessage{ID: "m1", Media: []types.MediaAttachment{voice(files.URL)}}

	// History without permission to compute: placeholder only, no call.
	ann := p.AnnotateAV(context.Background(), msg, AnnotateOptions{})
	if ann.Text != "\n[Audio 1: voice message, 3s]" || asrCalls != 0 {
		t.Fatalf("history placeholder = %q calls=%d", ann.Text, asrCalls)
	}
	// Current message: transcribed, whitespace collapsed onto one line.
	ann = p.AnnotateAV(context.Background(), msg, AnnotateOptions{Current: true})
	if ann.Text != "\n[Audio 1 transcript (voice message, 3s): see you at noon]" || len(ann.Parts) != 0 {
		t.Fatalf("current = %q parts=%d", ann.Text, len(ann.Parts))
	}
	// Later turns reuse the cached transcript (keyed by attachment ID, even
	// when the CDN URL was re-signed).
	resigned := *msg
	resigned.Media = []types.MediaAttachment{voice(files.URL)}
	resigned.Media[0].URL += "?ex=new&hm=sig"
	ann = p.AnnotateAV(context.Background(), &resigned, AnnotateOptions{})
	if !strings.Contains(ann.Text, "see you at noon") || asrCalls != 1 {
		t.Fatalf("cached history = %q calls=%d", ann.Text, asrCalls)
	}
}

func TestAnnotateAudioLimits(t *testing.T) {
	files := cdn(t)
	cfg := baseConfig("http://unused.invalid")
	cfg.AI.Modalities.Audio = config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeDirect, MaxDurationSec: 2}
	cfg.AI.Modalities.ApplyDefaults()
	p := New(cfg, nil, NewCache(time.Hour, 10))
	msg := &types.DiscordMessage{ID: "m1", Media: []types.MediaAttachment{voice(files.URL)}}
	ann := p.AnnotateAV(context.Background(), msg, AnnotateOptions{Current: true})
	if len(ann.Parts) != 0 || !strings.Contains(ann.Text, "duration limit") {
		t.Fatalf("over-long audio must not be sent: %q parts=%d", ann.Text, len(ann.Parts))
	}
}

func TestAnnotateDirectAudioParts(t *testing.T) {
	files := cdn(t)
	cfg := baseConfig("http://unused.invalid")
	cfg.AI.Modalities.Audio = config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeDirect}
	cfg.AI.Modalities.ApplyDefaults()
	p := New(cfg, nil, NewCache(time.Hour, 10))
	msg := &types.DiscordMessage{ID: "m1", Media: []types.MediaAttachment{voice(files.URL), {ID: "v1", Kind: types.MediaVideo, URL: files.URL + "/v.mp4"}}}

	ann := p.AnnotateAV(context.Background(), msg, AnnotateOptions{Current: true})
	if len(ann.Parts) != 1 {
		t.Fatalf("parts = %+v", ann.Parts)
	}
	part := ann.Parts[0]
	// Voice messages are ogg/opus; sent as an audio_url data URI as is.
	if part.Kind != ai.PartAudioURL || part.URL != "data:audio/ogg;base64,T2dnUy12b2ljZQ==" {
		t.Fatalf("part = %+v", part)
	}
	// Video is disabled: no line, no part.
	if ann.Text != "\n[Audio 1: voice message, 3s, attached]" {
		t.Fatalf("text = %q", ann.Text)
	}
	// History in direct mode only gets a placeholder (no media parts).
	hist := p.AnnotateAV(context.Background(), msg, AnnotateOptions{})
	if len(hist.Parts) != 0 || hist.Text != "\n[Audio 1: voice message, 3s]" {
		t.Fatalf("history = %+v", hist)
	}
	// No direct model override: the base client answers.
	base := ai.NewClient(&cfg.AI, nil)
	if p.ReplyClient(base, true) != base {
		t.Fatal("expected base client without override")
	}
}

func TestReplyClientOverride(t *testing.T) {
	cfg := baseConfig("http://unused.invalid")
	cfg.AI.Modalities.Image = config.ImageModalityConfig{Enabled: true, Mode: config.ImageModeDirect, ModalityModel: config.ModalityModel{Model: "vision-model"}}
	cfg.AI.Modalities.ApplyDefaults()
	p := New(cfg, nil, nil)
	base := ai.NewClient(&cfg.AI, nil)
	if p.ReplyClient(base, false) != base {
		t.Fatal("turns without direct media use the base client")
	}
	if p.ReplyClient(base, true) == base {
		t.Fatal("turns with direct media use the image model (legacy multimodal behavior)")
	}
}

func TestDescribeVideoTransportAndCache(t *testing.T) {
	files := cdn(t)
	var calls int32
	var bodies []map[string]any
	chat := chatServer(t, "a dog runs", &calls, &bodies)

	for _, transport := range []config.MediaTransport{config.TransportBase64, config.TransportURL} {
		calls, bodies = 0, nil
		cfg := baseConfig(chat.URL)
		cfg.AI.Modalities.Video = config.VideoModalityConfig{Enabled: true, Transport: transport, ModalityModel: config.ModalityModel{Model: "omni", MaxTokens: 12000}}
		cfg.AI.Modalities.ApplyDefaults()
		p := New(cfg, nil, NewCache(time.Hour, 10))
		msg := &types.DiscordMessage{ID: "m1", Media: []types.MediaAttachment{{ID: "vid1", Kind: types.MediaVideo, URL: files.URL + "/v.mp4", Filename: "v.mp4", ContentType: "video/mp4", Size: 9}}}

		ann := p.AnnotateAV(context.Background(), msg, AnnotateOptions{Current: true})
		if ann.Text != "\n[Video 1 (v.mp4): a dog runs]" {
			t.Fatalf("%s: text = %q", transport, ann.Text)
		}
		_ = p.AnnotateAV(context.Background(), msg, AnnotateOptions{Current: true})
		if calls != 1 {
			t.Fatalf("%s: video must be described once, got %d calls", transport, calls)
		}
		doc := bodies[0]
		if doc["model"] != "omni" || doc["max_tokens"].(float64) != 12000 || doc["max_tokens"] == nil {
			t.Fatalf("%s: request = %v", transport, doc)
		}
		content := doc["messages"].([]any)[0].(map[string]any)["content"].([]any)
		video := content[1].(map[string]any)["video_url"].(map[string]any)["url"].(string)
		want := files.URL + "/v.mp4"
		if transport == config.TransportBase64 {
			want = "data:video/mp4;base64,bXA0LWJ5dGVz"
		}
		if video != want {
			t.Fatalf("%s: video url = %q want %q", transport, video, want)
		}
	}
}

func TestDescribeImagesDescribeMode(t *testing.T) {
	files := cdn(t)
	var calls int32
	var bodies []map[string]any
	chat := chatServer(t, "a red square", &calls, &bodies)
	cfg := baseConfig(chat.URL)
	cfg.AI.Modalities.Image = config.ImageModalityConfig{Enabled: true, ModalityModel: config.ModalityModel{Model: "vm"}}
	cfg.AI.Modalities.ApplyDefaults()
	p := New(cfg, nil, nil)
	got, err := p.ImageDescriber().DescribeImages(context.Background(), []string{files.URL + "/x.png"})
	if err != nil || len(got) != 1 || got[0] != "a red square" {
		t.Fatalf("got %v %v", got, err)
	}
	content := bodies[0]["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if content[0].(map[string]any)["text"] != config.DefaultImagePrompt {
		t.Fatalf("prompt = %v", content[0])
	}
	if u := content[1].(map[string]any)["image_url"].(map[string]any)["url"]; u != "data:image/png;base64,cG5nLWJ5dGVz" {
		t.Fatalf("image url = %v", u)
	}
}

func TestDisabledModalitiesAreIgnored(t *testing.T) {
	cfg := baseConfig("http://unused.invalid")
	cfg.AI.Modalities.ApplyDefaults()
	p := New(cfg, nil, nil)
	msg := &types.DiscordMessage{ID: "m1", Media: []types.MediaAttachment{{ID: "a", Kind: types.MediaVoice}, {ID: "v", Kind: types.MediaVideo}}}
	if ann := p.AnnotateAV(context.Background(), msg, AnnotateOptions{Current: true}); ann.Text != "" || len(ann.Parts) != 0 {
		t.Fatalf("disabled modalities must add nothing: %+v", ann)
	}
	if p.ImageDescriber() != nil {
		t.Fatal("no image describer when images are off")
	}
}

func TestCacheTTLAndBound(t *testing.T) {
	c := NewCache(time.Minute, 2)
	now := time.Unix(0, 0)
	c.now = func() time.Time { return now }
	c.Set("a", "1")
	now = now.Add(time.Second)
	c.Set("b", "2")
	now = now.Add(time.Second)
	c.Set("c", "3")
	if _, ok := c.Get("a"); ok {
		t.Fatal("oldest entry must be evicted over the bound")
	}
	now = now.Add(2 * time.Minute)
	if _, ok := c.Get("c"); ok {
		t.Fatal("entry must expire after the TTL")
	}
}

func serveBytes(t *testing.T, files map[string][]byte, ct string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", ct)
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func serveASR(t *testing.T, seen func(filename, contentType string)) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, h, err := r.FormFile("file")
		if err != nil {
			t.Errorf("file: %v", err)
			return
		}
		seen(h.Filename, h.Header.Get("Content-Type"))
		_, _ = io.WriteString(w, `{"text":"ok"}`)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}
