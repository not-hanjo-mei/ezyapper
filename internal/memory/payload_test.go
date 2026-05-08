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
		Keywords:   []string{"golang", "backend"},
		Confidence: 0.88,
		CreatedAt:  now,
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
	if got.MemoryType != input.MemoryType || got.Content != input.Content {
		t.Fatalf("content fields mismatch: got=%+v", got)
	}
	if !reflect.DeepEqual(got.Keywords, input.Keywords) {
		t.Fatalf("keywords mismatch: got=%v want=%v", got.Keywords, input.Keywords)
	}
}

func TestMemoryPayloadRoundTrip_PreservesMentionedFields(t *testing.T) {
	qc := &QdrantClient{}
	now := time.Now().UTC().Truncate(time.Second)

	input := &Record{
		ID:                  "mem-2",
		UserID:              "123",
		GuildID:             "456",
		ChannelID:           "789",
		MemoryType:          TypeFact,
		Content:             "user mentioned someone",
		Keywords:            []string{"mention"},
		Confidence:          0.9,
		CreatedAt:           now,
		MentionedUserIDs:    []string{"user-1", "user-2"},
		MentionedChannelIDs: []string{"channel-1"},
	}

	payload, err := qc.memoryToPayload(input)
	if err != nil {
		t.Fatalf("memoryToPayload failed: %v", err)
	}
	got, err := qc.payloadToMemory(payload, input.ID)
	if err != nil {
		t.Fatalf("payloadToMemory failed: %v", err)
	}

	if !reflect.DeepEqual(got.MentionedUserIDs, input.MentionedUserIDs) {
		t.Fatalf("mentioned_user_ids mismatch: got=%v want=%v", got.MentionedUserIDs, input.MentionedUserIDs)
	}
	if !reflect.DeepEqual(got.MentionedChannelIDs, input.MentionedChannelIDs) {
		t.Fatalf("mentioned_channel_ids mismatch: got=%v want=%v", got.MentionedChannelIDs, input.MentionedChannelIDs)
	}
}

func TestMemoryPayloadRoundTrip_EmptyMentionedFields(t *testing.T) {
	qc := &QdrantClient{}
	now := time.Now().UTC().Truncate(time.Second)

	input := &Record{
		ID:                  "mem-3",
		UserID:              "123",
		GuildID:             "456",
		ChannelID:           "789",
		MemoryType:          TypeFact,
		Content:             "no mentions",
		Keywords:            []string{},
		Confidence:          0.9,
		CreatedAt:           now,
		MentionedUserIDs:    nil,
		MentionedChannelIDs: nil,
	}

	payload, err := qc.memoryToPayload(input)
	if err != nil {
		t.Fatalf("memoryToPayload failed: %v", err)
	}

	if _, ok := payload["mentioned_user_ids"]; ok {
		t.Fatal("mentioned_user_ids should not be in payload for empty input")
	}
	if _, ok := payload["mentioned_channel_ids"]; ok {
		t.Fatal("mentioned_channel_ids should not be in payload for empty input")
	}

	got, err := qc.payloadToMemory(payload, input.ID)
	if err != nil {
		t.Fatalf("payloadToMemory failed: %v", err)
	}

	if len(got.MentionedUserIDs) != 0 {
		t.Fatalf("expected empty MentionedUserIDs, got: %v", got.MentionedUserIDs)
	}
	if len(got.MentionedChannelIDs) != 0 {
		t.Fatalf("expected empty MentionedChannelIDs, got: %v", got.MentionedChannelIDs)
	}
}

