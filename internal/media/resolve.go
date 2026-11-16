package media

import (
	"context"
	"strings"

	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/types"
)

// scope is what a Policy resolves for.
type scope int

const (
	scopeCurrent scope = iota + 1
	scopeHistory
	scopeMemory
)

// Policy decides which missing results Resolve may compute (a model or ASR
// call). Cached results are always used, whatever the policy.
type Policy struct {
	scope scope
	// History: the message the user replied to, and the media kinds the
	// user's text mentions.
	replyToID string
	asked     map[string]bool
}

// CurrentPolicy is for the message being answered: every missing result is
// computed.
func CurrentPolicy() Policy { return Policy{scope: scopeCurrent} }

// MemoryPolicy is for memory consolidation: missing image, audio and video
// results are computed (images with the consolidation.vision model);
// stickers use a cached vision description or their Discord text.
func MemoryPolicy() Policy { return Policy{scope: scopeMemory} }

// mediaKeywords are the substrings of the user's text that ask about a
// media kind in history ("what's in that image?", "listen to the voice
// message").
var mediaKeywords = map[string][]string{
	KindImage: {"image", "img", "photo", "picture", "pic", "screenshot"},
	KindAudio: {"audio", "voice", "listen", "recording", "song"},
	KindVideo: {"video", "clip"},
}

// HistoryPolicy is for history messages. Per media kind (image, audio,
// video), only the most recent message carrying that kind may be processed,
// and only when userText mentions the kind or replyToID is that message.
// Everything else uses cached results. Stickers are never computed in
// history (their Discord text is used instead). The messages passed to
// Resolve must be newest first, as fetched.
func HistoryPolicy(userText, replyToID string) Policy {
	text := strings.ToLower(strings.TrimSpace(userText))
	asked := map[string]bool{}
	for kind, words := range mediaKeywords {
		for _, w := range words {
			if text != "" && strings.Contains(text, w) {
				asked[kind] = true
				break
			}
		}
	}
	return Policy{scope: scopeHistory, replyToID: replyToID, asked: asked}
}

// compute reports whether a missing result of kind may be computed for the
// message msgID; mostRecent says it is the newest message with that kind.
func (pol Policy) compute(kind, msgID string, mostRecent bool) bool {
	switch pol.scope {
	case scopeCurrent:
		return true
	case scopeMemory:
		return kind != KindSticker
	case scopeHistory:
		if !mostRecent || kind == KindSticker {
			return false
		}
		return pol.asked[kind] || (pol.replyToID != "" && pol.replyToID == msgID)
	}
	return false
}

// SetSelf tells the processor how to find the bot's own user ID. Messages
// the bot wrote are never resolved: their text is the source, and media the
// bot attached itself (a TTS clip, a generated image) must not come back to
// it as something a user sent.
func (p *Processor) SetSelf(selfID func() string) {
	if p != nil {
		p.selfID = selfID
	}
}

func (p *Processor) isSelf(msg *types.DiscordMessage) bool {
	if p.selfID == nil {
		return false
	}
	id := p.selfID()
	return id != "" && msg.AuthorID == id
}

// Resolve returns copies of msgs with ResolvedMedia filled: images,
// stickers, audio, voice messages and videos turned into text, from the
// cache or, where pol allows, by the modality's model. Content is never
// changed; render the result with types.RenderAttachments. Media of a
// disabled modality and items beyond its max_items are left out. Messages
// written by the bot itself get no media (see SetSelf). Nil messages stay
// nil. A nil processor returns copies without media.
func (p *Processor) Resolve(ctx context.Context, msgs []*types.DiscordMessage, pol Policy) []*types.DiscordMessage {
	out := make([]*types.DiscordMessage, len(msgs))
	var recent map[string]int
	if p != nil && pol.scope == scopeHistory {
		recent = p.mostRecent(msgs)
	}
	for i, msg := range msgs {
		if msg == nil {
			continue
		}
		cp := *msg
		cp.ResolvedMedia = nil
		if p != nil && !p.isSelf(msg) {
			allow := func(kind string) bool {
				idx, ok := recent[kind]
				return pol.compute(kind, msg.ID, ok && idx == i)
			}
			cp.ResolvedMedia = p.resolveMessage(ctx, msg, allow, pol.scope == scopeMemory)
		}
		out[i] = &cp
	}
	return out
}

// mostRecent returns, per media kind, the index of the first (newest)
// message carrying it. The bot's own messages are skipped: they are never
// resolved.
func (p *Processor) mostRecent(msgs []*types.DiscordMessage) map[string]int {
	recent := map[string]int{}
	mark := func(kind string, i int) {
		if _, ok := recent[kind]; !ok {
			recent[kind] = i
		}
	}
	for i, msg := range msgs {
		if msg == nil || p.isSelf(msg) {
			continue
		}
		if len(msg.ImageURLs) > 0 {
			mark(KindImage, i)
		}
		for _, a := range msg.Media {
			switch {
			case a.IsAudio():
				mark(KindAudio, i)
			case a.Kind == types.MediaVideo:
				mark(KindVideo, i)
			}
		}
	}
	return recent
}

