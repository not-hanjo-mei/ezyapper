package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewStorage(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "emote-data")
	s := NewStorage(dataDir)
	if s == nil {
		t.Fatal("NewStorage returned nil")
	}
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		t.Fatalf("expected data dir %q to exist, but it does not", dataDir)
	}
	if s.dataDir != dataDir {
		t.Fatalf("expected dataDir=%q, got %q", dataDir, s.dataDir)
	}
}

func TestSaveAndLoadMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	s := NewStorage(tmpDir)
	guildID := "test-guild"

	orig := &MetadataFile{
		Emotes: []EmoteEntry{
			{ID: "abc", Name: "happy_cat", SHA256: "deadbeef"},
			{ID: "def", Name: "sad_dog", SHA256: "cafebabe"},
		},
	}
	if err := s.SaveMetadata(guildID, orig); err != nil {
		t.Fatalf("SaveMetadata failed: %v", err)
	}

	loaded, err := s.LoadMetadata(guildID)
	if err != nil {
		t.Fatalf("LoadMetadata failed: %v", err)
	}
	if len(loaded.Emotes) != 2 {
		t.Fatalf("expected 2 emotes, got %d", len(loaded.Emotes))
	}
	if loaded.Emotes[0].ID != "abc" || loaded.Emotes[1].ID != "def" {
		t.Fatalf("loaded emotes do not match: %+v", loaded.Emotes)
	}
}

func TestLoadMetadata_NonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	s := NewStorage(tmpDir)

	mf, err := s.LoadMetadata("nonexistent-guild")
	if err != nil {
		t.Fatalf("LoadMetadata returned error for non-existent guild: %v", err)
	}
	if mf == nil {
		t.Fatal("LoadMetadata returned nil MetadataFile")
	}
	if len(mf.Emotes) != 0 {
		t.Fatalf("expected 0 emotes for non-existent guild, got %d", len(mf.Emotes))
	}
}

func TestSaveImage(t *testing.T) {
	tmpDir := t.TempDir()
	s := NewStorage(tmpDir)
	guildID := "g-img"
	imageData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A} // PNG header

	fpath, hash, err := s.SaveImage(guildID, imageData, "png")
	if err != nil {
		t.Fatalf("SaveImage failed: %v", err)
	}
	if fpath == "" {
		t.Fatal("SaveImage returned empty file path")
	}
	if hash == "" {
		t.Fatal("SaveImage returned empty SHA256 hash")
	}

	// Verify file exists on disk
	if _, err := os.Stat(fpath); os.IsNotExist(err) {
		t.Fatalf("image file %q does not exist after save", fpath)
	}

	// Verify SHA256 matches
	expected := sha256Hash(imageData)
	if hash != expected {
		t.Fatalf("hash mismatch: got %s, want %s", hash, expected)
	}

	// Verify file content matches
	saved, err := os.ReadFile(fpath)
	if err != nil {
		t.Fatalf("failed to read saved image: %v", err)
	}
	if string(saved) != string(imageData) {
		t.Fatal("saved image content does not match original")
	}

	// Verify filename format: UUID.format
	base := filepath.Base(fpath)
	ext := filepath.Ext(base)
	if ext != ".png" {
		t.Fatalf("expected .png extension, got %s", ext)
	}
	if len(base) != 40 { // 36-char UUID + dot + 3-char ext
		t.Fatalf("unexpected filename length: %d (%s)", len(base), base)
	}
}

func TestDedup(t *testing.T) {
	tmpDir := t.TempDir()
	s := NewStorage(tmpDir)
	guildID := "g-dedup"
	imageData := []byte("test-image-bytes")

	_, hash, err := s.SaveImage(guildID, imageData, "png")
	if err != nil {
		t.Fatalf("SaveImage failed: %v", err)
	}

	entry := EmoteEntry{
		ID:     "dedup-test-1",
		Name:   "test_emote",
		SHA256: hash,
	}
	if err := s.AddEmote(guildID, entry); err != nil {
		t.Fatalf("AddEmote failed: %v", err)
	}

	found, existing, err := s.Dedup(hash, guildID)
	if err != nil {
		t.Fatalf("Dedup failed: %v", err)
	}
	if !found {
		t.Fatal("Dedup should have found the emote by SHA256")
	}
	if existing == nil {
		t.Fatal("Dedup returned nil entry when found")
	}
	if existing.ID != "dedup-test-1" {
		t.Fatalf("Dedup returned wrong entry: %+v", existing)
	}
}

func TestDedup_NotExist(t *testing.T) {
	tmpDir := t.TempDir()
	s := NewStorage(tmpDir)

	found, existing, err := s.Dedup("nonexistent-hash-12345", "g-nope")
	if err != nil {
		t.Fatalf("Dedup returned error: %v", err)
	}
	if found {
		t.Fatal("Dedup should have returned false for non-existent hash")
	}
	if existing != nil {
		t.Fatalf("Dedup should have returned nil entry: got %+v", existing)
	}
}

