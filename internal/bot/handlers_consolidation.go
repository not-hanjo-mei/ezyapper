// Package bot provides Discord bot event handlers
package bot

import (
	"context"

	"ezyapper/internal/logger"
)

// triggerChannelConsolidation triggers memory consolidation for a channel if needed
func (b *Bot) triggerChannelConsolidation(ctx context.Context, channelID string, count int) {
	if !b.config.Memory.Consolidation.Enabled {
		logger.Debugf("[consolidation] skipped for channel=%s: consolidation is disabled", channelID)
		return
	}

	if count < b.config.Memory.ConsolidationInterval {
		logger.Debugf("[consolidation] not triggering for channel=%s: count=%d < interval=%d", channelID, count, b.config.Memory.ConsolidationInterval)
		return
	}

	if !b.tryStartChannelConsolidation(channelID) {
		logger.Debugf("[consolidation] already running for channel=%s, skipping duplicate trigger", channelID)
		return
	}

	logger.Infof("[consolidation] triggering for channel=%s: count=%d >= interval=%d", channelID, count, b.config.Memory.ConsolidationInterval)

	triggerCount := count
	if triggerCount < b.config.Memory.ConsolidationInterval {
		triggerCount = b.config.Memory.ConsolidationInterval
	}

	go func(consumedCount int) {
		defer func() {
			remaining := b.memory.ConsumeChannelMessageCount(channelID, consumedCount)
			b.finishChannelConsolidation(channelID)
			if remaining >= b.config.Memory.ConsolidationInterval {
				b.triggerChannelConsolidation(context.Background(), channelID, remaining)
			}
		}()

		consolidationCtx, cancel := context.WithTimeout(context.Background(), consolidationTimeout)
		defer cancel()

		channelMessages := b.getAndClearChannelMessageBuffer(channelID)
		if len(channelMessages) == 0 {
			logger.Infof("[consolidation] buffer empty for channel=%s, fetching from Discord", channelID)
			fetchedMessages, err := b.discordClient.FetchChannelMessages(channelID, b.config.Memory.Consolidation.MaxMessages)
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
		if err := b.memory.ConsolidateChannel(consolidationCtx, channelID, channelMessages); err != nil {
			logger.Errorf("[consolidation] failed for channel=%s: %v", channelID, err)
		} else {
			logger.Infof("[consolidation] completed successfully for channel=%s", channelID)
		}
	}(triggerCount)
}
