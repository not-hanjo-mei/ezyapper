package config

import (
	"fmt"
	"reflect"
	"strings"
)

// Per-modality model configuration (core.ai.modalities).
//
// Text is always on: core.ai is the base model that reasons, runs tools and
// writes every reply. It is treated as text-only: the reply request never
// carries media. Every other modality is optional and has its own on/off
// switch and its own model endpoint, which turns the media into text (a
// description or a transcript) that is added to the message in the user
// prompt ("[Image N: ...]", "[Audio N ...]", "[Video N ...]"). Results are
// cached by attachment ID, so the same media is not processed again on later
// turns. Endpoint fields left empty (or 0) fall back to the matching core.ai
// value, so a modality served by the same gateway only needs enabled + model.

// AudioMode selects how audio attachments and voice messages become text.
type AudioMode string

const (
	// AudioModeASR transcribes speech with an OpenAI-compatible
	// /audio/transcriptions endpoint; the transcript goes into the prompt.
	AudioModeASR AudioMode = "asr"
	// AudioModeDescribe sends the audio (an audio_url data URI part, or
	// input_audio) to an audio-capable chat-completions model, which
	// returns a transcript or description that goes into the prompt.
	AudioModeDescribe AudioMode = "describe"
)

// AudioPartType selects the content part used for audio in describe mode.
type AudioPartType string

const (
	// AudioPartURL sends an audio_url part with a data URI (any format the
	// model accepts; ogg/opus voice messages work on tested gateways).
	AudioPartURL AudioPartType = "audio_url"
	// AudioPartInput sends the OpenAI input_audio part, which only takes
	// mp3 or wav, so it needs audio.transcode.
	AudioPartInput AudioPartType = "input_audio"
)

// MediaTransport selects how media is handed to a model.
type MediaTransport string

const (
	// TransportBase64 downloads the media and sends it inline as a data URI.
	// Works with endpoints that cannot reach the Discord CDN.
	TransportBase64 MediaTransport = "base64"
	// TransportURL sends the (signed) Discord CDN URL and lets the model
	// provider fetch it.
	TransportURL MediaTransport = "url"
)

// Defaults applied to an enabled modality when the field is left empty.
const (
	DefaultImagePrompt        = "Describe this image in 1-2 sentences. Be compact but informative. Transcribe any visible text."
	DefaultAudioPrompt        = "Transcribe the speech in this audio verbatim, in its original language. If there is no speech, describe the sound or music in one sentence. Reply with the transcript or description only."
	DefaultVideoPrompt        = "Describe this video in 2-4 sentences: what happens, who or what appears, and any speech or on-screen text. Be compact but informative."
	DefaultStickerPrompt      = "Describe this Discord sticker in one short sentence: what it shows and the emotion or reaction it conveys. Include any visible text."
	DefaultStickerMaxItems    = 3
	DefaultStickerCacheTTL    = 1440 // minutes
	DefaultStickerCacheMax    = 1000
	DefaultVideoMaxHeight     = 480
	DefaultImageMaxItems      = 4
	DefaultAudioMaxItems      = 2
	DefaultVideoMaxItems      = 1
	DefaultAudioMaxBytes      = 25 << 20 // OpenAI transcription upload limit
	DefaultVideoMaxBytes      = 50 << 20
	DefaultAudioMaxDuration   = 600
	DefaultVideoMaxDuration   = 120
	legacyVisionDeprecation   = "core.ai.vision is deprecated: it was mapped onto core.ai.modalities.image; move the settings there"
	legacyVisionIgnoredNotice = "core.ai.vision is ignored because core.ai.modalities is set; remove core.ai.vision"
	removedModeNotice         = "was removed: %s are always described into text by the %s model (the reply model core.ai.model is text-only and never receives media; direct mode no longer exists) - remove the mode field"
)

// ModalityModel is the model endpoint a modality calls. Empty fields fall
// back to core.ai (see Resolve).
type ModalityModel struct {
	Model       string         `mapstructure:"model" yaml:"model,omitempty"`
	APIBaseURL  string         `mapstructure:"api_base_url" yaml:"api_base_url,omitempty"`
	APIKey      string         `mapstructure:"api_key" yaml:"api_key,omitempty"`
	Timeout     int            `mapstructure:"timeout" yaml:"timeout,omitempty"`
	MaxTokens   int            `mapstructure:"max_tokens" yaml:"max_tokens,omitempty"`
	Temperature float32        `mapstructure:"temperature" yaml:"temperature,omitempty"`
	RetryCount  int            `mapstructure:"retry_count" yaml:"retry_count,omitempty"`
	ExtraParams map[string]any `mapstructure:"extra_params" yaml:"extra_params,omitempty"`
}

