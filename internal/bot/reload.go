package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"ezyapper/internal/ai/decision"
	"ezyapper/internal/ai/mcp"
	"ezyapper/internal/config"
	"ezyapper/internal/hotreload"
	"ezyapper/internal/logger"
	"ezyapper/internal/media"

	"github.com/bwmarrin/discordgo"
)

// mcpCloseGrace is how long a replaced MCP manager stays connected so tool
// calls that already started on it can finish.
const mcpCloseGrace = 30 * time.Second

// Name implements hotreload.Component.
func (b *Bot) Name() string { return "discord bot" }

// Prepare implements hotreload.Component. Everything that can fail is done
// here, before the new config is published:
//
//   - core.discord.token: the token is checked against the Discord API and
//     a new gateway session is opened with the bot's handlers attached. The
//     old session keeps serving until Commit swaps them.
//   - operations.mcp.*: a new MCP manager connects to every configured
//     server and lists its tools; any server failing rejects the reload.
//   - core.decision.*: the decision provider (llm or system_one) is rebuilt.
//   - core.ai.modalities.*: an option that needs ffmpeg is checked, so a
//     missing ffmpeg rejects the reload instead of failing on a message.
//
// The chat model, the media processor, rate limits, prompts, access lists and filters
// are read from the config on every message (or rebuilt in Commit without
// any failure mode), so they need nothing prepared.
func (b *Bot) Prepare(ctx context.Context, old, next *config.Config, changes hotreload.Changes) (hotreload.Pending, error) {
	if !changes.Any("core", "operations.mcp", "memory_pipeline.memory.max_paginated_limit") {
		return nil, nil
	}
	r := &botReload{b: b, old: old, next: next}

	if changes.Any("core.ai.modalities") {
		if _, err := media.CheckFFmpeg(ctx, next.AI.Modalities); err != nil {
			return nil, fmt.Errorf("core.ai.modalities: %w", err)
		}
	}

	if changes.Any("core.discord.token") {
		s, err := b.openSession(ctx, next.Discord.Token)
		if err != nil {
			return nil, fmt.Errorf("core.discord.token: %w", err)
		}
		r.session = s
	}

	if changes.Any("operations.mcp") {
		m, mcpTools, err := PrepareMCP(ctx, next.MCP)
		if err != nil {
			r.Abort()
			return nil, fmt.Errorf("operations.mcp: %w", err)
		}
		r.mcpChanged, r.mcp, r.mcpTools = true, m, mcpTools
	}

	if changes.Any("core.decision") {
		r.deciderChanged = true
		if next.Decision.Enabled {
			d, err := decision.New(&next.Decision)
			if err != nil {
				r.Abort()
				return nil, fmt.Errorf("core.decision: %w", err)
			}
			r.decider = d
		}
	}
	return r, nil
}

// botReload is the bot's prepared change.
type botReload struct {
	b         *Bot
	old, next *config.Config

	session *discordgo.Session

	mcpChanged bool
	mcp        *mcp.MCPManager // nil when MCP is disabled in next
	mcpTools   []mcp.MCPTool

	deciderChanged bool
	decider        decision.Decider
}

func (r *botReload) Commit() {
	b := r.b
	if r.session != nil {
		b.swapSession(r.session)
	}
	if r.mcpChanged {
		b.swapMCP(r.mcp, r.mcpTools, r.next.MCP)
	}
	if r.deciderChanged {
		b.mu.Lock()
		b.decider = r.decider
		b.mu.Unlock()
		if r.decider != nil {
			logger.Infof("[bot] decision provider %q reloaded", r.next.Decision.EffectiveProvider())
		} else {
			logger.Info("[bot] decision provider disabled")
		}
	}
	if b.discordClient != nil {
		b.discordClient.SetMaxPaginatedLimit(r.next.Memory.MaxPaginatedLimit)
	}
	// Rate limiter, caches and the media processor are rebuilt from the
	// published config; ffmpeg was checked in Prepare.
	if err := b.ApplyRuntimeConfig(); err != nil {
		logger.Errorf("[bot] applying runtime config: %v", err)
	}
	// A new session registers /clear-context itself on Ready; otherwise
	// register it when it was just switched on. Switching it off leaves the
	// global command registered; the handler then refuses it.
	if r.session == nil && r.next.Discord.ClearCommand.Enabled && !r.old.Discord.ClearCommand.Enabled {
		b.registerClearCommand(b.sess())
	}
}

