package media

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ezyapper/internal/ai"
	"ezyapper/internal/config"
	"ezyapper/internal/types"
)

func TestCheckFFmpeg_NotNeededIsNeverLookedUp(t *testing.T) {
	mods := config.ModalitiesConfig{FFmpegPath: "/nonexistent/ffmpeg"}
	mods.Audio = config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeASR}
	mods.Video = config.VideoModalityConfig{Enabled: false, Transcode: true} // disabled: does not count
	if path, err := CheckFFmpeg(context.Background(), mods); err != nil || path != "" {
		t.Fatalf("ffmpeg must not be required: %q %v", path, err)
	}
}

func TestCheckFFmpeg_MissingRefusesWithClearError(t *testing.T) {
	mods := config.ModalitiesConfig{FFmpegPath: "/nonexistent/ffmpeg"}
	mods.Audio = config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeASR, Transcode: "mp3"}
	mods.Video = config.VideoModalityConfig{Enabled: true, Transcode: true}
	_, err := CheckFFmpeg(context.Background(), mods)
	if err == nil {
		t.Fatal("missing ffmpeg must be an error when an enabled option needs it")
	}
	msg := err.Error()
	for _, want := range []string{"audio.transcode=mp3", "video.transcode=true", "/nonexistent/ffmpeg", "ffmpeg_path"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
}

func TestCheckFFmpeg_BrokenBinary(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho nope >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	mods := config.ModalitiesConfig{FFmpegPath: fake}
	mods.Video = config.VideoModalityConfig{Enabled: true, Transcode: true}
	if _, err := CheckFFmpeg(context.Background(), mods); err == nil || !strings.Contains(err.Error(), "-version failed") {
		t.Fatalf("a failing ffmpeg -version must be rejected, got %v", err)
	}
	notFFmpeg := filepath.Join(dir, "other")
	_ = os.WriteFile(notFFmpeg, []byte("#!/bin/sh\necho hello\n"), 0o755)
	mods.FFmpegPath = notFFmpeg
	if _, err := CheckFFmpeg(context.Background(), mods); err == nil || !strings.Contains(err.Error(), "does not look like ffmpeg") {
		t.Fatalf("a non-ffmpeg binary must be rejected, got %v", err)
	}
}

func realFFmpeg(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	return path
}

func TestCheckFFmpeg_Real(t *testing.T) {
	bin := realFFmpeg(t)
	mods := config.ModalitiesConfig{}
	mods.Audio = config.AudioModalityConfig{Enabled: true, Transcode: "wav"}
	if path, err := CheckFFmpeg(context.Background(), mods); err != nil || path != bin {
		t.Fatalf("got %q %v", path, err)
	}
}

// makeMedia generates a test clip with ffmpeg (e.g. an opus/ogg voice message).
func makeMedia(t *testing.T, bin, name string, args ...string) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), name)
	cmd := exec.Command(bin, append(append([]string{"-hide_banner", "-loglevel", "error", "-y"}, args...), out)...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make %s: %v %s", name, err, b)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestTranscodeAudioAndInputAudioPart(t *testing.T) {
	bin := realFFmpeg(t)
	ogg := makeMedia(t, bin, "v.ogg", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:a", "libopus")
	files := serveBytes(t, map[string][]byte{"/voice.ogg": ogg}, "audio/ogg")

	cfg := baseConfig("http://unused.invalid")
	cfg.AI.Modalities.Audio = config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeDescribe, Transcode: "mp3", PartType: config.AudioPartInput, ModalityModel: config.ModalityModel{Model: "omni"}}
	cfg.AI.Modalities.ApplyDefaults()
	p := New(cfg, nil)
	part, err := p.AudioPart(context.Background(), types.MediaAttachment{ID: "a", Kind: types.MediaVoice, URL: files + "/voice.ogg", Filename: "voice-message.ogg", ContentType: "audio/ogg", Size: len(ogg)})
	if err != nil {
		t.Fatal(err)
	}
	if part.Kind != ai.PartInputAudio || part.Format != "mp3" || part.Data == "" {
		t.Fatalf("part = %+v", part)
	}
	// MP3 frames start with an ID3 tag or a frame sync.
	if !strings.HasPrefix(part.Data, "SUQz") && !strings.HasPrefix(part.Data, "//") {
		t.Fatalf("not mp3 data: %.16s", part.Data)
	}
}

func TestTranscodeVideoTrimsAndScales(t *testing.T) {
	bin := realFFmpeg(t)
	mp4 := makeMedia(t, bin, "v.mp4", "-f", "lavfi", "-i", "testsrc=size=1280x720:rate=10:duration=4", "-c:v", "libx264", "-pix_fmt", "yuv420p")
	out, err := transcodeVideo(context.Background(), bin, mp4, "v.mp4", 2, 240)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	f := filepath.Join(t.TempDir(), "o.mp4")
	_ = os.WriteFile(f, out, 0o600)
	info, err := exec.Command(probe, "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=height:format=duration", "-of", "default=nw=1", f).Output()
	if err != nil {
		t.Fatal(err)
	}
	s := string(info)
	if !strings.Contains(s, "height=240") || !strings.Contains(s, "duration=2.") {
		t.Fatalf("expected 240p, ~2s: %s", s)
	}
}

func TestTranscodedTranscriptionUploadsMP3(t *testing.T) {
	bin := realFFmpeg(t)
	ogg := makeMedia(t, bin, "v.ogg", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:a", "libopus")
	files := serveBytes(t, map[string][]byte{"/voice.ogg": ogg}, "audio/ogg")
	var gotName, gotCT string
	asr := serveASR(t, func(name, ct string) { gotName, gotCT = name, ct })
	cfg := baseConfig("http://unused.invalid")
	cfg.AI.Modalities.Audio = config.AudioModalityConfig{Enabled: true, Mode: config.AudioModeASR, Transcode: "mp3", ModalityModel: config.ModalityModel{Model: "asr", APIBaseURL: asr}}
	cfg.AI.Modalities.ApplyDefaults()
	p := New(cfg, NewCache(time.Hour, 10))
	if _, err := p.Transcribe(context.Background(), types.MediaAttachment{ID: "a", Kind: types.MediaVoice, URL: files + "/voice.ogg", Filename: "voice-message.ogg", ContentType: "audio/ogg"}); err != nil {
		t.Fatal(err)
	}
	if gotName != "voice-message.mp3" || gotCT != "audio/mpeg" {
		t.Fatalf("upload = %q %q", gotName, gotCT)
	}
}
