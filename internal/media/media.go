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
	// imageClient describes images (image enabled; any mode, so memory
	// consolidation can still describe images in direct mode).
	imageClient *ai.Client
	// videoClient describes videos.
	videoClient *ai.Client
	// asr transcribes audio (audio mode asr).
	asr *Transcriber
	// replyClient answers turns that carry direct media when a direct
	// modality names its own model; nil means the base client.
	replyClient *ai.Client

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

// New builds a Processor from cfg. registry is the tool registry for the
// direct-mode reply client (direct replies keep tool support).
func New(cfg *config.Config, registry *tools.ToolRegistry, cache *Cache) *Processor {
	mods := cfg.AI.Modalities
	if registry == nil {
		registry = tools.NewToolRegistry()
	}
	p := &Processor{
		mods:          mods,
		maxImageBytes: int64(cfg.AI.MaxImageBytes),
		cache:         cache,
		ffmpeg:        ffmpegBinary(mods),
	}
	base := cfg.AI
	p.base = ai.NewClient(&base, registry)

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
	if mods.Sticker.Enabled && mods.Sticker.Mode == config.StickerModeVision {
		sc := mods.StickerVisionModel().Resolve(cfg.AI)
		p.stickerClient = ai.NewClient(&sc, tools.NewToolRegistry())
	}
	for _, name := range []string{"image", "audio"} {
		mm, ok := mods.DirectModalities()[name]
		if ok && mm.HasEndpointOverride() {
			rc := mm.Resolve(cfg.AI)
			p.replyClient = ai.NewClient(&rc, registry)
			break
		}
	}
	return p
}

// Modalities returns the config snapshot the processor was built from.
func (p *Processor) Modalities() config.ModalitiesConfig { return p.mods }

// ReplyClient returns the client that should answer a turn: the direct-mode
// model when the turn carries direct media and one is configured, else base.
func (p *Processor) ReplyClient(base *ai.Client, hasDirectMedia bool) *ai.Client {
	if hasDirectMedia && p.replyClient != nil {
		return p.replyClient
	}
	return base
}

// ImageDescribeEnabled reports whether images are described into text.
func (p *Processor) ImageDescribeEnabled() bool {
	return p.mods.Image.Enabled && p.mods.Image.Mode == config.ImageModeDescribe
}

// ImageDirectEnabled reports whether images go straight to the reply model.
func (p *Processor) ImageDirectEnabled() bool {
	return p.mods.Image.Enabled && p.mods.Image.Mode == config.ImageModeDirect
}

// ImagePart returns the image as a content part, inlined as a data URI when
// the transport is base64.
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

// DescribeImages describes each image with the image model. It satisfies the
// image describer interface used by memory consolidation.
func (p *Processor) DescribeImages(ctx context.Context, urls []string) ([]string, error) {
	if p == nil || p.imageClient == nil {
		return nil, fmt.Errorf("image modality is disabled")
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("no images provided")
	}
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		part, err := p.ImagePart(ctx, u)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch image: %w", err)
		}
		d, err := p.imageClient.DescribeMedia(ctx, p.mods.Image.Prompt, part)
		if err != nil {
			return nil, fmt.Errorf("failed to describe image: %w", err)
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
	return data, ct, nil
}

// Transcribe returns the transcript of an audio attachment (asr mode),
// cached by attachment ID.
func (p *Processor) Transcribe(ctx context.Context, a types.MediaAttachment) (string, error) {
	if p.asr == nil {
		return "", fmt.Errorf("audio asr is disabled")
	}
	key := "audio:" + a.ID
	if v, ok := p.cache.Get(key); ok {
		return v, nil
	}
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
	text, err := p.asr.Transcribe(ctx, filename, ct, data)
	if err != nil {
		return "", err
	}
	p.cache.Set(key, text)
	return text, nil
}

// AudioPart returns an audio attachment as an audio_url data URI part
// (direct mode). Data URIs work for ogg/opus voice messages without
// transcoding; http URLs work too but are much slower on some gateways.
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

// VideoPart returns a video attachment as a video_url part.
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
	key := "video:" + a.ID
	if v, ok := p.cache.Get(key); ok {
		return v, nil
	}
	part, err := p.VideoPart(ctx, a)
	if err != nil {
		return "", err
	}
	d, err := p.videoClient.DescribeMedia(ctx, p.mods.Video.Prompt, part)
	if err != nil {
		return "", err
	}
	p.cache.Set(key, d)
	return d, nil
}

// Annotation is the result of processing a message's audio and video.
type Annotation struct {
	// Text is appended to the message content ("\n[Audio 1 transcript: ...]").
	Text string
	// Parts are media parts for the reply request (direct modes, current
	// message only).
	Parts []ai.MediaPart
}

// AnnotateOptions controls AnnotateAV.
type AnnotateOptions struct {
	// Current is true for the message being answered: missing results are
	// computed and direct-mode parts are built.
	Current bool
	// ComputeAudio / ComputeVideo allow computing missing results for a
	// history message (bounded on-demand enrichment).
	ComputeAudio, ComputeVideo bool
}

// AnnotateAV returns text lines (and, for the current message in direct
// mode, media parts) for the audio, voice and video attachments of msg.
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
			switch {
			case au.Mode == config.AudioModeDirect && opts.Current:
				part, err := p.AudioPart(ctx, a)
				if err != nil {
					logger.Warnf("[media] audio %s not sent: %v", a.ID, err)
					fmt.Fprintf(&sb, "\n[Audio %d: %s (not processed: %v)]", audioN, label, err)
					continue
				}
				out.Parts = append(out.Parts, part)
				fmt.Fprintf(&sb, "\n[Audio %d: %s, attached]", audioN, label)
			case au.Mode == config.AudioModeASR:
				text, ok := p.cache.Get("audio:" + a.ID)
				if !ok && (opts.Current || opts.ComputeAudio) {
					var err error
					text, err = p.Transcribe(ctx, a)
					if err != nil {
						logger.Warnf("[media] audio %s not transcribed: %v", a.ID, err)
					} else {
						ok = true
					}
				}
				if ok {
					fmt.Fprintf(&sb, "\n[Audio %d transcript (%s): %s]", audioN, label, oneLine(text))
				} else {
					fmt.Fprintf(&sb, "\n[Audio %d: %s]", audioN, label)
				}
			default:
				fmt.Fprintf(&sb, "\n[Audio %d: %s]", audioN, label)
			}
		case a.Kind == types.MediaVideo && vi.Enabled:
			videoN++
			if videoN > vi.MaxItems && vi.MaxItems > 0 {
				continue
			}
			label := videoLabel(a)
			text, ok := p.cache.Get("video:" + a.ID)
			if !ok && (opts.Current || opts.ComputeVideo) {
				var err error
				text, err = p.DescribeVideo(ctx, a)
				if err != nil {
					logger.Warnf("[media] video %s not described: %v", a.ID, err)
				} else {
					ok = true
				}
			}
			if ok {
				fmt.Fprintf(&sb, "\n[Video %d (%s): %s]", videoN, label, oneLine(text))
			} else {
				fmt.Fprintf(&sb, "\n[Video %d: %s]", videoN, label)
			}
		}
	}
	out.Text = sb.String()
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
