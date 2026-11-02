package config

import (
	"fmt"
	"reflect"
	"strings"
)

// Per-modality model configuration (core.ai.modalities).
//
// Text is always on: core.ai is the base model that reasons and writes every
// reply. Every other modality is optional and has its own on/off switch and
// its own model endpoint. Endpoint fields left empty (or 0) fall back to the
// matching core.ai value, so a modality served by the same gateway only needs
// enabled + model.
//
// Two ways a modality reaches the base model:
//   - describe / asr: a dedicated model turns the media into text (a
//     description or a transcript) that is added to the message in the user
//     prompt. Results are cached by attachment ID, so the same media is not
//     processed again on later turns.
//   - direct: the media itself is sent as a content part in the reply
//     request. The reply is written by core.ai.model, or by the modality's
//     model when one is set (that model must then handle tools as well).

// ImageMode selects how images reach the base model.
type ImageMode string

const (
	// ImageModeDescribe has a dedicated vision model describe each image;
	// the description is added to the prompt as "[Image N: ...]".
	ImageModeDescribe ImageMode = "describe"
	// ImageModeDirect sends the images themselves in the reply request.
	ImageModeDirect ImageMode = "direct"
)

// AudioMode selects how audio attachments and voice messages are handled.
type AudioMode string

const (
	// AudioModeASR transcribes speech with an OpenAI-compatible
	// /audio/transcriptions endpoint; the transcript goes into the prompt.
	AudioModeASR AudioMode = "asr"
	// AudioModeDirect sends the audio itself (an audio_url data URI part) in
	// the reply request, to an audio-capable model.
	AudioModeDirect AudioMode = "direct"
)

// VideoMode selects how video attachments are handled.
type VideoMode string

const (
	// VideoModeDescribe has a dedicated video-capable model describe each
	// video; the description is added to the prompt as "[Video N: ...]".
	VideoModeDescribe VideoMode = "describe"
)

// StickerMode selects how stickers are shown to the base model.
type StickerMode string

