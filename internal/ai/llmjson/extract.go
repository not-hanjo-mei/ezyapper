package llmjson

import "strings"

func ExtractJSON(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}

	if idx := strings.Index(content, "["); idx >= 0 {
		endIdx := strings.LastIndex(content, "]")
		if endIdx > idx {
			return strings.TrimSpace(content[idx : endIdx+1])
		}
	}

	if idx := strings.Index(content, "{"); idx >= 0 {
		endIdx := strings.LastIndex(content, "}")
		if endIdx > idx {
			return strings.TrimSpace(content[idx : endIdx+1])
		}
	}

	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	return strings.TrimSpace(content)
}
