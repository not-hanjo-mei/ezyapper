package memory

import (
	"reflect"
	"testing"
	"time"

	"github.com/qdrant/go-client/qdrant"
)

func TestMemoryPayloadRoundTrip_PreservesExtendedFields(t *testing.T) {
	qc := &QdrantClient{}
	now := time.Now().UTC().Truncate(time.Second)

	input := &Record{
		ID:         "mem-1",
		UserID:     "123",
		GuildID:    "456",
		ChannelID:  "789",
		MemoryType: TypeFact,
		Content:    "user likes golang",
		Summary:    "likes golang",
		Keywords:   []string{"golang", "backend"},
		Metadata: map[string]interface{}{
			"source": "consolidation",
			"vision": true,
		},
		Confidence:   0.88,
		MessageRange: [2]int{10, 20},
		CreatedAt:    now,
		UpdatedAt:    now,
		AccessCount:  3,
	}

	payload, err := qc.memoryToPayload(input)
	if err != nil {
		t.Fatalf("memoryToPayload failed: %v", err)
	}
	got, err := qc.payloadToMemory(payload, input.ID)
	if err != nil {
		t.Fatalf("payloadToMemory failed: %v", err)
	}

	if got.UserID != input.UserID || got.GuildID != input.GuildID || got.ChannelID != input.ChannelID {
		t.Fatalf("identity fields mismatch: got=%+v", got)
	}
	if got.MemoryType != input.MemoryType || got.Content != input.Content || got.Summary != input.Summary {
		t.Fatalf("content fields mismatch: got=%+v", got)
	}
	if !reflect.DeepEqual(got.Keywords, input.Keywords) {
		t.Fatalf("keywords mismatch: got=%v want=%v", got.Keywords, input.Keywords)
	}
	if got.MessageRange != input.MessageRange {
		t.Fatalf("message_range mismatch: got=%v want=%v", got.MessageRange, input.MessageRange)
	}
	if got.Metadata["source"] != input.Metadata["source"] || got.Metadata["vision"] != input.Metadata["vision"] {
		t.Fatalf("metadata mismatch: got=%v want=%v", got.Metadata, input.Metadata)
	}
}

func TestProfilePayloadRoundTrip_PreservesFactsPreferencesInterests(t *testing.T) {
	qc := &QdrantClient{}
	now := time.Now().UTC().Truncate(time.Second)

	input := &Profile{
		UserID:             "123",
		Traits:             []string{"curious", "concise"},
		Facts:              map[string]string{"name": "alice", "location": "tokyo"},
		Preferences:        map[string]string{"language": "go", "editor": "vscode"},
		Interests:          []string{"hiking", "rpg"},
		LastSummary:        "summary",
		PersonalitySummary: "personality",
		MessageCount:       42,
		MemoryCount:        7,
		FirstSeenAt:        now,
		LastActiveAt:       now,
		LastConsolidatedAt: now,
	}

	payload, err := qc.profileToPayload(input)
	if err != nil {
		t.Fatalf("profileToPayload failed: %v", err)
	}
	got, err := qc.payloadToProfile(payload, input.UserID)
	if err != nil {
		t.Fatalf("payloadToProfile failed: %v", err)
	}

	if !reflect.DeepEqual(got.Traits, input.Traits) {
		t.Fatalf("traits mismatch: got=%v want=%v", got.Traits, input.Traits)
	}
	if !reflect.DeepEqual(got.Interests, input.Interests) {
		t.Fatalf("interests mismatch: got=%v want=%v", got.Interests, input.Interests)
	}
	if !reflect.DeepEqual(got.Facts, input.Facts) {
		t.Fatalf("facts mismatch: got=%v want=%v", got.Facts, input.Facts)
	}
	if !reflect.DeepEqual(got.Preferences, input.Preferences) {
		t.Fatalf("preferences mismatch: got=%v want=%v", got.Preferences, input.Preferences)
	}
}

func TestPayloadToProfile_RejectsLegacyPayloadWithoutSchemaVersion(t *testing.T) {
	qc := &QdrantClient{}
	payload := map[string]*qdrant.Value{
		"user_id": {Kind: &qdrant.Value_StringValue{StringValue: "123"}},
	}

	if _, err := qc.payloadToProfile(payload, "123"); err == nil {
		t.Fatal("expected schema validation error for legacy profile payload")
	}
}

func TestPayloadToMemory_RejectsLegacyPayloadWithoutSchemaVersion(t *testing.T) {
	qc := &QdrantClient{}
	payload := map[string]*qdrant.Value{
		"user_id": {Kind: &qdrant.Value_StringValue{StringValue: "123"}},
	}

	if _, err := qc.payloadToMemory(payload, "mem-legacy"); err == nil {
		t.Fatal("expected schema validation error for legacy memory payload")
	}
}
