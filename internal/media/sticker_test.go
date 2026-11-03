package media

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/types"
)

func stickerProcessor(t *testing.T, mode config.StickerMode, chatURL string) (*Processor, *int32) {
	t.Helper()
	cfg := baseConfig(chatURL)
	cfg.AI.Modalities.Sticker = config.StickerModalityConfig{Enabled: true, Mode: mode}
	// Sticker vision falls back to the image modality's model.
	cfg.AI.Modalities.Image.Model = "vm"
	cfg.AI.Modalities.ApplyDefaults()
	p := New(cfg, nil, NewCache(time.Hour, 10))
	var lookups int32
	p.SetStickers(func(ctx context.Context, id string) (StickerInfo, error) {
		atomic.AddInt32(&lookups, 1)
		if id == "gone" {
			return StickerInfo{}, errors.New("404 Unknown Sticker")
		}
		return StickerInfo{Name: "Amika Thumbs Up", Description: "Amika gives a thumbs up", Tags: "👍"}, nil
	}, NewCache(24*time.Hour, 100))
	return p, &lookups
}

func stickerMsg(s ...types.StickerRef) *types.DiscordMessage {
	return &types.DiscordMessage{ID: "m", Stickers: s}
}

func TestStickerTextMode(t *testing.T) {
	p, lookups := stickerProcessor(t, config.StickerModeText, "http://unused.invalid")
	msg := stickerMsg(types.StickerRef{ID: "s1", Name: "Amika Thumbs Up", FormatType: types.StickerPNG})
	want := "\n[Sticker: Amika Thumbs Up — Amika gives a thumbs up (tags: 👍)]"
	if got := p.AnnotateStickers(context.Background(), msg, true); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// History reuses the cached details: one lookup per sticker ID.
	if got := p.AnnotateStickers(context.Background(), msg, false); got != want || *lookups != 1 {
		t.Fatalf("history got %q lookups=%d", got, *lookups)
	}
	// A failed lookup falls back to the name and is not retried every turn.
	gone := stickerMsg(types.StickerRef{ID: "gone", Name: "old one"})
	for i := 0; i < 2; i++ {
		if got := p.AnnotateStickers(context.Background(), gone, true); got != "\n[Sticker: old one]" {
			t.Fatalf("fallback got %q", got)
		}
	}
	if *lookups != 2 {
		t.Fatalf("failed lookup must be cached, lookups=%d", *lookups)
	}
}

func TestStickerVisionModeCacheAndFallbacks(t *testing.T) {
	var fetched []string
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetched = append(fetched, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, ".gif") {
			w.Header().Set("Content-Type", "image/gif")
		} else {
			w.Header().Set("Content-Type", "image/png")
		}
		_, _ = w.Write([]byte("img"))
	}))
	defer host.Close()
	old := StickerURLBase
	StickerURLBase = host.URL + "/stickers/"
	defer func() { StickerURLBase = old }()

	var calls int32
	var bodies []map[string]any
	chat := chatServer(t, "an anime girl\ngiving a thumbs up", &calls, &bodies)
	p, _ := stickerProcessor(t, config.StickerModeVision, chat.URL)

	guild := types.StickerRef{ID: "123", Name: "Amika Thumbs Up", FormatType: types.StickerPNG}
	// History before the sticker was ever described: text, no model call.
	if got := p.AnnotateStickers(context.Background(), stickerMsg(guild), false); !strings.Contains(got, "Amika gives a thumbs up") || calls != 0 {
		t.Fatalf("history fallback got %q calls=%d", got, calls)
	}
	want := "\n[Sticker: Amika Thumbs Up — an anime girl giving a thumbs up]"
	if got := p.AnnotateStickers(context.Background(), stickerMsg(guild), true); got != want {
		t.Fatalf("vision got %q", got)
	}
	// Cached by sticker ID: later turns (history or current) reuse it.
	_ = p.AnnotateStickers(context.Background(), stickerMsg(guild), true)
	if got := p.AnnotateStickers(context.Background(), stickerMsg(guild), false); got != want || calls != 1 {
		t.Fatalf("cache: got %q calls=%d", got, calls)
	}
	if bodies[0]["model"] != "vm" {
		t.Fatalf("sticker vision must use the image model, got %v", bodies[0]["model"])
	}
	content := bodies[0]["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if u := content[1].(map[string]any)["image_url"].(map[string]any)["url"].(string); u != "data:image/png;base64,aW1n" {
		t.Fatalf("image part = %s", u)
	}

	// GIF stickers are fetched as .gif; Lottie ones are never fetched.
	gif := types.StickerRef{ID: "456", Name: "dance", FormatType: types.StickerGIF}
	lottie := types.StickerRef{ID: "789", Name: "wumpus", FormatType: types.StickerLottie}
	got := p.AnnotateStickers(context.Background(), stickerMsg(gif, lottie), true)
	if !strings.Contains(got, "[Sticker: Amika Thumbs Up — an anime girl") || !strings.Contains(got, "[Sticker: Amika Thumbs Up — Amika gives a thumbs up (tags: 👍)]") {
		t.Fatalf("gif+lottie got %q", got)
	}
	if strings.Join(fetched, ",") != "/stickers/123.png,/stickers/456.gif" {
		t.Fatalf("fetched %v", fetched)
	}
}

func TestStickerDisabled(t *testing.T) {
	cfg := baseConfig("http://unused.invalid")
	cfg.AI.Modalities.ApplyDefaults()
	p := New(cfg, nil, nil)
	if got := p.AnnotateStickers(context.Background(), stickerMsg(types.StickerRef{ID: "1", Name: "x"}), true); got != "" {
		t.Fatalf("disabled sticker modality must add nothing, got %q", got)
	}
}