func (p *Processor) resolveMessage(ctx context.Context, msg *types.DiscordMessage, allow func(kind string) bool, memory bool) []types.ResolvedMedia {
	var out []types.ResolvedMedia
	if img := p.mods.Image; img.Enabled {
		for i, u := range msg.ImageURLs {
			if img.MaxItems > 0 && i >= img.MaxItems {
				break
			}
			it := types.ResolvedMedia{Kind: types.ResolvedImage, TextKind: types.TextDescription}
			it.Text, it.Status = p.result(ImageCacheKey(u), allow(KindImage), func() (string, error) {
				return p.describeImage(ctx, u, memory)
			}, "image", msg.ID)
			out = append(out, it)
		}
	}
	for i, s := range msg.Stickers {
		if st := p.mods.Sticker; st.MaxItems > 0 && i >= st.MaxItems {
			break
		}
		out = append(out, p.resolveSticker(ctx, s, allow(KindSticker)))
	}
	audioN, videoN := 0, 0
	au, vi := p.mods.Audio, p.mods.Video
	for _, a := range msg.Media {
		switch {
		case a.IsAudio() && au.Enabled:
			audioN++
			if au.MaxItems > 0 && audioN > au.MaxItems {
				continue
			}
			it := types.ResolvedMedia{Kind: a.Kind, Label: audioLabel(a), TextKind: types.TextTranscript}
			key, run := keyAudio+a.ID, p.Transcribe
			if au.Mode == config.AudioModeDescribe {
				key, run = keyAudioDescribe+a.ID, p.DescribeAudio
				it.TextKind = types.TextDescription
			}
			it.Text, it.Status = p.result(key, allow(KindAudio), func() (string, error) { return run(ctx, a) }, "audio", a.ID)
			out = append(out, it)
		case a.Kind == types.MediaVideo && vi.Enabled:
			videoN++
			if vi.MaxItems > 0 && videoN > vi.MaxItems {
				continue
			}
			it := types.ResolvedMedia{Kind: a.Kind, Label: videoLabel(a), TextKind: types.TextDescription}
			it.Text, it.Status = p.result(keyVideo+a.ID, allow(KindVideo), func() (string, error) { return p.DescribeVideo(ctx, a) }, "video", a.ID)
			out = append(out, it)
		}
	}
	return out
}

// result returns the cached text of key, or computes it with run when
// compute is true. Without either it is pending.
func (p *Processor) result(key string, compute bool, run func() (string, error), what, id string) (string, types.MediaStatus) {
	if v, ok := p.cache.Get(key); ok {
		return oneLine(v), types.MediaOK
	}
	if !compute {
		return "", types.MediaPending
	}
	v, err := run()
	if err != nil {
		logger.Warnf("[media] %s %s not processed: %v", what, id, err)
		return "", types.MediaFailed
	}
	return oneLine(v), types.MediaOK
}

// describeImage describes one image, cached by ImageCacheKey. Memory
// consolidation uses the consolidation.vision model when it is configured.
func (p *Processor) describeImage(ctx context.Context, u string, memory bool) (string, error) {
	client, prompt := p.imageClient, p.mods.Image.Prompt
	if memory && p.memoryImageClient != nil {
		client, prompt = p.memoryImageClient, p.memoryImagePrompt
	}
	return p.cache.Do(ImageCacheKey(u), func() (string, error) {
		part, err := p.ImagePart(ctx, u)
		if err != nil {
			return "", err
		}
		return client.DescribeMedia(ctx, prompt, part)
	})
}

// resolveSticker returns a sticker as text: the vision description of its
// image (sticker.enabled; cached, computed only when compute is true) or
// else the name, description and tags from Discord. Lottie stickers cannot
// be rendered and always use the text.
func (p *Processor) resolveSticker(ctx context.Context, s types.StickerRef, compute bool) types.ResolvedMedia {
	info := p.stickerInfo(ctx, s)
	it := types.ResolvedMedia{Kind: types.ResolvedSticker, Label: info.Name, TextKind: types.TextDescription, Status: types.MediaOK}
	if p.mods.Sticker.Enabled && s.FormatType != types.StickerLottie {
		desc, ok := p.stickerCache.Get(keyStickerVision + s.ID)
		if !ok && compute {
			var err error
			if desc, err = p.describeSticker(ctx, s); err != nil {
				logger.Warnf("[media] sticker %s not described, using its text: %v", s.ID, err)
			} else {
				ok = true
			}
		}
		if ok {
			it.Text = oneLine(desc)
			return it
		}
	}
	it.Text = stickerText(info)
	return it
}

// stickerText is a sticker's Discord description and tags.
func stickerText(info StickerInfo) string {
	text := oneLine(info.Description)
	if t := strings.TrimSpace(info.Tags); t != "" && !strings.EqualFold(t, info.Name) {
		text = strings.TrimSpace(text + " (tags: " + t + ")")
	}
	return text
}