func TestAddEmote(t *testing.T) {
	tmpDir := t.TempDir()
	s := NewStorage(tmpDir)
	guildID := "g-add"

	e1 := EmoteEntry{ID: "e1", Name: "first", SHA256: "aaa"}
	e2 := EmoteEntry{ID: "e2", Name: "second", SHA256: "bbb"}

	if err := s.AddEmote(guildID, e1); err != nil {
		t.Fatalf("AddEmote failed: %v", err)
	}
	if err := s.AddEmote(guildID, e2); err != nil {
		t.Fatalf("AddEmote failed: %v", err)
	}

	mf, err := s.LoadMetadata(guildID)
	if err != nil {
		t.Fatalf("LoadMetadata failed: %v", err)
	}
	if len(mf.Emotes) != 2 {
		t.Fatalf("expected 2 emotes, got %d", len(mf.Emotes))
	}
	if mf.Emotes[0].ID != "e1" {
		t.Fatalf("first emote ID mismatch: got %s", mf.Emotes[0].ID)
	}
	if mf.Emotes[1].ID != "e2" {
		t.Fatalf("second emote ID mismatch: got %s", mf.Emotes[1].ID)
	}
}

func TestCheckBlacklist_WhitelistPriority(t *testing.T) {
	s := NewStorage(t.TempDir())

	// Whitelist acts as a gate: only whitelisted channels reach blacklist check.
	// Channel in whitelist but not blacklisted → allowed
	if !s.CheckBlacklist("g", "wl-chan", "user1", nil, []string{"wl-chan"}, nil) {
		t.Error("whitelisted channel should pass (no blacklist)")
	}

	// Channel in both whitelist and blacklist → rejected (blacklist applies after whitelist gate)
	if s.CheckBlacklist("g", "wl-chan", "user1", []string{"wl-chan"}, []string{"wl-chan"}, nil) {
		t.Error("channel in both whitelist and blacklist should be rejected")
	}

	// Non-whitelisted channel rejected when whitelist is non-empty
	if s.CheckBlacklist("g", "random-chan", "user1", nil, []string{"wl-chan"}, nil) {
		t.Error("non-whitelisted channel should be rejected when whitelist is set")
	}
}

func TestCheckBlacklist_Blacklist(t *testing.T) {
	s := NewStorage(t.TempDir())

	// Channel in blacklist → rejected
	if s.CheckBlacklist("g", "bad-chan", "user1", []string{"bad-chan", "also-bad"}, nil, nil) {
		t.Error("blacklisted channel should be rejected")
	}

	// User in blacklist → rejected
	if s.CheckBlacklist("g", "good-chan", "bad-user", nil, nil, []string{"bad-user", "spammer"}) {
		t.Error("blacklisted user should be rejected")
	}

	// Neither in blacklist → allowed
	if !s.CheckBlacklist("g", "good-chan", "good-user", []string{"other-bad"}, nil, []string{"other-spammer"}) {
		t.Error("non-blacklisted channel and user should be allowed")
	}

	// Empty blacklists → allowed
	if !s.CheckBlacklist("g", "any-chan", "any-user", nil, nil, nil) {
		t.Error("should be allowed when no blacklists are set")
	}
}

func TestCheckRateLimit(t *testing.T) {
	s := NewStorage(t.TempDir())
	channelID := "chan-rl"
	maxPerMin := 100 // high enough to not trigger rate cap
	cooldown := time.Hour

	// First call should be allowed
	if !s.CheckRateLimit(channelID, maxPerMin, cooldown) {
		t.Fatal("first rate-limit check should be allowed")
	}

	// Second call within cooldown should be rejected
	if s.CheckRateLimit(channelID, maxPerMin, cooldown) {
		t.Fatal("second rate-limit check within cooldown should be rejected")
	}
}

func TestCheckRateLimit_PerChannelIsolation(t *testing.T) {
	s := NewStorage(t.TempDir())
	cooldown := time.Hour
	maxPerMin := 100

	// Channel A first call — allowed
	if !s.CheckRateLimit("chan-a", maxPerMin, cooldown) {
		t.Fatal("chan-a first call should be allowed")
	}

	// Channel B first call — also allowed (different channel)
	if !s.CheckRateLimit("chan-b", maxPerMin, cooldown) {
		t.Fatal("chan-b first call should be allowed (isolated)")
	}

	// Channel A second call — rejected (cooldown)
	if s.CheckRateLimit("chan-a", maxPerMin, cooldown) {
		t.Fatal("chan-a second call should be rejected (cooldown)")
	}
}

