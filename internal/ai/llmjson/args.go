package llmjson

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"ezyapper/internal/logger"
)

func ValidateArgsAgainstParameters(args map[string]any, parameters any) error {
	schema, ok := normalizeObjectSchema(parameters)
	if !ok {
		if parameters != nil {
			logger.Warnf("[llmjson] tool parameters not a usable object schema; skipping arg schema validation")
		}
		return nil
	}

	props, _ := schema["properties"].(map[string]any)
	required, reqOK := parseRequired(schema["required"])
	if schema["required"] != nil && !reqOK {
		logger.Warnf("[llmjson] tool parameters.required malformed; skipping arg schema validation")
		return nil
	}

	for _, name := range required {
		if _, exists := args[name]; !exists {
			return SchemaError(fmt.Sprintf("missing required argument %q", name), nil)
		}
	}

	if props == nil {
		return nil
	}

	for key, val := range args {
		propSchema, exists := props[key]
		if !exists {
			continue
		}
		propMap, ok := asObject(propSchema)
		if !ok {
			continue
		}
		if err := matchType(key, val, propMap); err != nil {
			return err
		}
	}
	return nil
}

func normalizeObjectSchema(parameters any) (map[string]any, bool) {
	if parameters == nil {
		return nil, false
	}
	if m, ok := asObject(parameters); ok {
		return m, true
	}
	data, err := json.Marshal(parameters)
	if err != nil {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, false
	}
	return m, true
}

func asObject(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func parseRequired(v any) ([]string, bool) {
	if v == nil {
		return nil, true
	}
	arr, ok := v.([]any)
	if !ok {
		if ss, ok := v.([]string); ok {
			return ss, true
		}
		return nil, false
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func matchType(path string, val any, schema map[string]any) error {
	typeName, _ := schema["type"].(string)
	typeName = strings.TrimSpace(typeName)
	if typeName == "" {
		return nil
	}

	switch typeName {
	case "string":
		if _, ok := val.(string); !ok {
			return SchemaError(fmt.Sprintf("argument %q must be a string", path), nil)
		}
	case "boolean":
		if _, ok := val.(bool); !ok {
			return SchemaError(fmt.Sprintf("argument %q must be a boolean", path), nil)
		}
	case "number":
		f, ok := val.(float64)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			return SchemaError(fmt.Sprintf("argument %q must be a number", path), nil)
		}
	case "integer":
		f, ok := val.(float64)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
			return SchemaError(fmt.Sprintf("argument %q must be an integer", path), nil)
		}
	case "object":
		m, ok := val.(map[string]any)
		if !ok {
			return SchemaError(fmt.Sprintf("argument %q must be an object", path), nil)
		}
		if nestedProps, ok := schema["properties"].(map[string]any); ok {
			req, reqOK := parseRequired(schema["required"])
			if schema["required"] != nil && !reqOK {
				return nil
			}
			for _, name := range req {
				if _, exists := m[name]; !exists {
					return SchemaError(fmt.Sprintf("missing required argument %q", path+"."+name), nil)
				}
			}
			for k, child := range m {
				ps, exists := nestedProps[k]
				if !exists {
					continue
				}
				pm, ok := asObject(ps)
				if !ok {
					continue
				}
				if err := matchType(path+"."+k, child, pm); err != nil {
					return err
				}
			}
		}
	case "array":
		arr, ok := val.([]any)
		if !ok {
			return SchemaError(fmt.Sprintf("argument %q must be an array", path), nil)
		}
		if items, ok := asObject(schema["items"]); ok {
			for i, elem := range arr {
				if err := matchType(fmt.Sprintf("%s[%d]", path, i), elem, items); err != nil {
					return err
				}
			}
		}
	case "null":
		if val != nil {
			return SchemaError(fmt.Sprintf("argument %q must be null", path), nil)
		}
	}
	return nil
}
