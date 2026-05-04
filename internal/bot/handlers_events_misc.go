package bot

import (
	"context"

	"ezyapper/internal/config"
	"ezyapper/internal/logger"

	"github.com/bwmarrin/discordgo"
)

// onMessageUpdate handles message edit events.
func (b *Bot) onMessageUpdate(s *discordgo.Session, m *discordgo.MessageUpdate) {
	if m == nil || m.Message == nil {
		logger.Warnf("[edit] received nil MESSAGE_UPDATE payload, skipping")
		return
	}

	if s == nil || s.State == nil || s.State.User == nil {
		logger.Warnf("[edit] session user state unavailable for message=%s, skipping", m.ID)
		return
	}

	if m.Author == nil {
		logger.Warnf("[edit] MESSAGE_UPDATE missing author: id=%s channel=%s guild=%s, skipping", m.ID, m.ChannelID, m.GuildID)
		return
	}

	// Ignore bot's own message edits.
	if m.Author.ID == s.State.User.ID {
		return
	}

	logger.Infof("[message edited] id=%s author=%s new_content=%q", m.ID, m.Author.Username, m.Content)

	// Get the processing message.
	oldPm := b.getProcessingMessage(m.ID)
	if oldPm == nil {
		// Message not being processed, ignore edit.
		logger.Debugf("[edit] Message %s not in processing queue, ignoring", m.ID)
		return
	}

	logger.Infof("[edit] Found processing message %s in phase %d, old_content=%q", m.ID, oldPm.GetPhase(), oldPm.GetContent())

	if !b.handleEditedMessage(oldPm, m.Content) {
		return
	}
	b.removeProcessingMessageIfMatch(m.ID, oldPm)

	// Create new processing message for reprocessing.
	// This ensures clean state and avoids race conditions with old goroutine.
	ctx := b.ctx
	messageCtx, cancel := context.WithCancel(ctx)
	newPm := b.registerProcessingMessage(m.ID, m.ChannelID, m.Author.ID, m.Content)
	newPm.CancelFunc = cancel
	newPm.SetPhase(PhaseDeciding)

	// Re-evaluate if we should respond.
	// Create a temporary MessageCreate from MessageUpdate.
	// Make sure to use the edited content.
	tempMsg := &discordgo.MessageCreate{
		Message: m.Message,
	}
	// Explicitly ensure Content is set from the edited message.
	if tempMsg.Message != nil {
		tempMsg.Message.Content = m.Content
	}

	logger.Infof("[reprocess] Re-evaluating message %s with content=%q", m.ID, tempMsg.Content)
	shouldRespond, reason := b.ShouldRespond(messageCtx, tempMsg, nil)
	logger.Infof("[reprocess] Message %s re-decision: shouldRespond=%v, reason=%s", m.ID, shouldRespond, reason)

	if !shouldRespond {
		b.clearProcessingMessage(newPm, m.ID)
		cancel()
		return
	}

	// Re-process with updated content.
	if b.cfg().AI.Vision.Mode == config.VisionModeTextOnly {
		b.wg.Add(1)
		go b.processMessageWithoutImages(messageCtx, s, tempMsg, newPm, nil)
	} else {
		b.wg.Add(1)
		go b.processMessage(messageCtx, s, tempMsg, newPm, nil)
	}
}

// onMessageDelete handles message deletion events.
// When a message being processed is deleted, cancels the processing context
// to abort AI generation and skip sending the reply.
func (b *Bot) onMessageDelete(s *discordgo.Session, m *discordgo.MessageDelete) {
	if m == nil || m.Message == nil {
		logger.Warnf("[delete] received nil MESSAGE_DELETE payload, skipping")
		return
	}

	if s == nil || s.State == nil || s.State.User == nil {
		logger.Warnf("[delete] session user state unavailable for message=%s, skipping", m.ID)
		return
	}

	// Ignore bot's own deleted messages — they're never in processingMessages.
	if m.Author != nil && m.Author.ID == s.State.User.ID {
		return
	}

	logger.Infof("[message deleted] id=%s channel=%s guild=%s", m.ID, m.ChannelID, m.GuildID)

	pm := b.getProcessingMessage(m.ID)
	if pm == nil {
		return // Not being processed, nothing to cancel.
	}

	if pm.CancelFunc != nil {
		pm.CancelFunc()
		logger.Infof("[delete] cancelled processing for message=%s", m.ID)
	}
	b.removeProcessingMessageIfMatch(m.ID, pm)
}

// onGuildJoin handles joining a new guild.
func (b *Bot) onGuildJoin(s *discordgo.Session, g *discordgo.GuildCreate) {
	logger.Infof("[guild] Joined guild: %s (%d members)", g.Name, g.MemberCount)
}

// onGuildLeave handles leaving a guild.
func (b *Bot) onGuildLeave(s *discordgo.Session, g *discordgo.GuildDelete) {
	logger.Infof("[guild] Left guild: %s", g.ID)
}