// Resolve returns a copy of base with this modality's non-empty fields
// applied. The result is a complete AIConfig an ai.Client can be built from.
func (m ModalityModel) Resolve(base AIConfig) AIConfig {
	out := base
	if m.Model != "" {
		out.Model = m.Model
	}
	if m.APIBaseURL != "" {
		out.APIBaseURL = m.APIBaseURL
	}
	if m.APIKey != "" {
		out.APIKey = m.APIKey
	}
	if m.Timeout > 0 {
		out.Timeout = m.Timeout
	}
	if m.MaxTokens > 0 {
		out.MaxTokens = m.MaxTokens
	}
	if m.Temperature > 0 {
		out.Temperature = m.Temperature
	}
	if m.RetryCount > 0 {
		out.RetryCount = m.RetryCount
	}
	if m.ExtraParams != nil {
		out.ExtraParams = m.ExtraParams
	}
	return out
}

// ImageModalityConfig configures images (attachments and image embeds).
type ImageModalityConfig struct {
	ModalityModel `mapstructure:",squash" yaml:",inline"`
	Enabled       bool `mapstructure:"enabled" yaml:"enabled"`
	// Prompt asks the vision model for a description.
	Prompt string `mapstructure:"prompt" yaml:"prompt,omitempty"`
	// MaxItems caps the images processed per message.
	MaxItems  int            `mapstructure:"max_items" yaml:"max_items,omitempty"`
	Transport MediaTransport `mapstructure:"transport" yaml:"transport,omitempty"`
	// RemovedMode catches the removed image.mode key so validation can tell
	// the operator to delete it. It is never written back.
	RemovedMode string `mapstructure:"mode" yaml:"-"`
}

// AudioModalityConfig configures audio attachments and voice messages.
type AudioModalityConfig struct {
	ModalityModel `mapstructure:",squash" yaml:",inline"`
	Enabled       bool      `mapstructure:"enabled" yaml:"enabled"`
	Mode          AudioMode `mapstructure:"mode" yaml:"mode,omitempty"`
	// Language is an optional ISO-639-1 hint for the ASR endpoint (asr
	// mode).
	Language string `mapstructure:"language" yaml:"language,omitempty"`
	// Prompt is the ASR prompt (vocabulary / spelling hints; optional) in
	// asr mode, and the instruction for the audio model in describe mode
	// (empty uses DefaultAudioPrompt).
	Prompt         string `mapstructure:"prompt" yaml:"prompt,omitempty"`
	MaxItems       int    `mapstructure:"max_items" yaml:"max_items,omitempty"`
	MaxBytes       int    `mapstructure:"max_bytes" yaml:"max_bytes,omitempty"`
	MaxDurationSec int    `mapstructure:"max_duration_sec" yaml:"max_duration_sec,omitempty"`
	// Transcode converts audio with ffmpeg before it is sent ("mp3" or
	// "wav"; empty sends it as is). Needed for an ASR endpoint that rejects
	// ogg/opus voice messages, and for part_type input_audio.
	Transcode string `mapstructure:"transcode" yaml:"transcode,omitempty"`
	// PartType is the content part describe mode sends: audio_url
	// (default) or input_audio.
	PartType AudioPartType `mapstructure:"part_type" yaml:"part_type,omitempty"`
}

// VideoModalityConfig configures video attachments.
type VideoModalityConfig struct {
	ModalityModel  `mapstructure:",squash" yaml:",inline"`
	Enabled        bool           `mapstructure:"enabled" yaml:"enabled"`
	Prompt         string         `mapstructure:"prompt" yaml:"prompt,omitempty"`
	MaxItems       int            `mapstructure:"max_items" yaml:"max_items,omitempty"`
	MaxBytes       int            `mapstructure:"max_bytes" yaml:"max_bytes,omitempty"`
	MaxDurationSec int            `mapstructure:"max_duration_sec" yaml:"max_duration_sec,omitempty"`
	Transport      MediaTransport `mapstructure:"transport" yaml:"transport,omitempty"`
	// Transcode re-encodes videos with ffmpeg before they are sent: trimmed
	// to max_duration_sec (instead of skipping longer ones when Discord
	// reports a duration) and scaled down to max_height, H.264/AAC mp4.
	Transcode bool `mapstructure:"transcode" yaml:"transcode,omitempty"`
	MaxHeight int  `mapstructure:"max_height" yaml:"max_height,omitempty"`
	// RemovedMode catches the removed video.mode key (see
	// ImageModalityConfig.RemovedMode).
	RemovedMode string `mapstructure:"mode" yaml:"-"`
}

