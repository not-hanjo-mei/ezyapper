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
	"ezyapper/internal/logger"
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
	// videoClient describes videos.
	videoClient *ai.Client
	// asr transcribes audio (audio mode asr).
	asr *Transcriber
	// audioClient transcribes or describes audio (audio mode describe).
	audioClient *ai.Client

	// stickerClient describes sticker images (sticker mode vision).
	stickerClient *ai.Client
	stickerLookup StickerLookup
	stickerCache  *Cache

	// ffmpeg is the ffmpeg binary for transcode options (checked at
	// startup by CheckFFmpeg when one is enabled).
	ffmpeg string

	maxImageBytes int64
	cache         *Cache
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
	if mods.Sticker.Enabled && mods.Sticker.Mode == config.StickerModeVision {
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
func ImageCacheKey(url string) string { return "image:" + types.StableImageKey(url) }

// DescribeImages describes each image with the image model. Descriptions are
// cached by attachment (ImageCacheKey); the model is only called on a cache
// miss, so the reply path and memory consolidation, sharing one cache,
// describe each image once. It satisfies the image describer interface used
// by memory consolidation.
func (p *Processor) DescribeImages(ctx context.Context, urls []string) ([]string, error) {
	if p == nil || p.imageClient == nil {
		return nil, fmt.Errorf("image modality is disabled")
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("no images provided")
	}
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		d, err := p.cache.Do(ImageCacheKey(u), func() (string, error) {
			part, err := p.ImagePart(ctx, u)
			if err != nil {
				return "", fmt.Errorf("failed to fetch image: %w", err)
			}
			d, err := p.imageClient.DescribeMedia(ctx, p.mods.Image.Prompt, part)
			if err != nil {
				return "", fmt.Errorf("failed to describe image: %w", err)
			}
			return d, nil
		})
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// ImageDescriber returns p as an image describer, or nil when images are not
// enabled (so callers can keep a nil check).
func (p *Processor) ImageDescriber() *Processor {
	if p == nil || p.imageClient == nil {
		return nil
	}
	return p
}

// checkLimits rejects media over the configured size or duration. The
// duration is only known for voice messages (Discord sends duration_secs).
func checkLimits(a types.MediaAttachment, maxBytes, maxDurationSec int) error {
	if maxBytes > 0 && a.Size > maxBytes {
		return fmt.Errorf("over the size limit (%d > %d bytes)", a.Size, maxBytes)
	}
	if maxDurationSec > 0 && a.DurationSec > float64(maxDurationSec) {
		return fmt.Errorf("over the duration limit (%.0fs > %ds)", a.DurationSec, maxDurationSec)
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
	return p.cache.Do("audio:"+a.ID, func() (string, error) {
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
				return "", err
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
			return ai.MediaPart{}, err
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
	return p.cache.Do("audio-describe:"+a.ID, func() (string, error) {
		part, err := p.AudioPart(ctx, a)
		if err != nil {
			return "", err
		}
		prompt := p.mods.Audio.Prompt
		if strings.TrimSpace(prompt) == "" {
			prompt = config.DefaultAudioPrompt
		}
		return p.audioClient.DescribeMedia(ctx, prompt, part)
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
			return ai.MediaPart{}, err
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
	return p.cache.Do("video:"+a.ID, func() (string, error) {
		part, err := p.VideoPart(ctx, a)
		if err != nil {
			return "", err
		}
		return p.videoClient.DescribeMedia(ctx, p.mods.Video.Prompt, part)
	})
}

// Annotation is the result of processing a message's audio and video.
type Annotation struct {
	// Text is appended to the message content ("\n[Audio 1 transcript: ...]").
	Text string
}

// AnnotateOptions controls AnnotateAV.
type AnnotateOptions struct {
	// Current is true for the message being answered: missing results are
	// computed.
	Current bool
	// ComputeAudio / ComputeVideo allow computing missing results for a
	// history message (bounded on-demand enrichment).
	ComputeAudio, ComputeVideo bool
}

// AVText returns the text an audio or video attachment was turned into: the
// transcript (audio, asr mode; transcript is true) or the description (audio
// in describe mode, video). Cached results are always used; a missing one is
// only computed when compute is true. ok is false when the modality is
// disabled, the attachment is not audio/video, or there is no result.
func (p *Processor) AVText(ctx context.Context, a types.MediaAttachment, compute bool) (text string, transcript bool, ok bool) {
	if p == nil {
		return "", false, false
	}
	switch {
	case a.IsAudio() && p.mods.Audio.Enabled:
		describe := p.mods.Audio.Mode == config.AudioModeDescribe
		key, run := "audio:"+a.ID, p.Transcribe
		if describe {
			key, run = "audio-describe:"+a.ID, p.DescribeAudio
		}
		if v, hit := p.cache.Get(key); hit {
			return v, !describe, true
		}
		if !compute {
			return "", !describe, false
		}
		v, err := run(ctx, a)
		if err != nil {
			logger.Warnf("[media] audio %s not processed: %v", a.ID, err)
			return "", !describe, false
		}
		return v, !describe, true
	case a.Kind == types.MediaVideo && p.mods.Video.Enabled:
		if v, hit := p.cache.Get("video:" + a.ID); hit {
			return v, false, true
		}
		if !compute {
			return "", false, false
		}
		v, err := p.DescribeVideo(ctx, a)
		if err != nil {
			logger.Warnf("[media] video %s not described: %v", a.ID, err)
			return "", false, false
		}
		return v, false, true
	}
	return "", false, false
}

// AnnotateAV returns text lines for the audio, voice and video attachments
// of msg.
// Cached results are always used; missing ones are only computed when opts
// allow it. Media whose modality is disabled is left out entirely.
func (p *Processor) AnnotateAV(ctx context.Context, msg *types.DiscordMessage, opts AnnotateOptions) Annotation {
	var out Annotation
	if p == nil || msg == nil || len(msg.Media) == 0 {
		return out
	}
	var sb strings.Builder
	audioN, videoN := 0, 0
	au, vi := p.mods.Audio, p.mods.Video
	for _, a := range msg.Media {
		switch {
		case a.IsAudio() && au.Enabled:
			audioN++
			if audioN > au.MaxItems && au.MaxItems > 0 {
				continue
			}
			label := audioLabel(a)
			text, transcript, ok := p.AVText(ctx, a, opts.Current || opts.ComputeAudio)
			switch {
			case ok && !transcript:
				fmt.Fprintf(&sb, "\n[Audio %d (%s): %s]", audioN, label, oneLine(text))
			case ok:
				fmt.Fprintf(&sb, "\n[Audio %d transcript (%s): %s]", audioN, label, oneLine(text))
			default:
				fmt.Fprintf(&sb, "\n[Audio %d: %s]", audioN, label)
			}
		case a.Kind == types.MediaVideo && vi.Enabled:
			videoN++
			if videoN > vi.MaxItems && vi.MaxItems > 0 {
				continue
			}
			label := videoLabel(a)
			if text, _, ok := p.AVText(ctx, a, opts.Current || opts.ComputeVideo); ok {
				fmt.Fprintf(&sb, "\n[Video %d (%s): %s]", videoN, label, oneLine(text))
			} else {
				fmt.Fprintf(&sb, "\n[Video %d: %s]", videoN, label)
			}
		}
	}
	out.Text = sb.String()
	return out
}

// AnnotateForMemory returns msgs with the Text of their audio, voice and
// video attachments filled, for memory consolidation. Results come from the
// cache (by attachment ID) when the reply path already produced them; a
// missing one is computed once and cached. The per-message max_items limits
// apply. Messages without such media are returned as is; the others are
// copies (the input is not modified).
func (p *Processor) AnnotateForMemory(ctx context.Context, msgs []*types.DiscordMessage) []*types.DiscordMessage {
	if p == nil || (!p.mods.Audio.Enabled && !p.mods.Video.Enabled) {
		return msgs
	}
	out := make([]*types.DiscordMessage, len(msgs))
	for i, msg := range msgs {
		out[i] = msg
		if msg == nil || len(msg.Media) == 0 {
			continue
		}
		atts := make([]types.MediaAttachment, len(msg.Media))
		copy(atts, msg.Media)
		changed := false
		audioN, videoN := 0, 0
		for j, a := range atts {
			switch {
			case a.IsAudio() && p.mods.Audio.Enabled:
				audioN++
				if p.mods.Audio.MaxItems > 0 && audioN > p.mods.Audio.MaxItems {
					continue
				}
			case a.Kind == types.MediaVideo && p.mods.Video.Enabled:
				videoN++
				if p.mods.Video.MaxItems > 0 && videoN > p.mods.Video.MaxItems {
					continue
				}
			default:
				continue
			}
			if text, transcript, ok := p.AVText(ctx, a, true); ok {
				atts[j].Text, atts[j].Transcript = oneLine(text), transcript
				changed = true
			}
		}
		if changed {
			cp := *msg
			cp.Media = atts
			out[i] = &cp
		}
	}
	return out
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