func TestRelationshipPayloadRoundTrip(t *testing.T) {
	qc := &QdrantClient{}
	now := time.Now().UTC().Truncate(time.Second)

	input := &Relationship{
		ID:                "rel-1",
		UserA:             "user-1",
		UserB:             "user-2",
		Type:              RelMention,
		InteractionCount:  5,
		LastInteractionAt: now,
		ChannelIDs:        []string{"channel-1", "channel-2"},
		Weight:            2.5,
	}

	payload, err := qc.relationshipToPayload(input)
	if err != nil {
		t.Fatalf("relationshipToPayload failed: %v", err)
	}
	got, err := qc.payloadToRelationship(payload, input.ID)
	if err != nil {
		t.Fatalf("payloadToRelationship failed: %v", err)
	}

	if got.UserA != input.UserA || got.UserB != input.UserB {
		t.Fatalf("user fields mismatch: got=%+v", got)
	}
	if got.Type != input.Type {
		t.Fatalf("type mismatch: got=%v want=%v", got.Type, input.Type)
	}
	if got.InteractionCount != input.InteractionCount {
		t.Fatalf("interaction_count mismatch: got=%v want=%v", got.InteractionCount, input.InteractionCount)
	}
	if !got.LastInteractionAt.Equal(input.LastInteractionAt) {
		t.Fatalf("last_interaction_at mismatch: got=%v want=%v", got.LastInteractionAt, input.LastInteractionAt)
	}
	if got.Weight != input.Weight {
		t.Fatalf("weight mismatch: got=%v want=%v", got.Weight, input.Weight)
	}
	if !reflect.DeepEqual(got.ChannelIDs, input.ChannelIDs) {
		t.Fatalf("channel_ids mismatch: got=%v want=%v", got.ChannelIDs, input.ChannelIDs)
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

func TestPayloadToRelationship_RejectsLegacyPayloadWithoutSchemaVersion(t *testing.T) {
	qc := &QdrantClient{}
	payload := map[string]*qdrant.Value{
		"user_a": {Kind: &qdrant.Value_StringValue{StringValue: "user-1"}},
	}

	if _, err := qc.payloadToRelationship(payload, "rel-legacy"); err == nil {
		t.Fatal("expected schema validation error for legacy relationship payload")
	}
}

func TestRelationshipPayloadRoundTrip_EmptyChannels(t *testing.T) {
	qc := &QdrantClient{}
	now := time.Now().UTC().Truncate(time.Second)

	input := &Relationship{
		ID:                "rel-empty-ch",
		UserA:             "user-1",
		UserB:             "user-2",
		Type:              RelMention,
		InteractionCount:  1,
		LastInteractionAt: now,
		ChannelIDs:        nil,
		Weight:            1.0,
	}

	payload, err := qc.relationshipToPayload(input)
	if err != nil {
		t.Fatalf("relationshipToPayload failed: %v", err)
	}

	if _, ok := payload["channel_ids"]; ok {
		t.Fatal("channel_ids should not be in payload for empty input")
	}

	got, err := qc.payloadToRelationship(payload, input.ID)
	if err != nil {
		t.Fatalf("payloadToRelationship failed: %v", err)
	}

	if len(got.ChannelIDs) != 0 {
		t.Fatalf("expected empty ChannelIDs, got: %v", got.ChannelIDs)
	}
}

func TestRelationshipPayloadRoundTrip_ManyChannels(t *testing.T) {
	qc := &QdrantClient{}
	now := time.Now().UTC().Truncate(time.Second)

	channelIDs := make([]string, 50)
	for i := range channelIDs {
		channelIDs[i] = "channel-" + string(rune('a'+i%26)) + string(rune('0'+i%10))
	}

	input := &Relationship{
		ID:                "rel-many-ch",
		UserA:             "user-1",
		UserB:             "user-2",
		Type:              RelMention,
		InteractionCount:  10,
		LastInteractionAt: now,
		ChannelIDs:        channelIDs,
		Weight:            5.0,
	}

	payload, err := qc.relationshipToPayload(input)
	if err != nil {
		t.Fatalf("relationshipToPayload failed: %v", err)
	}
	got, err := qc.payloadToRelationship(payload, input.ID)
	if err != nil {
		t.Fatalf("payloadToRelationship failed: %v", err)
	}

	if len(got.ChannelIDs) != 50 {
		t.Fatalf("expected 50 ChannelIDs, got %d", len(got.ChannelIDs))
	}
	if !reflect.DeepEqual(got.ChannelIDs, input.ChannelIDs) {
		t.Fatalf("channel IDs mismatch")
	}
}

func TestMemoryPayloadRoundTrip_MaxMentions(t *testing.T) {
	qc := &QdrantClient{}
	now := time.Now().UTC().Truncate(time.Second)

	userIDs := make([]string, 100)
	for i := range userIDs {
		userIDs[i] = "user-" + string(rune('0'+i%10))
	}

	input := &Record{
		ID:                  "mem-max",
		UserID:              "123",
		GuildID:             "456",
		ChannelID:           "789",
		MemoryType:          TypeFact,
		Content:             "max mentions test",
		Keywords:            []string{},
		Confidence:          0.9,
		CreatedAt:           now,
		MentionedUserIDs:    userIDs,
		MentionedChannelIDs: []string{"ch-1", "ch-2"},
	}

	payload, err := qc.memoryToPayload(input)
	if err != nil {
		t.Fatalf("memoryToPayload failed: %v", err)
	}
	got, err := qc.payloadToMemory(payload, input.ID)
	if err != nil {
		t.Fatalf("payloadToMemory failed: %v", err)
	}

	if len(got.MentionedUserIDs) != 100 {
		t.Fatalf("expected 100 MentionedUserIDs, got %d", len(got.MentionedUserIDs))
	}
}

func TestPayloadSchemaVersion_Validation(t *testing.T) {
	tests := []struct {
		name    string
		version int64
		wantErr bool
	}{
		{name: "version 0 invalid", version: 0, wantErr: true},
		{name: "version 1 invalid", version: 1, wantErr: true},
		{name: "version 2 valid", version: 2, wantErr: false},
		{name: "version 3 valid", version: 3, wantErr: false},
		{name: "version 4 valid", version: 4, wantErr: false},
		{name: "version 5 invalid", version: 5, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := map[string]*qdrant.Value{
				"schema_version": {Kind: &qdrant.Value_IntegerValue{IntegerValue: tt.version}},
			}
			err := validatePayloadSchema(payload)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePayloadSchema(version=%d) error=%v, wantErr=%v", tt.version, err, tt.wantErr)
			}
		})
	}
}

func TestPayloadSchemaVersion_Missing(t *testing.T) {
	payload := map[string]*qdrant.Value{}
	if err := validatePayloadSchema(payload); err == nil {
		t.Fatal("expected error for missing schema_version")
	}
}

func TestPayloadSchemaVersion_Nil(t *testing.T) {
	payload := map[string]*qdrant.Value{
		"schema_version": nil,
	}
	if err := validatePayloadSchema(payload); err == nil {
		t.Fatal("expected error for nil schema_version value")
	}
}

func BenchmarkPayloadConversion(b *testing.B) {
	qc := &QdrantClient{}
	now := time.Now().UTC().Truncate(time.Second)

	input := &Record{
		ID:                  "bench-1",
		UserID:              "123",
		GuildID:             "456",
		ChannelID:           "789",
		MemoryType:          TypeFact,
		Content:             "benchmark payload test with some content",
		Keywords:            []string{"bench", "test", "payload"},
		Confidence:          0.95,
		CreatedAt:           now,
		MentionedUserIDs:    []string{"user-a", "user-b", "user-c"},
		MentionedChannelIDs: []string{"ch-x", "ch-y"},
	}

	for b.Loop() {
		payload, _ := qc.memoryToPayload(input)
		_, _ = qc.payloadToMemory(payload, input.ID)
	}
}
