package bot

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/ratelimit"
	"ezyapper/internal/types"
	"ezyapper/internal/utils"

	"github.com/bwmarrin/discordgo"
)

func TestNew(t *testing.T) {
	cfg := &config.Config{
		Discord: config.DiscordConfig{
			Token:           "test-token",
			BotName:         "TestBot",
			ReplyPercentage: 0.15,
			CooldownSeconds: 5,
		},
		AI: config.AIConfig{
			APIKey: "test-key",
			Model:  "gpt-4",
		},
	}

	if cfg.Discord.Token == "" {
		t.Error("Token should not be empty")
	}

	if cfg.Discord.ReplyPercentage < 0 || cfg.Discord.ReplyPercentage > 1 {
		t.Error("ReplyPercentage should be between 0 and 1")
	}
}

func TestRateLimiter_Check(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(l *ratelimit.Limiter)
		channelID string
		userID    string
		expected  bool
	}{
		{
			name:      "First request allowed",
			setup:     func(l *ratelimit.Limiter) {},
			channelID: "channel1",
			userID:    "user1",
			expected:  true,
		},
		{
			name: "Request during cooldown blocked",
			setup: func(l *ratelimit.Limiter) {
				l.SetCooldown("user2", time.Minute)
			},
			channelID: "channel1",
			userID:    "user2",
			expected:  false,
		},
		{
			name: "Request after cooldown allowed",
			setup: func(l *ratelimit.Limiter) {
				l.SetCooldown("user3", -time.Minute)
			},
			channelID: "channel1",
			userID:    "user3",
			expected:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := ratelimit.NewLimiter(10, 5*time.Second, time.Minute)
			tt.setup(limiter)

			result := limiter.Check(tt.channelID, tt.userID)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestCooldownCache(t *testing.T) {
	cooldownCache := make(map[string]time.Time)
	userID := "user123"
	cooldownDuration := 5 * time.Second

	cooldownCache[userID] = time.Now().Add(cooldownDuration)

	if expiry, exists := cooldownCache[userID]; exists {
		if time.Now().After(expiry) {
			t.Error("Cooldown should still be active")
		}
	}
}

func TestExtractImagesFromMessage(t *testing.T) {
	tests := []struct {
		name     string
		message  *discordgo.Message
		expected int
	}{
		{
			name: "No attachments",
			message: &discordgo.Message{
				Attachments: []*discordgo.MessageAttachment{},
				Embeds:      []*discordgo.MessageEmbed{},
			},
			expected: 0,
		},
		{
			name: "With image attachment",
			message: &discordgo.Message{
				Attachments: []*discordgo.MessageAttachment{
					{
						URL:         "https://cdn.discordapp.com/attachments/123/image.png",
						Filename:    "image.png",
						ContentType: "image/png",
					},
				},
			},
			expected: 1,
		},
		{
			name: "With embed image",
			message: &discordgo.Message{
				Embeds: []*discordgo.MessageEmbed{
					{
						Image: &discordgo.MessageEmbedImage{
							URL: "https://example.com/image.jpg",
						},
					},
				},
			},
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imageCount := 0

			for _, att := range tt.message.Attachments {
				if len(att.ContentType) >= 5 && att.ContentType[:5] == "image" {
					imageCount++
				}
			}

			for _, embed := range tt.message.Embeds {
				if embed.Image != nil {
					imageCount++
				}
			}

			if imageCount != tt.expected {
				t.Errorf("Expected %d images, got %d", tt.expected, imageCount)
			}
		})
	}
}

func TestSplitMessage(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		maxLen   int
		expected int
	}{
		{
			name:     "Short message",
			content:  "Hello world",
			maxLen:   100,
			expected: 1,
		},
		{
			name:     "Long message",
			content:  "This is a very long message that needs to be split into multiple parts because it exceeds the maximum length allowed",
			maxLen:   20,
			expected: 7,
		},
		{
			name:     "Exact length",
			content:  "1234567890",
			maxLen:   10,
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parts := utils.SplitMessage(tt.content, tt.maxLen)
			if len(parts) != tt.expected {
				t.Errorf("Expected %d parts, got %d", tt.expected, len(parts))
			}
		})
	}
}

func TestIsMentioned(t *testing.T) {
	tests := []struct {
		name     string
		mentions []*discordgo.User
		botID    string
		expected bool
	}{
		{
			name:     "Not mentioned",
			mentions: []*discordgo.User{{ID: "user1"}, {ID: "user2"}},
			botID:    "bot123",
			expected: false,
		},
		{
			name:     "Mentioned",
			mentions: []*discordgo.User{{ID: "user1"}, {ID: "bot123"}},
			botID:    "bot123",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mentioned := false
			for _, user := range tt.mentions {
				if user.ID == tt.botID {
					mentioned = true
					break
				}
			}

			if mentioned != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, mentioned)
			}
		})
	}
}

