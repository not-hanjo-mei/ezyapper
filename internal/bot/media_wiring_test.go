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
	"ezyapper/internal/config/configtest"
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
	configtest.FillSuggested(t, &mods)
	cfg.AI.Modalities = mods
	store := &atomic.Value{}
	store.Store(cfg)
	b := &Bot{configStore: store, mediaCache: media.NewCache(time.Hour, 10), stickerCache: media.NewCache(time.Hour, 10)}
	b.media = media.New(cfg, b.mediaCache)
	b.media.SetSelf(b.selfID)
	return b, files
}

// resolveMsgs resolves msgs with the bot's processor.
func resolveMsgs(b *Bot, pol media.Policy, msgs ...*types.DiscordMessage) []*types.DiscordMessage {
	return b.mediaProcessor().Resolve(context.Background(), msgs, pol)
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

// Audio in describe mode reaches the reply prompt as text only, on the
// attachments line under the message, outside its quoted text.
func TestResolveCurrent_DescribeAudioIsText(t *testing.T) {
	var partTypes []string
	omni := describeServer(t, "listen to this", &partTypes)
	b, files := mediaTestBot(t, config.ModalitiesConfig{Audio: config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeDescribe, ModalityModel: config.ModalityModel{Model: "omni", APIBaseURL: omni.URL}}})
	cur := voiceMsg("cur", files.URL)
	cur.Content, cur.Username = "listen", "alice"
	line := renderMessageLine(resolveMsgs(b, media.CurrentPolicy(), cur)[0], "bot", nil, nil)
	if !strings.HasSuffix(line, `alice: "listen"`+"\n"+`  attachments: [Audio 1 (voice message, 2s): listen to this]`) {
		t.Fatalf("line = %q", line)
	}
	if strings.Join(partTypes, ",") != "text,audio_url" {
		t.Fatalf("describe request parts = %v", partTypes)
	}
}

// History audio is transcribed on demand only for the replied-to message,
// and only once (cached by attachment ID).
func TestResolveHistory_ASRBoundedAndCached(t *testing.T) {
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
	out := resolveMsgs(b, media.HistoryPolicy("ok", "old"), history...)
	if got := types.RenderAttachments(out[1], types.StylePrompt); calls != 0 || got != "  attachments: [Audio 1: voice message, 2s]" {
		t.Fatalf("calls=%d attachments=%q", calls, got)
	}
	// A reply to the most recent one transcribes it.
	out = resolveMsgs(b, media.HistoryPolicy("ok", "new"), history...)
	if got := types.RenderAttachments(out[0], types.StylePrompt); calls != 1 || !strings.Contains(got, "transcript (voice message, 2s): hi bot") {
		t.Fatalf("calls=%d attachments=%q", calls, got)
	}
	// Later turns reuse the transcript without asking again.
	out = resolveMsgs(b, media.HistoryPolicy("unrelated", ""), history...)
	if got := types.RenderAttachments(out[0], types.StylePrompt); calls != 1 || !strings.Contains(got, "hi bot") {
		t.Fatalf("calls=%d attachments=%q", calls, got)
	}
	if history[0].Content != "" || history[0].ResolvedMedia != nil {
		t.Fatal("Resolve must not modify its input")
	}
}

// The bot's own TTS reply (an mp3 the openai-tts plugin attached) is never
// annotated or transcribed, even when the next user message asks to listen
// to it or replies to it; its prompt line has no attachments line.
func TestOwnTTSAudioIsNotAnnotated(t *testing.T) {
	var calls int32
	asr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = io.WriteString(w, `{"text":"echo"}`)
	}))
	defer asr.Close()
	b, files := mediaTestBot(t, config.ModalitiesConfig{Audio: config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeASR, ModalityModel: config.ModalityModel{Model: "asr", APIBaseURL: asr.URL}}})
	b.cfg().Discord.OwnBotID = "bot"
	tts := &types.DiscordMessage{ID: "tts", AuthorID: "bot", Username: "yapper", Content: "here you go", Media: []types.MediaAttachment{{
		ID: "att-tts", Kind: types.MediaAudio, URL: files.URL + "/discord_1_2.mp3", Filename: "discord_1_2.mp3", ContentType: "audio/mpeg", DurationSec: 7,
	}}}
	for _, pol := range []media.Policy{media.HistoryPolicy("listen to that audio", "tts"), media.MemoryPolicy()} {
		out := resolveMsgs(b, pol, tts)
		if line := renderMessageLine(out[0], "bot", nil, nil); strings.Contains(line, "attachments:") || strings.Contains(line, "Audio") {
			t.Fatalf("own message line = %q", line)
		}
	}
	if calls != 0 {
		t.Fatalf("asr calls = %d, want none for the bot's own audio", calls)
	}
}

func TestResolveHistory_AllModalitiesOffIsNoop(t *testing.T) {
	b, files := mediaTestBot(t, config.ModalitiesConfig{})
	history := []*types.DiscordMessage{voiceMsg("a", files.URL)}
	if out := resolveMsgs(b, media.HistoryPolicy("voice", "a"), history...); len(out[0].ResolvedMedia) != 0 || out[0].Content != "" {
		t.Fatal("history must carry no media text when no modality is enabled")
	}
}

// With every modality off (sticker.enabled false) stickers are still shown,
// as their Discord name/description text; no model is called.
func TestStickersInCurrentAndHistory(t *testing.T) {
	b, _ := mediaTestBot(t, config.ModalitiesConfig{})
	b.media.SetStickers(func(ctx context.Context, id string) (media.StickerInfo, error) {
		return media.StickerInfo{Name: "wave", Description: "waves hello"}, nil
	}, media.NewCache(time.Hour, 10))
	msg := types.DiscordMessage{ID: "cur", Content: "hi", Stickers: []types.StickerRef{{ID: "1", Name: "wave", FormatType: types.StickerPNG}}}
	want := "  attachments: [Sticker: wave — waves hello]"
	if got := types.RenderAttachments(resolveMsgs(b, media.CurrentPolicy(), &msg)[0], types.StylePrompt); got != want {
		t.Fatalf("current = %q", got)
	}
	hist := msg
	hist.ID = "old"
	if got := types.RenderAttachments(resolveMsgs(b, media.HistoryPolicy("", ""), &hist)[0], types.StylePrompt); got != want {
		t.Fatalf("history = %q", got)
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
func TestResolveCurrent_ImageDescription(t *testing.T) {
	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = io.WriteString(w, "png")
	}))
	defer img.Close()
	var partTypes []string
	vl := describeServer(t, "a red square", &partTypes)
	b, _ := mediaTestBot(t, config.ModalitiesConfig{Image: config.ImageModalityConfig{Enabled: true, ModalityModel: config.ModalityModel{Model: "vl", APIBaseURL: vl.URL}}})
	cur := &types.DiscordMessage{ID: "c", Content: "look", ImageURLs: []string{img.URL + "/a.png"}}
	out := resolveMsgs(b, media.CurrentPolicy(), cur)[0]
	if got := types.RenderAttachments(out, types.StylePrompt); got != "  attachments: [Image 1: a red square]" || out.Content != "look" {
		t.Fatalf("attachments=%q content=%q", got, out.Content)
	}
	if strings.Join(partTypes, ",") != "text,image_url" {
		t.Fatalf("describe request parts = %v", partTypes)
	}
}
