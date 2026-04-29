// Package main — emote-plugin storage layer.
// Provides file-based emote storage with atomic metadata writes, SHA256 dedup,
// blacklist/whitelist checking, and per-channel rate limiting.
package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// EmoteEntry represents a single emote stored on disk.
type EmoteEntry struct {
	ID          string   `json:"id"`          // UUID
	Name        string   `json:"name"`        // snake_case name
	Description string   `json:"description"` // 1-2 sentence description
	Tags        []string `json:"tags"`        // searchable tags
	FileName    string   `json:"file_name"`   // filename on disk
	URL         string   `json:"url,omitempty"` // image URL (for URL-only emotes, no local file)
	Source      string   `json:"source"`      // "auto_steal" or "file"
	AddedBy     string   `json:"added_by"`    // user ID who triggered
	GuildID     string   `json:"guild_id"`    // guild or "global"
	ChannelID   string   `json:"channel_id"`  // channel where stolen
	SHA256      string   `json:"sha256"`      // content hash for dedup
	CreatedAt   string   `json:"created_at"`  // ISO 8601 timestamp
}

// MetadataFile holds all emote entries for a guild.
type MetadataFile struct {
	Emotes []EmoteEntry `json:"emotes"`
}

// rateLimiter provides per-channel sliding-window rate limiting with cooldown.
type rateLimiter struct {
	timestamps []time.Time
	mu         sync.Mutex
	MaxPerMin  int
	Cooldown   time.Duration
}

// Allow checks whether a new request is permitted under the rate limit.
// Returns false if per-minute cap or cooldown interval is exceeded.
func (r *rateLimiter) Allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-time.Minute)

	filtered := r.timestamps[:0]
	for _, t := range r.timestamps {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}
	r.timestamps = filtered

	if len(r.timestamps) >= r.MaxPerMin {
		return false
	}
	if len(r.timestamps) > 0 && now.Sub(r.timestamps[len(r.timestamps)-1]) < r.Cooldown {
		return false
	}

	r.timestamps = append(r.timestamps, now)
	return true
}

// Storage provides file-based emote storage with atomic writes, SHA256 dedup,
// blacklist/whitelist checking, and per-channel rate limiting.
type Storage struct {
	dataDir      string
	mu           sync.RWMutex
	rateLimiters map[string]*rateLimiter
}

// NewStorage creates a new Storage backed by dataDir.
// The data directory is created if it does not already exist.
func NewStorage(dataDir string) *Storage {
	os.MkdirAll(dataDir, 0755)
	return &Storage{
		dataDir:      dataDir,
		rateLimiters: make(map[string]*rateLimiter),
	}
}

// LoadMetadata reads the metadata file for a guild.
// Returns an empty MetadataFile if the file does not exist.
func (s *Storage) LoadMetadata(guildID string) (*MetadataFile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadMetadataLocked(guildID)
}

// loadMetadataLocked reads metadata without acquiring the lock (caller must hold it).
func (s *Storage) loadMetadataLocked(guildID string) (*MetadataFile, error) {
	path := filepath.Join(s.dataDir, guildID, "metadata.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &MetadataFile{}, nil
		}
		return nil, fmt.Errorf("failed to read metadata for guild %s: %w", guildID, err)
	}

	var mf MetadataFile
	if err := json.Unmarshal(data, &mf); err != nil {
		return nil, fmt.Errorf("failed to parse metadata for guild %s: %w", guildID, err)
	}
	if mf.Emotes == nil {
		mf.Emotes = []EmoteEntry{}
	}
	return &mf, nil
}

// SaveMetadata atomically writes the metadata file for a guild.
// Uses temp file + os.Rename to prevent corruption on crash.
func (s *Storage) SaveMetadata(guildID string, mf *MetadataFile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveMetadataLocked(guildID, mf)
}

