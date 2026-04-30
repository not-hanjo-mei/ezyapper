package bot

import (
	"context"
	"strings"
	"testing"

	"ezyapper/internal/types"
	"ezyapper/internal/utils"

	"github.com/bwmarrin/discordgo"
)

func TestCollectRecentUsers_Dedup(t *testing.T) {
	b := &Bot{}
	messages := []*types.DiscordMessage{
		{AuthorID: "user1", Username: "Alice"},
		{AuthorID: "user2", Username: "Bob"},
		{AuthorID: "user1", Username: "Alice"},
		{AuthorID: "user3", Username: "Charlie"},
		{AuthorID: "user2", Username: "Bob"},
	}

	users := b.collectRecentUsers(messages)

	if len(users) != 3 {
		t.Errorf("expected 3 unique users, got %d", len(users))
	}

	seen := make(map[string]string)
	for _, u := range users {
		seen[u.ID] = u.Username
	}

	if seen["user1"] != "Alice" {
		t.Errorf("expected user1 -> Alice, got %q", seen["user1"])
	}
	if seen["user2"] != "Bob" {
		t.Errorf("expected user2 -> Bob, got %q", seen["user2"])
	}
	if seen["user3"] != "Charlie" {
		t.Errorf("expected user3 -> Charlie, got %q", seen["user3"])
	}
}

func TestCollectRecentUsers_FromMessages(t *testing.T) {
	b := &Bot{}
	messages := []*types.DiscordMessage{
		{AuthorID: "user1", Username: "Alice", Content: "Hello"},
		{AuthorID: "user2", Username: "Bob", Content: "Hi there"},
		{AuthorID: "user1", Username: "Alice", Content: "How are you?"},
	}

	users := b.collectRecentUsers(messages)

	if len(users) != 2 {
		t.Errorf("expected 2 unique users, got %d", len(users))
	}

	// First occurrence should be kept
	for _, u := range users {
		switch u.ID {
		case "user1":
			if u.Username != "Alice" {
				t.Errorf("expected user1 -> Alice, got %q", u.Username)
			}
		case "user2":
			if u.Username != "Bob" {
				t.Errorf("expected user2 -> Bob, got %q", u.Username)
			}
		default:
			t.Errorf("unexpected user ID: %s", u.ID)
		}
	}
}

func TestBuildConversationHistory_ResolvedMentions(t *testing.T) {
	b := &Bot{}

	messages := []*types.DiscordMessage{
		{
			ID:       "msg1",
			AuthorID: "user1",
			Username: "Alice",
			Content:  "Hey <@user2> and <@!user3>, check <#12345>",
			IsBot:    false,
		},
		{
			ID:       "msg2",
			AuthorID: "user2",
			Username: "Bob",
			Content:  "Sure, in <#67890>?",
			IsBot:    false,
		},
	}

	userMentions := []*discordgo.User{
		{ID: "user2", Username: "Bob"},
		{ID: "user3", Username: "Charlie"},
	}
	channelMappings := []utils.ChannelMapping{
		{ID: "12345", Name: "general"},
		{ID: "67890", Name: "lounge"},
	}

	result := b.buildConversationHistoryText(
		context.Background(),
		messages,
		"",    // currentMsgID empty so no messages skipped
		"",    // botID empty so all treated as User
		false, // no on-demand enrichment
		userMentions,
		channelMappings,
	)

	// User mentions should be resolved
	if !strings.Contains(result, "@Bob") {
		t.Error("expected @Bob in result")
	}
	if !strings.Contains(result, "@Charlie") {
		t.Error("expected @Charlie in result")
	}

	// Channel mentions should be resolved
	if !strings.Contains(result, "#general") {
		t.Error("expected #general in result")
	}
	if !strings.Contains(result, "#lounge") {
		t.Error("expected #lounge in result")
	}

	// Raw mentions should NOT appear
	if strings.Contains(result, "<@user2>") {
		t.Error("raw <@user2> should be resolved to @Bob")
	}
	if strings.Contains(result, "<@!user3>") {
		t.Error("raw <@!user3> should be resolved to @Charlie")
	}
	if strings.Contains(result, "<#12345>") {
		t.Error("raw <#12345> should be resolved to #general")
	}
	if strings.Contains(result, "<#67890>") {
		t.Error("raw <#67890> should be resolved to #lounge")
	}

	// Message format should still be correct
	if !strings.Contains(result, "[User] Alice (ID:user1)") {
		t.Error("expected message format [User] Alice (ID:user1)")
	}
	if !strings.Contains(result, "[User] Bob (ID:user2)") {
		t.Error("expected message format [User] Bob (ID:user2)")
	}

	// Context tags should be present
	if !strings.Contains(result, "<context>") {
		t.Error("expected <context> tag")
	}
	if !strings.Contains(result, "</context>") {
		t.Error("expected </context> tag")
	}
}

