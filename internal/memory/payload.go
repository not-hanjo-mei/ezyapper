package memory

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/qdrant/go-client/qdrant"
)

const payloadSchemaVersion = 2

func (qc *QdrantClient) memoryToPayload(memory *Record) map[string]*qdrant.Value {
	payload := make(map[string]*qdrant.Value)
	payload["schema_version"] = &qdrant.Value{Kind: &qdrant.Value_IntegerValue{IntegerValue: payloadSchemaVersion}}

	payload["user_id"] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: memory.UserID}}
	payload["guild_id"] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: memory.GuildID}}
	payload["channel_id"] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: memory.ChannelID}}
	payload["memory_type"] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: string(memory.MemoryType)}}
	payload["content"] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: memory.Content}}
	payload["summary"] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: memory.Summary}}
	payload["confidence"] = &qdrant.Value{Kind: &qdrant.Value_DoubleValue{DoubleValue: memory.Confidence}}
	payload["created_at"] = &qdrant.Value{Kind: &qdrant.Value_DoubleValue{DoubleValue: float64(memory.CreatedAt.UnixMilli()) / 1000.0}}
	payload["updated_at"] = &qdrant.Value{Kind: &qdrant.Value_DoubleValue{DoubleValue: float64(memory.UpdatedAt.UnixMilli()) / 1000.0}}
	payload["access_count"] = &qdrant.Value{Kind: &qdrant.Value_IntegerValue{IntegerValue: int64(memory.AccessCount)}}

	var keywordValues []*qdrant.Value
	for _, kw := range memory.Keywords {
		keywordValues = append(keywordValues, &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: kw}})
	}
	payload["keywords"] = &qdrant.Value{Kind: &qdrant.Value_ListValue{ListValue: &qdrant.ListValue{Values: keywordValues}}}

	// Persist message range to preserve source boundaries.
	payload["message_range"] = &qdrant.Value{Kind: &qdrant.Value_ListValue{ListValue: &qdrant.ListValue{Values: []*qdrant.Value{
		{Kind: &qdrant.Value_IntegerValue{IntegerValue: int64(memory.MessageRange[0])}},
		{Kind: &qdrant.Value_IntegerValue{IntegerValue: int64(memory.MessageRange[1])}},
	}}}}

	metadata := memory.Metadata
	if metadata == nil {
		metadata = make(map[string]interface{})
	}
	if metadataJSON, err := json.Marshal(metadata); err == nil {
		payload["metadata_json"] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: string(metadataJSON)}}
	}

	return payload
}

func (qc *QdrantClient) payloadToMemory(payload map[string]*qdrant.Value, id string) (*Record, error) {
	memory := &Record{ID: id}

	if err := validatePayloadSchema(payload); err != nil {
		return nil, fmt.Errorf("invalid memory payload schema: %w", err)
	}

	var err error
	if memory.UserID, err = getRequiredString(payload, "user_id"); err != nil {
		return nil, err
	}
	if memory.GuildID, err = getRequiredString(payload, "guild_id"); err != nil {
		return nil, err
	}
	if memory.ChannelID, err = getRequiredString(payload, "channel_id"); err != nil {
		return nil, err
	}
	memoryType, err := getRequiredString(payload, "memory_type")
	if err != nil {
		return nil, err
	}
	memory.MemoryType = Type(memoryType)
	if memory.Content, err = getRequiredString(payload, "content"); err != nil {
		return nil, err
	}
	if memory.Summary, err = getRequiredString(payload, "summary"); err != nil {
		return nil, err
	}
	confidence, err := getRequiredDouble(payload, "confidence")
	if err != nil {
		return nil, err
	}
	memory.Confidence = confidence
	createdAt, err := getRequiredDouble(payload, "created_at")
	if err != nil {
		return nil, err
	}
	memory.CreatedAt = time.UnixMilli(int64(createdAt * 1000))
	updatedAt, err := getRequiredDouble(payload, "updated_at")
	if err != nil {
		return nil, err
	}
	memory.UpdatedAt = time.UnixMilli(int64(updatedAt * 1000))
	accessCount, err := getRequiredInt(payload, "access_count")
	if err != nil {
		return nil, err
	}
	memory.AccessCount = int(accessCount)

	keywords, err := getRequiredList(payload, "keywords")
	if err != nil {
		return nil, err
	}
	for _, kw := range keywords {
		memory.Keywords = append(memory.Keywords, kw.GetStringValue())
	}

	messageRangeValues, err := getRequiredList(payload, "message_range")
	if err != nil {
		return nil, err
	}
	if len(messageRangeValues) != 2 {
		return nil, fmt.Errorf("message_range must contain exactly 2 elements")
	}
	memory.MessageRange = [2]int{int(messageRangeValues[0].GetIntegerValue()), int(messageRangeValues[1].GetIntegerValue())}

	metadataJSON, err := getRequiredString(payload, "metadata_json")
	if err != nil {
		return nil, err
	}
	var metadata map[string]interface{}
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		return nil, fmt.Errorf("failed to parse metadata_json: %w", err)
	}
	memory.Metadata = metadata

	return memory, nil
}