// saveMetadataLocked writes metadata atomically (caller must hold write lock).
func (s *Storage) saveMetadataLocked(guildID string, mf *MetadataFile) error {
	dir := filepath.Join(s.dataDir, guildID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create guild directory %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata for guild %s: %w", guildID, err)
	}

	tmpFile, err := os.CreateTemp(dir, ".meta-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file for guild %s: %w", guildID, err)
	}
	tmpPath := tmpFile.Name()

	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write temp metadata for guild %s: %w", guildID, err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp metadata for guild %s: %w", guildID, err)
	}

	target := filepath.Join(dir, "metadata.json")
	if err := os.Rename(tmpPath, target); err != nil {
		return fmt.Errorf("failed to atomically save metadata for guild %s: %w", guildID, err)
	}

	return nil
}

// SaveImage saves image data to the guild's images directory.
// Returns the on-disk file path and SHA256 content hash.
// Creates the images directory if it does not exist.
func (s *Storage) SaveImage(guildID string, data []byte, format string) (string, string, error) {
	dir := filepath.Join(s.dataDir, guildID, "images")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", "", fmt.Errorf("failed to create images directory for guild %s: %w", guildID, err)
	}

	hash := sha256Hash(data)
	uuid := generateUUID()
	filename := uuid + "." + format
	fpath := filepath.Join(dir, filename)

	if err := os.WriteFile(fpath, data, 0644); err != nil {
		return "", "", fmt.Errorf("failed to write image for guild %s: %w", guildID, err)
	}

	return fpath, hash, nil
}

// Dedup checks whether an emote with the given SHA256 hash already exists in the guild.
// Returns true and a pointer to the existing entry if found.
func (s *Storage) Dedup(sha256hash string, guildID string) (bool, *EmoteEntry, error) {
	mf, err := s.LoadMetadata(guildID)
	if err != nil {
		return false, nil, err
	}
	for i := range mf.Emotes {
		if mf.Emotes[i].SHA256 == sha256hash {
			return true, &mf.Emotes[i], nil
		}
	}
	return false, nil, nil
}

// AddEmote appends a new emote entry to the guild's metadata.
// This is an atomic compound operation: load → append → save.
func (s *Storage) AddEmote(guildID string, entry EmoteEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mf, err := s.loadMetadataLocked(guildID)
	if err != nil {
		return fmt.Errorf("failed to load metadata for guild %s: %w", guildID, err)
	}

	mf.Emotes = append(mf.Emotes, entry)

	if err := s.saveMetadataLocked(guildID, mf); err != nil {
		return fmt.Errorf("failed to save metadata for guild %s: %w", guildID, err)
	}

	return nil
}

// CheckBlacklist checks whether a channel/user is allowed based on whitelist and blacklist.
// Whitelist takes priority: when non-empty, ONLY whitelisted channels are permitted.
// Returns true if the channel/user passes all checks.
func (s *Storage) CheckBlacklist(guildID, channelID, userID string, blacklistChannels, whitelistChannels, blacklistUsers []string) bool {
	_ = guildID

	if len(whitelistChannels) > 0 {
		for _, w := range whitelistChannels {
			if channelID == w {
				goto checkBlacklist
			}
		}
		return false
	}

checkBlacklist:
	for _, b := range blacklistChannels {
		if channelID == b {
			return false
		}
	}
	for _, b := range blacklistUsers {
		if userID == b {
			return false
		}
	}
	return true
}

// CheckRateLimit tests the per-channel rate limit and returns true if the action is allowed.
// Creates a new rate limiter for the channel on first use.
func (s *Storage) CheckRateLimit(channelID string, maxPerMin int, cooldown time.Duration) bool {
	s.mu.Lock()
	rl, ok := s.rateLimiters[channelID]
	if !ok {
		rl = &rateLimiter{
			MaxPerMin: maxPerMin,
			Cooldown:  cooldown,
		}
		s.rateLimiters[channelID] = rl
	}
	s.mu.Unlock()

	// Refresh config values in case they changed at runtime.
	rl.mu.Lock()
	rl.MaxPerMin = maxPerMin
	rl.Cooldown = cooldown
	rl.mu.Unlock()

	return rl.Allow()
}

// sha256Hash computes the SHA256 hash of data and returns it as a lowercase hex string.
func sha256Hash(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:])
}

// generateUUID creates a version 4 UUID using crypto/rand.
func generateUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read failure is effectively impossible on modern systems.
		panic(fmt.Sprintf("failed to generate UUID: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // UUID version 4
	b[8] = (b[8] & 0x3f) | 0x80 // UUID variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
