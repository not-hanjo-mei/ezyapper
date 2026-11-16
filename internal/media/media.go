package media

import (
	"context"
	"encoding/base64"
	"fmt"
	"path"
	"strings"
	"time"

	"ezyapper/internal/ai"
	"ezyapper/internal/ai/tools"
	"ezyapper/internal/config"
	"ezyapper/internal/types"
)

// Processor handles the non-text modalities of one config snapshot. Build a
// new one when the config changes; pass the same Cache so results survive.
type Processor struct {
	mods config.ModalitiesConfig

	// base is the core.ai client, used for downloads.
	base *ai.Client
	// imageClient describes images (image enabled).
	imageClient *ai.Client
	// memoryImageClient and memoryImagePrompt describe images for memory
	// consolidation (the image modality with consolidation.vision
	// overrides).
	memoryImageClient *ai.Client
	memoryImagePrompt string
	// videoClient describes videos.
	videoClient *ai.Client
	// asr transcribes audio (audio mode asr).
	asr *Transcriber
	// audioClient transcribes or describes audio (audio mode describe).
	audioClient *ai.Client

	// stickerClient describes sticker images (sticker.enabled).
	stickerClient *ai.Client
	stickerLookup StickerLookup
	stickerCache  *Cache

	// ffmpeg is the ffmpeg binary for transcode options (checked at
	// startup by CheckFFmpeg when one is enabled).
	ffmpeg string

	maxImageBytes int64
	cache         *Cache

	// selfID returns the bot's own user ID (see SetSelf).
	selfID func() string
}

// New builds a Processor from cfg. Every model it calls only turns media
// into text; the reply itself is always written by core.ai.model from text.
func New(cfg *config.Config, cache *Cache) *Processor {
	mods := cfg.AI.Modalities
	p := &Processor{
		mods:          mods,
		maxImageBytes: int64(cfg.AI.MaxImageBytes),
		cache:         cache,
		ffmpeg:        ffmpegBinary(mods),
	}
	base := cfg.AI
	p.base = ai.NewClient(&base, tools.NewToolRegistry())

	if mods.Image.Enabled {
		ic := mods.Image.Resolve(cfg.AI)
		p.imageClient = ai.NewClient(&ic, tools.NewToolRegistry())
		mi := cfg.ConsolidationImage()
		mc := mi.Resolve(cfg.AI)
		p.memoryImageClient = ai.NewClient(&mc, tools.NewToolRegistry())
		p.memoryImagePrompt = mi.Prompt
	}
	if mods.Video.Enabled {
		vc := mods.Video.Resolve(cfg.AI)
		p.videoClient = ai.NewClient(&vc, tools.NewToolRegistry())
	}
	if mods.Audio.Enabled && mods.Audio.Mode == config.AudioModeASR {
		ac := mods.Audio.Resolve(cfg.AI)
		p.asr = NewTranscriber(TranscriberConfig{
			BaseURL:    ac.APIBaseURL,
			APIKey:     ac.APIKey,
			Model:      ac.Model,
			Language:   mods.Audio.Language,
			Prompt:     mods.Audio.Prompt,
			Timeout:    time.Duration(ac.Timeout) * time.Second,
			RetryCount: ac.RetryCount,
		})
	}
	if mods.Audio.Enabled && mods.Audio.Mode == config.AudioModeDescribe {
		ac := mods.Audio.Resolve(cfg.AI)
		p.audioClient = ai.NewClient(&ac, tools.NewToolRegistry())
	}
	if mods.Sticker.Enabled {
		sc := mods.StickerVisionModel().Resolve(cfg.AI)
		p.stickerClient = ai.NewClient(&sc, tools.NewToolRegistry())
	}
	return p
}

// Modalities returns the config snapshot the processor was built from.
func (p *Processor) Modalities() config.ModalitiesConfig { return p.mods }

// ImageDescribeEnabled reports whether images are described into text.
func (p *Processor) ImageDescribeEnabled() bool {
	return p.mods.Image.Enabled
}

