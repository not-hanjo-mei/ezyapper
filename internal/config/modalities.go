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
// turns. Endpoint fields whose key is absent fall back to the matching
// core.ai value (documented inheritance between config keys), so a modality
// served by the same gateway does not repeat the endpoint. Presence, not
// the value, decides: an explicit 0 (temperature: 0, retry_count: 0) is
// used as is, and an explicit timeout: 0 or max_tokens: 0 is rejected.
//
// There are no built-in defaults: every other field an enabled modality
// uses (prompt, item and size limits, transport, ...) must be set in the
// config file. examples/config.yaml.example carries suggested values.

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

// Notices for removed and deprecated modality settings.
const (
	legacyVisionDeprecation   = "core.ai.vision is deprecated: it was mapped onto core.ai.modalities.image; move the settings there"
	legacyVisionIgnoredNotice = "core.ai.vision is ignored because core.ai.modalities.image is set; remove core.ai.vision"
	removedModeNotice         = "was removed: %s are always described into text by the %s model (the reply model core.ai.model is text-only and never receives media; direct mode no longer exists) - remove the mode field"
)

// ModalityModel is the model endpoint a modality calls (also the shape of
// the sticker and consolidation.vision overrides). Empty strings and absent
// keys fall back to the next model in the chain (see Resolve and
// StickerVisionModel). The numeric fields are pointers and ExtraParams is
// nil when its key is absent, so an absent key is distinct from an
// explicit 0 or {}: Load leaves an absent key nil, an explicit value
// (including 0 and {}) is kept, and Save writes back only what is set, so
// hot reload and the dashboard's Save keep the distinction.
type ModalityModel struct {
	Model       string         `mapstructure:"model" yaml:"model,omitempty"`
	APIBaseURL  string         `mapstructure:"api_base_url" yaml:"api_base_url,omitempty"`
	APIKey      string         `mapstructure:"api_key" yaml:"api_key,omitempty"`
	Timeout     *int           `mapstructure:"timeout" yaml:"timeout,omitempty"`
	MaxTokens   *int           `mapstructure:"max_tokens" yaml:"max_tokens,omitempty"`
	Temperature *float32       `mapstructure:"temperature" yaml:"temperature,omitempty"`
	RetryCount  *int           `mapstructure:"retry_count" yaml:"retry_count,omitempty"`
	ExtraParams map[string]any `mapstructure:"extra_params" yaml:"extra_params,omitempty"`
}

// Resolve returns a copy of base with this modality's set fields applied:
// non-empty strings and every key present in the config, whatever its
// value (temperature 0 is sent as 0, extra_params {} sends none). The
// result is a complete AIConfig an ai.Client can be built from.
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
	if m.Timeout != nil {
		out.Timeout = *m.Timeout
	}
	if m.MaxTokens != nil {
		out.MaxTokens = *m.MaxTokens
	}
	if m.Temperature != nil {
		out.Temperature = *m.Temperature
		out.ExplicitZeroTemperature = *m.Temperature == 0
	}
	if m.RetryCount != nil {
		out.RetryCount = *m.RetryCount
	}
	if m.ExtraParams != nil {
		out.ExtraParams = m.ExtraParams
	}
	return out
}

// Over returns m with every field it leaves unset (empty string, absent
// key) taken from parent: the sticker -> image and consolidation.vision ->
// image steps of the inheritance chain.
func (m ModalityModel) Over(parent ModalityModel) ModalityModel {
	if m.Model == "" {
		m.Model = parent.Model
	}
	if m.APIBaseURL == "" {
		m.APIBaseURL = parent.APIBaseURL
	}
	if m.APIKey == "" {
		m.APIKey = parent.APIKey
	}
	if m.Timeout == nil {
		m.Timeout = parent.Timeout
	}
	if m.MaxTokens == nil {
		m.MaxTokens = parent.MaxTokens
	}
	if m.Temperature == nil {
		m.Temperature = parent.Temperature
	}
	if m.RetryCount == nil {
		m.RetryCount = parent.RetryCount
	}
	if m.ExtraParams == nil {
		m.ExtraParams = parent.ExtraParams
	}
	return m
}