// StickerModalityConfig configures stickers. Stickers are always shown to
// the base model as "[Sticker: ...]" lines; Enabled only decides whether
// the image is read:
//
//   - true: a vision model describes the sticker image once per sticker ID.
//     Lottie stickers cannot be rendered and fall back to the text below.
//     The endpoint fields fall back to modalities.image, then core.ai.
//   - false: only the sticker's name, description and tags from Discord are
//     used; no model is called.
//
// The former mode key (text/vision) was removed; an old config that still
// sets it loads with a deprecation warning (see warnStickerMode).
type StickerModalityConfig struct {
	ModalityModel `mapstructure:",squash" yaml:",inline"`
	Enabled       bool   `mapstructure:"enabled" yaml:"enabled"`
	Prompt        string `mapstructure:"prompt" yaml:"prompt,omitempty"`
	MaxItems      int    `mapstructure:"max_items" yaml:"max_items,omitempty"`
	// Sticker details and vision results are cached by sticker ID. Stickers
	// rarely change, so the cache is long-lived.
	CacheTTLMin     int `mapstructure:"cache_ttl_min" yaml:"cache_ttl_min,omitempty"`
	CacheMaxEntries int `mapstructure:"cache_max_entries" yaml:"cache_max_entries,omitempty"`
}

// StickerVisionModel returns the model settings sticker vision uses: the sticker's
// own fields, then modalities.image's.
func (m ModalitiesConfig) StickerVisionModel() ModalityModel {
	s, img := m.Sticker.ModalityModel, m.Image.ModalityModel
	if s.Model == "" {
		s.Model = img.Model
	}
	if s.APIBaseURL == "" {
		s.APIBaseURL = img.APIBaseURL
	}
	if s.APIKey == "" {
		s.APIKey = img.APIKey
	}
	if s.Timeout == 0 {
		s.Timeout = img.Timeout
	}
	if s.MaxTokens == 0 {
		s.MaxTokens = img.MaxTokens
	}
	if s.Temperature == 0 {
		s.Temperature = img.Temperature
	}
	if s.RetryCount == 0 {
		s.RetryCount = img.RetryCount
	}
	if s.ExtraParams == nil {
		s.ExtraParams = img.ExtraParams
	}
	return s
}

// ModalitiesConfig is core.ai.modalities. Every section is optional.
type ModalitiesConfig struct {
	Image   ImageModalityConfig   `mapstructure:"image" yaml:"image"`
	Audio   AudioModalityConfig   `mapstructure:"audio" yaml:"audio"`
	Video   VideoModalityConfig   `mapstructure:"video" yaml:"video"`
	Sticker StickerModalityConfig `mapstructure:"sticker" yaml:"sticker"`
	// FFmpegPath is the ffmpeg binary (default: "ffmpeg" from PATH). ffmpeg
	// is optional; it is only needed by audio.transcode and video.transcode.
	FFmpegPath string `mapstructure:"ffmpeg_path" yaml:"ffmpeg_path,omitempty"`
}

// FFmpegRequirements lists the enabled options that need ffmpeg (empty when
// nothing does, in which case ffmpeg is never looked up).
func (m ModalitiesConfig) FFmpegRequirements() []string {
	var out []string
	if m.Audio.Enabled && m.Audio.Transcode != "" {
		out = append(out, "core.ai.modalities.audio.transcode="+m.Audio.Transcode)
	}
	if m.Video.Enabled && m.Video.Transcode {
		out = append(out, "core.ai.modalities.video.transcode=true")
	}
	return out
}

// AnyEnabled reports whether any non-text modality is on (calls a model).
// Stickers are annotated with their Discord text even when it is false.
func (m ModalitiesConfig) AnyEnabled() bool {
	return m.Image.Enabled || m.Audio.Enabled || m.Video.Enabled || m.Sticker.Enabled
}