func TestSaveMetadata_Atomicity(t *testing.T) {
	// Verify that SaveMetadata uses temp file + rename for atomicity.
	// We do this by checking that there are no temp files left behind
	// and the metadata.json exists with correct content.
	tmpDir := t.TempDir()
	s := NewStorage(tmpDir)
	guildID := "g-atomic"

	mf := &MetadataFile{
		Emotes: []EmoteEntry{{ID: "atomic-test", Name: "test", SHA256: "abc123"}},
	}
	if err := s.SaveMetadata(guildID, mf); err != nil {
		t.Fatalf("SaveMetadata failed: %v", err)
	}

	// Verify metadata.json exists
	metaPath := filepath.Join(tmpDir, guildID, "metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("metadata.json not found after save: %v", err)
	}

	var loaded MetadataFile
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("failed to parse metadata.json: %v", err)
	}
	if len(loaded.Emotes) != 1 || loaded.Emotes[0].ID != "atomic-test" {
		t.Fatal("metadata content mismatch after save")
	}

	// Verify no .tmp files left in the directory
	entries, err := os.ReadDir(filepath.Join(tmpDir, guildID))
	if err != nil {
		t.Fatalf("failed to read guild dir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestMetadataFile_JSONRoundtrip(t *testing.T) {
	orig := MetadataFile{
		Emotes: []EmoteEntry{
			{
				ID:          "550e8400-e29b-41d4-a716-446655440000",
				Name:        "pepe_smug",
				Description: "A smug Pepe the Frog with crossed arms",
				Tags:        []string{"pepe", "smug", "reaction"},
				FileName:    "abc.png",
				Source:      "auto_steal",
				AddedBy:     "user123",
				GuildID:     "guild456",
				ChannelID:   "chan789",
				SHA256:      "abcdef1234567890",
				CreatedAt:   "2026-01-15T10:30:00Z",
			},
		},
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var restored MetadataFile
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if len(restored.Emotes) != 1 {
		t.Fatalf("expected 1 emote after roundtrip, got %d", len(restored.Emotes))
	}
	r := restored.Emotes[0]
	if r.ID != orig.Emotes[0].ID {
		t.Fatalf("ID mismatch: %s vs %s", r.ID, orig.Emotes[0].ID)
	}
	if r.Name != orig.Emotes[0].Name {
		t.Fatalf("Name mismatch: %s vs %s", r.Name, orig.Emotes[0].Name)
	}
	if len(r.Tags) != 3 {
		t.Fatalf("Tags length mismatch: got %d", len(r.Tags))
	}
	for i, tag := range r.Tags {
		if tag != orig.Emotes[0].Tags[i] {
			t.Fatalf("Tag[%d] mismatch: %s vs %s", i, tag, orig.Emotes[0].Tags[i])
		}
	}
}

func TestSha256Hash(t *testing.T) {
	data := []byte("hello world")
	hash := sha256Hash(data)

	// Verify it's a proper hex string of 64 chars (SHA256 produces 32 bytes)
	if len(hash) != 64 {
		t.Fatalf("expected 64-char hex string, got %d chars", len(hash))
	}
	for _, c := range hash {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("non-hex character in hash: %c", c)
		}
	}

	// Verify determinism
	hash2 := sha256Hash(data)
	if hash != hash2 {
		t.Fatal("sha256Hash is not deterministic")
	}

	// Verify different data produces different hash
	hash3 := sha256Hash([]byte("different"))
	if hash == hash3 {
		t.Fatal("different data should produce different hash")
	}

	// Cross-verify against crypto/sha256
	expected := fmt.Sprintf("%x", sha256.Sum256(data))
	if hash != expected {
		t.Fatalf("hash mismatch: got %s, want %s", hash, expected)
	}
}

func TestGenerateUUID(t *testing.T) {
	// Test uniqueness
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		u := generateUUID()
		if seen[u] {
			t.Fatalf("duplicate UUID generated: %s", u)
		}
		seen[u] = true

		// Verify format: 8-4-4-4-12 hex digits
		if len(u) != 36 {
			t.Fatalf("expected 36-char UUID, got %d: %s", len(u), u)
		}
		if u[8] != '-' || u[13] != '-' || u[18] != '-' || u[23] != '-' {
			t.Fatalf("UUID has wrong dash positions: %s", u)
		}

		// Check version 4 (char at position 14 is '4')
		if u[14] != '4' {
			t.Fatalf("UUID is not version 4 (char at pos 14 should be '4'): %s", u)
		}
	}
}

func TestRateLimiter_MaxPerMin(t *testing.T) {
	rl := &rateLimiter{
		MaxPerMin: 3,
		Cooldown:  0,
	}

	// First 3 calls should be allowed
	for i := 0; i < 3; i++ {
		if !rl.Allow() {
			t.Fatalf("call %d should be allowed", i+1)
		}
	}

	// 4th call should be rejected (maxPerMin reached)
	if rl.Allow() {
		t.Fatal("4th call should be rejected (maxPerMin=3)")
	}
}

func TestRateLimiter_Cooldown(t *testing.T) {
	rl := &rateLimiter{
		MaxPerMin: 100,
		Cooldown:  100 * time.Millisecond,
	}

	if !rl.Allow() {
		t.Fatal("first call should be allowed")
	}

	if rl.Allow() {
		t.Fatal("second immediate call should be rejected (cooldown)")
	}

	// Wait for cooldown to expire
	time.Sleep(150 * time.Millisecond)

	if !rl.Allow() {
		t.Fatal("third call after cooldown should be allowed")
	}
}
