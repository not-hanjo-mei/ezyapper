package bot

import (
	"context"
	"errors"

	"ezyapper/internal/logger"

	"github.com/bwmarrin/discordgo"
)

func shouldSendGenerationFallback(err error) bool {
	if err == nil {
		return false
	}

	return errors.Is(err, context.DeadlineExceeded)
}

func (b *Bot) addGenerationFailureReaction(s *discordgo.Session, m *discordgo.MessageCreate) {
	if s == nil || m == nil {
		return
	}

	const timeoutReaction = "💀"
	if err := s.MessageReactionAdd(m.ChannelID, m.ID, timeoutReaction); err != nil {
		logger.Warnf("[context] Failed to add generation timeout reaction: %v", err)
	}
}
