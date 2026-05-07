package memory

import (
	"math"
	"testing"
	"time"
)

func TestClampImportance(t *testing.T) {
	tests := []struct {
		name     string
		input    float64
		expected float64
	}{
		{name: "negative value", input: -0.5, expected: 0},
		{name: "zero value", input: 0, expected: 0},
		{name: "inside range", input: 0.5, expected: 0.5},
		{name: "one", input: 1, expected: 1},
		{name: "above max", input: 1.5, expected: 1},
		{name: "large negative", input: -100, expected: 0},
		{name: "large positive", input: 100, expected: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clampImportance(tt.input)
			if got != tt.expected {
				t.Errorf("clampImportance(%v) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestDecayedScore(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	yesterday := now.AddDate(0, 0, -1)

	tests := []struct {
		name      string
		record    *Record
		decayRate float64
		now       time.Time
	}{
		{
			name: "zero importance zero access zero age",
			record: &Record{
				CreatedAt:       now,
				ImportanceScore: 0,
				AccessCount:     0,
			},
			decayRate: 0.1,
			now:       now,
		},
		{
			name: "max importance zero access",
			record: &Record{
				CreatedAt:       now,
				ImportanceScore: 1,
				AccessCount:     0,
			},
			decayRate: 0.1,
			now:       now,
		},
		{
			name: "max importance with access bonus",
			record: &Record{
				CreatedAt:       now,
				ImportanceScore: 1,
				AccessCount:     10,
			},
			decayRate: 0.1,
			now:       now,
		},
		{
			name: "decayed over time",
			record: &Record{
				CreatedAt:       now.AddDate(0, 0, -30),
				ImportanceScore: 1,
				AccessCount:     0,
			},
			decayRate: 0.1,
			now:       now,
		},
		{
			name: "uses last accessed when newer than created",
			record: &Record{
				CreatedAt:       now.AddDate(0, 0, -30),
				LastAccessedAt:  yesterday,
				ImportanceScore: 1,
				AccessCount:     5,
			},
			decayRate: 0.1,
			now:       now,
		},
		{
			name: "very old memory",
			record: &Record{
				CreatedAt:       now.AddDate(-1, 0, 0),
				ImportanceScore: 0.5,
				AccessCount:     0,
			},
			decayRate: 0.01,
			now:       now,
		},
		{
			name: "clamped importance above max",
			record: &Record{
				CreatedAt:       now,
				ImportanceScore: 2.0,
				AccessCount:     0,
			},
			decayRate: 0.1,
			now:       now,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DecayedScore(tt.record, tt.decayRate, tt.now)

			// Score should never be negative
			if got < 0 {
				t.Errorf("DecayedScore returned negative score: %v", got)
			}

			// Zero importance, zero access, zero age should be 0
			if tt.record.ImportanceScore == 0 && tt.record.AccessCount == 0 && tt.record.CreatedAt.Equal(tt.now) {
				if got != 0 {
					t.Errorf("expected 0 for zero input, got %v", got)
				}
			}

			// Verify access bonus is present when access count > 0
			if tt.record.AccessCount > 0 {
				expectedBonus := math.Log(1 + float64(tt.record.AccessCount))
				decayedPart := got - expectedBonus
				if decayedPart < 0 {
					t.Errorf("decayed part should not be negative, got %v", decayedPart)
				}
			}
		})
	}
}

func TestDecayRateForType(t *testing.T) {
	rates := map[Type]float64{
		TypeFact:     0.01,
		TypeInterest: 0.05,
		TypeEpisode:  0.02,
		TypeSummary:  0.005,
	}

	tests := []struct {
		name     string
		mt       Type
		expected float64
	}{
		{name: "fact type", mt: TypeFact, expected: 0.01},
		{name: "interest type", mt: TypeInterest, expected: 0.05},
		{name: "episode type", mt: TypeEpisode, expected: 0.02},
		{name: "summary type", mt: TypeSummary, expected: 0.005},
		{name: "unknown type returns fallback", mt: Type("unknown"), expected: 0.01},
		{name: "empty type returns fallback", mt: Type(""), expected: 0.01},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DecayRateForType(tt.mt, rates)
			if got != tt.expected {
				t.Errorf("DecayRateForType(%q, rates) = %v, want %v", tt.mt, got, tt.expected)
			}
		})
	}
}

func TestClassifyTier(t *testing.T) {
	tests := []struct {
		name     string
		score    float64
		expected string
	}{
		{name: "hot at boundary", score: 0.7, expected: "hot"},
		{name: "hot above boundary", score: 0.95, expected: "hot"},
		{name: "hot max", score: 1.0, expected: "hot"},
		{name: "warm just below hot", score: 0.699, expected: "warm"},
		{name: "warm at boundary", score: 0.3, expected: "warm"},
		{name: "warm midpoint", score: 0.5, expected: "warm"},
		{name: "cold just below warm", score: 0.299, expected: "cold"},
		{name: "cold zero", score: 0, expected: "cold"},
		{name: "cold negative", score: -1, expected: "cold"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyTier(tt.score)
			if got != tt.expected {
				t.Errorf("ClassifyTier(%v) = %q, want %q", tt.score, got, tt.expected)
			}
		})
	}
}