// ImagePart returns the image as a content part for the image model,
// inlined as a data URI when the transport is base64.
func (p *Processor) ImagePart(ctx context.Context, url string) (ai.MediaPart, error) {
	if strings.HasPrefix(url, "data:") || p.mods.Image.Transport == config.TransportURL {
		return ai.MediaPart{Kind: ai.PartImageURL, URL: url}, nil
	}
	data, ct, err := p.base.FetchMedia(ctx, url, p.maxImageBytes, "image/")
	if err != nil {
		return ai.MediaPart{}, err
	}
	if ct == "" {
		ct = "image/png"
	}
	return ai.MediaPart{Kind: ai.PartImageURL, URL: ai.DataURI(ct, data)}, nil
}

// ImageCacheKey is the cache key of an image description: the image's stable
// key (for Discord CDN URLs the attachment path with its attachment ID, so a
// re-signed URL still hits).
func ImageCacheKey(url string) string { return keyImage + types.StableImageKey(url) }

// checkLimits rejects media over the configured size or duration. The
// duration is only known for voice messages (Discord sends duration_secs).
func checkLimits(a types.MediaAttachment, maxBytes, maxDurationSec int) error {
	if maxBytes > 0 && a.Size > maxBytes {
		return &permanentError{err: fmt.Errorf("over the size limit (%d > %d bytes)", a.Size, maxBytes)}
	}
	if maxDurationSec > 0 && a.DurationSec > float64(maxDurationSec) {
		return &permanentError{err: fmt.Errorf("over the duration limit (%.0fs > %ds)", a.DurationSec, maxDurationSec)}
	}
	return nil
}

func (p *Processor) fetchAttachment(ctx context.Context, a types.MediaAttachment, maxBytes int, typePrefix string) ([]byte, string, error) {
	data, ct, err := p.base.FetchMedia(ctx, a.URL, int64(maxBytes), typePrefix)
	if err != nil {
		return nil, "", err
	}
	if baseContentType(a.ContentType) != "" {
		ct = a.ContentType // Discord's type is more precise than the CDN's
	}
	return data, normalizeMediaType(ct), nil
}

// mediaTypeAliases maps non-standard content types Discord reports (an
// uploaded .mp3 arrives as "audio/mpeg3") to the registered ones models
// and transcription endpoints recognize.
var mediaTypeAliases = map[string]string{
	"audio/mpeg3":  "audio/mpeg",
	"audio/x-mpeg": "audio/mpeg",
	"audio/mp3":    "audio/mpeg",
	"audio/x-mp3":  "audio/mpeg",
	"audio/x-wav":  "audio/wav",
	"audio/wave":   "audio/wav",
	"audio/x-m4a":  "audio/mp4",
	"audio/x-flac": "audio/flac",
}

func normalizeMediaType(ct string) string {
	if alias, ok := mediaTypeAliases[strings.ToLower(baseContentType(ct))]; ok {
		return alias
	}
	return ct
}

// Transcribe returns the transcript of an audio attachment (asr mode),
// cached by attachment ID.
func (p *Processor) Transcribe(ctx context.Context, a types.MediaAttachment) (string, error) {
	if p.asr == nil {
		return "", fmt.Errorf("audio asr is disabled")
	}
	return p.cache.Do(keyAudio+a.ID, func() (string, error) {
		au := p.mods.Audio
		if err := checkLimits(a, au.MaxBytes, au.MaxDurationSec); err != nil {
			return "", err
		}
		data, ct, err := p.fetchAttachment(ctx, a, au.MaxBytes, "audio/")
		if err != nil {
			return "", err
		}
		filename := a.Filename
		if au.Transcode != "" {
			if data, ct, err = transcodeAudio(ctx, p.ffmpeg, data, audioFilename(a.Filename, a.ContentType), au.Transcode); err != nil {
				return "", permanent(ctx, err)
			}
			filename = strings.TrimSuffix(filename, path.Ext(filename)) + "." + au.Transcode
		}
		return p.asr.Transcribe(ctx, filename, ct, data)
	})
}

