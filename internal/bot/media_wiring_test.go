package bot

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

	"ezyapper/internal/config"
	"ezyapper/internal/media"
	"ezyapper/internal/ratelimit"
	"ezyapper/internal/types"
)

func mediaTestBot(t *testing.T, mods config.ModalitiesConfig) (*Bot, *httptest.Server) {
	t.Helper()
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/ogg")
		_, _ = io.WriteString(w, "OggS")
	}))
	t.Cleanup(files.Close)
	cfg := &config.Config{}
	cfg.AI.APIBaseURL, cfg.AI.APIKey, cfg.AI.Model = "http://unused.invalid", "k", "base"
	cfg.AI.Timeout, cfg.AI.MaxTokens = 5, 100
	cfg.Discord.ImageCacheTTLMin, cfg.Discord.ImageCacheMaxEntries = 60, 10
	mods.ApplyDefaults()
	cfg.AI.Modalities = mods
	store := &atomic.Value{}
	store.Store(cfg)
	b := &Bot{configStore: store, historicalImageDescCache: map[string]historicalImageDescCacheEntry{}, mediaCache: media.NewCache(time.Hour, 10)}
	b.media = media.New(cfg, b.mediaCache)
	return b, files
}

func voiceMsg(id, base string) *types.DiscordMessage {
	return &types.DiscordMessage{ID: id, Content: "", Media: []types.MediaAttachment{{ID: "att-" + id, Kind: types.MediaVoice, URL: base + "/" + id + ".ogg", ContentType: "audio/ogg", DurationSec: 2}}}
}

// describeServer is a chat-completions endpoint that answers every request
// with content and records the content-part types it received.
func describeServer(t *testing.T, content string, partTypes *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var doc struct {
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&doc)
		for _, m := range doc.Messages {
			var parts []struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(m.Content, &parts) == nil {
				for _, p := range parts {
					*partTypes = append(*partTypes, p.Type)
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Audio in describe mode reaches the reply prompt as text only.
func TestAnnotateCurrent_DescribeAudioIsText(t *testing.T) {
	var partTypes []string
	omni := describeServer(t, "listen to this", &partTypes)
	b, files := mediaTestBot(t, config.ModalitiesConfig{Audio: config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeDescribe, ModalityModel: config.ModalityModel{Model: "omni", APIBaseURL: omni.URL}}})
	cur := *voiceMsg("cur", files.URL)
	cur.Content = "listen"
	content := b.annotateCurrent(context.Background(), cur, GenerateContext{})
	if content != "listen\n[Audio 1 (voice message, 2s): listen to this]" {
		t.Fatalf("content = %q", content)
	}
	if strings.Join(partTypes, ",") != "text,audio_url" {
		t.Fatalf("describe request parts = %v", partTypes)
	}
}

// History audio is transcribed on demand only for the replied-to message,
// and only once (cached by attachment ID).
func TestEnrichHistory_ASRBoundedAndCached(t *testing.T) {
	var calls int32
	asr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = io.WriteString(w, `{"text":"hi bot"}`)
	}))
	defer asr.Close()
	b, files := mediaTestBot(t, config.ModalitiesConfig{Audio: config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeASR, ModalityModel: config.ModalityModel{Model: "asr", APIBaseURL: asr.URL}}})

	history := []*types.DiscordMessage{voiceMsg("new", files.URL), voiceMsg("old", files.URL)} // newest first

	// A reply to the older voice message: it is not the most recent audio
	// message, so nothing is computed.
	out := b.enrichHistory(context.Background(), history, "cur", planRecentImageEnrichment("ok", "old"))
	if calls != 0 || out[1].Content != "\n[Audio 1: voice message, 2s]" {
		t.Fatalf("calls=%d content=%q", calls, out[1].Content)
	}
	// A reply to the most recent one transcribes it.
	out = b.enrichHistory(context.Background(), history, "cur", planRecentImageEnrichment("ok", "new"))
	if calls != 1 || !strings.Contains(out[0].Content, "transcript (voice message, 2s): hi bot") {
		t.Fatalf("calls=%d content=%q", calls, out[0].Content)
	}
	// Later turns reuse the transcript without asking again.
	out = b.enrichHistory(context.Background(), history, "cur", planRecentImageEnrichment("unrelated", ""))
	if calls != 1 || !strings.Contains(out[0].Content, "hi bot") {
		t.Fatalf("calls=%d content=%q", calls, out[0].Content)
	}
	if history[0].Content != "" {
		t.Fatal("enrichHistory must not modify its input")
	}
}

