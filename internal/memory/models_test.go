package memory

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestDiscordMessage_ReplyFields(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	original := DiscordMessage{
		ID:                "100",
		ChannelID:         "200",
		GuildID:           "300",
		AuthorID:          "400",
		Username:          "testuser",
		Content:           "hello world",
		ImageURLs:         nil,
		ImageDescriptions: nil,
		Timestamp:         now,
		IsBot:             false,
		ReplyToID:         "999",
		ReplyToUsername:   "parentuser",
		ReplyToContent:    "original message",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var decoded DiscordMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if decoded.ReplyToID != "999" {
		t.Fatalf("ReplyToID: got %q, want %q", decoded.ReplyToID, "999")
	}
	if decoded.ReplyToUsername != "parentuser" {
		t.Fatalf("ReplyToUsername: got %q, want %q", decoded.ReplyToUsername, "parentuser")
	}
	if decoded.ReplyToContent != "original message" {
		t.Fatalf("ReplyToContent: got %q, want %q", decoded.ReplyToContent, "original message")
	}

	if !reflect.DeepEqual(original, decoded) {
		t.Fatalf("round-trip mismatch:\n  original=%+v\n  decoded=%+v", original, decoded)
	}
}

func TestDiscordMessage_ReplyFields_Empty(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	msg := DiscordMessage{
		ID:                "100",
		ChannelID:         "200",
		GuildID:           "300",
		AuthorID:          "400",
		Username:          "testuser",
		Content:           "no reply",
		ImageURLs:         nil,
		ImageDescriptions: nil,
		Timestamp:         now,
		IsBot:             false,
		// ReplyToID, ReplyToUsername, ReplyToContent left zero-value
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal into map failed: %v", err)
	}

	// Reply fields should be present and marshal as empty strings (not omitted)
	if v, ok := raw["reply_to_id"]; !ok {
		t.Fatal("reply_to_id field missing from JSON output")
	} else if s, ok := v.(string); !ok {
		t.Fatalf("reply_to_id is not a string, got %T", v)
	} else if s != "" {
		t.Fatalf("reply_to_id: got %q, want empty string", s)
	}

	if v, ok := raw["reply_to_username"]; !ok {
		t.Fatal("reply_to_username field missing from JSON output")
	} else if s, ok := v.(string); !ok {
		t.Fatalf("reply_to_username is not a string, got %T", v)
	} else if s != "" {
		t.Fatalf("reply_to_username: got %q, want empty string", s)
	}

	if v, ok := raw["reply_to_content"]; !ok {
		t.Fatal("reply_to_content field missing from JSON output")
	} else if s, ok := v.(string); !ok {
		t.Fatalf("reply_to_content is not a string, got %T", v)
	} else if s != "" {
		t.Fatalf("reply_to_content: got %q, want empty string", s)
	}
}