const (
	// StickerModeText uses the sticker's name, description and tags (from
	// Discord; no model call).
	StickerModeText StickerMode = "text"
	// StickerModeVision has a vision model describe the sticker image once
	// per sticker ID. Lottie stickers cannot be rendered and fall back to
	// text.
	StickerModeVision StickerMode = "vision"
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
	DefaultVideoPrompt        = "Describe this video in 2-4 sentences: what happens, who or what appears, and any speech or on-screen text. Be compact but informative."
	DefaultStickerPrompt      = "Describe this Discord sticker in one short sentence: what it shows and the emotion or reaction it conveys. Include any visible text."
	DefaultStickerMaxItems    = 3
	DefaultStickerCacheTTL    = 1440 // minutes
	DefaultStickerCacheMax    = 1000
	DefaultImageMaxItems      = 4
	DefaultAudioMaxItems      = 2
	DefaultVideoMaxItems      = 1
	DefaultAudioMaxBytes      = 25 << 20 // OpenAI transcription upload limit
	DefaultVideoMaxBytes      = 50 << 20
	DefaultAudioMaxDuration   = 600
	DefaultVideoMaxDuration   = 120
	legacyVisionDeprecation   = "core.ai.vision is deprecated: it was mapped onto core.ai.modalities.image; move the settings there"
	legacyVisionIgnoredNotice = "core.ai.vision is ignored because core.ai.modalities is set; remove core.ai.vision"
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

// HasEndpointOverride reports whether the modality names its own model or
// endpoint (rather than using core.ai as is).
func (m ModalityModel) HasEndpointOverride() bool {
	return m.Model != "" || m.APIBaseURL != "" || m.APIKey != ""
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
	Enabled       bool      `mapstructure:"enabled" yaml:"enabled"`
	Mode          ImageMode `mapstructure:"mode" yaml:"mode,omitempty"`
	// Prompt asks the vision model for a description (describe mode).
	Prompt string `mapstructure:"prompt" yaml:"prompt,omitempty"`
	// MaxItems caps the images processed per message.
	MaxItems  int            `mapstructure:"max_items" yaml:"max_items,omitempty"`
	Transport MediaTransport `mapstructure:"transport" yaml:"transport,omitempty"`
}

// AudioModalityConfig configures audio attachments and voice messages.
type AudioModalityConfig struct {
	ModalityModel `mapstructure:",squash" yaml:",inline"`
	Enabled       bool      `mapstructure:"enabled" yaml:"enabled"`
	Mode          AudioMode `mapstructure:"mode" yaml:"mode,omitempty"`
	// Language is an optional ISO-639-1 hint for the ASR endpoint.
	Language string `mapstructure:"language" yaml:"language,omitempty"`
	// Prompt is an optional ASR prompt (vocabulary / spelling hints).
	Prompt         string `mapstructure:"prompt" yaml:"prompt,omitempty"`
	MaxItems       int    `mapstructure:"max_items" yaml:"max_items,omitempty"`
	MaxBytes       int    `mapstructure:"max_bytes" yaml:"max_bytes,omitempty"`
	MaxDurationSec int    `mapstructure:"max_duration_sec" yaml:"max_duration_sec,omitempty"`
}

// VideoModalityConfig configures video attachments.
type VideoModalityConfig struct {
	ModalityModel  `mapstructure:",squash" yaml:",inline"`
	Enabled        bool           `mapstructure:"enabled" yaml:"enabled"`
	Mode           VideoMode      `mapstructure:"mode" yaml:"mode,omitempty"`
	Prompt         string         `mapstructure:"prompt" yaml:"prompt,omitempty"`
	MaxItems       int            `mapstructure:"max_items" yaml:"max_items,omitempty"`
	MaxBytes       int            `mapstructure:"max_bytes" yaml:"max_bytes,omitempty"`
	MaxDurationSec int            `mapstructure:"max_duration_sec" yaml:"max_duration_sec,omitempty"`
	Transport      MediaTransport `mapstructure:"transport" yaml:"transport,omitempty"`
}

// StickerModalityConfig configures stickers. In vision mode the endpoint
// fields fall back to modalities.image, then core.ai.
type StickerModalityConfig struct {
	ModalityModel `mapstructure:",squash" yaml:",inline"`
	Enabled       bool        `mapstructure:"enabled" yaml:"enabled"`
	Mode          StickerMode `mapstructure:"mode" yaml:"mode,omitempty"`
	Prompt        string      `mapstructure:"prompt" yaml:"prompt,omitempty"`
	MaxItems      int         `mapstructure:"max_items" yaml:"max_items,omitempty"`
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
}

// AnyEnabled reports whether any non-text modality is on.
func (m ModalitiesConfig) AnyEnabled() bool {
	return m.Image.Enabled || m.Audio.Enabled || m.Video.Enabled || m.Sticker.Enabled
}

func (m ModalitiesConfig) isZero() bool {
	return reflect.ValueOf(m).IsZero()
}

// DirectModalities returns the names and model settings of the enabled
// modalities that send media straight to the reply request.
func (m ModalitiesConfig) DirectModalities() map[string]ModalityModel {
	out := map[string]ModalityModel{}
	if m.Image.Enabled && m.Image.Mode == ImageModeDirect {
		out["image"] = m.Image.ModalityModel
	}
	if m.Audio.Enabled && m.Audio.Mode == AudioModeDirect {
		out["audio"] = m.Audio.ModalityModel
	}
	return out
}

// ApplyDefaults fills empty fields of the modalities with their defaults.
// It is idempotent.
func (m *ModalitiesConfig) ApplyDefaults() {
	img := &m.Image
	if img.Mode == "" {
		img.Mode = ImageModeDescribe
	}
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

	v := &m.Video
	if v.Mode == "" {
		v.Mode = VideoModeDescribe
	}
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

	st := &m.Sticker
	if st.Mode == "" {
		st.Mode = StickerModeText
	}
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
//	hybrid     -> image enabled, mode describe (vision model describes)
//	multimodal -> image enabled, mode direct (vision model gets the images
//	              and writes the reply, as before)
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
	case "hybrid":
		img.Enabled = true
		img.Mode = ImageModeDescribe
	case "multimodal":
		img.Enabled = true
		img.Mode = ImageModeDirect
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

	img := m.Image
	switch img.Mode {
	case ImageModeDescribe, ImageModeDirect:
	default:
		*errs = append(*errs, fmt.Sprintf(p+"image.mode %q is invalid (must be one of: describe, direct)", img.Mode))
	}
	nonNegative(p+"image.max_items", img.MaxItems)
	checkTransport(p+"image.transport", img.Transport)
	if img.Enabled {
		checkModel(p+"image.", img.ModalityModel, img.Mode == ImageModeDescribe)
	}

	au := m.Audio
	switch au.Mode {
	case AudioModeASR, AudioModeDirect:
	default:
		*errs = append(*errs, fmt.Sprintf(p+"audio.mode %q is invalid (must be one of: asr, direct)", au.Mode))
	}
	nonNegative(p+"audio.max_items", au.MaxItems)
	nonNegative(p+"audio.max_bytes", au.MaxBytes)
	nonNegative(p+"audio.max_duration_sec", au.MaxDurationSec)
	if au.Enabled {
		checkModel(p+"audio.", au.ModalityModel, au.Mode == AudioModeASR)
	}

	v := m.Video
	if v.Mode != VideoModeDescribe {
		*errs = append(*errs, fmt.Sprintf(p+"video.mode %q is invalid (must be: describe)", v.Mode))
	}
	nonNegative(p+"video.max_items", v.MaxItems)
	nonNegative(p+"video.max_bytes", v.MaxBytes)
	nonNegative(p+"video.max_duration_sec", v.MaxDurationSec)
	checkTransport(p+"video.transport", v.Transport)
	if v.Enabled {
		checkModel(p+"video.", v.ModalityModel, true)
	}

	st := m.Sticker
	switch st.Mode {
	case StickerModeText, StickerModeVision:
	default:
		*errs = append(*errs, fmt.Sprintf(p+"sticker.mode %q is invalid (must be one of: text, vision)", st.Mode))
	}
	nonNegative(p+"sticker.max_items", st.MaxItems)
	nonNegative(p+"sticker.cache_ttl_min", st.CacheTTLMin)
	nonNegative(p+"sticker.cache_max_entries", st.CacheMaxEntries)
	if st.Enabled && st.Mode == StickerModeVision {
		checkModel(p+"sticker.", st.ModalityModel, false)
		if m.StickerVisionModel().Model == "" {
			*errs = append(*errs, p+"sticker.model is required when sticker.mode is vision (or set "+p+"image.model)")
		}
	}

	// All direct modalities share one reply request, so the ones that name
	// their own model must agree on it.
	var first string
	var firstModel ModalityModel
	for _, name := range []string{"image", "audio"} {
		mm, ok := m.DirectModalities()[name]
		if !ok || !mm.HasEndpointOverride() {
			continue
		}
		if first == "" {
			first, firstModel = name, mm
			continue
		}
		if mm.Model != firstModel.Model || mm.APIBaseURL != firstModel.APIBaseURL || mm.APIKey != firstModel.APIKey {
			*errs = append(*errs, fmt.Sprintf(p+"%s and "+p+"%s are both in direct mode but name different models/endpoints; direct media share one reply request, so set the same model, api_base_url and api_key (or leave them empty to use core.ai)", first, name))
		}
	}
}