func (qc *QdrantClient) profileToPayload(profile *Profile) map[string]*qdrant.Value {
	payload := make(map[string]*qdrant.Value)
	payload["schema_version"] = &qdrant.Value{Kind: &qdrant.Value_IntegerValue{IntegerValue: payloadSchemaVersion}}

	payload["user_id"] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: profile.UserID}}
	payload["last_summary"] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: profile.LastSummary}}
	payload["personality_summary"] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: profile.PersonalitySummary}}
	payload["message_count"] = &qdrant.Value{Kind: &qdrant.Value_IntegerValue{IntegerValue: int64(profile.MessageCount)}}
	payload["memory_count"] = &qdrant.Value{Kind: &qdrant.Value_IntegerValue{IntegerValue: int64(profile.MemoryCount)}}
	payload["first_seen_at"] = &qdrant.Value{Kind: &qdrant.Value_DoubleValue{DoubleValue: float64(profile.FirstSeenAt.UnixMilli()) / 1000.0}}
	payload["last_active_at"] = &qdrant.Value{Kind: &qdrant.Value_DoubleValue{DoubleValue: float64(profile.LastActiveAt.UnixMilli()) / 1000.0}}
	payload["last_consolidated_at"] = &qdrant.Value{Kind: &qdrant.Value_DoubleValue{DoubleValue: float64(profile.LastConsolidatedAt.UnixMilli()) / 1000.0}}

	var traitValues []*qdrant.Value
	for _, t := range profile.Traits {
		traitValues = append(traitValues, &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: t}})
	}
	payload["traits"] = &qdrant.Value{Kind: &qdrant.Value_ListValue{ListValue: &qdrant.ListValue{Values: traitValues}}}

	var interestValues []*qdrant.Value
	for _, i := range profile.Interests {
		interestValues = append(interestValues, &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: i}})
	}
	payload["interests"] = &qdrant.Value{Kind: &qdrant.Value_ListValue{ListValue: &qdrant.ListValue{Values: interestValues}}}

	facts := profile.Facts
	if facts == nil {
		facts = make(map[string]string)
	}
	if factsJSON, err := json.Marshal(facts); err == nil {
		payload["facts_json"] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: string(factsJSON)}}
	}

	prefs := profile.Preferences
	if prefs == nil {
		prefs = make(map[string]string)
	}
	if prefsJSON, err := json.Marshal(prefs); err == nil {
		payload["preferences_json"] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: string(prefsJSON)}}
	}

	return payload
}

