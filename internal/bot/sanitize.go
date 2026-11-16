package bot

import (
	"regexp"
	"strings"
)

var (
	// annotationLineRe matches a line written in one of the bot's own prompt
	// formats for media and embeds ("[Image 1: ...]", "[Audio 2 transcript
	// (...): ...]", "[Video 1 (...): ...]", "[Sticker: ...]", "[Embed ...]",
	// "[Attached Image 1: ...]", "attachments: ..."). The model copies these
	// from the prompt; they are never part of a reply.
	annotationLineRe = regexp.MustCompile(`^(?:attachments:|\[(?:(?:Image|Audio|Video) \d+[\]:\s(]|Sticker:|Embed[\]:\s]|Attached (?:Image|Audio|Video|Sticker)\b))`)
	// toolMarkupLineRe matches a line that starts with a tool-call tag a
	// model leaked as text: <function_calls>, <tool_call>, <parameter
	// name="...">, a namespaced <xxx:invoke ...> or </xxx:parameter>, and
	// their closing forms. Only these tag names count, so ordinary text
	// with angle brackets (<div>, a < b, Vec<T>, <@id>) is kept.
	toolMarkupLineRe = regexp.MustCompile(`^</?(?:[A-Za-z][\w.-]*:)?(?:function_calls|function_call|function_results|invoke|parameter|tool_calls|tool_call|tool_use|tool_result)(?:[\s/>]|$)`)
)

// sanitizeReply removes lines a reply must never contain: copies of the
// prompt's media/embed annotation formats and leaked tool-call markup. Lines
// inside fenced code blocks are kept as written. It returns the trimmed text
// and how many lines were removed; an empty result means there is nothing
// left to send.
func sanitizeReply(text string) (string, int) {
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	removed := 0
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			kept = append(kept, line)
			continue
		}
		if !inFence && (annotationLineRe.MatchString(trimmed) || toolMarkupLineRe.MatchString(trimmed)) {
			removed++
			continue
		}
		kept = append(kept, line)
	}
	if removed == 0 {
		return strings.TrimSpace(text), 0
	}
	return strings.TrimSpace(strings.Join(kept, "\n")), removed
}
