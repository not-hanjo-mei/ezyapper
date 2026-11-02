package memory

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"ezyapper/internal/logger"

	"github.com/qdrant/go-client/qdrant"
)

const (
	// Source collection metadata keys used to make the legacy migration
	// resumable and idempotent.
	metaLegacyMigration       = "legacy_migration"
	legacyMigrationInProgress = "in_progress"
	legacyMigrationDone       = "done"

	// tierSyncBatchSize is the number of records read per page during the
	// legacy migration and the vector sync.
	tierSyncBatchSize = 128
	// tierSyncConcurrency bounds concurrent embedding requests during the
	// vector sync, to stay friendly to embedding API rate limits.
	tierSyncConcurrency = 4
	// vectorReuseProbeCount is how many legacy records are re-embedded to
	// verify that legacy vectors came from the current model.
	vectorReuseProbeCount = 3
)

// errEmbeddingDimMismatch aborts a vector sync when the embedder returns
// vectors of a different size than the vector collection: every further
// request would fail the same way.
var errEmbeddingDimMismatch = errors.New("embedding dimension does not match vector collection")

// migrationAction is the outcome of planLegacyMigration.
type migrationAction int

const (
	// migrationNone: nothing to migrate (no legacy collection, or already done).
	migrationNone migrationAction = iota
	// migrationStart: copy legacy payloads into an empty source tier.
	migrationStart
	// migrationResume: a previous copy was interrupted; copy again (upserts
	// are idempotent by record ID).
	migrationResume
	// migrationSkipForeignData: the source tier already holds data that did
	// not come from a migration; refuse to mix in legacy data automatically.
	migrationSkipForeignData
)

func (a migrationAction) String() string {
	switch a {
	case migrationNone:
		return "none"
	case migrationStart:
		return "start"
	case migrationResume:
		return "resume"
	case migrationSkipForeignData:
		return "skip-foreign-data"
	default:
		return "unknown"
	}
}

// migrationState captures what planLegacyMigration needs to know.
type migrationState struct {
	LegacyExists bool
	LegacyCount  uint64
	SourceCount  uint64
	// Marker is the value of the source collection's legacy_migration
	// metadata key ("", in_progress or done).
	Marker string
}

// planLegacyMigration decides whether the legacy single "memories"
// collection has to be copied into the source tier.
//
// The copy starts only when the source tier is empty, and the marker on the
// source collection makes it resumable: in_progress is written before the
// first page is copied and done after the last one, so an interrupted copy is
// resumed on the next start and a finished one never runs again.
func planLegacyMigration(st migrationState) migrationAction {
	if !st.LegacyExists {
		return migrationNone
	}
	switch st.Marker {
	case legacyMigrationDone:
		return migrationNone
	case legacyMigrationInProgress:
		return migrationResume
	}
	if st.LegacyCount == 0 {
		return migrationNone
	}
	if st.SourceCount == 0 {
		return migrationStart
	}
	return migrationSkipForeignData
}

// vectorReuseMinCosine is the minimum cosine similarity between a stored
// legacy vector and a fresh embedding of the same text for the legacy vectors
// to be considered produced by the current model.
const vectorReuseMinCosine = 0.95

// decideVectorReuse decides whether legacy vectors can be copied into the
// current vector collection instead of re-embedding.
//
// A matching dimension is necessary but not sufficient: two different models
// can share a dimension. probeSims are cosine similarities between stored
// vectors and fresh embeddings of the same records; when any probe is below
// vectorReuseMinCosine the vectors are not reused. With no probes (e.g. the
// embedding API was unreachable) the dimension match alone decides.
func decideVectorReuse(legacyDim, currentDim int, probeSims []float32) bool {
	if legacyDim <= 0 || legacyDim != currentDim {
		return false
	}
	for _, s := range probeSims {
		if s < vectorReuseMinCosine {
			return false
		}
	}
	return true
}

// markerValue returns the string metadata value for key, or "".
func markerValue(md map[string]*qdrant.Value, key string) string {
	if md == nil {
		return ""
	}
	if v, ok := md[key]; ok && v != nil {
		return v.GetStringValue()
	}
	return ""
}

