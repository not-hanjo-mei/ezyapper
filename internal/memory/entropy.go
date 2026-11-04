package memory

import (
	"ezyapper/internal/config"
	"regexp"
	"strings"
)

// EntropyGateConfig holds thresholds for the entropy gate.
type EntropyGateConfig struct {
	OtherBotPolicy     config.OtherBotPolicy
	MinUniqueWordRatio float64
}

// PassesEntropyGate returns true if the message has sufficient information content
// to be worth processing for memory extraction.
// Pure heuristic — NO LLM call.
func PassesEntropyGate(msg *DiscordMessage, cfg EntropyGateConfig) bool {
	if msg == nil {
		return false
	}
	if msg.IsBot && cfg.OtherBotPolicy == config.OtherBotIgnore {
		return false
	}

	// Images and processed audio/video carry information even when the
	// message has no text (a voice message, a photo); the gate only judges
	// the text.
	if hasMediaContent(msg) {
		return true
	}

	content := strings.TrimSpace(msg.Content)

	// Check if purely emoji/reaction
	if isPurelyEmoji(content) {
		return false
	}

	// Compute unique word ratio
	words := tokenize(strings.ToLower(content))
	if len(words) == 0 {
		return false
	}

	unique := make(map[string]struct{})
	for _, w := range words {
		unique[w] = struct{}{}
	}
	ratio := float64(len(unique)) / float64(len(words))
	if ratio < cfg.MinUniqueWordRatio {
		return false
	}

	return true
}

var emojiPattern = regexp.MustCompile(`^[\p{So}\p{Sk}\p{Sm}\s\!\.\?\<\>\:\;\[\]\(\)\@\#\$\%\^\&\*\+\=\-\~\'\"\/\\\|\,\_\{\}\x60]+$`)

func isPurelyEmoji(s string) bool {
	return emojiPattern.MatchString(s)
}

// hasMediaContent reports whether msg carries an image or an audio/video
// attachment that was turned into text.
func hasMediaContent(msg *DiscordMessage) bool {
	if len(msg.ImageURLs) > 0 {
		return true
	}
	for _, a := range msg.Media {
		if strings.TrimSpace(a.Text) != "" {
			return true
		}
	}
	return false
}
