package media

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"ezyapper/internal/ai"
	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/types"
)

// StickerInfo is what Discord says about a sticker.
type StickerInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Tags        string `json:"tags,omitempty"`
}

// StickerLookup fetches sticker details by ID (state cache, then
// GET /stickers/{id}).
type StickerLookup func(ctx context.Context, id string) (StickerInfo, error)

// StickerURLBase is the Discord media host sticker images are fetched from.
// A variable so tests can point it at a fake server.
var StickerURLBase = "https://media.discordapp.net/stickers/"

// StickerImageURL returns the image URL of a non-Lottie sticker.
func StickerImageURL(s types.StickerRef) string {
	ext := "png" // PNG and APNG
	if s.FormatType == types.StickerGIF {
		ext = "gif"
	}
	return StickerURLBase + s.ID + "." + ext
}

// SetStickers wires the sticker lookup and the long-lived sticker cache.
// Both outlive config reloads, so the bot owns them.
func (p *Processor) SetStickers(lookup StickerLookup, cache *Cache) {
	if p == nil {
		return
	}
	p.stickerLookup = lookup
	p.stickerCache = cache
}

// stickerInfo returns cached details, looking them up once per sticker ID.
// A permanent lookup failure (a deleted or foreign sticker: Discord answers
// 4xx) is cached for FailureTTL so it is not looked up on every turn; a
// transient one is not cached. Either way the sticker's name is used.
func (p *Processor) stickerInfo(ctx context.Context, s types.StickerRef) StickerInfo {
	fallback := StickerInfo{Name: s.Name}
	if p.stickerLookup == nil {
		return fallback
	}
	raw, err := p.stickerCache.Do(keyStickerInfo+s.ID, func() (string, error) {
		got, err := p.stickerLookup(ctx, s.ID)
		if err != nil {
			return "", err
		}
		if got.Name == "" {
			got.Name = s.Name
		}
		b, err := json.Marshal(got)
		if err != nil {
			return "", err
		}
		return string(b), nil
	})
	if err != nil {
		logger.Debugf("[media] sticker %s lookup failed: %v", s.ID, err)
		return fallback
	}
	var info StickerInfo
	if json.Unmarshal([]byte(raw), &info) != nil {
		return fallback
	}
	return info
}

// describeSticker runs vision on the sticker image, cached by sticker ID
// (permanent failures for FailureTTL, see Cache.Do).
func (p *Processor) describeSticker(ctx context.Context, s types.StickerRef) (string, error) {
	if p.stickerClient == nil {
		if v, ok := p.stickerCache.Get(keyStickerVision + s.ID); ok {
			return v, nil
		}
		return "", fmt.Errorf("sticker vision is disabled")
	}
	return p.stickerCache.Do(keyStickerVision+s.ID, func() (string, error) {
		data, ct, err := p.base.FetchMedia(ctx, StickerImageURL(s), p.maxImageBytes, "image/")
		if err != nil {
			return "", err
		}
		if baseContentType(ct) == "" || baseContentType(ct) == "application/octet-stream" {
			ct = "image/png"
			if s.FormatType == types.StickerGIF {
				ct = "image/gif"
			}
		}
		d, err := p.stickerClient.DescribeMedia(ctx, p.mods.Sticker.Prompt, ai.MediaPart{Kind: ai.PartImageURL, URL: ai.DataURI(ct, data)})
		if err != nil {
			return "", err
		}
		return oneLine(d), nil
	})
}

// AnnotateStickers returns "[Sticker: ...]" lines for msg's stickers.
//
// Text mode: name, description and tags from Discord (looked up once per
// sticker ID, for history messages too: it is a cheap Discord API call, not
// a model call). Vision mode: the cached vision description; a missing one
// is only computed for the message being answered (current), history falls
// back to text. Lottie stickers cannot be rendered and always use text.
func (p *Processor) AnnotateStickers(ctx context.Context, msg *types.DiscordMessage, current bool) string {
	if p == nil || msg == nil || !p.mods.Sticker.Enabled || len(msg.Stickers) == 0 {
		return ""
	}
	st := p.mods.Sticker
	var sb strings.Builder
	for i, s := range msg.Stickers {
		if st.MaxItems > 0 && i >= st.MaxItems {
			break
		}
		info := p.stickerInfo(ctx, s)
		if st.Mode == config.StickerModeVision && s.FormatType != types.StickerLottie {
			desc, ok := p.stickerCache.Get(keyStickerVision + s.ID)
			if !ok && current {
				var err error
				desc, err = p.describeSticker(ctx, s)
				if err != nil {
					logger.Warnf("[media] sticker %s not described, using its text: %v", s.ID, err)
				} else {
					ok = true
				}
			}
			if ok {
				fmt.Fprintf(&sb, "\n[Sticker: %s — %s]", info.Name, desc)
				continue
			}
		}
		sb.WriteString("\n" + stickerTextLine(info))
	}
	return sb.String()
}

func stickerTextLine(info StickerInfo) string {
	line := "[Sticker: " + info.Name
	if d := oneLine(info.Description); d != "" {
		line += " — " + d
	}
	if t := strings.TrimSpace(info.Tags); t != "" && !strings.EqualFold(t, info.Name) {
		line += " (tags: " + t + ")"
	}
	return line + "]"
}
