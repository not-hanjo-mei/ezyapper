// Package bot provides Discord bot event handlers
package bot

import (
	"context"

	"ezyapper/internal/ai"

	openai "github.com/sashabaranov/go-openai"
)

// createToolHandler creates a tool handler for AI function calling
func (b *Bot) createToolHandler() ai.ToolHandler {
	return func(ctx context.Context, toolCall openai.ToolCall) (string, error) {
		return ai.HandleToolCall(ctx, b.toolRegistry, toolCall)
	}
}