func (m ModalitiesConfig) isZero() bool {
	return reflect.ValueOf(m).IsZero()
}

// ApplyDefaults fills empty fields of the modalities with their defaults.
// It is idempotent.
func (m *ModalitiesConfig) ApplyDefaults() {
	img := &m.Image
	if img.Prompt == "" {
		img.Prompt = DefaultImagePrompt
	}
	if img.MaxItems == 0 {
		img.MaxItems = DefaultImageMaxItems
	}
	if img.Transport == "" {
		img.Transport = TransportBase64
	}

	au := &m.Audio
	if au.Mode == "" {
		au.Mode = AudioModeASR
	}
	if au.MaxItems == 0 {
		au.MaxItems = DefaultAudioMaxItems
	}
	if au.MaxBytes == 0 {
		au.MaxBytes = DefaultAudioMaxBytes
	}
	if au.MaxDurationSec == 0 {
		au.MaxDurationSec = DefaultAudioMaxDuration
	}
	if au.PartType == "" {
		au.PartType = AudioPartURL
	}

	v := &m.Video
	if v.Prompt == "" {
		v.Prompt = DefaultVideoPrompt
	}
	if v.MaxItems == 0 {
		v.MaxItems = DefaultVideoMaxItems
	}
	if v.MaxBytes == 0 {
		v.MaxBytes = DefaultVideoMaxBytes
	}
	if v.MaxDurationSec == 0 {
		v.MaxDurationSec = DefaultVideoMaxDuration
	}
	if v.Transport == "" {
		v.Transport = TransportBase64
	}
	if v.MaxHeight == 0 {
		v.MaxHeight = DefaultVideoMaxHeight
	}

	st := &m.Sticker
	if st.Prompt == "" {
		st.Prompt = DefaultStickerPrompt
	}
	if st.MaxItems == 0 {
		st.MaxItems = DefaultStickerMaxItems
	}
	if st.CacheTTLMin == 0 {
		st.CacheTTLMin = DefaultStickerCacheTTL
	}
	if st.CacheMaxEntries == 0 {
		st.CacheMaxEntries = DefaultStickerCacheMax
	}
}

// mapLegacyVision maps the deprecated core.ai.vision block onto
// core.ai.modalities.image when the new block is not set, then clears the
// legacy block so it is not written back by Save. It returns a notice for
// the operator (empty when there was nothing to map).
//
//	text_only  -> image disabled
//	hybrid     -> image enabled (the vision model describes images)
//	multimodal -> image enabled, also described: the reply model is
//	              text-only, so images never go to it directly
func mapLegacyVision(ai *AIConfig, errs *[]string) string {
	legacy := ai.Vision
	if reflect.ValueOf(legacy).IsZero() {
		return ""
	}
	if ai.modalitiesSet || !ai.Modalities.isZero() {
		ai.Vision = VisionConfig{}
		return legacyVisionIgnoredNotice
	}
	model := legacy.Model
	if model == "" {
		model = legacy.LLMConfig.Model
	}
	img := ImageModalityConfig{
		ModalityModel: ModalityModel{
			Model:       model,
			APIBaseURL:  legacy.APIBaseURL,
			APIKey:      legacy.APIKey,
			Timeout:     legacy.Timeout,
			MaxTokens:   legacy.MaxTokens,
			Temperature: legacy.Temperature,
			RetryCount:  legacy.RetryCount,
			ExtraParams: legacy.ExtraParams,
		},
		Prompt:   legacy.DescriptionPrompt,
		MaxItems: legacy.MaxImages,
	}
	if legacy.Base64 {
		img.Transport = TransportBase64
	} else {
		img.Transport = TransportURL
	}
	switch strings.TrimSpace(legacy.Mode) {
	case "", "text_only":
		img.Enabled = false
	case "hybrid", "multimodal":
		img.Enabled = true
	default:
		*errs = append(*errs, fmt.Sprintf("core.ai.vision.mode %q is invalid (deprecated block; must be one of: text_only, hybrid, multimodal) - use core.ai.modalities.image instead", legacy.Mode))
	}
	ai.Modalities.Image = img
	ai.Vision = VisionConfig{}
	return legacyVisionDeprecation
}

