// Package main — emote-plugin storage layer.
// Provides file-based emote storage with atomic metadata writes
// and blacklist/whitelist checking.
package main

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// EmoteEntry represents a single emote stored on disk.
type EmoteEntry struct {
	ID          string   `json:"id"`                  // md5(URL) or md5(file_name)
	Name        string   `json:"name"`                // snake_case
	Description string   `json:"description"`         // 1-2 sentence
	Tags        []string `json:"tags"`                // searchable
	URL         string   `json:"url,omitempty"`       // bare URL (no ?params), mutually exclusive with FileName
	FileName    string   `json:"file_name,omitempty"` // local filename, mutually exclusive with URL
	CreatedAt   string   `json:"created_at"`          // ISO 8601
}

// MetadataFile holds all emote entries for a guild.
type MetadataFile struct {
	Emotes []EmoteEntry `json:"emotes"`
}

// Storage provides file-based emote storage with atomic writes
// and blacklist/whitelist checking.
type Storage struct {
	dataDir string
	mu      sync.RWMutex
}

// NewStorage creates a new Storage backed by dataDir.
// The data directory is created if it does not already exist.
func NewStorage(dataDir string) *Storage {
	os.MkdirAll(dataDir, 0755)
	return &Storage{
		dataDir: dataDir,
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

// md5Hash computes the MD5 hash of s and returns it as a lowercase hex string.
func md5Hash(s string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(s)))
}
