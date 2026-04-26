package memory

import (
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestConvertMessage_WithReply(t *testing.T) {
	client := &ShortTermClient{session: nil}

	now := time.Now().UTC().Truncate(time.Second)

	replyContent := "this is the original message being replied to"
	discordMsg := &discordgo.Message{
		ID:        "msg-100",
		ChannelID: "chan-200",
		GuildID:   "guild-300",
		Author: &discordgo.User{
			ID:       "user-400",
			Username: "testuser",
			Bot:      false,
		},
		Content:   "hello world",
		Timestamp: now,
		MessageReference: &discordgo.MessageReference{
			MessageID: "parent-999",
			ChannelID: "chan-200",
			GuildID:   "guild-300",
		},
		ReferencedMessage: &discordgo.Message{
			ID:      "parent-999",
			Content: replyContent,
			Author: &discordgo.User{
				ID:       "parent-user-555",
				Username: "parentuser",
			},
		},
	}

	result := client.convertMessage(discordMsg)

	if result.ReplyToID != "parent-999" {
		t.Fatalf("ReplyToID: got %q, want %q", result.ReplyToID, "parent-999")
	}
	if result.ReplyToUsername != "parentuser" {
		t.Fatalf("ReplyToUsername: got %q, want %q", result.ReplyToUsername, "parentuser")
	}
	if result.ReplyToContent != replyContent {
		t.Fatalf("ReplyToContent: got %q, want %q", result.ReplyToContent, replyContent)
	}
}

func TestConvertMessage_WithReply_Truncation(t *testing.T) {
	client := &ShortTermClient{session: nil}

	now := time.Now().UTC().Truncate(time.Second)

	longContent := ""
	for i := 0; i < 150; i++ {
		longContent += "x"
	}

	discordMsg := &discordgo.Message{
		ID:        "msg-100",
		ChannelID: "chan-200",
		GuildID:   "guild-300",
		Author: &discordgo.User{
			ID:       "user-400",
			Username: "testuser",
		},
		Content:   "hello",
		Timestamp: now,
		MessageReference: &discordgo.MessageReference{
			MessageID: "parent-999",
		},
		ReferencedMessage: &discordgo.Message{
			Content: longContent,
			Author: &discordgo.User{
				ID:       "parent-user-555",
				Username: "parentuser",
			},
		},
	}

	result := client.convertMessage(discordMsg)

	if len(result.ReplyToContent) != 100 {
		t.Fatalf("ReplyToContent length: got %d, want 100", len(result.ReplyToContent))
	}
	if result.ReplyToContent != longContent[:100] {
		t.Fatalf("ReplyToContent: got %q, want %q", result.ReplyToContent, longContent[:100])
	}
}

func TestConvertMessage_NoReply(t *testing.T) {
	client := &ShortTermClient{session: nil}

	now := time.Now().UTC().Truncate(time.Second)

	discordMsg := &discordgo.Message{
		ID:        "msg-100",
		ChannelID: "chan-200",
		GuildID:   "guild-300",
		Author: &discordgo.User{
			ID:       "user-400",
			Username: "testuser",
			Bot:      false,
		},
		Content:           "hello world",
		Timestamp:         now,
		MessageReference:  nil,
		ReferencedMessage: nil,
	}

	result := client.convertMessage(discordMsg)

	if result.ReplyToID != "" {
		t.Fatalf("ReplyToID: got %q, want empty string", result.ReplyToID)
	}
	if result.ReplyToUsername != "" {
		t.Fatalf("ReplyToUsername: got %q, want empty string", result.ReplyToUsername)
	}
	if result.ReplyToContent != "" {
		t.Fatalf("ReplyToContent: got %q, want empty string", result.ReplyToContent)
	}
}

func TestConvertMessage_ReplyToDeleted(t *testing.T) {
	client := &ShortTermClient{session: nil}

	now := time.Now().UTC().Truncate(time.Second)

	discordMsg := &discordgo.Message{
		ID:        "msg-100",
		ChannelID: "chan-200",
		GuildID:   "guild-300",
		Author: &discordgo.User{
			ID:       "user-400",
			Username: "testuser",
			Bot:      false,
		},
		Content:   "hello world",
		Timestamp: now,
		MessageReference: &discordgo.MessageReference{
			MessageID: "parent-999",
			ChannelID: "chan-200",
			GuildID:   "guild-300",
		},
		ReferencedMessage: nil,
	}

	result := client.convertMessage(discordMsg)

	if result.ReplyToID != "parent-999" {
		t.Fatalf("ReplyToID: got %q, want %q", result.ReplyToID, "parent-999")
	}
	if result.ReplyToUsername != "(deleted message)" {
		t.Fatalf("ReplyToUsername: got %q, want %q", result.ReplyToUsername, "(deleted message)")
	}
	if result.ReplyToContent != "" {
		t.Fatalf("ReplyToContent: got %q, want empty string", result.ReplyToContent)
	}
}
