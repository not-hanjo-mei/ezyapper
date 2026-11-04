package media

import (
	"fmt"

	"ezyapper/internal/config"
	"ezyapper/internal/types"
)

// Cache key prefixes. Every path that turns media into text (the reply
// prompt, history, memory consolidation) uses these keys, so each
// attachment is processed once.
const (
	keyImage         = "image:"
	keyAudio         = "audio:"
	keyAudioDescribe = "audio-describe:"
	keyVideo         = "video:"
	keyStickerVision = "sticker:vision:"
	keyStickerInfo   = "sticker:info:"
)

// Media kinds, as used by Fingerprints and KindPrefixes.
const (
	KindImage   = "image"
	KindAudio   = "audio"
	KindVideo   = "video"
	KindSticker = "sticker"
)

// KindPrefixes returns the cache key prefixes of a media kind's model
// results. Sticker details (sticker:info:) come from Discord, not a model,
// and are not included.
func KindPrefixes(kind string) []string {
	switch kind {
	case KindImage:
		return []string{keyImage}
	case KindAudio:
		return []string{keyAudio, keyAudioDescribe}
	case KindVideo:
		return []string{keyVideo}
	case KindSticker:
		return []string{keyStickerVision}
	}
	return nil
}

// Fingerprints returns, per media kind, a string of the settings its cached
// text depends on: the resolved model and endpoint, the prompt and the
// transcode settings. When a kind's fingerprint changes on reload its
// cached results are stale.
func Fingerprints(cfg *config.Config) map[string]string {
	m := cfg.AI.Modalities
	img := m.Image.Resolve(cfg.AI)
	au := m.Audio.Resolve(cfg.AI)
	vi := m.Video.Resolve(cfg.AI)
	st := m.StickerVisionModel().Resolve(cfg.AI)
	return map[string]string{
		KindImage:   fmt.Sprintf("%q|%q|%q", img.Model, img.APIBaseURL, m.Image.Prompt),
		KindAudio:   fmt.Sprintf("%q|%q|%q|%q|%q|%q|%q", au.Model, au.APIBaseURL, m.Audio.Mode, m.Audio.Prompt, m.Audio.Language, m.Audio.Transcode, m.Audio.PartType),
		KindVideo:   fmt.Sprintf("%q|%q|%q|%t|%d|%d", vi.Model, vi.APIBaseURL, m.Video.Prompt, m.Video.Transcode, m.Video.MaxDurationSec, m.Video.MaxHeight),
		KindSticker: fmt.Sprintf("%q|%q|%q", st.Model, st.APIBaseURL, m.Sticker.Prompt),
	}
}

// ChangedKinds returns the media kinds whose fingerprint differs between
// two Fingerprints results. An empty old (first load) changes nothing.
func ChangedKinds(old, cur map[string]string) []string {
	if len(old) == 0 {
		return nil
	}
	var out []string
	for _, k := range []string{KindImage, KindAudio, KindVideo, KindSticker} {
		if old[k] != cur[k] {
			out = append(out, k)
		}
	}
	return out
}

// WindowKeys returns the cache keys of every media item msgs carry: image
// stable keys, audio / audio-describe / video keys by attachment ID, and
// sticker vision / info keys by sticker ID. Pin them so what the prompt
// window shows stays cached. Nil messages are skipped.
func WindowKeys(msgs ...*types.DiscordMessage) []string {
	var keys []string
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		for _, u := range msg.ImageURLs {
			keys = append(keys, ImageCacheKey(u))
		}
		for _, a := range msg.Media {
			if a.ID == "" {
				continue
			}
			switch {
			case a.IsAudio():
				keys = append(keys, keyAudio+a.ID, keyAudioDescribe+a.ID)
			case a.Kind == types.MediaVideo:
				keys = append(keys, keyVideo+a.ID)
			}
		}
		for _, s := range msg.Stickers {
			if s.ID != "" {
				keys = append(keys, keyStickerVision+s.ID, keyStickerInfo+s.ID)
			}
		}
	}
	return keys
}
