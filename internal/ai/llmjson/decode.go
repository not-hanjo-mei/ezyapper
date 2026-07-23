package llmjson

import (
	"encoding/json"
	"fmt"
)

func Decode[T any](raw string, validate func(*T) error) (*T, error) {
	content := SanitizeUTF8(ExtractJSON(raw))
	if content == "" {
		return nil, ParseError("empty json content after extract", nil)
	}
	var v T
	if err := json.Unmarshal([]byte(content), &v); err != nil {
		return nil, ParseError("failed to unmarshal llm json", err)
	}
	if validate != nil {
		if err := validate(&v); err != nil {
			if IsOutputError(err) {
				return nil, err
			}
			return nil, SchemaError("llm json schema validation failed", err)
		}
	}
	return &v, nil
}

func DecodeSlice[T any](raw string, validateElem func(*T) error, validateSlice func([]T) error) ([]T, error) {
	content := SanitizeUTF8(ExtractJSON(raw))
	if content == "" {
		return nil, ParseError("empty json content after extract", nil)
	}
	var items []T
	if err := json.Unmarshal([]byte(content), &items); err != nil {
		return nil, ParseError("failed to unmarshal llm json array", err)
	}
	if validateElem != nil {
		for i := range items {
			if err := validateElem(&items[i]); err != nil {
				if IsOutputError(err) {
					return nil, err
				}
				return nil, SchemaError(fmt.Sprintf("llm json schema validation failed at index %d", i), err)
			}
		}
	}
	if validateSlice != nil {
		if err := validateSlice(items); err != nil {
			if IsOutputError(err) {
				return nil, err
			}
			return nil, SchemaError("llm json schema validation failed for slice", err)
		}
	}
	return items, nil
}

func RequiredKeysPresent(rawJSON string, required []string) error {
	content := SanitizeUTF8(ExtractJSON(rawJSON))
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &m); err != nil {
		return ParseError("failed to unmarshal object for required keys", err)
	}
	for _, key := range required {
		if _, ok := m[key]; !ok {
			return SchemaError(fmt.Sprintf("missing required field %q", key), nil)
		}
	}
	return nil
}