func TestEnrichHistory_AllModalitiesOffIsNoop(t *testing.T) {
	b, files := mediaTestBot(t, config.ModalitiesConfig{})
	history := []*types.DiscordMessage{voiceMsg("a", files.URL)}
	if out := b.enrichHistory(context.Background(), history, "cur", planRecentImageEnrichment("voice", "a")); out[0] != history[0] {
		t.Fatal("history must be returned unchanged when no modality is enabled")
	}
}

func TestStickersInCurrentAndHistory(t *testing.T) {
	b, _ := mediaTestBot(t, config.ModalitiesConfig{Sticker: config.StickerModalityConfig{Enabled: true}})
	b.media.SetStickers(func(ctx context.Context, id string) (media.StickerInfo, error) {
		return media.StickerInfo{Name: "wave", Description: "waves hello"}, nil
	}, media.NewCache(time.Hour, 10))
	msg := types.DiscordMessage{ID: "cur", Content: "hi", Stickers: []types.StickerRef{{ID: "1", Name: "wave", FormatType: types.StickerPNG}}}
	content := b.annotateCurrent(context.Background(), msg, GenerateContext{})
	if content != "hi\n[Sticker: wave — waves hello]" {
		t.Fatalf("current = %q", content)
	}
	hist := msg
	hist.ID = "old"
	out := b.enrichHistory(context.Background(), []*types.DiscordMessage{&hist}, "cur", planRecentImageEnrichment("", ""))
	if out[0].Content != "hi\n[Sticker: wave — waves hello]" {
		t.Fatalf("history = %q", out[0].Content)
	}
}

// A config reload that enables an ffmpeg option without ffmpeg is refused
// (the WebUI then keeps the old config), like startup.
func TestApplyRuntimeConfig_RefusesMissingFFmpeg(t *testing.T) {
	b, _ := mediaTestBot(t, config.ModalitiesConfig{})
	cfg := *b.cfg()
	cfg.AI.Modalities.FFmpegPath = "/nonexistent/ffmpeg"
	cfg.AI.Modalities.Audio = config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeASR, Transcode: "mp3", ModalityModel: config.ModalityModel{Model: "asr"}}
	b.configStore.Store(&cfg)
	b.rateLimiter = ratelimit.NewLimiter(1, time.Second, time.Second)
	b.ctx = context.Background()
	err := b.ApplyRuntimeConfig()
	if err == nil || !strings.Contains(err.Error(), "ffmpeg is required by core.ai.modalities.audio.transcode=mp3") {
		t.Fatalf("expected ffmpeg refusal, got %v", err)
	}
}

// Images are described by the image model; the reply prompt gets the text.
func TestAnnotateCurrent_ImageDescription(t *testing.T) {
	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = io.WriteString(w, "png")
	}))
	defer img.Close()
	var partTypes []string
	vl := describeServer(t, "a red square", &partTypes)
	b, _ := mediaTestBot(t, config.ModalitiesConfig{Image: config.ImageModalityConfig{Enabled: true, ModalityModel: config.ModalityModel{Model: "vl", APIBaseURL: vl.URL}}})
	content := b.annotateCurrent(context.Background(), types.DiscordMessage{ID: "c", Content: "look"}, GenerateContext{ImageURLs: []string{img.URL + "/a.png"}})
	if content != "look [Image 1: a red square]" {
		t.Fatalf("content=%q", content)
	}
	if strings.Join(partTypes, ",") != "text,image_url" {
		t.Fatalf("describe request parts = %v", partTypes)
	}
}
