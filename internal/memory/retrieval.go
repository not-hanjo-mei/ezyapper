// Package memory provides long-term memory management using Qdrant vector database
package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ezyapper/internal/logger"
	"github.com/qdrant/go-client/qdrant"
)

// stopWords is a set of common English stop words to filter from keyword extraction.
var stopWordSet = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "is": {}, "are": {}, "was": {}, "were": {},
	"be": {}, "been": {}, "being": {}, "have": {}, "has": {}, "had": {},
	"do": {}, "does": {}, "did": {}, "will": {}, "would": {}, "shall": {},
	"should": {}, "may": {}, "might": {}, "must": {}, "can": {}, "could": {},
	"i": {}, "you": {}, "he": {}, "she": {}, "it": {}, "we": {}, "they": {},
	"me": {}, "him": {}, "her": {}, "us": {}, "them": {}, "my": {}, "your": {},
	"his": {}, "its": {}, "our": {}, "their": {}, "this": {}, "that": {},
	"these": {}, "those": {}, "in": {}, "on": {}, "at": {}, "to": {},
	"for": {}, "of": {}, "with": {}, "by": {}, "from": {}, "up": {},
	"down": {}, "out": {}, "off": {}, "over": {}, "under": {}, "and": {},
	"but": {}, "or": {}, "not": {}, "so": {}, "if": {}, "then": {},
	"else": {}, "when": {}, "where": {}, "why": {}, "how": {}, "all": {},
	"no": {}, "yes": {}, "just": {}, "now": {}, "also": {}, "very": {},
	"too": {}, "some": {}, "any": {}, "more": {}, "got": {}, "get": {},
	"yeah": {}, "oh": {}, "ok": {}, "okay": {}, "well": {}, "like": {},
	"really": {}, "still": {},
}

// isStopWord returns true if the word is a common stop word.
func isStopWord(word string) bool {
	_, ok := stopWordSet[word]
	return ok
}

// stringIsNumeric returns true if the string consists only of digit characters.
func stringIsNumeric(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// SearchHybrid performs hybrid search with RRF fusion of dense + sparse.
func (qc *QdrantClient) SearchHybrid(ctx context.Context, userID string, embedding []float32, sparseIndices []uint32, sparseValues []float32, opts *SearchOptions, rrfK int) ([]*Record, error) {
	if opts == nil {
		return nil, fmt.Errorf("search options are required")
	}
	limit := uint64(opts.TopK)

	filter := &qdrant.Filter{
		Must: []*qdrant.Condition{qdrant.NewMatch("user_id", userID)},
	}
	if len(opts.MemoryTypes) > 0 {
		conditions := make([]*qdrant.Condition, len(opts.MemoryTypes))
		for i, mt := range opts.MemoryTypes {
			conditions[i] = qdrant.NewMatch("memory_type", mt)
		}
		filter.Should = conditions
	}

	// Build prefetch queries
	prefetch := []*qdrant.PrefetchQuery{
		{
			Query: qdrant.NewQuery(embedding...),
			Using: qdrant.PtrOf(""),
			Limit: qdrant.PtrOf(uint64(opts.TopK * 2)),
		},
	}

	// Add sparse prefetch if we have sparse vectors
	if len(sparseIndices) > 0 {
		prefetch = append(prefetch, &qdrant.PrefetchQuery{
			Query: qdrant.NewQuerySparse(sparseIndices, sparseValues),
			Using: qdrant.PtrOf("bm25_keywords"),
			Limit: qdrant.PtrOf(uint64(opts.TopK)),
		})
	}

	results, err := qc.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: CollectionMemories,
		Prefetch:       prefetch,
		Query:          qdrant.NewQueryFusion(qdrant.Fusion_RRF),
		Filter:         filter,
		Limit:          &limit,
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("hybrid search: %w", err)
	}

	_ = rrfK // reserved for future RRF parameterization

	memories := make([]*Record, 0, len(results))
	for _, result := range results {
		if result.Score < float32(opts.MinScore) {
			continue
		}
		memory, err := qc.payloadToMemory(result.Payload, result.Id.GetUuid())
		if err != nil {
			logger.Warnf("[qdrant] hybrid search: convert payload %s: %v", result.Id.GetUuid(), err)
			continue
		}
		memories = append(memories, memory)
	}
	return memories, nil
}

// PostProcessResults sorts by decayed score, applies type diversity, and assigns tiers.
func PostProcessResults(records []*Record, decayRate float64, now time.Time, weights ScoringWeights) []*Record {
	if len(records) == 0 {
		return records
	}

	// Sort by decayed score descending
	SortByDecayedScore(records, decayRate, now)

	// Type diversity: keep at most 5 per type, ensure at least 1 of each if available
	typeCounts := make(map[Type]int)
	maxPerType := 5
	diverse := make([]*Record, 0, len(records))
	pending := make([]*Record, 0)

	for _, r := range records {
		mt := r.MemoryType
		if typeCounts[mt] < maxPerType {
			diverse = append(diverse, r)
			typeCounts[mt]++
		} else {
			pending = append(pending, r)
		}
	}

	// Fill remaining with pending records by score
	for _, r := range pending {
		diverse = append(diverse, r)
	}

	// Assign tiers
	for _, r := range diverse {
		score := CompositeScore(r, weights, decayRate, now)
		r.DecayCategory = ClassifyTier(score)
	}

	return diverse
}

// BuildSearchQuery constructs a search query from user message + recent messages.
// Pure algorithm — NO LLM call.
func BuildSearchQuery(userMessage string, recentMessages []*DiscordMessage) (string, []string) {
	// Extract keywords from recent messages (non-bot, non-duplicate)
	seen := make(map[string]struct{})
	keywords := make([]string, 0)

	for _, msg := range recentMessages {
		if msg == nil || msg.IsBot {
			continue
		}
		if msg.Content == userMessage {
			continue
		}

		for _, word := range tokenize(msg.Content) {
			word = strings.ToLower(word)
			if len(word) < 3 {
				continue
			}
			if _, ok := seen[word]; ok {
				continue
			}
			// Skip common stop words
			if isStopWord(word) {
				continue
			}
			// Skip numeric-only tokens
			if stringIsNumeric(word) {
				continue
			}
			seen[word] = struct{}{}
			keywords = append(keywords, word)
		}
	}

	// Build query: "Recent context: kw1, kw2, ... Current: userMessage"
	var query strings.Builder
	if len(keywords) > 0 {
		// Limit keywords
		if len(keywords) > 10 {
			keywords = keywords[:10]
		}
		query.WriteString("Recent context: ")
		query.WriteString(strings.Join(keywords, ", "))
		query.WriteString(". ")
	}
	query.WriteString(userMessage)

	return query.String(), keywords
}