// migrationReport summarizes a legacy migration run.
type migrationReport struct {
	Action        migrationAction
	Copied        int
	VectorsReused bool
}

// vectorSyncReport summarizes a vector sync run.
type vectorSyncReport struct {
	Scanned  int
	Present  int
	Embedded int
	Failed   int
}

// MigrateLegacy copies the pre-split "memories" collection into the source
// tier when needed (see planLegacyMigration). When the legacy vectors come
// from the current model (same dimension, verified by re-embedding a few
// records) they are copied into the current vector collection as well, so no
// re-embedding is needed. The legacy collection is only read.
func (qc *QdrantClient) MigrateLegacy(ctx context.Context, embedder Embedder) (migrationReport, error) {
	report := migrationReport{}

	legacyInfo, err := qc.store.CollectionInfo(ctx, qc.legacyCollection)
	if err != nil {
		return report, fmt.Errorf("inspect legacy collection: %w", err)
	}
	st := migrationState{LegacyExists: legacyInfo != nil}
	if legacyInfo != nil {
		if st.LegacyCount, err = qc.store.Count(ctx, qc.legacyCollection); err != nil {
			return report, fmt.Errorf("count legacy collection: %w", err)
		}
		srcInfo, err := qc.store.CollectionInfo(ctx, qc.sourceCollection)
		if err != nil {
			return report, fmt.Errorf("inspect source collection: %w", err)
		}
		if srcInfo != nil {
			st.Marker = markerValue(srcInfo.Metadata, metaLegacyMigration)
		}
		if st.SourceCount, err = qc.store.Count(ctx, qc.sourceCollection); err != nil {
			return report, fmt.Errorf("count source collection: %w", err)
		}
	}

	report.Action = planLegacyMigration(st)
	switch report.Action {
	case migrationNone:
		return report, nil
	case migrationSkipForeignData:
		logger.Warnf("[memory] legacy collection %s has %d points but %s already holds %d records not created by a migration; skipping automatic migration",
			qc.legacyCollection, st.LegacyCount, qc.sourceCollection, st.SourceCount)
		return report, nil
	}

	logger.Infof("[memory] legacy migration (%s): copying %d points from %s into %s",
		report.Action, st.LegacyCount, qc.legacyCollection, qc.sourceCollection)

	if err := qc.store.SetCollectionMetadata(ctx, qc.sourceCollection, map[string]*qdrant.Value{
		metaLegacyMigration: qdrant.NewValueString(legacyMigrationInProgress),
	}); err != nil {
		return report, fmt.Errorf("mark migration in progress: %w", err)
	}

	report.VectorsReused = qc.shouldReuseLegacyVectors(ctx, legacyInfo.DenseSize, embedder)

	offset := ""
	for {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		page, next, err := qc.store.Scroll(ctx, qc.legacyCollection, nil, offset, tierSyncBatchSize, true, report.VectorsReused)
		if err != nil {
			return report, fmt.Errorf("scroll legacy collection: %w", err)
		}

		srcPoints := make([]*tierPoint, 0, len(page))
		vecPoints := make([]*tierPoint, 0, len(page))
		for _, pt := range page {
			if pt.ID == "" {
				continue
			}
			srcPoints = append(srcPoints, &tierPoint{ID: pt.ID, Payload: pt.Payload})
			if report.VectorsReused && len(pt.Dense) == qc.vectorSize {
				vp := &tierPoint{
					ID:            pt.ID,
					Payload:       vectorPayloadFromSource(pt.Payload),
					Dense:         pt.Dense,
					SparseIndices: pt.SparseIndices,
					SparseValues:  pt.SparseValues,
				}
				if len(vp.SparseIndices) == 0 {
					content, keywords := contentAndKeywords(pt.Payload)
					vp.SparseIndices, vp.SparseValues = computeBM25SparseVector(content, keywords)
				}
				vecPoints = append(vecPoints, vp)
			}
		}

		if err := qc.retryWithBackoff(ctx, "migrate source page", func() error {
			return qc.store.Upsert(ctx, qc.sourceCollection, srcPoints)
		}); err != nil {
			return report, err
		}
		if len(vecPoints) > 0 {
			if err := qc.retryWithBackoff(ctx, "migrate vector page", func() error {
				return qc.store.Upsert(ctx, qc.vectorCollection, vecPoints)
			}); err != nil {
				return report, err
			}
		}
		report.Copied += len(srcPoints)
		logger.Infof("[memory] legacy migration: copied %d/%d records (vectors reused: %v)", report.Copied, st.LegacyCount, report.VectorsReused)

		if next == "" || len(page) == 0 {
			break
		}
		offset = next
	}

	if err := qc.store.SetCollectionMetadata(ctx, qc.sourceCollection, map[string]*qdrant.Value{
		metaLegacyMigration:       qdrant.NewValueString(legacyMigrationDone),
		"legacy_migrated_records": qdrant.NewValueInt(int64(report.Copied)),
		"legacy_vectors_reused":   qdrant.NewValueBool(report.VectorsReused),
		"legacy_migrated_at":      qdrant.NewValueString(time.Now().UTC().Format(time.RFC3339)),
	}); err != nil {
		return report, fmt.Errorf("mark migration done: %w", err)
	}
	logger.Infof("[memory] legacy migration complete: %d records copied into %s; %s left untouched", report.Copied, qc.sourceCollection, qc.legacyCollection)
	return report, nil
}

