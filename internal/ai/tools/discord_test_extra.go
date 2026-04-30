package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestCreateThread_MessageIDWrongType(t *testing.T) {
	dt := &DiscordTools{session: &discordgo.Session{}}

	_, err := dt.createThread(context.Background(), map[string]any{
		"channel_id": "123",
		"name":       "test-thread",
		"message_id": 42,
	})
	if err == nil {
		t.Fatal("expected error for invalid message_id type")
	}
	if !strings.Contains(err.Error(), "message_id must be a string") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateThread_MessageIDNil(t *testing.T) {
	// Test that nil message_id is handled (treated as empty string = thread without message)
	// This won't succeed with a mock session, but validates the nil case doesn't panic
	dt := &DiscordTools{session: &discordgo.Session{}}

	args := map[string]any{
		"channel_id": "123",
		"name":       "test-thread",
	}
	// message_id not set at all
	_, err := dt.createThread(context.Background(), args)
	// Session is not connected, so we expect a Discord API error, not a panic
	if err == nil {
		t.Log("create thread returned success (unexpected with mock session)")
	}
}

func TestGetStringArg_EmptyKey(t *testing.T) {
	args := map[string]any{"": "value"}
	_, err := getStringArg(args, "")
	if err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestMarshalJSON_Numeric(t *testing.T) {
	result := marshalJSON(map[string]interface{}{"count": 42})
	if !strings.Contains(result, "42") {
		t.Fatalf("expected 42 in JSON: %s", result)
	}
}

func TestMarshalJSON_Array(t *testing.T) {
	result := marshalJSON([]string{"a", "b", "c"})
	if !strings.Contains(result, "a") || !strings.Contains(result, "b") {
		t.Fatalf("expected array: %s", result)
	}
}
