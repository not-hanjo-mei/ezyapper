package media

// Opt-in live tests against real endpoints. Run with
//
//	EZYAPPER_LIVE_MEDIA=1 EZYAPPER_LIVE_MEDIA_DIR=/path/to/media go test ./internal/media -run Live -v
//
// The media dir must hold a.mp3, a.ogg, v.mp4 and img.webp. Endpoints and
// models default to the project's test gateway and ASR service and can be
// overridden with EZYAPPER_LIVE_GATEWAY_URL / _GATEWAY_MODEL / _GATEWAY_KEY
// (the audio/video describe model), EZYAPPER_LIVE_IMAGE_MODEL and
// EZYAPPER_LIVE_ASR_URL / _ASR_MODEL / _ASR_KEY (keys may stay empty when a
// proxy injects them).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/types"
)

func liveEnv(t *testing.T, key, def string) string {
	t.Helper()
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func liveSetup(t *testing.T) (*config.Config, string) {
	t.Helper()
	if os.Getenv("EZYAPPER_LIVE_MEDIA") != "1" {
		t.Skip("set EZYAPPER_LIVE_MEDIA=1 to run live media tests")
	}
	dir := liveEnv(t, "EZYAPPER_LIVE_MEDIA_DIR", "")
	if dir == "" {
		t.Fatal("EZYAPPER_LIVE_MEDIA_DIR is required")
	}
	files := httptest.NewServer(http.FileServer(http.Dir(dir)))
	t.Cleanup(files.Close)

	cfg := &config.Config{}
	cfg.AI.APIBaseURL = liveEnv(t, "EZYAPPER_LIVE_GATEWAY_URL", "https://us-west.omgpizzatnt.top/v1")
	cfg.AI.APIKey = os.Getenv("EZYAPPER_LIVE_GATEWAY_KEY")
	cfg.AI.Model = liveEnv(t, "EZYAPPER_LIVE_GATEWAY_MODEL", "nvidia/nemotron-3-nano-omni-30b-a3b-reasoning")
	cfg.AI.MaxTokens = 12000
	cfg.AI.Timeout = 180
	cfg.AI.RetryCount = 1
	cfg.AI.MaxImageBytes = 10 << 20
	return cfg, files.URL
}

func liveAttachment(t *testing.T, base, dir, name, kind, ct string) types.MediaAttachment {
	t.Helper()
	st, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return types.MediaAttachment{ID: "live-" + name, Kind: kind, URL: base + "/" + name, Filename: name, ContentType: ct, Size: int(st.Size())}
}

func TestLiveASR(t *testing.T) {
	cfg, base := liveSetup(t)
	dir := os.Getenv("EZYAPPER_LIVE_MEDIA_DIR")
	cfg.AI.Modalities.Audio = config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeASR, ModalityModel: config.ModalityModel{
		Model:      liveEnv(t, "EZYAPPER_LIVE_ASR_MODEL", "IFM/Jais-ASR"),
		APIBaseURL: liveEnv(t, "EZYAPPER_LIVE_ASR_URL", "https://api.ifm.ai/v1"),
		APIKey:     os.Getenv("EZYAPPER_LIVE_ASR_KEY"),
		Timeout:    120,
	}}
	cfg.AI.Modalities.ApplyDefaults()
	p := New(cfg, NewCache(time.Hour, 10))
	for _, f := range []struct{ name, ct string }{{"a.ogg", "audio/ogg"}, {"a.mp3", "audio/mpeg"}} {
		start := time.Now()
		text, err := p.Transcribe(context.Background(), liveAttachment(t, base, dir, f.name, types.MediaVoice, f.ct))
		if err != nil {
			t.Fatalf("%s: %v", f.name, err)
		}
		if strings.TrimSpace(text) == "" {
			t.Fatalf("%s: empty transcript", f.name)
		}
		t.Logf("%s (%s): %q", f.name, time.Since(start).Round(time.Millisecond), text)
	}
}

