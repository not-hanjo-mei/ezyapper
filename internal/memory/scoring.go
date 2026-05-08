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

// hoursSince returns hours elapsed since t (fractional).
func hoursSince(t time.Time, now time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return now.Sub(t).Hours()
}

// HumanLikeDecay approximates the Ebbinghaus forgetting curve.
// hours: age of the memory in hours (fractional).
// multiplier: MemoryStrengthMultiplier from config.
//
// Piecewise segments:
//
//	0-1h:   linear from 1.0 to 0.44
//	1-24h:  logarithmic from 0.44 to 0.34
//	1-7d:   logarithmic from 0.34 to 0.25
//	7d+:    logarithmic from 0.25 to 0.21 (asymptote)
func HumanLikeDecay(hours float64, multiplier float64) float64 {
	var raw float64
	switch {
	case hours <= 0:
		raw = 1.0
	case hours <= 1:
		raw = 1.0 - 0.56*hours
	case hours <= 24:
		t := math.Log(hours) / math.Log(24)
		raw = 0.44 - 0.10*t
	case hours <= 168:
		t := (math.Log(hours) - math.Log(24)) / (math.Log(168) - math.Log(24))
		raw = 0.34 - 0.09*t
	default:
		raw = 0.21 + 0.04*math.Log(168)/math.Log(hours)
	}
	return raw * multiplier
}

// DecayedScore computes the decayed relevance score.
// Formula: importance * HumanLikeDecay(age, multiplier) + log(1 + accessCount)
func DecayedScore(r *Record, multiplier float64, now time.Time) float64 {
	importance := clampImportance(r.ImportanceScore)
	age := hoursSince(r.CreatedAt, now)
	if !r.LastAccessedAt.IsZero() && r.LastAccessedAt.After(r.CreatedAt) {
		age = hoursSince(r.LastAccessedAt, now)
	}
	decayed := importance * HumanLikeDecay(age, multiplier)
	accessBonus := math.Log(1 + float64(r.AccessCount))
	return decayed + accessBonus
}

// ClassifyTier classifies a score into hot/warm/cold tier.
//
//	>= 0.7 -> "hot"
//	>= 0.3 -> "warm"
//	< 0.3 -> "cold"
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
// Formula: w1*Importance + w2*HumanLikeDecay(age, multiplier) + w3*log(1+access) + w4*Confidence
func CompositeScore(r *Record, weights ScoringWeights, multiplier float64, now time.Time) float64 {
	importance := clampImportance(r.ImportanceScore) * weights.Importance

	age := hoursSince(r.CreatedAt, now)
	if !r.LastAccessedAt.IsZero() && r.LastAccessedAt.After(r.CreatedAt) {
		age = hoursSince(r.LastAccessedAt, now)
	}
	recency := HumanLikeDecay(age, multiplier) * weights.Recency

	access := math.Log(1+float64(r.AccessCount)) * weights.Access
	confidence := r.Confidence * weights.Confidence

	return importance + recency + access + confidence
}

// SortByDecayedScore sorts records in-place by decayed score descending.
func SortByDecayedScore(records []*Record, multiplier float64, now time.Time) {
	sort.Slice(records, func(i, j int) bool {
		return DecayedScore(records[i], multiplier, now) > DecayedScore(records[j], multiplier, now)
	})
}
