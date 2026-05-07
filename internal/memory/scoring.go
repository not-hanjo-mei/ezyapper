package memory

import (
	"math"
	"sort"
	"time"
)

// clampImportance clamps a value to [0.0, 1.0].
func clampImportance(v float64) float64 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 1
	}
	return v
}

// daysSince returns days elapsed since t (fractional).
func daysSince(t time.Time, now time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return now.Sub(t).Hours() / 24
}

// DecayedScore computes the decayed relevance score.
// Formula: importanceScore * exp(-decayRate * days) + log(1 + accessCount)
func DecayedScore(r *Record, decayRate float64, now time.Time) float64 {
	importance := clampImportance(r.ImportanceScore)
	age := daysSince(r.CreatedAt, now)
	if !r.LastAccessedAt.IsZero() && r.LastAccessedAt.After(r.CreatedAt) {
		age = daysSince(r.LastAccessedAt, now)
	}
	decayed := importance * math.Exp(-decayRate*age)
	accessBonus := math.Log(1 + float64(r.AccessCount))
	return decayed + accessBonus
}

// DecayRateForType returns the decay rate for a memory type from the config map.
func DecayRateForType(mt Type, rates map[Type]float64) float64 {
	if rate, ok := rates[mt]; ok {
		return rate
	}
	return 0.01 // sensible fallback
}

// ClassifyTier classifies a score into hot/warm/cold tier.
//
//	>= 0.7 → "hot"
//	>= 0.3 → "warm"
//	< 0.3 → "cold"
func ClassifyTier(score float64) string {
	if score >= 0.7 {
		return "hot"
	}
	if score >= 0.3 {
		return "warm"
	}
	return "cold"
}

// TypePriority returns a priority multiplier for ordering (higher = more important in context).
func TypePriority(mt Type) float64 {
	switch mt {
	case TypeFact:
		return 1.0
	case TypeInterest:
		return 0.9
	case TypeEpisode:
		return 0.8
	case TypeSummary:
		return 0.6
	default:
		return 0.5
	}
}

// CompositeScore computes a weighted composite score.
// Formula: w1*Importance + w2*exp(-λ*days) + w3*log(1+access) + w4*Confidence
func CompositeScore(r *Record, weights ScoringWeights, decayRate float64, now time.Time) float64 {
	importance := clampImportance(r.ImportanceScore) * weights.Importance

	age := daysSince(r.CreatedAt, now)
	if !r.LastAccessedAt.IsZero() && r.LastAccessedAt.After(r.CreatedAt) {
		age = daysSince(r.LastAccessedAt, now)
	}
	recency := math.Exp(-decayRate*age) * weights.Recency

	access := math.Log(1+float64(r.AccessCount)) * weights.Access
	confidence := r.Confidence * weights.Confidence

	return importance + recency + access + confidence
}

// SortByDecayedScore sorts records in-place by decayed score descending.
func SortByDecayedScore(records []*Record, decayRate float64, now time.Time) {
	sort.Slice(records, func(i, j int) bool {
		return DecayedScore(records[i], decayRate, now) > DecayedScore(records[j], decayRate, now)
	})
}
