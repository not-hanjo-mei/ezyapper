// Package memory provides long-term memory management using Qdrant vector database
package memory

import (
	"time"
)

// Type represents the type of memory being stored
type Type string

const (
	// TypeSummary represents a conversation summary
	TypeSummary Type = "summary"
	// TypeFact represents a factual memory
	TypeFact Type = "fact"
	// TypeEpisode represents an episodic memory (specific event)
	TypeEpisode Type = "episode"
)

// Record represents a stored memory in the vector database
type Record struct {
	// Unique identifier (UUID)
	ID string `json:"id"`

	// Discord identifiers
	UserID    string `json:"user_id"`
	GuildID   string `json:"guild_id,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`

	// Record content
	MemoryType Type `json:"memory_type"`
	Content    string     `json:"content"`
	Summary    string     `json:"summary"`

	// Vector embedding (1536 dimensions for text-embedding-3-small)
	Embedding []float32 `json:"embedding"`

	// Metadata
	Keywords []string               `json:"keywords"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`

	// Quality metrics
	Confidence   float64 `json:"confidence"`
	MessageRange [2]int  `json:"message_range,omitempty"` // [start, end] message IDs

	// Tracking
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	AccessCount int       `json:"access_count"`
}

// Profile represents a user's profile stored in Qdrant
type Profile struct {
	// Primary key - Discord User ID
	UserID string `json:"user_id"`

	// Profile data
	DisplayName string            `json:"display_name"`
	Traits      []string          `json:"traits"`
	Facts       map[string]string `json:"facts"`
	Preferences map[string]string `json:"preferences"`
	Interests   []string          `json:"interests"`

	// Dynamic summary
	LastSummary        string `json:"last_summary"`
	PersonalitySummary string `json:"personality_summary"`

	// Statistics
	MessageCount       int       `json:"message_count"`
	MemoryCount        int       `json:"memory_count"`
	FirstSeenAt        time.Time `json:"first_seen_at"`
	LastActiveAt       time.Time `json:"last_active_at"`
	LastConsolidatedAt time.Time `json:"last_consolidated_at"`

	// Vector representation for similar user discovery (optional)
	Embedding []float32 `json:"embedding,omitempty"`
}

// SearchOptions defines options for memory search
type SearchOptions struct {
	TopK        int        // Number of results to return
	MinScore    float64    // Minimum similarity score (0.0-1.0)
	MemoryTypes []string   // Filter by memory types
	TimeRange   *TimeRange // Filter by time range
}

// TimeRange defines a time range for filtering
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// Extract represents an extracted memory from consolidation
type Extract struct {
	Content    string   `json:"content"`
	Type       string   `json:"type"`
	Confidence float64  `json:"confidence"`
	Keywords   []string `json:"keywords"`
}

// UserMemoryExtract represents extracted memories for a specific user
type UserMemoryExtract struct {
	UserID   string          `json:"user_id"`
	Memories []Extract `json:"memories"`
}

// ConsolidationResult represents the result of a consolidation operation
type ConsolidationResult struct {
	Summary      string          `json:"summary"`
	ProfileDelta ProfileDelta    `json:"profile_delta"`
	Memories     []Extract `json:"memories"`
}

// ProfileDelta represents changes to a user profile
type ProfileDelta struct {
	NewTraits      []string          `json:"new_traits"`
	NewFacts       map[string]string `json:"new_facts"`
	NewPreferences map[string]string `json:"new_preferences"`
	NewInterests   []string          `json:"new_interests"`
}

// DiscordMessage represents a simplified Discord message for short-term context
type DiscordMessage struct {
	ID                string    `json:"id"`
	ChannelID         string    `json:"channel_id"`
	GuildID           string    `json:"guild_id"`
	AuthorID          string    `json:"author_id"`
	Username          string    `json:"username"`
	Content           string    `json:"content"`
	ImageURLs         []string  `json:"image_urls,omitempty"`
	ImageDescriptions []string  `json:"image_descriptions,omitempty"` // Cached image descriptions to avoid redundant API calls
	Timestamp         time.Time `json:"timestamp"`
	IsBot             bool      `json:"is_bot"`

	// ReplyToID is the ID of the message being replied to (from MessageReference)
	ReplyToID string `json:"reply_to_id"`
	// ReplyToUsername is the username of the author of the replied-to message
	ReplyToUsername string `json:"reply_to_username"`
	// ReplyToContent is the content of the replied-to message
	ReplyToContent string `json:"reply_to_content"`
}

// UserStats represents statistics for a user
type UserStats struct {
	UserID        string    `json:"user_id"`
	MessageCount  int       `json:"message_count"`
	MemoryCount   int       `json:"memory_count"`
	FirstSeenAt   time.Time `json:"first_seen_at"`
	LastActiveAt  time.Time `json:"last_active_at"`
	LastSummaryAt time.Time `json:"last_summary_at"`
}

// GlobalStats represents global statistics
type GlobalStats struct {
	TotalUsers       int64     `json:"total_users"`
	TotalMemories    int64     `json:"total_memories"`
	TotalMessages    int64     `json:"total_messages"`
	LastConsolidated time.Time `json:"last_consolidated"`
}
