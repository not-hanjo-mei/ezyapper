// Package bot provides Discord bot event handlers
package bot

import (
	"context"
	"runtime/debug"

	"ezyapper/internal/logger"
)

// triggerChannelConsolidation triggers memory consolidation for a channel if needed
func (b *Bot) triggerChannelConsolidation(ctx context.Context, channelID string, count int) {
	if !b.cfg().Memory.Consolidation.Enabled {
		logger.Debugf("[consolidation] skipped for channel=%s: consolidation is disabled", channelID)
		return
	}

	if count < b.cfg().Memory.ConsolidationInterval {
		logger.Debugf("[consolidation] not triggering for channel=%s: count=%d < interval=%d", channelID, count, b.cfg().Memory.ConsolidationInterval)
		return
	}

	if !b.tryStartChannelConsolidation(channelID) {
		logger.Debugf("[consolidation] already running for channel=%s, skipping duplicate trigger", channelID)
		return
	}

	logger.Infof("[consolidation] triggering for channel=%s: count=%d >= interval=%d", channelID, count, b.cfg().Memory.ConsolidationInterval)

	triggerCount := count
	if triggerCount < b.cfg().Memory.ConsolidationInterval {
		triggerCount = b.cfg().Memory.ConsolidationInterval
	}

	go func(consumedCount int) {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("[consolidation] panic recovered: %v\n%s", r, debug.Stack())
			}
		}()

		defer func() {
			remaining := b.consolidation.ConsumeChannelMessageCount(channelID, consumedCount)
			b.finishChannelConsolidation(channelID)
			if remaining >= b.cfg().Memory.ConsolidationInterval {
				b.triggerChannelConsolidation(b.ctx, channelID, remaining)
			}
		}()

		consolidationCtx, cancel := context.WithTimeout(b.ctx, consolidationTimeout)
		defer cancel()

		channelMessages := b.getAndClearChannelMessageBuffer(channelID)
		if len(channelMessages) == 0 {
			logger.Infof("[consolidation] buffer empty for channel=%s, fetching from Discord", channelID)
			fetchedMessages, err := b.discordClient.FetchChannelMessages(consolidationCtx, channelID, b.cfg().Memory.Consolidation.MaxMessages)
			if err != nil {
				logger.Warnf("[consolidation] failed to fetch messages from Discord for channel=%s: %v", channelID, err)
				return
			}
			channelMessages = fetchedMessages
		}

		if len(channelMessages) == 0 {
			logger.Warnf("[consolidation] no messages available for channel=%s", channelID)
			return
		}

		logger.Infof("[consolidation] starting batch for channel=%s with %d messages", channelID, len(channelMessages))
		if err := b.consolidation.ConsolidateChannel(consolidationCtx, channelID, channelMessages); err != nil {
			logger.Errorf("[consolidation] failed for channel=%s: %v", channelID, err)
		} else {
			logger.Infof("[consolidation] completed successfully for channel=%s", channelID)
		}
	}(triggerCount)
}