// shouldReuseLegacyVectors decides whether legacy vectors can be copied into
// the current vector collection, probing a few records with the embedder.
func (qc *QdrantClient) shouldReuseLegacyVectors(ctx context.Context, legacyDim int, embedder Embedder) bool {
	if legacyDim != qc.vectorSize {
		logger.Infof("[memory] legacy vectors have dimension %d, current model %q uses %d: records will be re-embedded", legacyDim, qc.embeddingModel, qc.vectorSize)
		return false
	}

	var sims []float32
	if embedder != nil {
		page, _, err := qc.store.Scroll(ctx, qc.legacyCollection, nil, "", vectorReuseProbeCount, true, true)
		if err != nil {
			logger.Warnf("[memory] legacy vector probe: scroll failed: %v", err)
		}
		for _, pt := range page {
			content, _ := contentAndKeywords(pt.Payload)
			if content == "" || len(pt.Dense) != legacyDim {
				continue
			}
			emb, err := embedder.Embed(ctx, content)
			if err != nil {
				logger.Warnf("[memory] legacy vector probe: embedding failed, deciding by dimension only: %v", err)
				sims = nil
				break
			}
			sims = append(sims, cosineSimilarity(emb, pt.Dense))
		}
	}

	reuse := decideVectorReuse(legacyDim, qc.vectorSize, sims)
	switch {
	case reuse && len(sims) == 0:
		logger.Warnf("[memory] reusing legacy vectors based on matching dimension (%d) only; if the embedding model was changed at the same time, delete %s to force re-embedding", legacyDim, qc.vectorCollection)
	case reuse:
		logger.Infof("[memory] legacy vectors verified against model %q (probe similarities %v): reusing them", qc.embeddingModel, sims)
	default:
		logger.Infof("[memory] legacy vectors do not match model %q (probe similarities %v): records will be re-embedded", qc.embeddingModel, sims)
	}
	return reuse
}

// contentAndKeywords reads the text fields needed for embedding and BM25.
func contentAndKeywords(payload map[string]*qdrant.Value) (string, []string) {
	var content string
	if v, ok := payload["content"]; ok && v != nil {
		content = v.GetStringValue()
	}
	var keywords []string
	if v, ok := payload["keywords"]; ok && v != nil {
		for _, kw := range v.GetListValue().GetValues() {
			keywords = append(keywords, kw.GetStringValue())
		}
	}
	return content, keywords
}