func (qc *QdrantClient) payloadToProfile(payload map[string]*qdrant.Value, userID string) (*Profile, error) {
	if err := validatePayloadSchema(payload); err != nil {
		return nil, fmt.Errorf("invalid profile payload schema: %w", err)
	}

	profile := &Profile{
		UserID:      userID,
		Facts:       make(map[string]string),
		Preferences: make(map[string]string),
		Interests:   []string{},
		Traits:      []string{},
	}

	var err error
	if profile.LastSummary, err = getRequiredString(payload, "last_summary"); err != nil {
		return nil, err
	}
	if profile.PersonalitySummary, err = getRequiredString(payload, "personality_summary"); err != nil {
		return nil, err
	}
	messageCount, err := getRequiredInt(payload, "message_count")
	if err != nil {
		return nil, err
	}
	profile.MessageCount = int(messageCount)
	memoryCount, err := getRequiredInt(payload, "memory_count")
	if err != nil {
		return nil, err
	}
	profile.MemoryCount = int(memoryCount)
	firstSeenAt, err := getRequiredDouble(payload, "first_seen_at")
	if err != nil {
		return nil, err
	}
	profile.FirstSeenAt = time.UnixMilli(int64(firstSeenAt * 1000))
	lastActiveAt, err := getRequiredDouble(payload, "last_active_at")
	if err != nil {
		return nil, err
	}
	profile.LastActiveAt = time.UnixMilli(int64(lastActiveAt * 1000))
	lastConsolidatedAt, err := getRequiredDouble(payload, "last_consolidated_at")
	if err != nil {
		return nil, err
	}
	profile.LastConsolidatedAt = time.UnixMilli(int64(lastConsolidatedAt * 1000))

	traits, err := getRequiredList(payload, "traits")
	if err != nil {
		return nil, err
	}
	for _, t := range traits {
		profile.Traits = append(profile.Traits, t.GetStringValue())
	}
	interests, err := getRequiredList(payload, "interests")
	if err != nil {
		return nil, err
	}
	for _, i := range interests {
		profile.Interests = append(profile.Interests, i.GetStringValue())
	}

	factsJSON, err := getRequiredString(payload, "facts_json")
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(factsJSON), &profile.Facts); err != nil {
		return nil, fmt.Errorf("failed to parse facts_json: %w", err)
	}
	prefsJSON, err := getRequiredString(payload, "preferences_json")
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(prefsJSON), &profile.Preferences); err != nil {
		return nil, fmt.Errorf("failed to parse preferences_json: %w", err)
	}

	return profile, nil
}

func validatePayloadSchema(payload map[string]*qdrant.Value) error {
	v, ok := payload["schema_version"]
	if !ok || v == nil {
		return fmt.Errorf("missing schema_version")
	}
	if v.GetIntegerValue() != payloadSchemaVersion {
		return fmt.Errorf("unsupported schema_version: %d", v.GetIntegerValue())
	}
	return nil
}

func getRequiredString(payload map[string]*qdrant.Value, key string) (string, error) {
	v, ok := payload[key]
	if !ok || v == nil {
		return "", fmt.Errorf("missing required payload key: %s", key)
	}
	return v.GetStringValue(), nil
}

func getRequiredDouble(payload map[string]*qdrant.Value, key string) (float64, error) {
	v, ok := payload[key]
	if !ok || v == nil {
		return 0, fmt.Errorf("missing required payload key: %s", key)
	}
	return v.GetDoubleValue(), nil
}

func getRequiredInt(payload map[string]*qdrant.Value, key string) (int64, error) {
	v, ok := payload[key]
	if !ok || v == nil {
		return 0, fmt.Errorf("missing required payload key: %s", key)
	}
	return v.GetIntegerValue(), nil
}

func getRequiredList(payload map[string]*qdrant.Value, key string) ([]*qdrant.Value, error) {
	v, ok := payload[key]
	if !ok || v == nil {
		return nil, fmt.Errorf("missing required payload key: %s", key)
	}
	list := v.GetListValue()
	if list == nil {
		return nil, fmt.Errorf("payload key %q is not a list", key)
	}
	return list.GetValues(), nil
}
