package memory

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"ezyapper/internal/logger"

	"github.com/qdrant/go-client/qdrant"
)

// maintenanceStore is the subset of QdrantClient methods used by MaintenanceWorker.
type maintenanceStore interface {
	ScrollMemories(ctx context.Context, limit uint32) ([]*scrollPoint, error)
	ScrollRelationships(ctx context.Context, limit uint32) ([]*scrollRelationshipPoint, error)
	DeletePoint(ctx context.Context, id string) error
	DeleteRelationshipPoint(ctx context.Context, id string) error
}

// scrollPoint is the result type for a single memory point from Scroll.
type scrollPoint struct {
	ID        string
	Payload   map[string]*qdrantValue
	Embedding []float32
}

// scrollRelationshipPoint is the result type for a single relationship point from Scroll.
type scrollRelationshipPoint struct {
	ID      string
	Payload map[string]*qdrantValue
}

// qdrantValue is an alias for the qdrant Value type used in payload maps.
type qdrantValue = qdrant.Value

// MaintenanceWorker handles scheduled memory lifecycle operations
// (merge duplicates, summarize cold memories, prune expired).
type MaintenanceWorker struct {
	store  maintenanceStore
	config *ServiceConfig

	done chan struct{}
	wg   sync.WaitGroup
}

// StartMaintenanceWorker creates and starts a background maintenance worker.
func StartMaintenanceWorker(parentCtx context.Context, store maintenanceStore, config *ServiceConfig) *MaintenanceWorker {
	w := &MaintenanceWorker{
		store:  store,
		config: config,
		done:   make(chan struct{}),
	}

	w.wg.Add(1)
	go w.run(parentCtx)

	logger.Info("[maintenance] worker started")
	return w
}

func (w *MaintenanceWorker) run(ctx context.Context) {
	defer w.wg.Done()

	d := time.Duration(w.config.MaintenanceIntervalSec) * time.Second
	if d <= 0 {
		d = 60 * time.Second
	}
	ticker := time.NewTicker(d)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			return
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			w.runScheduled(ctx, now)
		}
	}
}

// runScheduled inspects the current time and dispatches to the appropriate
// maintenance job based on configured cron schedules.
func (w *MaintenanceWorker) runScheduled(ctx context.Context, now time.Time) {
	utcNow := now.UTC()
	mergeHour := w.config.MergeCronHourUTC

	if utcNow.Hour() == mergeHour && utcNow.Minute() < 5 {
		if err := w.mergeDuplicates(ctx); err != nil {
			logger.Warnf("[maintenance] merge duplicates failed: %v", err)
		}

		if utcNow.Weekday() == time.Weekday(w.config.SummarizeCronDay) {
			if err := w.summarizeAndPrune(ctx); err != nil {
				logger.Warnf("[maintenance] summarize/prune failed: %v", err)
			}
		}
	}

	if utcNow.Day() == 1 && utcNow.Hour() == mergeHour && utcNow.Minute() < 5 {
		if err := w.pruneExpired(ctx); err != nil {
			logger.Warnf("[maintenance] expired prune failed: %v", err)
		}

		if err := w.pruneStaleRelationships(ctx, time.Now()); err != nil {
			logger.Warnf("[maintenance] stale relationship prune failed: %v", err)
		}
	}
}