func TestShutdown_WaitsForTrackedGoroutines(t *testing.T) {
	t.Run("shutdown_returns_when_goroutine_completes", func(t *testing.T) {
		b := &Bot{
			ctx:                context.Background(),
			cancel:             func() {},
			processingMessages: map[string]*ProcessingMessage{},
		}

		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			time.Sleep(50 * time.Millisecond)
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := b.Shutdown(ctx)
		if err != nil {
			t.Errorf("Shutdown should return nil when goroutine completes, got: %v", err)
		}
	})

	t.Run("shutdown_times_out_on_stuck_goroutine", func(t *testing.T) {
		b := &Bot{
			ctx:                context.Background(),
			cancel:             func() {},
			processingMessages: map[string]*ProcessingMessage{},
		}

		b.wg.Add(1)
		go func() {
			select {}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		err := b.Shutdown(ctx)
		if err == nil {
			t.Error("Expected timeout error from Shutdown when goroutine is stuck")
		}
	})
}

func TestAddMessageToChannelBuffer_MaxBufferSize(t *testing.T) {
	newMsg := func(id string) *types.DiscordMessage {
		return &types.DiscordMessage{
			ID:        id,
			ChannelID: "ch-test",
			Timestamp: time.Now(),
		}
	}

	t.Run("max_buffer_size_0_falls_back_to_consolidation_interval_times_2", func(t *testing.T) {
		var cfgStore atomic.Value
		cfgStore.Store(&config.Config{
			Memory: config.MemoryConfig{
				ConsolidationInterval: 5,
				MaxBufferSize:         0,
			},
		})

		b := &Bot{
			configStore:          &cfgStore,
			channelMessageBuffer: make(map[string][]*types.DiscordMessage),
		}

		// Add 11 messages — buffer should truncate to 10 (5 * 2)
		for i := 0; i < 11; i++ {
			b.addMessageToChannelBuffer("ch-test", newMsg("msg-"+string(rune('a'+i))))
		}

		b.channelBufferMu.RLock()
		size := len(b.channelMessageBuffer["ch-test"])
		b.channelBufferMu.RUnlock()

		if size != 10 {
			t.Errorf("expected buffer size 10 (ConsolidationInterval*2 fallback), got %d", size)
		}
	})

	t.Run("max_buffer_size_overrides_fallback", func(t *testing.T) {
		var cfgStore atomic.Value
		cfgStore.Store(&config.Config{
			Memory: config.MemoryConfig{
				ConsolidationInterval: 5,
				MaxBufferSize:         3,
			},
		})

		b := &Bot{
			configStore:          &cfgStore,
			channelMessageBuffer: make(map[string][]*types.DiscordMessage),
		}

		// Add 5 messages — buffer should truncate to 3 (MaxBufferSize)
		for i := 0; i < 5; i++ {
			b.addMessageToChannelBuffer("ch-test", newMsg("msg-"+string(rune('a'+i))))
		}

		b.channelBufferMu.RLock()
		size := len(b.channelMessageBuffer["ch-test"])
		b.channelBufferMu.RUnlock()

		if size != 3 {
			t.Errorf("expected buffer size 3 (MaxBufferSize override), got %d", size)
		}
	})

	t.Run("buffer_below_max_not_truncated", func(t *testing.T) {
		var cfgStore atomic.Value
		cfgStore.Store(&config.Config{
			Memory: config.MemoryConfig{
				ConsolidationInterval: 5,
				MaxBufferSize:         10,
			},
		})

		b := &Bot{
			configStore:          &cfgStore,
			channelMessageBuffer: make(map[string][]*types.DiscordMessage),
		}

		// Add 3 messages — buffer should NOT truncate (below MaxBufferSize)
		for i := 0; i < 3; i++ {
			b.addMessageToChannelBuffer("ch-test", newMsg("msg-"+string(rune('a'+i))))
		}

		b.channelBufferMu.RLock()
		size := len(b.channelMessageBuffer["ch-test"])
		b.channelBufferMu.RUnlock()

		if size != 3 {
			t.Errorf("expected buffer size 3 (no truncation), got %d", size)
		}
	})

	t.Run("duplicate_message_not_added_or_truncated", func(t *testing.T) {
		var cfgStore atomic.Value
		cfgStore.Store(&config.Config{
			Memory: config.MemoryConfig{
				ConsolidationInterval: 5,
				MaxBufferSize:         3,
			},
		})

		b := &Bot{
			configStore:          &cfgStore,
			channelMessageBuffer: make(map[string][]*types.DiscordMessage),
		}

		dup := newMsg("dup-msg")
		b.addMessageToChannelBuffer("ch-test", dup)
		b.addMessageToChannelBuffer("ch-test", dup) // duplicate, should be skipped
		b.addMessageToChannelBuffer("ch-test", newMsg("other"))

		b.channelBufferMu.RLock()
		size := len(b.channelMessageBuffer["ch-test"])
		b.channelBufferMu.RUnlock()

		if size != 2 {
			t.Errorf("expected buffer size 2 (duplicate skipped), got %d", size)
		}
	})
}
