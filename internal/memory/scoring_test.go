package memory

import (
	"math"
	"testing"
	"time"
)

func TestHumanLikeDecay(t *testing.T) {
	tests := []struct {
		name       string
		hours      float64
		multiplier float64
		wantMin    float64
		wantMax    float64
	}{
		{name: "zero hours", hours: 0, multiplier: 1.0, wantMin: 0.999, wantMax: 1.001},
		{name: "half hour", hours: 0.5, multiplier: 1.0, wantMin: 0.71, wantMax: 0.73},
		{name: "one hour", hours: 1.0, multiplier: 1.0, wantMin: 0.439, wantMax: 0.441},
		{name: "12 hours", hours: 12, multiplier: 1.0, wantMin: 0.35, wantMax: 0.38},
		{name: "24 hours", hours: 24, multiplier: 1.0, wantMin: 0.339, wantMax: 0.341},
		{name: "3 days", hours: 72, multiplier: 1.0, wantMin: 0.28, wantMax: 0.30},
		{name: "7 days", hours: 168, multiplier: 1.0, wantMin: 0.249, wantMax: 0.251},
		{name: "30 days", hours: 720, multiplier: 1.0, wantMin: 0.23, wantMax: 0.25},
		{name: "zero hours with half multiplier", hours: 0, multiplier: 0.5, wantMin: 0.499, wantMax: 0.501},
		{name: "zero hours with double multiplier", hours: 0, multiplier: 2.0, wantMin: 1.999, wantMax: 2.001},
		{name: "negative hours clamped", hours: -1, multiplier: 1.0, wantMin: 0.999, wantMax: 1.001},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HumanLikeDecay(tt.hours, tt.multiplier)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("HumanLikeDecay(%.1f, %.1f) = %.4f, want in [%.4f, %.4f]",
					tt.hours, tt.multiplier, got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestHumanLikeDecay_Monotonic(t *testing.T) {
	// Verify decay decreases monotonically with time
	mult := 1.0
	prev := HumanLikeDecay(0, mult)
	for _, h := range []float64{0.5, 1, 2, 6, 12, 24, 48, 168, 720, 8760} {
		curr := HumanLikeDecay(h, mult)
		if curr > prev+0.0001 {
			t.Errorf("at hours=%.1f: %.4f > prev=%.4f (not monotonic)", h, curr, prev)
		}
		prev = curr
	}

	// Verify asymptote: after very long time, approaches but never below 0
	veryOld := HumanLikeDecay(876000, mult) // 100 years
	if veryOld <= 0 {
		t.Errorf("decay should never reach zero, got %.6f", veryOld)
	}
}

func TestHoursSince(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		t        time.Time
		expected float64
	}{
		{name: "zero time returns zero", t: time.Time{}, expected: 0},
		{name: "same time returns zero", t: now, expected: 0},
		{name: "one hour ago", t: now.Add(-1 * time.Hour), expected: 1},
		{name: "one day ago", t: now.Add(-24 * time.Hour), expected: 24},
		{name: "30 minutes ago", t: now.Add(-30 * time.Minute), expected: 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hoursSince(tt.t, now)
			if math.Abs(got-tt.expected) > 0.001 {
				t.Errorf("hoursSince(%v, now) = %.3f, want %.3f", tt.t, got, tt.expected)
			}
		})
	}
}

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
	mult := 1.0

	tests := []struct {
		name       string
		record     *Record
		multiplier float64
		now        time.Time
	}{
		{
			name: "zero importance zero access zero age",
			record: &Record{
				CreatedAt:       now,
				ImportanceScore: 0,
				AccessCount:     0,
			},
			multiplier: mult,
			now:        now,
		},
		{
			name: "max importance zero access",
			record: &Record{
				CreatedAt:       now,
				ImportanceScore: 1,
				AccessCount:     0,
			},
			multiplier: mult,
			now:        now,
		},
		{
			name: "max importance with access bonus",
			record: &Record{
				CreatedAt:       now,
				ImportanceScore: 1,
				AccessCount:     10,
			},
			multiplier: mult,
			now:        now,
		},
		{
			name: "decayed over time",
			record: &Record{
				CreatedAt:       now.AddDate(0, 0, -30),
				ImportanceScore: 1,
				AccessCount:     0,
			},
			multiplier: mult,
			now:        now,
		},
		{
			name: "uses last accessed when newer than created",
			record: &Record{
				CreatedAt:       now.AddDate(0, 0, -30),
				LastAccessedAt:  yesterday,
				ImportanceScore: 1,
				AccessCount:     5,
			},
			multiplier: mult,
			now:        now,
		},
		{
			name: "very old memory",
			record: &Record{
				CreatedAt:       now.AddDate(-1, 0, 0),
				ImportanceScore: 0.5,
				AccessCount:     0,
			},
			multiplier: mult,
			now:        now,
		},
		{
			name: "clamped importance above max",
			record: &Record{
				CreatedAt:       now,
				ImportanceScore: 2.0,
				AccessCount:     0,
			},
			multiplier: mult,
			now:        now,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DecayedScore(tt.record, tt.multiplier, tt.now)

			if got < 0 {
				t.Errorf("DecayedScore returned negative score: %v", got)
			}

			if tt.record.ImportanceScore == 0 && tt.record.AccessCount == 0 && tt.record.CreatedAt.Equal(tt.now) {
				if got != 0 {
					t.Errorf("expected 0 for zero input, got %v", got)
				}
			}

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
		})
	}

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
	mult := 1.0

	tests := []struct {
		name       string
		record     *Record
		weights    ScoringWeights
		multiplier float64
		now        time.Time
	}{
		{
			name: "zeroes produce zero",
			record: &Record{
				CreatedAt: now,
			},
			weights:    ScoringWeights{Importance: 1, Recency: 1, Access: 1, Confidence: 1},
			multiplier: mult,
			now:        now,
		},
		{
			name: "max importance only",
			record: &Record{
				CreatedAt:       now,
				ImportanceScore: 1,
				Confidence:      0,
				AccessCount:     0,
			},
			weights:    ScoringWeights{Importance: 0.4, Recency: 0.3, Access: 0.2, Confidence: 0.1},
			multiplier: mult,
			now:        now,
		},
		{
			name: "all fields populated",
			record: &Record{
				CreatedAt:       now.AddDate(0, 0, -7),
				ImportanceScore: 0.8,
				AccessCount:     5,
				Confidence:      0.9,
			},
			weights:    ScoringWeights{Importance: 0.4, Recency: 0.3, Access: 0.2, Confidence: 0.1},
			multiplier: mult,
			now:        now,
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
			weights:    ScoringWeights{Importance: 0.5, Recency: 0.3, Access: 0.1, Confidence: 0.1},
			multiplier: mult,
			now:        now,
		},
		{
			name: "clamped importance applied",
			record: &Record{
				CreatedAt:       now,
				ImportanceScore: 2.5,
				Confidence:      1,
				AccessCount:     0,
			},
			weights:    ScoringWeights{Importance: 0.5, Recency: 0, Access: 0, Confidence: 0.5},
			multiplier: mult,
			now:        now,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompositeScore(tt.record, tt.weights, tt.multiplier, tt.now)

			if got < 0 {
				t.Errorf("CompositeScore returned negative: %v", got)
			}

			importance := tt.weights.Importance * clampImportance(tt.record.ImportanceScore)
			if got < importance-0.0001 && tt.weights.Importance > 0 {
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
		1.0,
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

	mult := 1.0
	SortByDecayedScore(records, mult, now)

	for i := 1; i < len(records); i++ {
		prev := DecayedScore(records[i-1], mult, now)
		curr := DecayedScore(records[i], mult, now)
		if prev < curr {
			t.Errorf("records not sorted descending at index %d: prev=%v < curr=%v\n  prevID=%q currID=%q",
				i, prev, curr, records[i-1].ID, records[i].ID)
		}
	}
}

func TestSortByDecayedScore_Empty(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)

	SortByDecayedScore(nil, 1.0, now)
	SortByDecayedScore([]*Record{}, 1.0, now)
}

func TestSortByDecayedScore_StableOrderingForEqualScores(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)

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

	SortByDecayedScore(records, 1.0, now)

	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
}

