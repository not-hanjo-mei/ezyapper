package llmjson

import (
	"strings"
	"testing"
)

func TestExtractJSON_ArrayPreferred(t *testing.T) {
	raw := "Here you go:\n```json\n[{\"a\":1}]\n```\n"
	got := ExtractJSON(raw)
	if !strings.HasPrefix(got, "[") {
		t.Fatalf("expected array, got %q", got)
	}
}

func TestExtractJSON_Object(t *testing.T) {
	raw := "note {\"x\": true} trailing"
	got := ExtractJSON(raw)
	if got != `{"x": true}` {
		t.Fatalf("got %q", got)
	}
}

func TestDecode_Validate(t *testing.T) {
	type sample struct {
		Name string `json:"name"`
	}
	v, err := Decode[sample](`{"name":"ok"}`, func(s *sample) error {
		if s.Name == "" {
			return SchemaError("name required", nil)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if v.Name != "ok" {
		t.Fatalf("name=%q", v.Name)
	}
}

func TestDecode_Empty(t *testing.T) {
	_, err := Decode[map[string]any]("   ", nil)
	if !IsParseError(err) {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestRequiredKeysPresent(t *testing.T) {
	err := RequiredKeysPresent(`{"a":1,"b":2}`, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	err = RequiredKeysPresent(`{"a":1}`, []string{"a", "b"})
	if !IsSchemaError(err) {
		t.Fatalf("expected schema error, got %v", err)
	}
}

func TestValidateArgs_RequiredAndTypes(t *testing.T) {
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"guild_id": map[string]any{"type": "string"},
			"limit":    map[string]any{"type": "integer"},
		},
		"required": []any{"guild_id"},
	}

	if err := ValidateArgsAgainstParameters(map[string]any{"guild_id": "1", "limit": float64(3)}, params); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if err := ValidateArgsAgainstParameters(map[string]any{"limit": float64(1)}, params); err == nil {
		t.Fatal("expected missing required")
	}
	if err := ValidateArgsAgainstParameters(map[string]any{"guild_id": "1", "limit": 3.5}, params); err == nil {
		t.Fatal("expected integer reject for 3.5")
	}
	if err := ValidateArgsAgainstParameters(map[string]any{"guild_id": "1", "limit": float64(3)}, params); err != nil {
		t.Fatalf("3.0 should be ok: %v", err)
	}
}

func TestValidateArgs_ExtraKeysAllowed(t *testing.T) {
	params := map[string]any{
		"type":       "object",
		"properties": map[string]any{"a": map[string]any{"type": "string"}},
		"required":   []any{"a"},
	}
	err := ValidateArgsAgainstParameters(map[string]any{"a": "x", "extra": 1}, params)
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateArgs_NilSchema(t *testing.T) {
	if err := ValidateArgsAgainstParameters(map[string]any{"a": 1}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeSlice(t *testing.T) {
	type item struct {
		ID string `json:"id"`
	}
	items, err := DecodeSlice[item](`[{"id":"1"},{"id":"2"}]`, func(i *item) error {
		if i.ID == "" {
			return SchemaError("id required", nil)
		}
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("len=%d", len(items))
	}
}