func TestBuildConversationHistory_WithReply(t *testing.T) {
	b := &Bot{}

	messages := []*types.DiscordMessage{
		{
			ID:              "msg2",
			AuthorID:        "user2",
			Username:        "Bob",
			Content:         "I agree",
			IsBot:           false,
			ReplyToID:       "msg1",
			ReplyToUsername: "Alice",
		},
		{
			ID:       "msg1",
			AuthorID: "user1",
			Username: "Alice",
			Content:  "Great idea",
			IsBot:    false,
		},
	}

	result := b.buildConversationHistoryText(
		context.Background(),
		messages,
		"", // no current message to skip
		"", // no bot ID
		false,
		nil,
		nil,
	)

	if !strings.Contains(result, "(replying to @Alice)") {
		t.Error("expected reply marker '(replying to @Alice)' for Bob's message")
	}
	if strings.Contains(result, "I agree") && !strings.Contains(result, "I agree (replying to @Alice)") {
		t.Error("expected '(replying to @Alice)' appended to Bob's content")
	}
	if !strings.Contains(result, "[User] Bob (ID:user2)") {
		t.Error("expected base format preserved for Bob")
	}
	if !strings.Contains(result, "[User] Alice (ID:user1)") {
		t.Error("expected base format preserved for Alice")
	}
	// Alice's message has no reply
	if strings.Contains(result, "(replying to") && strings.Contains(result, "Alice") {
		// Only Alice's own output should not contain reply marker
		// Check that the line for Alice doesn't have it
		if strings.Contains(result, "Great idea (replying to") {
			t.Error("Alice's message should not have a reply marker")
		}
	}
}

func TestBuildConversationHistory_WithReplyDeleted(t *testing.T) {
	b := &Bot{}

	messages := []*types.DiscordMessage{
		{
			ID:              "msg2",
			AuthorID:        "user2",
			Username:        "Bob",
			Content:         "What did it say?",
			IsBot:           false,
			ReplyToID:       "msg1",
			ReplyToUsername: "(deleted message)",
		},
	}

	result := b.buildConversationHistoryText(
		context.Background(),
		messages,
		"",
		"",
		false,
		nil,
		nil,
	)

	if !strings.Contains(result, "(replying to deleted message)") {
		t.Error("expected '(replying to deleted message)' for deleted reply")
	}
	if strings.Contains(result, "@") {
		t.Error("deleted message reply should not contain @ symbol")
	}
}

func TestBuildConversationHistory_NoReply(t *testing.T) {
	b := &Bot{}

	messages := []*types.DiscordMessage{
		{
			ID:       "msg1",
			AuthorID: "user1",
			Username: "Alice",
			Content:  "Hello there",
			IsBot:    false,
		},
		{
			ID:       "msg2",
			AuthorID: "user2",
			Username: "Bob",
			Content:  "Hi Alice",
			IsBot:    false,
		},
	}

	result := b.buildConversationHistoryText(
		context.Background(),
		messages,
		"",
		"",
		false,
		nil,
		nil,
	)

	if strings.Contains(result, "(replying to") {
		t.Error("no reply marker should appear when messages have no ReplyToID")
	}
}

func TestBuildConversationHistory_WithRename(t *testing.T) {
	b := &Bot{}

	messages := []*types.DiscordMessage{
		{
			ID:       "msg1",
			AuthorID: "user1",
			Username: "OriginalName",
			Content:  "Hello everyone",
			IsBot:    false,
		},
		{
			ID:              "msg2",
			AuthorID:        "user1",
			Username:        "NewName",
			Content:         "I renamed myself",
			IsBot:           false,
			ReplyToID:       "msg0",
			ReplyToUsername: "Mei",
		},
		{
			ID:       "msg3",
			AuthorID: "bot1",
			Username: "EZyapper",
			Content:  "I am a bot",
			IsBot:    true,
		},
		{
			ID:       "msg4",
			AuthorID: "user2",
			Username: "Alice",
			Content:  "First message",
			IsBot:    false,
		},
		{
			ID:              "msg5",
			AuthorID:        "user1",
			Username:        "NewName",
			Content:         "Same name again",
			IsBot:           false,
			ReplyToID:       "msgX",
			ReplyToUsername: "(deleted message)",
		},
	}

	result := b.buildConversationHistoryText(
		context.Background(),
		messages,
		"",
		"bot1",
		false,
		nil,
		nil,
	)

	// First message should have NO rename marker (first appearance)
	if !strings.Contains(result, "[User] OriginalName (ID:user1): Hello everyone") {
		t.Error("expected first message of user1 without rename marker")
	}

	// Second message (user1 renamed to NewName) should have rename marker + reply marker
	expectedLine := "[User] NewName (ID:user1): I renamed myself (was @OriginalName) (replying to @Mei)"
	if !strings.Contains(result, expectedLine) {
		t.Errorf("expected rename marker before reply marker\n  want: %s\n  got:\n%s", expectedLine, result)
	}

	// Bot message should NOT have rename marker even though bot1 username hasn't changed
	if strings.Contains(result, "EZyapper (was @") {
		t.Error("bot's own messages should not get rename marker")
	}

	// Alice's first message should have no rename marker
	if !strings.Contains(result, "[User] Alice (ID:user2): First message") {
		t.Error("expected Alice's first message without rename marker")
	}

	// Third user1 message with same name should NOT have rename marker
	if strings.Contains(result, "Same name again (was @") {
		t.Error("same username should not trigger rename marker again")
	}

	// Deleted message reply marker should still appear
	if !strings.Contains(result, "(replying to deleted message)") {
		t.Error("expected '(replying to deleted message)' on msg5")
	}
}
