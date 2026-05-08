package memory

import (
	"strings"
	"testing"
	"time"
)

func TestFormatMemoriesForContext_Empty(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	got := FormatMemoriesForContext(nil, 1.0, now, 10, 10)
	if got != "" {
		t.Errorf("expected empty string for nil memories, got %q", got)
	}

	got = FormatMemoriesForContext([]*Record{}, 1.0, now, 10, 10)
	if got != "" {
		t.Errorf("expected empty string for empty memories, got %q", got)
	}
}

func TestFormatMemoriesForContext_KeyFactsOnly(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	memories := []*Record{
		{
			MemoryType:      TypeFact,
			Content:         "user is a Go developer",
			ImportanceScore: 0.9,
			CreatedAt:       now.Add(-2 * time.Hour),
			DecayCategory:   "hot",
		},
	}

	got := FormatMemoriesForContext(memories, 1.0, now, 10, 10)

	if !strings.Contains(got, "<key_facts>") {
		t.Error("expected <key_facts> block")
	}
	if !strings.Contains(got, "user is a Go developer") {
		t.Error("expected fact content in output")
	}
	if !strings.Contains(got, "</key_facts>") {
		t.Error("expected closing </key_facts> tag")
	}
}

func TestFormatMemoriesForContext_FactBelowThreshold(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	memories := []*Record{
		{
			MemoryType:      TypeFact,
			Content:         "low importance fact",
			ImportanceScore: 0.3,
			CreatedAt:       now.Add(-1 * time.Hour),
			DecayCategory:   "cold",
		},
		{
			MemoryType:      TypeFact,
			Content:         "high importance fact",
			ImportanceScore: 0.7,
			CreatedAt:       now.Add(-1 * time.Hour),
			DecayCategory:   "hot",
		},
	}

	got := FormatMemoriesForContext(memories, 1.0, now, 10, 10)

	if !strings.Contains(got, "high importance fact") {
		t.Error("expected high-importance fact in output")
	}
	if !strings.Contains(got, "low importance fact") {
		t.Error("low-importance fact should still appear (in recent_memories)")
	}
	keyFactsStart := strings.Index(got, "<key_facts>")
	keyFactsEnd := strings.Index(got, "</key_facts>")
	lowPos := strings.Index(got, "low importance fact")

	if lowPos > keyFactsStart && lowPos < keyFactsEnd {
		t.Error("low-importance fact (0.3) should not be in <key_facts> block")
	}
	recentStart := strings.Index(got, "<recent_memories>")
	recentEnd := strings.Index(got, "</recent_memories>")

	if lowPos < recentStart || lowPos > recentEnd {
		t.Error("low-importance fact should be in <recent_memories> block")
	}
}

func TestFormatMemoriesForContext_RecentMemories(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	memories := []*Record{
		{
			MemoryType:      TypeSummary,
			Content:         "discussed weekend plans",
			ImportanceScore: 0.4,
			CreatedAt:       now.Add(-3 * time.Hour),
			DecayCategory:   "warm",
		},
	}

	got := FormatMemoriesForContext(memories, 1.0, now, 10, 10)

	if !strings.Contains(got, "<recent_memories>") {
		t.Error("expected <recent_memories> block")
	}
	if !strings.Contains(got, "discussed weekend plans") {
		t.Error("expected recent memory content in output")
	}
}

func TestFormatMemoriesForContext_MentionedBlock(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	memories := []*Record{
		{
			MemoryType:       TypeEpisode,
			Content:          "talked with John",
			ImportanceScore:  0.5,
			CreatedAt:        now.Add(-4 * time.Hour),
			DecayCategory:    "warm",
			MentionedUserIDs: []string{"user-john"},
		},
	}

	got := FormatMemoriesForContext(memories, 1.0, now, 10, 10)

	if !strings.Contains(got, "<mentioned_memories>") {
		t.Error("expected <mentioned_memories> block")
	}
	if !strings.Contains(got, "talked with John") {
		t.Error("expected mentioned memory content")
	}
}

func TestFormatMemoriesForContext_MentionedBlock_Capped(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	memories := make([]*Record, 0, 10)
	for i := range 10 {
		memories = append(memories, &Record{
			ID:               "mem-" + string(rune('a'+i)),
			MemoryType:       TypeEpisode,
			Content:          "content " + string(rune('a'+i)),
			ImportanceScore:  0.5,
			CreatedAt:        now.Add(-time.Duration(i) * time.Hour),
			DecayCategory:    "warm",
			MentionedUserIDs: []string{"user-x"},
		})
	}

	got := FormatMemoriesForContext(memories, 1.0, now, 3, 5)

	mentionCount := strings.Count(got, "[episode |")
	if mentionCount > 3 {
		t.Errorf("expected at most 3 mentioned memories, got %d", mentionCount)
	}
}

func TestFormatMemoriesForContext_MentionedBlock_Disabled(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	memories := []*Record{
		{
			MemoryType:       TypeEpisode,
			Content:          "should not appear",
			ImportanceScore:  0.5,
			CreatedAt:        now.Add(-1 * time.Hour),
			DecayCategory:    "warm",
			MentionedUserIDs: []string{"user-x"},
		},
	}

	got := FormatMemoriesForContext(memories, 1.0, now, 0, 10)

	if strings.Contains(got, "<mentioned_memories>") {
		t.Error("mentioned block should be omitted when maxMentionedMemories=0")
	}
}

func TestFormatMemoriesForContext_ChannelBlock(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	memories := []*Record{
		{
			MemoryType:          TypeSummary,
			Content:             "channel discussion topic",
			ImportanceScore:     0.6,
			CreatedAt:           now.Add(-5 * time.Hour),
			DecayCategory:       "warm",
			MentionedChannelIDs: []string{"general"},
		},
	}

	got := FormatMemoriesForContext(memories, 1.0, now, 10, 10)

	if !strings.Contains(got, "<channel_memories>") {
		t.Error("expected <channel_memories> block")
	}
	if !strings.Contains(got, "channel discussion topic") {
		t.Error("expected channel memory content")
	}
}