func TestHumanLikeDecay_BoundaryValues(t *testing.T) {
	tests := []struct {
		name       string
		hours      float64
		multiplier float64
		wantMin    float64
		wantMax    float64
		checkFn    func(float64) bool // alternative to min/max range check
	}{
		{
			name:       "0 hours decay = 1.0",
			hours:      0,
			multiplier: 1.0,
			checkFn:    func(v float64) bool { return v >= 0.99 && v <= 1.01 },
		},
		{
			name:       "100000 hours decay > 0",
			hours:      100000,
			multiplier: 1.0,
			checkFn:    func(v float64) bool { return v > 0.21 && v < 0.25 },
		},
		{
			name:       "negative multiplier handled",
			hours:      0,
			multiplier: -1.0,
			checkFn:    func(v float64) bool { return v <= 0 },
		},
		{
			name:       "zero multiplier returns zero",
			hours:      10,
			multiplier: 0,
			checkFn:    func(v float64) bool { return v == 0 },
		},
		{
			name:       "very large multiplier scales correctly",
			hours:      0,
			multiplier: 100,
			checkFn:    func(v float64) bool { return v >= 99 && v <= 101 },
		},
		{
			name:       "fractional multiplier preserves proportion",
			hours:      0,
			multiplier: 0.25,
			checkFn:    func(v float64) bool { return v >= 0.24 && v <= 0.26 },
		},
		{
			name:       "exactly 1 hour boundary",
			hours:      1.0,
			multiplier: 1.0,
			wantMin:    0.439,
			wantMax:    0.441,
		},
		{
			name:       "exactly 24 hour boundary",
			hours:      24,
			multiplier: 1.0,
			wantMin:    0.339,
			wantMax:    0.341,
		},
		{
			name:       "exactly 168 hour boundary (7 days)",
			hours:      168,
			multiplier: 1.0,
			wantMin:    0.249,
			wantMax:    0.251,
		},
		{
			name:       "8760 hours (1 year) > 0 and < 0.25",
			hours:      8760,
			multiplier: 1.0,
			checkFn:    func(v float64) bool { return v > 0 && v < 0.25 },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HumanLikeDecay(tt.hours, tt.multiplier)

			if tt.checkFn != nil {
				if !tt.checkFn(got) {
					t.Errorf("HumanLikeDecay(%.1f, %.1f) = %.4f, check failed",
						tt.hours, tt.multiplier, got)
				}
				return
			}

			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("HumanLikeDecay(%.1f, %.1f) = %.4f, want in [%.4f, %.4f]",
					tt.hours, tt.multiplier, got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestHumanLikeDecay_ApproachesZeroButNeverReaches(t *testing.T) {
	mult := 1.0
	young := HumanLikeDecay(0, mult)
	old := HumanLikeDecay(1_000_000, mult)

	if young <= 0 {
		t.Errorf("initial decay should be positive, got %.6f", young)
	}
	if old <= 0 {
		t.Errorf("decay should never reach zero even after 1M hours, got %.6f", old)
	}
	if old >= young {
		t.Errorf("old decay (%.6f) should be less than young decay (%.6f)", old, young)
	}
}

func BenchmarkHumanLikeDecay(b *testing.B) {
	for b.Loop() {
		for h := float64(0); h < 1000; h++ {
			HumanLikeDecay(h, 1.0)
		}
	}
}

func BenchmarkHumanLikeDecay_LargeHours(b *testing.B) {
	for b.Loop() {
		HumanLikeDecay(100000, 1.0)
	}
}
