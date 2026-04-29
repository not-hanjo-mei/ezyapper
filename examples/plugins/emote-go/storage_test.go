package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
			{ID: "abc", Name: "happy_cat"},
			{ID: "def", Name: "sad_dog"},
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

func TestAddEmote(t *testing.T) {
	tmpDir := t.TempDir()
	s := NewStorage(tmpDir)
	guildID := "g-add"

	e1 := EmoteEntry{ID: "e1", Name: "first"}
	e2 := EmoteEntry{ID: "e2", Name: "second"}

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

	if !s.CheckBlacklist("g", "wl-chan", "user1", nil, []string{"wl-chan"}, nil) {
		t.Error("whitelisted channel should pass (no blacklist)")
	}

	if s.CheckBlacklist("g", "wl-chan", "user1", []string{"wl-chan"}, []string{"wl-chan"}, nil) {
		t.Error("channel in both whitelist and blacklist should be rejected")
	}

	if s.CheckBlacklist("g", "random-chan", "user1", nil, []string{"wl-chan"}, nil) {
		t.Error("non-whitelisted channel should be rejected when whitelist is set")
	}
}

func TestCheckBlacklist_Blacklist(t *testing.T) {
	s := NewStorage(t.TempDir())

	if s.CheckBlacklist("g", "bad-chan", "user1", []string{"bad-chan", "also-bad"}, nil, nil) {
		t.Error("blacklisted channel should be rejected")
	}

	if s.CheckBlacklist("g", "good-chan", "bad-user", nil, nil, []string{"bad-user", "spammer"}) {
		t.Error("blacklisted user should be rejected")
	}

	if !s.CheckBlacklist("g", "good-chan", "good-user", []string{"other-bad"}, nil, []string{"other-spammer"}) {
		t.Error("non-blacklisted channel and user should be allowed")
	}

	if !s.CheckBlacklist("g", "any-chan", "any-user", nil, nil, nil) {
		t.Error("should be allowed when no blacklists are set")
	}
}

func TestSaveMetadata_Atomicity(t *testing.T) {
	tmpDir := t.TempDir()
	s := NewStorage(tmpDir)
	guildID := "g-atomic"

	mf := &MetadataFile{
		Emotes: []EmoteEntry{{ID: "atomic-test", Name: "test"}},
	}
	if err := s.SaveMetadata(guildID, mf); err != nil {
		t.Fatalf("SaveMetadata failed: %v", err)
	}

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

func TestMd5Hash(t *testing.T) {
	hash := md5Hash("hello world")

	if len(hash) != 32 {
		t.Fatalf("expected 32-char hex string, got %d chars", len(hash))
	}
	for _, c := range hash {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("non-hex character in hash: %c", c)
		}
	}

	hash2 := md5Hash("hello world")
	if hash != hash2 {
		t.Fatal("md5Hash is not deterministic")
	}

	hash3 := md5Hash("different")
	if hash == hash3 {
		t.Fatal("different data should produce different hash")
	}
}