func TestFormatMemoriesForContext_ChannelBlock_Disabled(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	memories := []*Record{
		{
			MemoryType:          TypeSummary,
			Content:             "should not appear",
			ImportanceScore:     0.5,
			CreatedAt:           now.Add(-1 * time.Hour),
			DecayCategory:       "warm",
			MentionedChannelIDs: []string{"ch-1"},
		},
	}

	got := FormatMemoriesForContext(memories, 1.0, now, 10, 0)

	if strings.Contains(got, "<channel_memories>") {
		t.Error("channel block should be omitted when maxChannelMemories=0")
	}
}

func TestFormatMemoriesForContext_AllBlocks(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	memories := []*Record{
		{
			MemoryType:      TypeFact,
			Content:         "key fact about user",
			ImportanceScore: 0.8,
			CreatedAt:       now.Add(-2 * time.Hour),
			DecayCategory:   "hot",
		},
		{
			MemoryType:      TypeInterest,
			Content:         "user likes hiking",
			ImportanceScore: 0.4,
			CreatedAt:       now.Add(-3 * time.Hour),
			DecayCategory:   "warm",
		},
		{
			MemoryType:       TypeEpisode,
			Content:          "talked with Alice",
			ImportanceScore:  0.5,
			CreatedAt:        now.Add(-4 * time.Hour),
			DecayCategory:    "warm",
			MentionedUserIDs: []string{"alice"},
		},
		{
			MemoryType:          TypeSummary,
			Content:             "topic in general chat",
			ImportanceScore:     0.6,
			CreatedAt:           now.Add(-5 * time.Hour),
			DecayCategory:       "warm",
			MentionedChannelIDs: []string{"general"},
		},
	}

	got := FormatMemoriesForContext(memories, 1.0, now, 10, 10)

	if !strings.Contains(got, "<key_facts>") {
		t.Error("expected key_facts block")
	}
	if !strings.Contains(got, "<recent_memories>") {
		t.Error("expected recent_memories block")
	}
	if !strings.Contains(got, "<mentioned_memories>") {
		t.Error("expected mentioned_memories block")
	}
	if !strings.Contains(got, "<channel_memories>") {
		t.Error("expected channel_memories block")
	}
}

func TestFormatMemoriesForContext_MentionTakesPriority(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	memories := []*Record{
		{
			MemoryType:       TypeFact,
			Content:          "high importance fact with mention",
			ImportanceScore:  0.9,
			CreatedAt:        now.Add(-1 * time.Hour),
			DecayCategory:    "hot",
			MentionedUserIDs: []string{"bob"},
		},
	}

	got := FormatMemoriesForContext(memories, 1.0, now, 10, 10)

	if !strings.Contains(got, "<key_facts>") {
		t.Error("key fact with mention should go to key_facts, not mentioned_memories")
	}
	if strings.Contains(got, "<mentioned_memories>") {
		t.Error("key fact should not also appear in mentioned_memories")
	}
}

func TestFormatMemoriesForContext_SortedByDecay(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	memories := []*Record{
		{
			ID:              "older",
			MemoryType:      TypeSummary,
			Content:         "older memory",
			ImportanceScore: 0.5,
			CreatedAt:       now.Add(-100 * time.Hour),
			DecayCategory:   "cold",
		},
		{
			ID:              "newer",
			MemoryType:      TypeSummary,
			Content:         "newer memory",
			ImportanceScore: 0.5,
			CreatedAt:       now.Add(-1 * time.Hour),
			DecayCategory:   "hot",
		},
	}

	got := FormatMemoriesForContext(memories, 1.0, now, 10, 10)

	olderPos := strings.Index(got, "older memory")
	newerPos := strings.Index(got, "newer memory")

	if olderPos < newerPos {
		t.Errorf("newer memory should appear before older (higher decayed score): olderPos=%d newerPos=%d", olderPos, newerPos)
	}
}

func TestFormatTimeAgo(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		offset   time.Duration
		expected string
	}{
		{name: "just now", offset: 30 * time.Second, expected: "just now"},
		{name: "minutes ago", offset: 30 * time.Minute, expected: "30m ago"},
		{name: "hours ago", offset: 5 * time.Hour, expected: "5h ago"},
		{name: "days ago", offset: 3 * 24 * time.Hour, expected: "3d ago"},
		{name: "weeks ago", offset: 14 * 24 * time.Hour, expected: "2w ago"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTimeAgo(now.Add(-tt.offset), now)
			if got != tt.expected {
				t.Errorf("formatTimeAgo(%v) = %q, want %q", tt.offset, got, tt.expected)
			}
		})
	}
}

func BenchmarkFormatMemoriesForContext(b *testing.B) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	memories := make([]*Record, 50)
	for i := range memories {
		memories[i] = &Record{
			ID:              "mem-" + string(rune('a'+i%26)),
			MemoryType:      TypeFact,
			Content:         "benchmark memory content " + string(rune('a'+i%26)),
			ImportanceScore: 0.5,
			CreatedAt:       now.Add(-time.Duration(i) * time.Hour),
			DecayCategory:   "warm",
		}
		if i%3 == 0 {
			memories[i].MentionedUserIDs = []string{"user-x"}
		}
		if i%5 == 0 {
			memories[i].MentionedChannelIDs = []string{"ch-y"}
		}
	}

	for b.Loop() {
		FormatMemoriesForContext(memories, 1.0, now, 10, 10)
	}
}
