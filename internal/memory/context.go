package memory

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// FormatMemoriesForContext formats memories for LLM context with scores and recency markers.
// Sorts by type priority (facts first) then by decayed score within each type.
// Memories are classified into four XML blocks:
//  1. <key_facts> — TypeFact with ImportanceScore >= 0.5
//  2. <recent_memories> — all remaining (not facts, mentioned, or channel)
//  3. <mentioned_memories> — memories with MentionedUserIDs (capped by maxMentionedMemories)
//  4. <channel_memories> — memories with MentionedChannelIDs (capped by maxChannelMemories)
func FormatMemoriesForContext(memories []*Record, multiplier float64, now time.Time, maxMentionedMemories int, maxChannelMemories int) string {
	if len(memories) == 0 {
		return ""
	}

	facts := make([]*Record, 0)
	mentioned := make([]*Record, 0)
	channel := make([]*Record, 0)
	others := make([]*Record, 0)

	for _, m := range memories {
		switch {
		case m.MemoryType == TypeFact && m.ImportanceScore >= 0.5:
			facts = append(facts, m)
		case len(m.MentionedUserIDs) > 0:
			mentioned = append(mentioned, m)
		case len(m.MentionedChannelIDs) > 0:
			channel = append(channel, m)
		default:
			others = append(others, m)
		}
	}

	var b strings.Builder

	// Key facts section
	if len(facts) > 0 {
		b.WriteString("<key_facts>\n")
		SortByDecayedScore(facts, multiplier, now)
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
	SortByDecayedScore(allSorted, multiplier, now)
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

	mentionedBlock := formatMentionedMemories(mentioned, multiplier, now, maxMentionedMemories)
	if mentionedBlock != "" {
		needsSep := b.Len() > 0
		if needsSep {
			b.WriteString("\n\n")
		}
		b.WriteString(mentionedBlock)
	}

	channelBlock := formatChannelMemories(channel, multiplier, now, maxChannelMemories)
	if channelBlock != "" {
		needsSep := b.Len() > 0
		if needsSep {
			b.WriteString("\n\n")
		}
		b.WriteString(channelBlock)
	}

	return b.String()
}

// formatMentionedMemories formats memories with MentionedUserIDs into an XML block.
// Capped by maxItems (0 means skip entirely).
func formatMentionedMemories(memories []*Record, multiplier float64, now time.Time, maxItems int) string {
	if len(memories) == 0 || maxItems == 0 {
		return ""
	}

	SortByDecayedScore(memories, multiplier, now)

	var b strings.Builder
	b.WriteString("<mentioned_memories>\n")

	limit := maxItems
	if limit > len(memories) {
		limit = len(memories)
	}
	for _, m := range memories[:limit] {
		fmt.Fprintf(&b, "[%s | %s | %s] %s\n",
			m.MemoryType,
			m.DecayCategory,
			formatTimeAgo(m.CreatedAt, now),
			m.Content)
	}
	b.WriteString("</mentioned_memories>")

	return b.String()
}

// formatChannelMemories formats memories with MentionedChannelIDs into an XML block.
// Capped by maxItems (0 means skip entirely).
func formatChannelMemories(memories []*Record, multiplier float64, now time.Time, maxItems int) string {
	if len(memories) == 0 || maxItems == 0 {
		return ""
	}

	SortByDecayedScore(memories, multiplier, now)

	var b strings.Builder
	b.WriteString("<channel_memories>\n")

	limit := maxItems
	if limit > len(memories) {
		limit = len(memories)
	}
	for _, m := range memories[:limit] {
		fmt.Fprintf(&b, "[%s | %s | %s] %s\n",
			m.MemoryType,
			m.DecayCategory,
			formatTimeAgo(m.CreatedAt, now),
			m.Content)
	}
	b.WriteString("</channel_memories>")

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
