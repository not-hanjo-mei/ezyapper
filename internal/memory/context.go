package memory

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// FormatMemoriesForContext formats memories for LLM context with scores and recency markers.
// Sorts by type priority (facts first) then by decayed score within each type.
func FormatMemoriesForContext(memories []*Record, decayRate float64, now time.Time) string {
	if len(memories) == 0 {
		return ""
	}

	// Separate key facts from other memories
	facts := make([]*Record, 0)
	others := make([]*Record, 0)

	for _, m := range memories {
		if m.MemoryType == TypeFact && m.ImportanceScore >= 0.5 {
			facts = append(facts, m)
		} else {
			others = append(others, m)
		}
	}

	var b strings.Builder

	// Key facts section
	if len(facts) > 0 {
		b.WriteString("<key_facts>\n")
		SortByDecayedScore(facts, decayRate, now)
		for _, m := range facts {
			fmt.Fprintf(&b, "[fact | score=%.1f | %s] %s\n",
				m.ImportanceScore,
				formatTimeAgo(m.CreatedAt, now),
				m.Content)
		}
		b.WriteString("</key_facts>\n")
	}

	// Recent/other memories section
	allSorted := make([]*Record, len(others))
	copy(allSorted, others)
	SortByDecayedScore(allSorted, decayRate, now)
	sortByTypeThenScore(allSorted)

	if len(allSorted) > 0 {
		if len(facts) > 0 {
			b.WriteString("\n")
		}
		b.WriteString("<recent_memories>\n")
		for _, m := range allSorted {
			fmt.Fprintf(&b, "[%s | %s | %s] %s\n",
				m.MemoryType,
				m.DecayCategory,
				formatTimeAgo(m.CreatedAt, now),
				m.Content)
		}
		b.WriteString("</recent_memories>")
	}

	return b.String()
}

func formatTimeAgo(t time.Time, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dw ago", int(d.Hours()/(24*7)))
	}
}

// sortByTypeThenScore sorts: facts first, then interests, episodes, summaries.
// Within each type, sorted by decayed score descending (must be pre-sorted).
func sortByTypeThenScore(records []*Record) {
	typePriority := map[Type]int{
		TypeFact: 0, TypeInterest: 1, TypeEpisode: 2, TypeSummary: 3,
	}
	sort.SliceStable(records, func(i, j int) bool {
		pi := typePriority[records[i].MemoryType]
		pj := typePriority[records[j].MemoryType]
		if pi != pj {
			return pi < pj
		}
		return records[i].ImportanceScore > records[j].ImportanceScore
	})
}
