package media

import (
	"context"
	"encoding/json"
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

const selfID = "bot-self"

// resolveProcessor has ASR audio, images and video enabled, each backed by a
// counting fake.
func resolveProcessor(t *testing.T) (p *Processor, files *httptest.Server, asrCalls, chatCalls *int32) {
	t.Helper()
	files = cdn(t)
	asrCalls, chatCalls = new(int32), new(int32)
	asr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(asrCalls, 1)
		_, _ = io.WriteString(w, `{"text":"meet at\nnoon"}`)
	}))
	t.Cleanup(asr.Close)
	chat := chatServer(t, "a red bike", chatCalls, nil)

	cfg := baseConfig(chat.URL)
	cfg.AI.Modalities.Image = config.ImageModalityConfig{Enabled: true, ModalityModel: config.ModalityModel{Model: "vl"}}
	cfg.AI.Modalities.Audio = config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeASR, ModalityModel: config.ModalityModel{Model: "asr", APIBaseURL: asr.URL}}
	cfg.AI.Modalities.Video = config.VideoModalityConfig{Enabled: true, Transport: config.TransportURL, ModalityModel: config.ModalityModel{Model: "omni"}}
	configtest.FillSuggested(t, &cfg.AI.Modalities)
	p = New(cfg, NewCache(time.Hour, 100))
	p.SetSelf(func() string { return selfID })
	return p, files, asrCalls, chatCalls
}

func audioMsg(id, author, url string) *types.DiscordMessage {
	return &types.DiscordMessage{ID: id, AuthorID: author, Media: []types.MediaAttachment{{
		ID: "att-" + id, Kind: types.MediaAudio, URL: url + "/" + id + ".ogg", Filename: "discord_" + id + ".mp3", ContentType: "audio/ogg", Size: 10, DurationSec: 7,
	}}}
}

func TestResolveCurrentComputesAndKeepsContent(t *testing.T) {
	p, files, asrCalls, chatCalls := resolveProcessor(t)
	in := &types.DiscordMessage{ID: "c", AuthorID: "u1", Content: "look", ImageURLs: []string{files.URL + "/a.png"}, Media: []types.MediaAttachment{voice(files.URL)}}

	out := p.Resolve(context.Background(), []*types.DiscordMessage{in}, CurrentPolicy())
	if out[0] == in || in.ResolvedMedia != nil {
		t.Fatal("Resolve must return copies and leave its input alone")
	}
	if out[0].Content != "look" {
		t.Fatalf("Content must not change, got %q", out[0].Content)
	}
	got := types.RenderAttachments(out[0], types.StylePrompt)
	want := "  attachments: [Image 1: a red bike] [Audio 1 transcript (voice message, 3s): meet at noon]"
	if got != want || *asrCalls != 1 || *chatCalls != 1 {
		t.Fatalf("got %q (asr=%d chat=%d), want %q", got, *asrCalls, *chatCalls, want)
	}

	// Resolved again (another path, same turn): served from the cache.
	_ = p.Resolve(context.Background(), []*types.DiscordMessage{in}, CurrentPolicy())
	if *asrCalls != 1 || *chatCalls != 1 {
		t.Fatalf("second resolve must hit the cache, asr=%d chat=%d", *asrCalls, *chatCalls)
	}
}

func TestResolveHistoryOnlyMostRecentWhenAsked(t *testing.T) {
	p, files, asrCalls, _ := resolveProcessor(t)
	newer := audioMsg("new", "u1", files.URL)
	older := audioMsg("old", "u2", files.URL)
	history := []*types.DiscordMessage{newer, older} // newest first

	out := p.Resolve(context.Background(), history, HistoryPolicy("ok", ""))
	if *asrCalls != 0 || out[0].ResolvedMedia[0].Status != types.MediaPending {
		t.Fatalf("unrelated text must not process history audio (asr=%d, %+v)", *asrCalls, out[0].ResolvedMedia)
	}
	if got := types.RenderAttachments(out[0], types.StylePrompt); got != "  attachments: [Audio 1: discord_new.mp3, 7s]" {
		t.Fatalf("pending audio placeholder = %q", got)
	}

	// A reply to the older message does not process it: only the most
	// recent audio message may be processed.
	out = p.Resolve(context.Background(), history, HistoryPolicy("ok", "old"))
	if *asrCalls != 0 || out[1].ResolvedMedia[0].Status != types.MediaPending {
		t.Fatalf("a reply to an older audio message must not process it (asr=%d)", *asrCalls)
	}

	out = p.Resolve(context.Background(), history, HistoryPolicy("listen to that audio", ""))
	if *asrCalls != 1 || out[0].ResolvedMedia[0].Status != types.MediaOK || out[1].ResolvedMedia[0].Status != types.MediaPending {
		t.Fatalf("keyword must process the most recent audio only (asr=%d)", *asrCalls)
	}
}

