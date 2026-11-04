package media

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ezyapper/internal/config"
)

// ffmpeg is optional. It is only needed by audio.transcode and
// video.transcode; when one of those is on and ffmpeg is missing the bot
// refuses to start (CheckFFmpeg) instead of failing on the first message.

// ffmpegBinary returns the configured binary name or path
// (core.ai.modalities.ffmpeg_path, required whenever an option needs ffmpeg).
func ffmpegBinary(mods config.ModalitiesConfig) string {
	return strings.TrimSpace(mods.FFmpegPath)
}

// CheckFFmpeg verifies that ffmpeg is usable when an enabled option needs
// it, and returns the resolved binary path ("" when nothing needs it). It
// looks the binary up and runs "ffmpeg -version".
func CheckFFmpeg(ctx context.Context, mods config.ModalitiesConfig) (string, error) {
	reasons := mods.FFmpegRequirements()
	if len(reasons) == 0 {
		return "", nil
	}
	fail := func(cause string) error {
		return fmt.Errorf("ffmpeg is required by %s, but %s: install ffmpeg, set core.ai.modalities.ffmpeg_path, or turn those options off", strings.Join(reasons, ", "), cause)
	}
	bin := ffmpegBinary(mods)
	if bin == "" {
		return "", fail("core.ai.modalities.ffmpeg_path is not set (no built-in default)")
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return "", fail(fmt.Sprintf("%q was not found (%v)", bin, err))
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-hide_banner", "-version").CombinedOutput()
	if err != nil {
		return "", fail(fmt.Sprintf("%q -version failed (%v: %s)", path, err, strings.TrimSpace(firstLine(string(out)))))
	}
	if !strings.HasPrefix(string(out), "ffmpeg version") {
		return "", fail(fmt.Sprintf("%q does not look like ffmpeg (-version printed %q)", path, firstLine(string(out))))
	}
	return path, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// transcode runs ffmpeg on data through temporary files (mp4 inputs need a
// seekable file) and returns the output. outArgs go between the input and
// the output file; ext picks the output container.
func transcode(ctx context.Context, bin string, data []byte, inName string, outArgs []string, ext string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "ezyapper-ffmpeg-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	inExt := filepath.Ext(inName)
	if inExt == "" {
		inExt = ".bin"
	}
	in := filepath.Join(dir, "in"+inExt)
	out := filepath.Join(dir, "out."+ext)
	if err := os.WriteFile(in, data, 0o600); err != nil {
		return nil, err
	}
	args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-i", in}, outArgs...)
	args = append(args, out)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return os.ReadFile(out)
}

// transcodeAudio converts audio to mp3 or wav (mono, 16 kHz: what speech
// models expect, and small).
func transcodeAudio(ctx context.Context, bin string, data []byte, inName, format string) ([]byte, string, error) {
	var args []string
	ct := ""
	switch format {
	case "mp3":
		args = []string{"-vn", "-ac", "1", "-ar", "16000", "-c:a", "libmp3lame", "-b:a", "64k", "-f", "mp3"}
		ct = "audio/mpeg"
	case "wav":
		args = []string{"-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", "-f", "wav"}
		ct = "audio/wav"
	default:
		return nil, "", fmt.Errorf("unsupported transcode format %q", format)
	}
	out, err := transcode(ctx, bin, data, inName, args, format)
	return out, ct, err
}

// transcodeVideo trims to maxDurationSec, scales down to maxHeight and
// re-encodes as H.264/AAC mp4.
func transcodeVideo(ctx context.Context, bin string, data []byte, inName string, maxDurationSec, maxHeight int) ([]byte, error) {
	var args []string
	if maxDurationSec > 0 {
		args = append(args, "-t", strconv.Itoa(maxDurationSec))
	}
	if maxHeight > 0 {
		args = append(args, "-vf", fmt.Sprintf("scale=-2:'min(%d,ih)'", maxHeight))
	}
	args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-crf", "28", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "64k", "-movflags", "+faststart", "-f", "mp4")
	return transcode(ctx, bin, data, inName, args, "mp4")
}