func TestLiveDescribeAudio(t *testing.T) {
	cfg, base := liveSetup(t)
	dir := os.Getenv("EZYAPPER_LIVE_MEDIA_DIR")
	cfg.AI.Modalities.Audio = config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeDescribe, ModalityModel: config.ModalityModel{Model: cfg.AI.Model}}
	cfg.AI.Modalities.ApplyDefaults()
	p := New(cfg, NewCache(time.Hour, 10))
	for _, f := range []struct{ name, ct string }{{"a.ogg", "audio/ogg"}, {"a.mp3", "audio/mpeg"}} {
		start := time.Now()
		text, err := p.DescribeAudio(context.Background(), liveAttachment(t, base, dir, f.name, types.MediaVoice, f.ct))
		if err != nil {
			t.Fatalf("%s: %v", f.name, err)
		}
		t.Logf("describe %s (%s): %q", f.name, time.Since(start).Round(time.Millisecond), text)
	}
}

func TestLiveVideoDescribe(t *testing.T) {
	cfg, base := liveSetup(t)
	dir := os.Getenv("EZYAPPER_LIVE_MEDIA_DIR")
	cfg.AI.Modalities.Video = config.VideoModalityConfig{Enabled: true, ModalityModel: config.ModalityModel{Model: cfg.AI.Model, MaxTokens: 12000}}
	cfg.AI.Modalities.ApplyDefaults()
	p := New(cfg, NewCache(time.Hour, 10))
	start := time.Now()
	text, err := p.DescribeVideo(context.Background(), liveAttachment(t, base, dir, "v.mp4", types.MediaVideo, "video/mp4"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("video (%s): %q", time.Since(start).Round(time.Millisecond), text)
}

func TestLiveImageDescribe(t *testing.T) {
	cfg, base := liveSetup(t)
	cfg.AI.Modalities.Image = config.ImageModalityConfig{Enabled: true, ModalityModel: config.ModalityModel{Model: liveEnv(t, "EZYAPPER_LIVE_IMAGE_MODEL", "nvidia/nemotron-3.5-super-vl-120b-a12b")}}
	cfg.AI.Modalities.ApplyDefaults()
	p := New(cfg, nil)
	start := time.Now()
	got, err := p.DescribeImages(context.Background(), []string{base + "/img.webp"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("image (%s): %q", time.Since(start).Round(time.Millisecond), got[0])
}

// TestLiveStickerVision describes a real sticker from the Discord media
// host. Set EZYAPPER_LIVE_STICKER_ID to a PNG/APNG/GIF sticker ID.
func TestLiveStickerVision(t *testing.T) {
	cfg, _ := liveSetup(t)
	id := os.Getenv("EZYAPPER_LIVE_STICKER_ID")
	if id == "" {
		t.Skip("set EZYAPPER_LIVE_STICKER_ID")
	}
	cfg.AI.Modalities.Image.Model = liveEnv(t, "EZYAPPER_LIVE_IMAGE_MODEL", "nvidia/nemotron-3.5-super-vl-120b-a12b")
	cfg.AI.Modalities.Sticker = config.StickerModalityConfig{Enabled: true, Mode: config.StickerModeVision}
	cfg.AI.Modalities.ApplyDefaults()
	p := New(cfg, nil)
	p.SetStickers(nil, NewCache(time.Hour, 10))
	start := time.Now()
	got := p.AnnotateStickers(context.Background(), &types.DiscordMessage{Stickers: []types.StickerRef{{ID: id, Name: "live", FormatType: types.StickerPNG}}}, true)
	if !strings.Contains(got, "[Sticker: live — ") {
		t.Fatalf("no vision description: %q", got)
	}
	t.Logf("sticker (%s): %q", time.Since(start).Round(time.Millisecond), got)
}

// TestLiveDescribeAudioInputAudio sends the ogg voice clip transcoded to
// mp3 as an input_audio part to the audio describe model (needs ffmpeg).
func TestLiveDescribeAudioInputAudio(t *testing.T) {
	cfg, base := liveSetup(t)
	realFFmpeg(t)
	dir := os.Getenv("EZYAPPER_LIVE_MEDIA_DIR")
	cfg.AI.Modalities.Audio = config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeDescribe, Transcode: "mp3", PartType: config.AudioPartInput, ModalityModel: config.ModalityModel{Model: cfg.AI.Model}}
	cfg.AI.Modalities.ApplyDefaults()
	p := New(cfg, nil)
	start := time.Now()
	text, err := p.DescribeAudio(context.Background(), liveAttachment(t, base, dir, "a.ogg", types.MediaVoice, "audio/ogg"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("input_audio mp3 from ogg (%s): %q", time.Since(start).Round(time.Millisecond), text)
}
