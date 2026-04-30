package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"ezyapper/internal/types"
)

// mockFetcher implements MessageFetcher for testing.
type mockFetcher struct {
	messages []types.DiscordMessage
	err      error
}

func (m *mockFetcher) FetchMessages(ctx context.Context, channelID string, limit int) ([]types.DiscordMessage, error) {
	if m.err != nil {
		return nil, m.err
	}
	if limit > len(m.messages) {
		limit = len(m.messages)
	}
	return m.messages[:limit], nil
}

func TestFetchRecentMessages(t *testing.T) {
	msgs := []types.DiscordMessage{
		{ID: "1", AuthorID: "u1", Content: "hello"},
		{ID: "2", AuthorID: "u2", Content: "world"},
	}
	client := NewShortTermClient(&mockFetcher{messages: msgs}, 500)

	result, err := client.FetchRecentMessages(context.Background(), "chan", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("got %d messages, want 2", len(result))
	}
}

func TestFetchUserMessages(t *testing.T) {
	msgs := []types.DiscordMessage{
		{ID: "1", AuthorID: "u1", Content: "hello"},
		{ID: "2", AuthorID: "u2", Content: "world"},
		{ID: "3", AuthorID: "u1", Content: "again"},
	}
	client := NewShortTermClient(&mockFetcher{messages: msgs}, 500)

	result, err := client.FetchUserMessages(context.Background(), "chan", "u1", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("got %d messages, want 2", len(result))
	}
}

func TestFetchChannelMessages(t *testing.T) {
	msgs := []types.DiscordMessage{
		{ID: "1", Content: "hello"},
	}
	client := NewShortTermClient(&mockFetcher{messages: msgs}, 500)

	result, err := client.FetchChannelMessages(context.Background(), "chan", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("got %d messages, want 1", len(result))
	}
}

func TestFetchRecentMessages_Error(t *testing.T) {
	client := NewShortTermClient(&mockFetcher{err: errors.New("api down")}, 500)

	_, err := client.FetchRecentMessages(context.Background(), "chan", 10)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestValidateLimit(t *testing.T) {
	tests := []struct {
		limit   int
		wantErr bool
		errText string
		wantCap int
	}{
		{0, true, "limit must be greater than 0", 0},
		{-1, true, "limit must be greater than 0", 0},
		{1, false, "", 1},
		{100, false, "", 100},
		{500, false, "", 500},
		{600, false, "", 500}, // capped to maxPaginatedLimit
		{1000, false, "", 500},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("limit=%d", tt.limit), func(t *testing.T) {
			got, err := validateLimit(tt.limit, 500, "TestFunc")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errText != "" && !strings.Contains(err.Error(), tt.errText) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.errText)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantCap {
				t.Fatalf("got %d, want %d", got, tt.wantCap)
			}
		})
	}
}
