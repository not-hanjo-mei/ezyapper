package bot

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ezyapper/internal/ai"
	"ezyapper/internal/config"
	"ezyapper/internal/media"
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
	b.media = media.New(cfg, nil, b.mediaCache)
	return b, files
}

func voiceMsg(id, base string) *types.DiscordMessage {
	return &types.DiscordMessage{ID: id, Content: "", Media: []types.MediaAttachment{{ID: "att-" + id, Kind: types.MediaVoice, URL: base + "/" + id + ".ogg", ContentType: "audio/ogg", DurationSec: 2}}}
}

func TestAnnotateCurrent_DirectAudioBuildsParts(t *testing.T) {
	b, files := mediaTestBot(t, config.ModalitiesConfig{Audio: config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeDirect}})
	cur := *voiceMsg("cur", files.URL)
	cur.Content = "listen"
	content, parts := b.annotateCurrent(context.Background(), cur, GenerateContext{})
	if len(parts) != 1 || parts[0].Kind != ai.PartAudioURL || !strings.HasPrefix(parts[0].URL, "data:audio/ogg;base64,") {
		t.Fatalf("parts = %+v", parts)
	}
	if content != "listen\n[Audio 1: voice message, 2s, attached]" {
		t.Fatalf("content = %q", content)
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
	content, parts := b.annotateCurrent(context.Background(), msg, GenerateContext{})
	if content != "hi\n[Sticker: wave — waves hello]" || len(parts) != 0 {
		t.Fatalf("current = %q", content)
	}
	hist := msg
	hist.ID = "old"
	out := b.enrichHistory(context.Background(), []*types.DiscordMessage{&hist}, "cur", planRecentImageEnrichment("", ""))
	if out[0].Content != "hi\n[Sticker: wave — waves hello]" {
		t.Fatalf("history = %q", out[0].Content)
	}
}