// AudioPart returns an audio attachment as a content part for the audio
// model (describe mode): an audio_url data URI part, or input_audio. Data
// URIs work for ogg/opus voice messages without transcoding; http URLs work
// too but are much slower on some gateways.
func (p *Processor) AudioPart(ctx context.Context, a types.MediaAttachment) (ai.MediaPart, error) {
	au := p.mods.Audio
	if err := checkLimits(a, au.MaxBytes, au.MaxDurationSec); err != nil {
		return ai.MediaPart{}, err
	}
	data, ct, err := p.fetchAttachment(ctx, a, au.MaxBytes, "audio/")
	if err != nil {
		return ai.MediaPart{}, err
	}
	if au.Transcode != "" {
		if data, ct, err = transcodeAudio(ctx, p.ffmpeg, data, audioFilename(a.Filename, a.ContentType), au.Transcode); err != nil {
			return ai.MediaPart{}, permanent(ctx, err)
		}
	}
	if au.PartType == config.AudioPartInput {
		// Validation guarantees transcode is mp3 or wav here.
		return ai.MediaPart{Kind: ai.PartInputAudio, Data: base64.StdEncoding.EncodeToString(data), Format: au.Transcode}, nil
	}
	return ai.MediaPart{Kind: ai.PartAudioURL, URL: ai.DataURI(ct, data)}, nil
}

// DescribeAudio returns the audio model's transcript or description of an
// audio attachment (describe mode), cached by attachment ID.
func (p *Processor) DescribeAudio(ctx context.Context, a types.MediaAttachment) (string, error) {
	if p.audioClient == nil {
		return "", fmt.Errorf("audio describe is disabled")
	}
	return p.cache.Do(keyAudioDescribe+a.ID, func() (string, error) {
		part, err := p.AudioPart(ctx, a)
		if err != nil {
			return "", err
		}
		// audio.prompt is required in describe mode (validated at load).
		return p.audioClient.DescribeMedia(ctx, p.mods.Audio.Prompt, part)
	})
}

// VideoPart returns a video attachment as a video_url part for the video
// model.
func (p *Processor) VideoPart(ctx context.Context, a types.MediaAttachment) (ai.MediaPart, error) {
	v := p.mods.Video
	maxDuration := v.MaxDurationSec
	if v.Transcode {
		maxDuration = 0 // longer videos are trimmed instead of skipped
	}
	if err := checkLimits(a, v.MaxBytes, maxDuration); err != nil {
		return ai.MediaPart{}, err
	}
	if v.Transport == config.TransportURL {
		return ai.MediaPart{Kind: ai.PartVideoURL, URL: a.URL}, nil
	}
	data, ct, err := p.fetchAttachment(ctx, a, v.MaxBytes, "video/")
	if err != nil {
		return ai.MediaPart{}, err
	}
	if v.Transcode {
		name := a.Filename
		if name == "" {
			name = "video.mp4"
		}
		if data, err = transcodeVideo(ctx, p.ffmpeg, data, name, v.MaxDurationSec, v.MaxHeight); err != nil {
			return ai.MediaPart{}, permanent(ctx, err)
		}
		ct = "video/mp4"
	}
	return ai.MediaPart{Kind: ai.PartVideoURL, URL: ai.DataURI(ct, data)}, nil
}

// DescribeVideo returns a description of a video attachment, cached by
// attachment ID.
func (p *Processor) DescribeVideo(ctx context.Context, a types.MediaAttachment) (string, error) {
	if p.videoClient == nil {
		return "", fmt.Errorf("video modality is disabled")
	}
	return p.cache.Do(keyVideo+a.ID, func() (string, error) {
		part, err := p.VideoPart(ctx, a)
		if err != nil {
			return "", err
		}
		return p.videoClient.DescribeMedia(ctx, p.mods.Video.Prompt, part)
	})
}

func audioLabel(a types.MediaAttachment) string {
	name := "audio file"
	if a.Kind == types.MediaVoice {
		name = "voice message"
	} else if a.Filename != "" {
		name = a.Filename
	}
	if a.DurationSec > 0 {
		return fmt.Sprintf("%s, %.0fs", name, a.DurationSec)
	}
	return name
}

func videoLabel(a types.MediaAttachment) string {
	if a.Filename != "" {
		return a.Filename
	}
	return "video"
}

// oneLine collapses whitespace so a transcript stays on its prompt line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