func validateModalities(cfg *Config, errs *[]string) {
	m := &cfg.AI.Modalities
	const p = "core.ai.modalities."

	checkModel := func(prefix string, mm ModalityModel, needsModel bool) {
		if needsModel && mm.Model == "" {
			*errs = append(*errs, prefix+"model is required when the modality is enabled in this mode")
		}
		if mm.Timeout < 0 {
			*errs = append(*errs, prefix+"timeout must be greater than or equal to 0")
		}
		if mm.MaxTokens < 0 {
			*errs = append(*errs, prefix+"max_tokens must be greater than 0 (or omitted to use core.ai.max_tokens)")
		}
		if mm.RetryCount < 0 {
			*errs = append(*errs, prefix+"retry_count must be greater than or equal to 0")
		}
		if mm.Temperature < 0 || mm.Temperature > 2 {
			*errs = append(*errs, prefix+"temperature must be between 0 and 2")
		}
	}
	nonNegative := func(path string, v int) {
		if v < 0 {
			*errs = append(*errs, path+" must be greater than or equal to 0")
		}
	}
	checkTransport := func(path string, t MediaTransport) {
		if t != TransportBase64 && t != TransportURL {
			*errs = append(*errs, fmt.Sprintf("%s %q is invalid (must be one of: base64, url)", path, t))
		}
	}

	removedMode := func(section, kind, v string) {
		if v == "" {
			return
		}
		*errs = append(*errs, fmt.Sprintf(p+"%s.mode (%q) "+removedModeNotice, section, v, kind, p+section))
	}

	img := m.Image
	removedMode("image", "images", img.RemovedMode)
	nonNegative(p+"image.max_items", img.MaxItems)
	checkTransport(p+"image.transport", img.Transport)
	if img.Enabled {
		checkModel(p+"image.", img.ModalityModel, true)
	}

	au := m.Audio
	switch au.Mode {
	case AudioModeASR, AudioModeDescribe:
	case "direct":
		*errs = append(*errs, p+"audio.mode \"direct\" was removed: the reply model core.ai.model is text-only and never receives media - use mode asr (a transcription endpoint) or describe (an audio-capable chat model writes a transcript)")
	default:
		*errs = append(*errs, fmt.Sprintf(p+"audio.mode %q is invalid (must be one of: asr, describe)", au.Mode))
	}
	nonNegative(p+"audio.max_items", au.MaxItems)
	nonNegative(p+"audio.max_bytes", au.MaxBytes)
	nonNegative(p+"audio.max_duration_sec", au.MaxDurationSec)
	switch au.Transcode {
	case "", "mp3", "wav":
	default:
		*errs = append(*errs, fmt.Sprintf(p+"audio.transcode %q is invalid (must be one of: mp3, wav, or empty)", au.Transcode))
	}
	switch au.PartType {
	case AudioPartURL:
	case AudioPartInput:
		if au.Transcode == "" {
			*errs = append(*errs, p+"audio.part_type input_audio only accepts mp3 or wav: set "+p+"audio.transcode to mp3 or wav")
		}
	default:
		*errs = append(*errs, fmt.Sprintf(p+"audio.part_type %q is invalid (must be one of: audio_url, input_audio)", au.PartType))
	}
	if au.Enabled {
		checkModel(p+"audio.", au.ModalityModel, true)
	}

	v := m.Video
	removedMode("video", "videos", v.RemovedMode)
	nonNegative(p+"video.max_items", v.MaxItems)
	nonNegative(p+"video.max_bytes", v.MaxBytes)
	nonNegative(p+"video.max_duration_sec", v.MaxDurationSec)
	checkTransport(p+"video.transport", v.Transport)
	nonNegative(p+"video.max_height", v.MaxHeight)
	if v.Transcode && v.Transport == TransportURL {
		*errs = append(*errs, p+"video.transcode needs transport base64 (a transcoded video has no URL)")
	}
	if v.Enabled {
		checkModel(p+"video.", v.ModalityModel, true)
	}

	st := m.Sticker
	nonNegative(p+"sticker.max_items", st.MaxItems)
	nonNegative(p+"sticker.cache_ttl_min", st.CacheTTLMin)
	nonNegative(p+"sticker.cache_max_entries", st.CacheMaxEntries)
	if st.Enabled {
		checkModel(p+"sticker.", st.ModalityModel, false)
		if m.StickerVisionModel().Model == "" {
			*errs = append(*errs, p+"sticker.model is required when sticker.enabled is true (or set "+p+"image.model); set sticker.enabled to false to use only the sticker's name, description and tags")
		}
	}
}