func (r *botReload) Abort() {
	if r.session != nil {
		_ = r.session.Close()
	}
	if r.mcp != nil {
		_ = r.mcp.Close()
	}
}

// ValidateToken checks a bot token against the Discord API and returns the
// bot user it belongs to.
func ValidateToken(ctx context.Context, token string) (*discordgo.User, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("token is empty")
	}
	s, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}
	u, err := s.User("@me", discordgo.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("token rejected by Discord: %w", err)
	}
	return u, nil
}

// openSession validates token, then opens a new gateway session for it with
// the bot's handlers attached. Events from it are ignored until it becomes
// the current session (see registerHandlers).
func (b *Bot) openSession(ctx context.Context, token string) (*discordgo.Session, error) {
	if _, err := ValidateToken(ctx, token); err != nil {
		return nil, err
	}
	s, err := newDiscordSession(token)
	if err != nil {
		return nil, err
	}
	b.registerHandlers(s)
	if err := s.Open(); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("open Discord gateway: %w", err)
	}
	return s, nil
}

// swapSession makes s the current session and closes the previous one's
// gateway connection. In-flight replies that hold the old session keep
// working: its REST client stays usable after the gateway closes.
func (b *Bot) swapSession(s *discordgo.Session) {
	b.reconnectMu.Lock()
	old := b.session.Swap(s)
	b.reconnectMu.Unlock()
	if s.State != nil && s.State.User != nil {
		logger.Infof("[bot] Discord reconnected as %s (%s)", s.State.User.Username, s.State.User.ID)
	}
	if old != nil && old != s {
		go func() {
			if err := old.Close(); err != nil {
				logger.Warnf("[bot] closing the previous Discord session: %v", err)
			}
		}()
	}
}

// ReconnectDiscord switches the bot to token in place: the new session is
// opened (and the token validated) first, so a bad token leaves the bot
// connected with the old one.
func (b *Bot) ReconnectDiscord(ctx context.Context, token string) error {
	s, err := b.openSession(ctx, token)
	if err != nil {
		return err
	}
	b.swapSession(s)
	return nil
}

// PrepareMCP connects a new MCP manager for cfg and lists its tools. It
// returns a nil manager when MCP is disabled. Any server failing to
// connect is an error (the partially connected manager is closed): a hot
// reload must not silently drop tools the old config provided.
func PrepareMCP(ctx context.Context, cfg config.MCPConfig) (*mcp.MCPManager, []mcp.MCPTool, error) {
	if !cfg.Enabled || len(cfg.Servers) == 0 {
		return nil, nil, nil
	}
	m := mcp.NewMCPManager(cfg.Servers)
	// The sessions must outlive this reload's context (stdio servers and
	// streams may be tied to the context they were connected with), so
	// connect on a detached context and only bound the wait by ctx.
	done := make(chan error, 1)
	go func() { done <- m.Connect(context.WithoutCancel(ctx)) }()
	select {
	case err := <-done:
		if err != nil {
			_ = m.Close()
			return nil, nil, err
		}
	case <-ctx.Done():
		go func() { <-done; _ = m.Close() }()
		return nil, nil, fmt.Errorf("connecting MCP servers: %w", ctx.Err())
	}
	mcpTools, err := m.GetAllTools(ctx)
	if err != nil {
		_ = m.Close()
		return nil, nil, fmt.Errorf("list MCP tools: %w", err)
	}
	return m, mcpTools, nil
}

// swapMCP installs m (nil = MCP off) and its tools, and closes the previous
// manager after a grace period.
func (b *Bot) swapMCP(m *mcp.MCPManager, mcpTools []mcp.MCPTool, cfg config.MCPConfig) {
	installed := m
	if installed == nil {
		// Keep a non-nil, unconnected manager so Stop and Start keep
		// their nil-free assumptions.
		installed = mcp.NewMCPManager(cfg.Servers)
	}
	b.mu.Lock()
	old := b.mcpManager
	b.mcpManager = installed
	b.mu.Unlock()

	b.installMCPTools(m, mcpTools)
	if old != nil && old != installed {
		time.AfterFunc(mcpCloseGrace, func() {
			if err := old.Close(); err != nil {
				logger.Warnf("[bot] closing the previous MCP manager: %v", err)
			}
		})
	}
}