// mergeDuplicates finds memories with high cosine similarity within the same
// user+type group and keeps only the higher-scored one. Pure algorithm - no LLM call.
func (w *MaintenanceWorker) mergeDuplicates(ctx context.Context) error {
	start := time.Now()
	logger.Info("[maintenance] starting duplicate merge scan")

	points, err := w.store.ScrollMemories(ctx, 500)
	if err != nil {
		return fmt.Errorf("scroll memories: %w", err)
	}

	records := make([]*Record, 0, len(points))
	for _, pt := range points {
		rec := &Record{
			ID:        pt.ID,
			Embedding: pt.Embedding,
		}
		if v, ok := pt.Payload["user_id"]; ok && v != nil {
			rec.UserID = v.GetStringValue()
		}
		if v, ok := pt.Payload["memory_type"]; ok && v != nil {
			rec.MemoryType = Type(v.GetStringValue())
		}
		if v, ok := pt.Payload["importance_score"]; ok && v != nil {
			rec.ImportanceScore = v.GetDoubleValue()
		}
		if v, ok := pt.Payload["access_count"]; ok && v != nil {
			rec.AccessCount = int(v.GetIntegerValue())
		}
		if v, ok := pt.Payload["confidence"]; ok && v != nil {
			rec.Confidence = v.GetDoubleValue()
		}
		if v, ok := pt.Payload["created_at"]; ok && v != nil {
			ts := v.GetDoubleValue()
			if ts > 0 {
				rec.CreatedAt = time.UnixMilli(int64(ts * 1000))
			}
		}
		if v, ok := pt.Payload["last_accessed_at"]; ok && v != nil {
			ts := v.GetDoubleValue()
			if ts > 0 {
				rec.LastAccessedAt = time.UnixMilli(int64(ts * 1000))
			}
		}
		if rec.UserID == "" || len(rec.Embedding) == 0 {
			continue
		}
		records = append(records, rec)
	}

	type memKey struct {
		userID string
		mt     Type
	}
	groups := make(map[memKey][]*Record)
	for _, rec := range records {
		k := memKey{rec.UserID, rec.MemoryType}
		groups[k] = append(groups[k], rec)
	}

	now := time.Now()
	merged := 0

	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		for i := 0; i < len(group)-1; i++ {
			for j := i + 1; j < len(group); j++ {
				a, b := group[i], group[j]
				if a.ID == b.ID {
					continue
				}

				sim := cosineSimilarity(a.Embedding, b.Embedding)
				if sim >= float32(w.config.MergeCosineThreshold) {
					mult := w.config.Scoring.MemoryStrengthMultiplier
					// drB removed — using unified mult
					scoreA := CompositeScore(a, w.config.Scoring.Weights, mult, now)
					scoreB := CompositeScore(b, w.config.Scoring.Weights, mult, now)

					var toDelete string
					if scoreA >= scoreB {
						toDelete = b.ID
						group[j].ID = a.ID
					} else {
						toDelete = a.ID
						group[i].ID = b.ID
					}

					if err := w.store.DeletePoint(ctx, toDelete); err != nil {
						logger.Warnf("[maintenance] failed to delete duplicate %s: %v", toDelete, err)
					} else {
						merged++
					}
				}
			}
		}
	}

	elapsed := time.Since(start)
	if merged > 0 {
		logger.Infof("[maintenance] merge complete: merged=%d pairs duration=%s", merged, elapsed)
	} else {
		logger.Debugf("[maintenance] merge scan: no duplicates found duration=%s", elapsed)
	}
	return nil
}

// summarizeAndPrune removes memories with decay scores below threshold
// or very old memories with zero access. Pure algorithm - no LLM call.
func (w *MaintenanceWorker) summarizeAndPrune(ctx context.Context) error {
	start := time.Now()
	logger.Info("[maintenance] starting summarize/prune scan")

	points, err := w.store.ScrollMemories(ctx, 1000)
	if err != nil {
		return fmt.Errorf("scroll memories: %w", err)
	}

	now := time.Now()
	pruned := 0

	for _, pt := range points {
		if pt.ID == "" {
			continue
		}

		var (
			memType         Type
			createdAt       time.Time
			lastAccessedAt  time.Time
			accessCount     int
			importanceScore float64
		)

		if v, ok := pt.Payload["memory_type"]; ok && v != nil {
			memType = Type(v.GetStringValue())
		}
		if v, ok := pt.Payload["created_at"]; ok && v != nil {
			ts := v.GetDoubleValue()
			if ts > 0 {
				createdAt = time.UnixMilli(int64(ts * 1000))
			}
		}
		if v, ok := pt.Payload["last_accessed_at"]; ok && v != nil {
			ts := v.GetDoubleValue()
			if ts > 0 {
				lastAccessedAt = time.UnixMilli(int64(ts * 1000))
			}
		}
		if v, ok := pt.Payload["access_count"]; ok && v != nil {
			accessCount = int(v.GetIntegerValue())
		}
		if v, ok := pt.Payload["importance_score"]; ok && v != nil {
			importanceScore = v.GetDoubleValue()
		}

		rec := &Record{
			ID:              pt.ID,
			MemoryType:      memType,
			CreatedAt:       createdAt,
			LastAccessedAt:  lastAccessedAt,
			AccessCount:     accessCount,
			ImportanceScore: importanceScore,
		}

		mult := w.config.Scoring.MemoryStrengthMultiplier
		score := DecayedScore(rec, mult, now)
		age := now.Sub(rec.CreatedAt)

		if score < w.config.PruneDecayThreshold ||
			(age.Hours()/24 > float64(w.config.PruneAgeDays) && rec.AccessCount == 0) {
			if err := w.store.DeletePoint(ctx, pt.ID); err != nil {
				logger.Warnf("[maintenance] failed to prune %s: %v", pt.ID, err)
			} else {
				pruned++
			}
		}
	}

	elapsed := time.Since(start)
	if pruned > 0 {
		logger.Infof("[maintenance] summarize/prune complete: pruned=%d duration=%s", pruned, elapsed)
	} else {
		logger.Debugf("[maintenance] summarize/prune scan: nothing to prune duration=%s", elapsed)
	}
	return nil
}