// checkModalityModel validates the keys of a model override that are set.
// Absent keys inherit (parent names what they inherit from); an explicit
// timeout or max_tokens of 0 is rejected instead of inherited, because 0
// would time out at once or allow no output.
func checkModalityModel(prefix string, mm ModalityModel, parent string, errs *[]string) {
	if mm.Timeout != nil && *mm.Timeout <= 0 {
		*errs = append(*errs, fmt.Sprintf("%stimeout must be greater than 0 (omit the key to use %s.timeout)", prefix, parent))
	}
	if mm.MaxTokens != nil && *mm.MaxTokens <= 0 {
		*errs = append(*errs, fmt.Sprintf("%smax_tokens must be greater than 0 (omit the key to use %s.max_tokens)", prefix, parent))
	}
	if mm.RetryCount != nil && *mm.RetryCount < 0 {
		*errs = append(*errs, fmt.Sprintf("%sretry_count must be greater than or equal to 0 (omit the key to use %s.retry_count)", prefix, parent))
	}
	if mm.Temperature != nil && (*mm.Temperature < 0 || *mm.Temperature > 2) {
		*errs = append(*errs, fmt.Sprintf("%stemperature must be between 0 and 2 (omit the key to use %s.temperature)", prefix, parent))
	}
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
	// Prompt is the ASR prompt (vocabulary / spelling hints; optional, empty
	// sends none) in asr mode, and the instruction for the audio model in
	// describe mode (required there).
	Prompt         string `mapstructure:"prompt" yaml:"prompt,omitempty"`
	MaxItems       int    `mapstructure:"max_items" yaml:"max_items,omitempty"`
	MaxBytes       int    `mapstructure:"max_bytes" yaml:"max_bytes,omitempty"`
	MaxDurationSec int    `mapstructure:"max_duration_sec" yaml:"max_duration_sec,omitempty"`
	// Transcode converts audio with ffmpeg before it is sent ("mp3" or
	// "wav"; empty sends it as is). Needed for an ASR endpoint that rejects
	// ogg/opus voice messages, and for part_type input_audio.
	Transcode string `mapstructure:"transcode" yaml:"transcode,omitempty"`
	// PartType is the content part describe mode sends: audio_url or
	// input_audio. Required in describe mode.
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
// the base model as "[Sticker: ...]" lines, so MaxItems, CacheTTLMin and
// CacheMaxEntries are always required; Enabled only decides whether the
// image is read:
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

// StickerVisionModel returns the model settings sticker vision uses: the
// sticker's own set fields, then modalities.image's (then core.ai's, when
// the result is resolved).
func (m ModalitiesConfig) StickerVisionModel() ModalityModel {
	return m.Sticker.ModalityModel.Over(m.Image.ModalityModel)
}

// ModalitiesConfig is core.ai.modalities. Every section is optional.
type ModalitiesConfig struct {
	Image   ImageModalityConfig   `mapstructure:"image" yaml:"image"`
	Audio   AudioModalityConfig   `mapstructure:"audio" yaml:"audio"`
	Video   VideoModalityConfig   `mapstructure:"video" yaml:"video"`
	Sticker StickerModalityConfig `mapstructure:"sticker" yaml:"sticker"`
	// FFmpegPath is the ffmpeg binary (a name looked up in PATH, or a path).
	// ffmpeg is optional; it is only needed by audio.transcode and
	// video.transcode, and this key is required when one of them is on.
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

// mapLegacyVision maps the deprecated core.ai.vision block onto
// core.ai.modalities.image when that block is not set, then clears the
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
	if ai.imageSet || !reflect.ValueOf(ai.Modalities.Image).IsZero() {
		ai.Vision = VisionConfig{}
		return legacyVisionIgnoredNotice
	}
	img := ImageModalityConfig{
		ModalityModel: legacy.ModalityModel,
		Prompt:        legacy.DescriptionPrompt,
		MaxItems:      legacy.MaxImages,
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
		checkModalityModel(prefix, mm, "core.ai", errs)
	}
	nonNegative := func(path string, v int) {
		if v < 0 {
			*errs = append(*errs, path+" must be greater than or equal to 0")
		}
	}
	// checkTransport validates a set transport; an enabled modality must
	// set one (there is no built-in default).
	checkTransport := func(path string, t MediaTransport, enabled bool, cond string) {
		switch t {
		case TransportBase64, TransportURL:
		case "":
			if enabled {
				*errs = append(*errs, requiredWhen(path, cond)+" (one of: base64, url)")
			}
		default:
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
	const imgOn = p + "image.enabled is true"
	removedMode("image", "images", img.RemovedMode)
	nonNegative(p+"image.max_items", img.MaxItems)
	checkTransport(p+"image.transport", img.Transport, img.Enabled, imgOn)
	if img.Enabled {
		checkModel(p+"image.", img.ModalityModel, true)
		requireTextWhen(img.Prompt, p+"image.prompt", imgOn, errs)
		requirePositiveWhen(img.MaxItems, p+"image.max_items", imgOn, errs)
	}

	au := m.Audio
	const auOn = p + "audio.enabled is true"
	switch au.Mode {
	case AudioModeASR, AudioModeDescribe:
	case "":
		if au.Enabled {
			*errs = append(*errs, requiredWhen(p+"audio.mode", auOn)+" (one of: asr, describe)")
		}
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
	case "":
		if au.Enabled && au.Mode == AudioModeDescribe {
			*errs = append(*errs, requiredWhen(p+"audio.part_type", auOn+" and audio.mode is describe")+" (one of: audio_url, input_audio)")
		}
	default:
		*errs = append(*errs, fmt.Sprintf(p+"audio.part_type %q is invalid (must be one of: audio_url, input_audio)", au.PartType))
	}
	if au.Enabled {
		checkModel(p+"audio.", au.ModalityModel, true)
		requirePositiveWhen(au.MaxItems, p+"audio.max_items", auOn, errs)
		requirePositiveWhen(au.MaxBytes, p+"audio.max_bytes", auOn, errs)
		requirePositiveWhen(au.MaxDurationSec, p+"audio.max_duration_sec", auOn, errs)
		if au.Mode == AudioModeDescribe {
			requireTextWhen(au.Prompt, p+"audio.prompt", auOn+" and audio.mode is describe", errs)
		}
	}

	v := m.Video
	const viOn = p + "video.enabled is true"
	removedMode("video", "videos", v.RemovedMode)
	nonNegative(p+"video.max_items", v.MaxItems)
	nonNegative(p+"video.max_bytes", v.MaxBytes)
	nonNegative(p+"video.max_duration_sec", v.MaxDurationSec)
	checkTransport(p+"video.transport", v.Transport, v.Enabled, viOn)
	nonNegative(p+"video.max_height", v.MaxHeight)
	if v.Transcode && v.Transport == TransportURL {
		*errs = append(*errs, p+"video.transcode needs transport base64 (a transcoded video has no URL)")
	}
	if v.Enabled {
		checkModel(p+"video.", v.ModalityModel, true)
		requireTextWhen(v.Prompt, p+"video.prompt", viOn, errs)
		requirePositiveWhen(v.MaxItems, p+"video.max_items", viOn, errs)
		requirePositiveWhen(v.MaxBytes, p+"video.max_bytes", viOn, errs)
		requirePositiveWhen(v.MaxDurationSec, p+"video.max_duration_sec", viOn, errs)
		if v.Transcode {
			requirePositiveWhen(v.MaxHeight, p+"video.max_height", viOn+" and video.transcode is true", errs)
		}
	}

	if reasons := m.FFmpegRequirements(); len(reasons) > 0 && strings.TrimSpace(m.FFmpegPath) == "" {
		*errs = append(*errs, requiredWhen(p+"ffmpeg_path", strings.Join(reasons, ", ")+" is set"))
	}

	// Stickers are always annotated (the text from Discord when enabled is
	// false), so the item cap and the sticker cache bounds are always used.
	st := m.Sticker
	const stAlways = "stickers are always added to the message"
	requirePositiveWhen(st.MaxItems, p+"sticker.max_items", stAlways, errs)
	requirePositiveWhen(st.CacheTTLMin, p+"sticker.cache_ttl_min", stAlways, errs)
	requirePositiveWhen(st.CacheMaxEntries, p+"sticker.cache_max_entries", stAlways, errs)
	if st.Enabled {
		checkModalityModel(p+"sticker.", st.ModalityModel, p+"image", errs)
		if m.StickerVisionModel().Model == "" {
			*errs = append(*errs, p+"sticker.model is required when sticker.enabled is true (or set "+p+"image.model); set sticker.enabled to false to use only the sticker's name, description and tags")
		}
		requireTextWhen(st.Prompt, p+"sticker.prompt", p+"sticker.enabled is true", errs)
	}
}
