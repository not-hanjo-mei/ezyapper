// Package bot provides Discord bot event handlers
package bot

import (
	"context"
	"runtime/debug"
	"time"

	"ezyapper/internal/logger"
)

// triggerChannelConsolidation triggers memory consolidation for a channel if needed
func (b *Bot) triggerChannelConsolidation(ctx context.Context, channelID string, count int) {
	b.triggerChannelConsolidationDepth(ctx, channelID, count, 0)
}

const maxConsolidationChainDepth = 5

func (b *Bot) triggerChannelConsolidationDepth(ctx context.Context, channelID string, count int, depth int) {
	if depth >= maxConsolidationChainDepth {
		logger.Warnf("[consolidation] chain depth limit reached for channel=%s at depth=%d, deferring", channelID, depth)
		return
	}
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

	b.wg.Add(1)
	go func() {
		defer b.wg.Done()

		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("[consolidation] panic recovered: %v\n%s", r, debug.Stack())
			}
		}()

		consolidationCtx, cancel := context.WithTimeout(ctx, time.Duration(b.cfg().Discord.ConsolidationTimeoutSec)*time.Second)
		defer cancel()

		channelMessages := b.getAndClearChannelMessageBuffer(channelID)
		if len(channelMessages) == 0 {
			logger.Infof("[consolidation] buffer empty for channel=%s, fetching from Discord", channelID)
			fetchedMessages, err := b.discordClient.FetchChannelMessages(consolidationCtx, channelID, b.cfg().Memory.ConsolidationInterval)
			if err != nil {
				logger.Warnf("[consolidation] failed to fetch messages from Discord for channel=%s: %v", channelID, err)
				b.finishChannelConsolidation(channelID)
				return
			}
			channelMessages = fetchedMessages
		}

		if len(channelMessages) == 0 {
			logger.Warnf("[consolidation] no messages available for channel=%s", channelID)
			b.finishChannelConsolidation(channelID)
			return
		}

		// Consume counter by actual number of messages processed — not by trigger-time
		// count which may have diverged due to concurrent buffer/counter mutations.
		consumed := len(channelMessages)
		defer func() {
			remaining := b.consolidation.ConsumeChannelMessageCount(channelID, consumed)
			b.finishChannelConsolidation(channelID)
			if remaining >= b.cfg().Memory.ConsolidationInterval {
				b.triggerChannelConsolidationDepth(b.ctx, channelID, remaining, depth+1)
			}
		}()

		logger.Infof("[consolidation] starting batch for channel=%s with %d messages", channelID, len(channelMessages))
		if err := b.consolidation.ConsolidateChannel(consolidationCtx, channelID, channelMessages); err != nil {
			logger.Errorf("[consolidation] failed for channel=%s: %v", channelID, err)
		} else {
			logger.Infof("[consolidation] completed successfully for channel=%s", channelID)
		}
	}()
}