// pruneExpired removes memories whose ExpiresAt is in the past.
func (w *MaintenanceWorker) pruneExpired(ctx context.Context) error {
	points, err := w.store.ScrollMemories(ctx, 1000)
	if err != nil {
		return fmt.Errorf("scroll memories: %w", err)
	}

	now := time.Now()
	pruned := 0

	for _, pt := range points {
		if pt.ID == "" {
			continue
		}

		var expiresAt time.Time
		if v, ok := pt.Payload["expires_at"]; ok && v != nil {
			ts := v.GetDoubleValue()
			if ts > 0 {
				expiresAt = time.UnixMilli(int64(ts * 1000))
			}
		}

		if !expiresAt.IsZero() && now.After(expiresAt) {
			if err := w.store.DeletePoint(ctx, pt.ID); err != nil {
				logger.Warnf("[maintenance] failed to delete expired %s: %v", pt.ID, err)
			} else {
				pruned++
			}
		}
	}

	if pruned > 0 {
		logger.Infof("[maintenance] expired prune: removed=%d", pruned)
	}
	return nil
}

// pruneStaleRelationships removes relationship records whose last_interaction_at
// timestamp is older than RelationshipPruneAgeDays days. Uses the configured
// monthly cadence (same as pruneExpired). Scrolls in batches of 500.
func (w *MaintenanceWorker) pruneStaleRelationships(ctx context.Context, now time.Time) error {
	points, err := w.store.ScrollRelationships(ctx, 500)
	if err != nil {
		return fmt.Errorf("scroll relationships: %w", err)
	}

	pruned := 0
	maxAge := float64(w.config.RelationshipPruneAgeDays) * 24.0 // hours

	for _, pt := range points {
		if pt.ID == "" {
			continue
		}

		var lastInteractionAt time.Time
		if v, ok := pt.Payload["last_interaction_at"]; ok && v != nil {
			ts := v.GetDoubleValue()
			if ts > 0 {
				lastInteractionAt = time.UnixMilli(int64(ts * 1000))
			}
		}

		if !lastInteractionAt.IsZero() && now.Sub(lastInteractionAt).Hours() > maxAge {
			if err := w.store.DeleteRelationshipPoint(ctx, pt.ID); err != nil {
				logger.Warnf("[maintenance] failed to delete stale relationship %s: %v", pt.ID, err)
			} else {
				pruned++
			}
		}
	}

	if pruned > 0 {
		logger.Infof("[maintenance] stale relationship prune: removed=%d", pruned)
	} else {
		logger.Debugf("[maintenance] stale relationship prune: nothing to prune")
	}
	return nil
}

// cosineSimilarity computes cosine similarity between two float32 vectors.
// Returns 0 for empty or mismatched-length vectors.
func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(normA) * math.Sqrt(normB)))
}

// Stop signals the maintenance worker to shut down and waits for it to finish.
// Safe to call multiple times.
func (w *MaintenanceWorker) Stop() {
	select {
	case <-w.done:
	default:
		close(w.done)
	}
	w.wg.Wait()
	logger.Info("[maintenance] worker stopped")
}