// The bot's own messages (a TTS clip it attached) are never resolved: no
// annotation and no ASR call, even when the user asks about the audio or
// replies to that message. The user's own older audio stays eligible.
func TestResolveSkipsOwnMessages(t *testing.T) {
	p, files, asrCalls, chatCalls := resolveProcessor(t)
	own := audioMsg("tts", selfID, files.URL)
	own.ImageURLs = []string{files.URL + "/gen.png"}
	own.Stickers = []types.StickerRef{{ID: "s", Name: "wave"}}
	user := audioMsg("user", "u1", files.URL)
	history := []*types.DiscordMessage{own, user}

	for _, pol := range []Policy{CurrentPolicy(), MemoryPolicy(), HistoryPolicy("listen to that audio", "tts")} {
		out := p.Resolve(context.Background(), history[:1], pol)
		if len(out[0].ResolvedMedia) != 0 || types.RenderAttachments(out[0], types.StylePrompt) != "" {
			t.Fatalf("own message got media %+v", out[0].ResolvedMedia)
		}
	}
	if *asrCalls != 0 || *chatCalls != 0 {
		t.Fatalf("own media must not be processed, asr=%d chat=%d", *asrCalls, *chatCalls)
	}

	out := p.Resolve(context.Background(), history, HistoryPolicy("listen to that audio", ""))
	if *asrCalls != 1 || out[1].ResolvedMedia[0].Status != types.MediaOK {
		t.Fatalf("the user's audio must be the most recent eligible one (asr=%d)", *asrCalls)
	}
}

// Other bots' media is processed like a user's.
func TestResolveOtherBotsMedia(t *testing.T) {
	p, files, asrCalls, _ := resolveProcessor(t)
	other := audioMsg("o", "other-bot", files.URL)
	other.IsBot = true
	out := p.Resolve(context.Background(), []*types.DiscordMessage{other}, CurrentPolicy())
	if *asrCalls != 1 || out[0].ResolvedMedia[0].Text != "meet at noon" {
		t.Fatalf("other bot audio must be transcribed (asr=%d %+v)", *asrCalls, out[0].ResolvedMedia)
	}
}

func TestResolveFailureIsAPlaceholder(t *testing.T) {
	p, files, _, _ := resolveProcessor(t)
	msg := &types.DiscordMessage{ID: "c", AuthorID: "u1", ImageURLs: []string{files.URL + "/missing.gif"}}
	out := p.Resolve(context.Background(), []*types.DiscordMessage{msg}, CurrentPolicy())
	if got := types.RenderAttachments(out[0], types.StylePrompt); got != "  attachments: [Image 1: (could not be processed)]" {
		t.Fatalf("failed image = %q", got)
	}
}

// Memory consolidation describes images with the consolidation.vision model.
func TestResolveMemoryUsesConsolidationVision(t *testing.T) {
	files := cdn(t)
	var models []string
	chat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var doc map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &doc)
		models = append(models, doc["model"].(string))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "desc"}, "finish_reason": "stop"}}})
	}))
	defer chat.Close()
	cfg := baseConfig(chat.URL)
	cfg.AI.Modalities.Image = config.ImageModalityConfig{Enabled: true, ModalityModel: config.ModalityModel{Model: "vl"}}
	cfg.Memory.Consolidation.Vision.Model = "cheap-vl"
	configtest.FillSuggested(t, &cfg.AI.Modalities)
	p := New(cfg, NewCache(time.Hour, 10))

	a := &types.DiscordMessage{ID: "a", ImageURLs: []string{files.URL + "/a.png"}}
	b := &types.DiscordMessage{ID: "b", ImageURLs: []string{files.URL + "/b.png"}}
	out := p.Resolve(context.Background(), []*types.DiscordMessage{a}, MemoryPolicy())
	_ = p.Resolve(context.Background(), []*types.DiscordMessage{b}, CurrentPolicy())
	if len(models) != 2 || models[0] != "cheap-vl" || models[1] != "vl" {
		t.Fatalf("models = %v, want [cheap-vl vl]", models)
	}
	if got := types.RenderAttachments(out[0], types.StyleMemory); got != "  [Attached Image 1: desc]" {
		t.Fatalf("memory style = %q", got)
	}
}

func TestResolveNilProcessor(t *testing.T) {
	var p *Processor
	msg := &types.DiscordMessage{ID: "x", Content: "hi", Stickers: []types.StickerRef{{ID: "s", Name: "n"}}}
	out := p.Resolve(context.Background(), []*types.DiscordMessage{msg, nil}, CurrentPolicy())
	if out[0] == nil || out[0].Content != "hi" || out[0].ResolvedMedia != nil || out[1] != nil {
		t.Fatalf("nil processor must return plain copies, got %+v", out)
	}
}

func TestHistoryPolicyKeywords(t *testing.T) {
	cases := []struct {
		text, reply, kind, id string
		want                  bool
	}{
		{"", "", KindImage, "m", false},
		{"", "m", KindImage, "m", true},
		{"ok sure", "other", KindImage, "m", false},
		{"an IMAGE link", "", KindImage, "m", true},
		{"IMGUR", "", KindImage, "m", true},
		{"my photo album", "", KindImage, "m", true},
		{"nice picture", "", KindImage, "m", true},
		{"take a pic", "", KindImage, "m", true},
		{"full screenshot", "", KindImage, "m", true},
		{"plain text", "", KindImage, "m", false},
		{"   \t ", "", KindImage, "m", false},
		{"listen", "", KindAudio, "m", true},
		{"listen", "", KindVideo, "m", false},
		{"that clip", "", KindVideo, "m", true},
		{"image", "m", KindSticker, "m", false},
	}
	for _, c := range cases {
		if got := HistoryPolicy(c.text, c.reply).compute(c.kind, c.id, true); got != c.want {
			t.Errorf("HistoryPolicy(%q,%q).compute(%s)=%v want %v", c.text, c.reply, c.kind, got, c.want)
		}
		if HistoryPolicy(c.text, c.reply).compute(c.kind, c.id, false) {
			t.Errorf("only the most recent message may be computed (%q)", c.text)
		}
	}
}
