package bot

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"ezyapper/internal/config"
	"ezyapper/internal/media"
	"ezyapper/internal/types"
)

func imageTestBot(t *testing.T, calls *int32) *Bot {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"a cat"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)
	return mustImageBot(t, srv.URL)
}

func mustImageBot(t *testing.T, url string) *Bot {
	t.Helper()
	b, _ := mediaTestBot(t, config.ModalitiesConfig{Image: config.ImageModalityConfig{Enabled: true, Transport: config.TransportURL, ModalityModel: config.ModalityModel{Model: "vision", APIBaseURL: url}}})
	return b
}

// The reply path describes an image once; history reuses that description
// from the shared media cache on every later (plain-text) turn, even after
// Discord re-signed the CDN URL, and never calls the model again. This
// replaces the history-only description cache, keyed by message ID.
func TestHistoryReusesReplyPathImageDescription(t *testing.T) {
	var calls int32
	b := imageTestBot(t, &calls)
	first := "https://cdn.discordapp.com/attachments/1/2/cat.png?ex=1&is=2&hm=3&"
	resigned := "https://cdn.discordapp.com/attachments/1/2/cat.png?ex=9&is=8&hm=7&"

	if _, err := b.imageDescriber().DescribeImages(context.Background(), []string{first}); err != nil {
		t.Fatal(err)
	}
	history := []*types.DiscordMessage{{ID: "m1", Content: "look", ImageURLs: []string{resigned}}}
	for range 3 {
		out := b.enrichHistory(context.Background(), history, "cur", planRecentImageEnrichment("thanks", ""))
		if out[0].Content != "look\n[Image 1: a cat]" {
			t.Fatalf("content = %q", out[0].Content)
		}
	}
	if calls != 1 {
		t.Fatalf("model calls = %d, want 1", calls)
	}
	// A different image is not served from the cache, and a plain-text turn
	// does not describe it.
	other := []*types.DiscordMessage{{ID: "m2", Content: "x", ImageURLs: []string{"https://cdn.discordapp.com/attachments/1/3/dog.png"}}}
	if out := b.enrichHistory(context.Background(), other, "cur", planRecentImageEnrichment("ok", "")); out[0].Content != "x" || calls != 1 {
		t.Fatalf("content=%q calls=%d", out[0].Content, calls)
	}
}

// pinMediaWindow pins every media key of the prompt window, and a channel's
// next prompt replaces its pins.
func TestPinMediaWindowReplacesChannelPins(t *testing.T) {
	b, _ := mediaTestBot(t, config.ModalitiesConfig{})
	img := "https://cdn.discordapp.com/attachments/1/2/cat.png?ex=1"
	msg := &types.DiscordMessage{
		ImageURLs: []string{img},
		Media:     []types.MediaAttachment{{ID: "v1", Kind: types.MediaVoice}, {ID: "vid", Kind: types.MediaVideo}},
		Stickers:  []types.StickerRef{{ID: "s1"}},
	}
	b.pinMediaWindow("c1", msg)
	for _, k := range []string{media.ImageCacheKey(img), "audio:v1", "audio-describe:v1", "video:vid"} {
		if !b.mediaCache.Pinned(k) {
			t.Fatalf("%s not pinned", k)
		}
	}
	for _, k := range []string{"sticker:vision:s1", "sticker:info:s1"} {
		if !b.stickerCache.Pinned(k) {
			t.Fatalf("%s not pinned in the sticker cache", k)
		}
	}
	b.pinMediaWindow("c1", &types.DiscordMessage{Content: "text only"})
	if b.mediaCache.Pinned("audio:v1") || b.stickerCache.Pinned("sticker:info:s1") {
		t.Fatal("the channel's next prompt must replace its pins")
	}
}

// A reload clears only the kinds whose model, endpoint, prompt or transcode
// settings changed.
func TestApplyRuntimeConfigInvalidatesChangedKindsOnly(t *testing.T) {
	b, _ := mediaTestBot(t, config.ModalitiesConfig{})
	cfg := b.cfg()
	b.invalidateChangedMediaLocked(cfg) // first apply: nothing to compare
	seed := func() {
		b.mediaCache.Set("image:x", "img")
		b.mediaCache.Set("audio:a", "tr")
		b.mediaCache.Set("audio-describe:a", "desc")
		b.mediaCache.Set("video:v", "vid")
		b.stickerCache.Set("sticker:vision:s", "sv")
		b.stickerCache.Set("sticker:info:s", "{}")
	}
	has := func(c *media.Cache, k string) bool { _, ok := c.Get(k); return ok }
	seed()

	// Unrelated change (max items): nothing dropped.
	next := *cfg
	next.AI.Modalities.Image.MaxItems = 7
	b.invalidateChangedMediaLocked(&next)
	if b.mediaCache.Len() != 4 || b.stickerCache.Len() != 2 {
		t.Fatal("an unrelated change must not drop cached media")
	}

	// Audio transcode changes: only audio entries go.
	next2 := next
	next2.AI.Modalities.Audio.Transcode = "mp3"
	b.invalidateChangedMediaLocked(&next2)
	if has(b.mediaCache, "audio:a") || has(b.mediaCache, "audio-describe:a") {
		t.Fatal("audio entries must be dropped")
	}
	if !has(b.mediaCache, "image:x") || !has(b.mediaCache, "video:v") || !has(b.stickerCache, "sticker:vision:s") {
		t.Fatal("other kinds must stay")
	}

	// Image model change (sticker vision inherits it): image and sticker
	// vision go, sticker details stay.
	seed()
	next3 := next2
	next3.AI.Modalities.Image.Model = "other-vision"
	b.invalidateChangedMediaLocked(&next3)
	if has(b.mediaCache, "image:x") || has(b.stickerCache, "sticker:vision:s") {
		t.Fatal("image and sticker vision entries must be dropped")
	}
	if !has(b.stickerCache, "sticker:info:s") || !has(b.mediaCache, "audio:a") || !has(b.mediaCache, "video:v") {
		t.Fatal("sticker details, audio and video must stay")
	}

	// Video prompt change.
	next4 := next3
	next4.AI.Modalities.Video.Prompt = "new"
	b.invalidateChangedMediaLocked(&next4)
	if has(b.mediaCache, "video:v") || !has(b.mediaCache, "audio:a") {
		t.Fatal("only video entries must be dropped")
	}
}