// SyncVectors makes the current vector collection complete: it pages through
// the source tier, finds records with no point in the vector tier and embeds
// them with bounded concurrency. It is idempotent and resumable by record ID:
// an interrupted run simply continues with whatever is still missing on the
// next start. Records that fail to embed are counted and retried next time.
func (qc *QdrantClient) SyncVectors(ctx context.Context, embedder Embedder) (vectorSyncReport, error) {
	report := vectorSyncReport{}
	if embedder == nil {
		return report, fmt.Errorf("embedder is required")
	}

	total, err := qc.store.Count(ctx, qc.sourceCollection)
	if err != nil {
		return report, fmt.Errorf("count source records: %w", err)
	}
	vecCount, err := qc.store.Count(ctx, qc.vectorCollection)
	if err != nil {
		return report, fmt.Errorf("count vectors: %w", err)
	}
	logger.Infof("[memory] vector sync: %d source records, %d vectors in %s", total, vecCount, qc.vectorCollection)

	start := time.Now()
	offset := ""
	for {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		page, next, err := qc.store.Scroll(ctx, qc.sourceCollection, nil, offset, tierSyncBatchSize, true, false)
		if err != nil {
			return report, fmt.Errorf("scroll source records: %w", err)
		}

		embedded, failed, present, err := qc.syncPage(ctx, page, embedder)
		report.Scanned += len(page)
		report.Present += present
		report.Embedded += embedded
		report.Failed += failed
		if err != nil {
			return report, err
		}
		if embedded > 0 || failed > 0 {
			logger.Infof("[memory] vector sync: scanned %d/%d, embedded %d, failed %d", report.Scanned, total, report.Embedded, report.Failed)
		}

		if next == "" || len(page) == 0 {
			break
		}
		offset = next
	}

	logger.Infof("[memory] vector sync done in %s: scanned %d, already present %d, embedded %d, failed %d",
		time.Since(start).Round(time.Millisecond), report.Scanned, report.Present, report.Embedded, report.Failed)
	return report, nil
}

// syncPage embeds and upserts the records of one source page that are
// missing from the vector tier.
func (qc *QdrantClient) syncPage(ctx context.Context, page []*tierPoint, embedder Embedder) (embedded, failed, present int, err error) {
	if len(page) == 0 {
		return 0, 0, 0, nil
	}
	ids := make([]string, len(page))
	for i, pt := range page {
		ids[i] = pt.ID
	}
	existing, err := qc.store.Get(ctx, qc.vectorCollection, ids, false, false)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("check existing vectors: %w", err)
	}
	have := make(map[string]struct{}, len(existing))
	for _, pt := range existing {
		have[pt.ID] = struct{}{}
	}

	var missing []*tierPoint
	for _, pt := range page {
		if _, ok := have[pt.ID]; ok {
			present++
			continue
		}
		missing = append(missing, pt)
	}
	if len(missing) == 0 {
		return 0, 0, present, nil
	}

	results := make([]*tierPoint, len(missing))
	errs := make([]error, len(missing))
	sem := make(chan struct{}, tierSyncConcurrency)
	var wg sync.WaitGroup
	for i, pt := range missing {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, pt *tierPoint) {
			defer wg.Done()
			defer func() { <-sem }()
			content, keywords := contentAndKeywords(pt.Payload)
			if content == "" {
				errs[i] = fmt.Errorf("record %s has no content", pt.ID)
				return
			}
			emb, err := embedder.Embed(ctx, content)
			if err != nil {
				errs[i] = fmt.Errorf("embed record %s: %w", pt.ID, err)
				return
			}
			if len(emb) != qc.vectorSize {
				errs[i] = fmt.Errorf("%w: got %d, want %d", errEmbeddingDimMismatch, len(emb), qc.vectorSize)
				return
			}
			results[i] = vectorPointFor(pt.ID, pt.Payload, emb, content, keywords)
		}(i, pt)
	}
	wg.Wait()

	toWrite := make([]*tierPoint, 0, len(missing))
	for i, r := range results {
		if r != nil {
			toWrite = append(toWrite, r)
			continue
		}
		failed++
		if errors.Is(errs[i], errEmbeddingDimMismatch) {
			return 0, failed, present, fmt.Errorf("vector sync aborted: %w (check memory_pipeline.qdrant.vector_size against the embedding model)", errs[i])
		}
		logger.Warnf("[memory] vector sync: %v", errs[i])
	}

	if len(toWrite) > 0 {
		if err := qc.retryWithBackoff(ctx, "upsert synced vectors", func() error {
			return qc.store.Upsert(ctx, qc.vectorCollection, toWrite)
		}); err != nil {
			return 0, failed + len(toWrite), present, err
		}
	}
	return len(toWrite), failed, present, nil
}
