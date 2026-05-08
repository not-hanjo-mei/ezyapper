// Package memory provides long-term memory management using Qdrant vector database
package memory

import (
	"time"

	"ezyapper/internal/types"
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
	// TypeInterest represents a user interest memory
	TypeInterest Type = "interest"
)

// Record represents a stored memory in the vector database.
type Record struct {
	ID                  string   `json:"id"`
	UserID              string   `json:"user_id"`
	GuildID             string   `json:"guild_id,omitempty"`
	ChannelID           string   `json:"channel_id,omitempty"`
	MentionedUserIDs    []string `json:"mentioned_user_ids,omitempty"`
	MentionedChannelIDs []string `json:"mentioned_channel_ids,omitempty"`
	MemoryType          Type     `json:"memory_type"`
	Content             string   `json:"content"`

	// Vector embedding (size must match the configured embedding model)
	Embedding  []float32 `json:"embedding"`
	Keywords   []string  `json:"keywords"`
	Confidence float64   `json:"confidence"`
	CreatedAt  time.Time `json:"created_at"`

	ImportanceScore float64   `json:"importance_score"`
	AccessCount     int       `json:"access_count"`
	LastAccessedAt  time.Time `json:"last_accessed_at"`
	DecayCategory   string    `json:"decay_category"`
	ExpiresAt       time.Time `json:"expires_at,omitempty"`
}

// Profile represents a user's profile stored in Qdrant.
type Profile struct {
	UserID             string            `json:"user_id"`
	DisplayName        string            `json:"display_name"`
	Traits             []string          `json:"traits"`
	Facts              map[string]string `json:"facts"`
	Preferences        map[string]string `json:"preferences"`
	Interests          []string          `json:"interests"`
	MessageCount       int               `json:"message_count"`
	MemoryCount        int               `json:"memory_count"`
	FirstSeenAt        time.Time         `json:"first_seen_at"`
	LastActiveAt       time.Time         `json:"last_active_at"`
	LastConsolidatedAt time.Time         `json:"last_consolidated_at"`

	// Vector representation for similar user discovery (optional)
	Embedding []float32 `json:"embedding,omitempty"`
}

// SearchOptions defines options for memory search.
type SearchOptions struct {
	TopK                int
	MinScore            float64    // Minimum similarity score (0.0-1.0)
	MemoryTypes         []string   // Filter by memory types
	MentionedUserIDs    []string   // Filter by mentioned users
	MentionedChannelIDs []string   // Filter by mentioned channels
	ChannelID           string     // Filter by channel
	TimeRange           *TimeRange // Filter by time range
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

	MentionedUserIDs    []string `json:"mentioned_user_ids,omitempty"`
	MentionedChannelIDs []string `json:"mentioned_channel_ids,omitempty"`

	ImportanceScore *float64          `json:"importance_score,omitempty"`
	ProfileUpdates  *ProfileUpdateSet `json:"profile_updates,omitempty"`
}

// UserMemoryExtract represents extracted memories for a specific user
type UserMemoryExtract struct {
	UserID   string    `json:"user_id"`
	Memories []Extract `json:"memories"`
}

// ProfileUpdateSet represents structured profile updates from LLM extraction.
type ProfileUpdateSet struct {
	Traits      []string          `json:"traits,omitempty"`
	Facts       map[string]string `json:"facts,omitempty"`
	Preferences map[string]string `json:"preferences,omitempty"`
	Interests   []string          `json:"interests,omitempty"`
}

// ScoringConfig holds config for memory scoring and decay.
type ScoringConfig struct {
	Weights                  ScoringWeights
	DecayRates               map[Type]float64
	MemoryStrengthMultiplier float64
}

// ScoringWeights holds weights for compositing scores.
type ScoringWeights struct {
	Importance float64
	Recency    float64
	Access     float64
	Confidence float64
}

// DiscordMessage is an alias for the canonical type defined in internal/types.
type DiscordMessage = types.DiscordMessage

// GlobalStats represents global statistics
type GlobalStats struct {
	TotalUsers       int64     `json:"total_users"`
	TotalMemories    int64     `json:"total_memories"`
	LastConsolidated time.Time `json:"last_consolidated"`
}

// RelationshipType represents the type of relationship between two users
type RelationshipType string

const (
	RelMention  RelationshipType = "mention"
	RelReply    RelationshipType = "reply"
	RelReaction RelationshipType = "reaction"
)

// Relationship represents a relationship between two users.
type Relationship struct {
	ID                string           `json:"id"`
	UserA             string           `json:"user_a"`
	UserB             string           `json:"user_b"`
	Type              RelationshipType `json:"type"`
	InteractionCount  int              `json:"interaction_count"`
	LastInteractionAt time.Time        `json:"last_interaction_at"`
	ChannelIDs        []string         `json:"channel_ids"`
	Weight            float64          `json:"weight"`
	Embedding         []float32        `json:"embedding,omitempty"`
}