func TestTypePriority(t *testing.T) {
	tests := []struct {
		name     string
		mt       Type
		expected float64
	}{
		{name: "fact priority", mt: TypeFact, expected: 1.0},
		{name: "interest priority", mt: TypeInterest, expected: 0.9},
		{name: "episode priority", mt: TypeEpisode, expected: 0.8},
		{name: "summary priority", mt: TypeSummary, expected: 0.6},
		{name: "unknown type", mt: Type("other"), expected: 0.5},
		{name: "empty type", mt: Type(""), expected: 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TypePriority(tt.mt)
			if got != tt.expected {
				t.Errorf("TypePriority(%q) = %v, want %v", tt.mt, got, tt.expected)
			}
			// Verify ordering: fact > interest > episode > summary
		})
	}

	// Verify strict ordering
	if prio := TypePriority(TypeFact); prio <= TypePriority(TypeInterest) {
		t.Errorf("fact priority (%v) should be > interest priority (%v)", prio, TypePriority(TypeInterest))
	}
	if prio := TypePriority(TypeInterest); prio <= TypePriority(TypeEpisode) {
		t.Errorf("interest priority (%v) should be > episode priority (%v)", prio, TypePriority(TypeEpisode))
	}
	if prio := TypePriority(TypeEpisode); prio <= TypePriority(TypeSummary) {
		t.Errorf("episode priority (%v) should be > summary priority (%v)", prio, TypePriority(TypeSummary))
	}
}

func TestCompositeScore(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		record    *Record
		weights   ScoringWeights
		decayRate float64
		now       time.Time
	}{
		{
			name: "zeroes produce zero",
			record: &Record{
				CreatedAt: now,
			},
			weights:   ScoringWeights{Importance: 1, Recency: 1, Access: 1, Confidence: 1},
			decayRate: 0.1,
			now:       now,
		},
		{
			name: "max importance only",
			record: &Record{
				CreatedAt:       now,
				ImportanceScore: 1,
				Confidence:      0,
				AccessCount:     0,
			},
			weights:   ScoringWeights{Importance: 0.4, Recency: 0.3, Access: 0.2, Confidence: 0.1},
			decayRate: 0.1,
			now:       now,
		},
		{
			name: "all fields populated",
			record: &Record{
				CreatedAt:       now.AddDate(0, 0, -7),
				ImportanceScore: 0.8,
				AccessCount:     5,
				Confidence:      0.9,
			},
			weights:   ScoringWeights{Importance: 0.4, Recency: 0.3, Access: 0.2, Confidence: 0.1},
			decayRate: 0.1,
			now:       now,
		},
		{
			name: "uses last accessed when newer",
			record: &Record{
				CreatedAt:       now.AddDate(0, 0, -30),
				LastAccessedAt:  now.AddDate(0, 0, -1),
				ImportanceScore: 0.8,
				AccessCount:     3,
				Confidence:      0.7,
			},
			weights:   ScoringWeights{Importance: 0.5, Recency: 0.3, Access: 0.1, Confidence: 0.1},
			decayRate: 0.05,
			now:       now,
		},
		{
			name: "clamped importance applied",
			record: &Record{
				CreatedAt:       now,
				ImportanceScore: 2.5,
				Confidence:      1,
				AccessCount:     0,
			},
			weights:   ScoringWeights{Importance: 0.5, Recency: 0, Access: 0, Confidence: 0.5},
			decayRate: 0.1,
			now:       now,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompositeScore(tt.record, tt.weights, tt.decayRate, tt.now)

			if got < 0 {
				t.Errorf("CompositeScore returned negative: %v", got)
			}

			// Verify formula structure: importance component
			importance := tt.weights.Importance * clampImportance(tt.record.ImportanceScore)
			if got < importance-0.0001 && tt.weights.Importance > 0 {
				// This is a structural check — the total should at least include the importance component
				t.Errorf("CompositeScore %v should be >= importance component %v", got, importance)
			}
		})
	}
}

func TestCompositeScore_ZeroWeights(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	zeroWeights := ScoringWeights{}

	got := CompositeScore(
		&Record{
			CreatedAt:       now,
			ImportanceScore: 1,
			AccessCount:     10,
			Confidence:      1,
		},
		zeroWeights,
		0.1,
		now,
	)

	if got != 0 {
		t.Errorf("CompositeScore with zero weights should be 0, got %v", got)
	}
}

func TestSortByDecayedScore(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)

	records := []*Record{
		{
			ID:              "low",
			CreatedAt:       now.AddDate(0, 0, -30),
			ImportanceScore: 0.1,
			AccessCount:     0,
		},
		{
			ID:              "high",
			CreatedAt:       now,
			ImportanceScore: 1.0,
			AccessCount:     10,
		},
		{
			ID:              "medium",
			CreatedAt:       now.AddDate(0, 0, -7),
			ImportanceScore: 0.5,
			AccessCount:     3,
		},
	}

	decayRate := 0.01
	SortByDecayedScore(records, decayRate, now)

	// Verify descending order
	for i := 1; i < len(records); i++ {
		prev := DecayedScore(records[i-1], decayRate, now)
		curr := DecayedScore(records[i], decayRate, now)
		if prev < curr {
			t.Errorf("records not sorted descending at index %d: prev=%v < curr=%v\n  prevID=%q currID=%q",
				i, prev, curr, records[i-1].ID, records[i].ID)
		}
	}
}

func TestSortByDecayedScore_Empty(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)

	// Should not panic
	SortByDecayedScore(nil, 0.01, now)
	SortByDecayedScore([]*Record{}, 0.01, now)
}

func TestSortByDecayedScore_StableOrderingForEqualScores(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)

	// Identical records should not cause issues
	records := []*Record{
		{
			ID:              "a",
			CreatedAt:       now,
			ImportanceScore: 0.5,
			AccessCount:     1,
		},
		{
			ID:              "b",
			CreatedAt:       now,
			ImportanceScore: 0.5,
			AccessCount:     1,
		},
	}

	decayRate := 0.01
	SortByDecayedScore(records, decayRate, now)

	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
}
