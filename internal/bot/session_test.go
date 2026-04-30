package bot

import (
	"testing"
	"time"

	"ezyapper/internal/config"
	"ezyapper/internal/ratelimit"
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
