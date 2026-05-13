package memory

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"ezyapper/internal/config"
	"ezyapper/internal/logger"
	"ezyapper/internal/retry"

	"github.com/google/uuid"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// discordIDToUint64 converts a Discord ID string to uint64 for Qdrant.
// Returns an error if the ID cannot be parsed (instead of silently returning 0).
func discordIDToUint64(discordID string) (uint64, error) {
	id, err := strconv.ParseUint(discordID, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid Discord ID %q: %w", discordID, err)
	}
	return id, nil
}

// QdrantClient wraps the Qdrant client
type QdrantClient struct {
	client      *qdrant.Client
	host        string
	port        int
	vectorSize  int
	maxRetries  int
	baseBackoff time.Duration
	maxBackoff  time.Duration
}

const (
	CollectionMemories      = "memories"
	CollectionProfiles      = "profiles"
	CollectionRelationships = "relationships"
)

var ErrProfileNotFound = errors.New("profile not found")

func (qc *QdrantClient) retryWithBackoff(ctx context.Context, operation string, fn func() error) error {
	_, err := retry.Retry(ctx, qc.maxRetries, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, fn()
	}, retry.WithErrorClassifier(isRetryableGrpc),
		retry.WithBaseDelay(qc.baseBackoff),
		retry.WithMaxDelay(qc.maxBackoff))
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}

func isRetryableGrpc(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
		return true
	default:
		return false
	}
}

// NewQdrantClient creates a new Qdrant client using configuration from config package.
func NewQdrantClient(ctx context.Context, cfg *config.QdrantConfig) (*QdrantClient, error) {
	qdrantCfg := &qdrant.Config{
		Host: cfg.Host,
		Port: cfg.Port,
	}

	// Add API key and enable TLS if provided
	if cfg.APIKey != "" {
		qdrantCfg.APIKey = cfg.APIKey
		qdrantCfg.UseTLS = true
	}

	client, err := qdrant.NewClient(qdrantCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create qdrant client: %w", err)
	}

	qc := &QdrantClient{
		client:      client,
		host:        cfg.Host,
		port:        cfg.Port,
		vectorSize:  cfg.VectorSize,
		maxRetries:  cfg.MaxRetries,
		baseBackoff: time.Duration(cfg.RetryBaseDelayMs) * time.Millisecond,
		maxBackoff:  time.Duration(cfg.RetryMaxDelayMs) * time.Millisecond,
	}

	// Initialize collections
	if err := qc.initializeCollections(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize collections: %w", err)
	}

	return qc, nil
}

// Close closes the connection
func (qc *QdrantClient) Close() error {
	if qc.client != nil {
		return qc.client.Close()
	}
	return nil
}

// initializeCollections creates collections if they don't exist
func (qc *QdrantClient) initializeCollections(ctx context.Context) error {
	// Create memories collection
	if err := qc.createCollectionIfNotExists(ctx, CollectionMemories); err != nil {
		return fmt.Errorf("failed to create memories collection: %w", err)
	}

	// Create profiles collection
	if err := qc.createCollectionIfNotExists(ctx, CollectionProfiles); err != nil {
		return fmt.Errorf("failed to create profiles collection: %w", err)
	}

	// Create relationships collection
	if err := qc.createCollectionIfNotExists(ctx, CollectionRelationships); err != nil {
		return fmt.Errorf("failed to create relationships collection: %w", err)
	}

	return nil
}

// createCollectionIfNotExists creates a collection with proper configuration
func (qc *QdrantClient) createCollectionIfNotExists(ctx context.Context, name string) error {
	exists, err := qc.client.CollectionExists(ctx, name)
	if err != nil {
		return err
	}
	if exists {
		logger.Infof("[qdrant] Collection %s already exists", name)
		return nil
	}

	createReq := &qdrant.CreateCollection{
		CollectionName: name,
		VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
			Size:     uint64(qc.vectorSize),
			Distance: qdrant.Distance_Cosine,
		}),
	}

	// Memories-specific: tenant HNSW + quantization + sparse vectors
	if name == CollectionMemories {
		createReq.HnswConfig = &qdrant.HnswConfigDiff{
			M:        qdrant.PtrOf(uint64(0)),
			PayloadM: qdrant.PtrOf(uint64(16)),
		}
		createReq.QuantizationConfig = qdrant.NewQuantizationScalar(&qdrant.ScalarQuantization{
			Type:      qdrant.QuantizationType_Int8,
			Quantile:  qdrant.PtrOf(float32(0.99)),
			AlwaysRam: qdrant.PtrOf(true),
		})
		createReq.SparseVectorsConfig = qdrant.NewSparseVectorsConfig(map[string]*qdrant.SparseVectorParams{
			"bm25_keywords": {},
		})
		createReq.OnDiskPayload = qdrant.PtrOf(true)
	}

	err = qc.client.CreateCollection(ctx, createReq)
	if err != nil {
		return fmt.Errorf("failed to create collection: %w", err)
	}

	logger.Infof("[qdrant] Created collection: %s", name)

	if err := qc.createPayloadIndexes(ctx, name); err != nil {
		logger.Warnf("[qdrant] Failed to create payload indexes for %s: %v", name, err)
	}
	return nil
}

// createPayloadIndexes creates indexes for payload fields used in filtering
func (qc *QdrantClient) createPayloadIndexes(ctx context.Context, collectionName string) error {
	switch collectionName {
	case CollectionMemories:
		return qc.createMemoriesPayloadIndexes(ctx, collectionName)
	case CollectionRelationships:
		return qc.createRelationshipsPayloadIndexes(ctx, collectionName)
	default:
		return nil
	}
}

func (qc *QdrantClient) createMemoriesPayloadIndexes(ctx context.Context, collectionName string) error {
	if _, err := qc.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
		CollectionName: collectionName,
		FieldName:      "user_id",
		FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
		FieldIndexParams: qdrant.NewPayloadIndexParamsKeyword(&qdrant.KeywordIndexParams{
			IsTenant: qdrant.PtrOf(true),
		}),
	}); err != nil {
		return fmt.Errorf("failed to create user_id index: %w", err)
	}

	if _, err := qc.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
		CollectionName: collectionName,
		FieldName:      "memory_type",
		FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
	}); err != nil {
		return fmt.Errorf("failed to create memory_type index: %w", err)
	}

	if _, err := qc.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
		CollectionName: collectionName,
		FieldName:      "created_at",
		FieldType:      qdrant.FieldType_FieldTypeDatetime.Enum(),
	}); err != nil {
		return fmt.Errorf("failed to create created_at index: %w", err)
	}

	if _, err := qc.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
		CollectionName: collectionName,
		FieldName:      "content",
		FieldType:      qdrant.FieldType_FieldTypeText.Enum(),
		FieldIndexParams: qdrant.NewPayloadIndexParamsText(&qdrant.TextIndexParams{
			Tokenizer: qdrant.TokenizerType_Word,
			Lowercase: qdrant.PtrOf(true),
		}),
	}); err != nil {
		return fmt.Errorf("failed to create content index: %w", err)
	}

	if _, err := qc.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
		CollectionName: collectionName,
		FieldName:      "mentioned_user_ids",
		FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
	}); err != nil {
		return fmt.Errorf("failed to create mentioned_user_ids index: %w", err)
	}

	if _, err := qc.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
		CollectionName: collectionName,
		FieldName:      "mentioned_channel_ids",
		FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
	}); err != nil {
		return fmt.Errorf("failed to create mentioned_channel_ids index: %w", err)
	}

	if _, err := qc.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
		CollectionName: collectionName,
		FieldName:      "channel_id",
		FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
	}); err != nil {
		return fmt.Errorf("failed to create channel_id index: %w", err)
	}

	logger.Infof("[qdrant] Created payload indexes for collection: %s", collectionName)
	return nil
}

func (qc *QdrantClient) createRelationshipsPayloadIndexes(ctx context.Context, collectionName string) error {
	if _, err := qc.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
		CollectionName: collectionName,
		FieldName:      "user_a",
		FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
	}); err != nil {
		return fmt.Errorf("failed to create user_a index: %w", err)
	}

	if _, err := qc.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
		CollectionName: collectionName,
		FieldName:      "user_b",
		FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
	}); err != nil {
		return fmt.Errorf("failed to create user_b index: %w", err)
	}

	if _, err := qc.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
		CollectionName: collectionName,
		FieldName:      "type",
		FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
	}); err != nil {
		return fmt.Errorf("failed to create type index: %w", err)
	}

	if _, err := qc.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
		CollectionName: collectionName,
		FieldName:      "weight",
		FieldType:      qdrant.FieldType_FieldTypeFloat.Enum(),
	}); err != nil {
		return fmt.Errorf("failed to create weight index: %w", err)
	}

	logger.Infof("[qdrant] Created payload indexes for collection: %s", collectionName)
	return nil
}

// UpsertMemory stores or updates a memory
func (qc *QdrantClient) UpsertMemory(ctx context.Context, memory *Record) error {
	if memory.ID == "" {
		memory.ID = uuid.New().String()
	}

	if memory.CreatedAt.IsZero() {
		memory.CreatedAt = time.Now()
	}

	logger.Debugf("[qdrant] userID=%s type=%s content=%.50s", memory.UserID, memory.MemoryType, memory.Content)

	payload, err := qc.memoryToPayload(memory)
	if err != nil {
		return fmt.Errorf("failed to prepare memory payload: %w", err)
	}
	memID := memory.ID
	embedding := memory.Embedding

	sparseIndices, sparseValues := computeBM25SparseVector(memory.Content, memory.Keywords)

	_, err = retry.Retry(ctx, qc.maxRetries, func(ctx context.Context) (*qdrant.UpdateResult, error) {
		var vectorsConfig *qdrant.Vectors
		if len(sparseIndices) > 0 {
			vectorsConfig = qdrant.NewVectorsMap(map[string]*qdrant.Vector{
				"":              qdrant.NewVectorDense(embedding),
				"bm25_keywords": qdrant.NewVectorSparse(sparseIndices, sparseValues),
			})
		} else {
			vectorsConfig = qdrant.NewVectors(embedding...)
		}

		return qc.client.Upsert(ctx, &qdrant.UpsertPoints{
			CollectionName: CollectionMemories,
			UpdateMode:     qdrant.UpdateMode_Upsert.Enum(),
			Points: []*qdrant.PointStruct{
				{
					Id:      qdrant.NewID(memID),
					Vectors: vectorsConfig,
					Payload: payload,
				},
			},
		})
	}, retry.WithErrorClassifier(isRetryableGrpc), retry.WithBaseDelay(qc.baseBackoff), retry.WithMaxDelay(qc.maxBackoff))
	if err != nil {
		return fmt.Errorf("upsert memory for userID=%s: %w", memory.UserID, err)
	}
	logger.Debugf("[qdrant] successfully stored memoryID=%s for userID=%s", memID, memory.UserID)
	return nil
}

// IncrementAccessCount atomically increments the access_count payload field
// and updates updated_at for the given memory IDs, without touching vectors.
func (qc *QdrantClient) IncrementAccessCount(ctx context.Context, memoryIDs []string, increments map[string]int) error {
	if len(memoryIDs) == 0 {
		return nil
	}

	now := float64(time.Now().UnixMilli()) / 1000.0
	pointIDs := make([]*qdrant.PointId, len(memoryIDs))
	for i, id := range memoryIDs {
		pointIDs[i] = qdrant.NewID(id)
	}

	// Build payload: updated_at is common across all points; access_count
	// differs per point. Both use SetPayload (merge semantics) to avoid
	// wiping existing payload fields.
	commonPayload := map[string]*qdrant.Value{
		"updated_at": {Kind: &qdrant.Value_DoubleValue{DoubleValue: now}},
	}

	_, err := qc.client.SetPayload(ctx, &qdrant.SetPayloadPoints{
		CollectionName: CollectionMemories,
		Payload:        commonPayload,
		PointsSelector: qdrant.NewPointsSelector(pointIDs...),
	})
	if err != nil {
		return fmt.Errorf("increment access count: %w", err)
	}

	for _, id := range memoryIDs {
		inc, ok := increments[id]
		if !ok {
			continue
		}
		perPointPayload := map[string]*qdrant.Value{
			"access_count": {Kind: &qdrant.Value_IntegerValue{IntegerValue: int64(inc)}},
		}
		_, err := qc.client.SetPayload(ctx, &qdrant.SetPayloadPoints{
			CollectionName: CollectionMemories,
			Payload:        perPointPayload,
			PointsSelector: qdrant.NewPointsSelector(qdrant.NewID(id)),
		})
		if err != nil {
			logger.Warnf("[qdrant] failed to set access_count for memoryID=%s: %v", id, err)
		}
	}

	return nil
}

// GetMemoryPayloads retrieves lightweight payload data for multiple memory IDs.
// Returns a map of memoryID -> access_count. Only fetches necessary fields.
func (qc *QdrantClient) GetMemoryPayloads(ctx context.Context, memoryIDs []string) (map[string]int, error) {
	if len(memoryIDs) == 0 {
		return map[string]int{}, nil
	}

	pointIDs := make([]*qdrant.PointId, len(memoryIDs))
	for i, id := range memoryIDs {
		pointIDs[i] = qdrant.NewID(id)
	}

	points, err := qc.client.Get(ctx, &qdrant.GetPoints{
		CollectionName: CollectionMemories,
		Ids:            pointIDs,
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("get memory payloads: %w", err)
	}

	result := make(map[string]int, len(points))
	for _, pt := range points {
		id := pt.Id.GetUuid()
		ac, err := getRequiredInt(pt.Payload, "access_count")
		if err != nil {
			logger.Warnf("[qdrant] missing access_count for memoryID=%s: %v", id, err)
			result[id] = 0
			continue
		}
		result[id] = int(ac)
	}
	return result, nil
}

// SearchMemories searches for similar memories. opts must be non-nil.
func (qc *QdrantClient) SearchMemories(ctx context.Context, userID string, embedding []float32, opts *SearchOptions) ([]*Record, error) {
	if opts == nil {
		return nil, fmt.Errorf("search options are required")
	}

	limit := uint64(opts.TopK)

	filter := &qdrant.Filter{
		Must: []*qdrant.Condition{
			qdrant.NewMatch("user_id", userID),
		},
	}

	// Add channel ID filter if specified
	if opts.ChannelID != "" {
		filter.Must = append(filter.Must, qdrant.NewMatch("channel_id", opts.ChannelID))
	}

	// Add memory type filter if specified
	if len(opts.MemoryTypes) > 0 {
		conditions := []*qdrant.Condition{}
		for _, mt := range opts.MemoryTypes {
			conditions = append(conditions, qdrant.NewMatch("memory_type", mt))
		}
		filter.Should = conditions
	}

	// Add mentioned user filter if specified
	if len(opts.MentionedUserIDs) > 0 {
		filter.Must = append(filter.Must, qdrant.NewMatchKeywords(
			"mentioned_user_ids", opts.MentionedUserIDs...,
		))
	}

	// Add mentioned channel filter if specified
	if len(opts.MentionedChannelIDs) > 0 {
		filter.Must = append(filter.Must, qdrant.NewMatchKeywords(
			"mentioned_channel_ids", opts.MentionedChannelIDs...,
		))
	}

	results, err := qc.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: CollectionMemories,
		Query:          qdrant.NewQuery(embedding...),
		Filter:         filter,
		Limit:          &limit,
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to search memories: %w", err)
	}

	memories := []*Record{}
	errs := []error{}
	logger.Debugf("[qdrant] got %d results, min_score=%.4f", len(results), opts.MinScore)
	for i, result := range results {
		logger.Debugf("[qdrant] result %d: score=%.4f", i+1, result.Score)
		if result.Score < float32(opts.MinScore) {
			continue
		}
		memory, err := qc.payloadToMemory(result.Payload, result.Id.GetUuid())
		if err != nil {
			logger.Warnf("[qdrant] Failed to convert payload to memory (id=%s): %v", result.Id.GetUuid(), err)
			errs = append(errs, fmt.Errorf("convert payload %s: %w", result.Id.GetUuid(), err))
			continue
		}
		logger.Debugf("[qdrant] result %d: score=%.4f type=%s content=%q", i+1, result.Score, memory.MemoryType, memory.Content)
		memories = append(memories, memory)
	}

	if len(errs) > 0 {
		logger.Warnf("[qdrant] %d payloads failed to convert", len(errs))
	}

	return memories, nil
}

// SearchMemoriesByChannel searches for memories in a specific channel using semantic similarity.
// Only filters by channel_id (no user_id restriction), allowing cross-user memory retrieval.
func (qc *QdrantClient) SearchMemoriesByChannel(ctx context.Context, channelID string, embedding []float32, opts *SearchOptions) ([]*Record, error) {
	if channelID == "" {
		return nil, fmt.Errorf("channel ID is required")
	}
	if opts == nil {
		return nil, fmt.Errorf("search options are required")
	}

	limit := uint64(opts.TopK)

	filter := &qdrant.Filter{
		Must: []*qdrant.Condition{
			qdrant.NewMatch("channel_id", channelID),
		},
	}

	if len(opts.MemoryTypes) > 0 {
		conditions := []*qdrant.Condition{}
		for _, mt := range opts.MemoryTypes {
			conditions = append(conditions, qdrant.NewMatch("memory_type", mt))
		}
		filter.Should = conditions
	}

	results, err := qc.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: CollectionMemories,
		Query:          qdrant.NewQuery(embedding...),
		Filter:         filter,
		Limit:          &limit,
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to search memories by channel: %w", err)
	}

	memories := []*Record{}
	errs := []error{}
	for i, result := range results {
		if result.Score < float32(opts.MinScore) {
			continue
		}
		memory, err := qc.payloadToMemory(result.Payload, result.Id.GetUuid())
		if err != nil {
			logger.Warnf("[qdrant] Failed to convert payload to memory (id=%s): %v", result.Id.GetUuid(), err)
			errs = append(errs, fmt.Errorf("convert payload %s: %w", result.Id.GetUuid(), err))
			continue
		}
		logger.Debugf("[qdrant] result %d: score=%.4f type=%s content=%q", i+1, result.Score, memory.MemoryType, memory.Content)
		memories = append(memories, memory)
	}

	if len(errs) > 0 {
		logger.Warnf("[qdrant] %d payloads failed to convert", len(errs))
	}

	return memories, nil
}

// GetMemoriesByUser retrieves all memories for a user
func (qc *QdrantClient) GetMemoriesByUser(ctx context.Context, userID string, limit int) ([]*Record, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be greater than 0, got: %d", limit)
	}

	logger.Debugf("[qdrant] retrieving memories for userID=%s limit=%d", userID, limit)

	// Use scroll to get all memories for a user
	filter := &qdrant.Filter{
		Must: []*qdrant.Condition{
			qdrant.NewMatch("user_id", userID),
		},
	}

	limitPtr := uint32(limit)
	results, err := qc.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: CollectionMemories,
		Filter:         filter,
		Limit:          &limitPtr,
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("query memories for userID=%s: %w", userID, err)
	}

	memories := []*Record{}
	for _, point := range results {
		memory, err := qc.payloadToMemory(point.Payload, point.Id.GetUuid())
		if err != nil {
			logger.Warnf("[qdrant] failed to convert payload to memory: %v", err)
			continue
		}
		memories = append(memories, memory)
	}

	logger.Debugf("[qdrant] retrieved %d memories for userID=%s", len(memories), userID)
	return memories, nil
}

// ListMemoriesByType retrieves all memories for a user filtered by memory_type
func (qc *QdrantClient) ListMemoriesByType(ctx context.Context, userID string, memoryType string) ([]*Record, error) {
	logger.Debugf("[qdrant] listing memories for userID=%s type=%s", userID, memoryType)

	filter := &qdrant.Filter{
		Must: []*qdrant.Condition{
			qdrant.NewMatch("user_id", userID),
			qdrant.NewMatch("memory_type", memoryType),
		},
	}

	limit := uint32(1000)
	results, err := qc.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: CollectionMemories,
		Filter:         filter,
		Limit:          &limit,
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("list memories for userID=%s type=%s: %w", userID, memoryType, err)
	}

	memories := []*Record{}
	for _, point := range results {
		memory, err := qc.payloadToMemory(point.Payload, point.Id.GetUuid())
		if err != nil {
			logger.Warnf("[qdrant] failed to convert payload to memory in list: %v", err)
			continue
		}
		memories = append(memories, memory)
	}

	logger.Debugf("[qdrant] listed %d memories for userID=%s type=%s", len(memories), userID, memoryType)
	return memories, nil
}

// ScrollMemories scrolls all memories with payload and vectors, limited to `limit` points.
func (qc *QdrantClient) ScrollMemories(ctx context.Context, limit uint32) ([]*scrollPoint, error) {
	limitPtr := limit
	results, err := qc.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: CollectionMemories,
		Limit:          &limitPtr,
		WithPayload:    qdrant.NewWithPayload(true),
		WithVectors:    qdrant.NewWithVectors(true),
	})
	if err != nil {
		return nil, fmt.Errorf("scroll memories: %w", err)
	}

	points := make([]*scrollPoint, 0, len(results))
	for _, pt := range results {
		sp := &scrollPoint{
			ID:      pt.Id.GetUuid(),
			Payload: pt.Payload,
		}
		if vo := pt.GetVectors(); vo != nil {
			if vout := vo.GetVector(); vout != nil {
				if dense, ok := vout.GetVector().(*qdrant.VectorOutput_Dense); ok {
					sp.Embedding = dense.Dense.Data
				}
			}
		}
		points = append(points, sp)
	}
	return points, nil
}

// DetectDamagedMemories scans memory points for payload damage.
// A point is damaged when its payload is empty or missing the schema_version key.
// Returns the list of damaged memory UUIDs.
func (qc *QdrantClient) DetectDamagedMemories(ctx context.Context, limit uint32) ([]string, error) {
	points, err := qc.ScrollMemories(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("detect damaged memories: %w", err)
	}

	damaged := detectDamagedPoints(points)
	logger.Infof("[qdrant] detect damaged memories: scanned %d, found %d damaged", len(points), len(damaged))
	return damaged, nil
}

// detectDamagedPoints checks each scrollPoint and returns UUIDs of points
// whose payload is empty or missing schema_version.
func detectDamagedPoints(points []*scrollPoint) []string {
	var damaged []string
	for _, pt := range points {
		if len(pt.Payload) == 0 {
			damaged = append(damaged, pt.ID)
			continue
		}
		if _, ok := pt.Payload["schema_version"]; !ok {
			damaged = append(damaged, pt.ID)
		}
	}
	return damaged
}

// repairOrDeleteDamagedPoints deletes damaged points and returns counts + affected user IDs.
// It iterates point payloads, deletes those detected as damaged, extracts user_id
// from raw payload, and accumulates unique user IDs.
func repairOrDeleteDamagedPoints(ctx context.Context, points []*scrollPoint, deleteFn func(ctx context.Context, id string) error) (deleted int, userIDs []string) {
	damaged := detectDamagedPoints(points)
	if len(damaged) == 0 {
		return 0, nil
	}

	damagedSet := make(map[string]bool, len(damaged))
	for _, id := range damaged {
		damagedSet[id] = true
	}

	seenUsers := make(map[string]struct{})

	for _, pt := range points {
		if !damagedSet[pt.ID] {
			continue
		}

		// Extract user_id from raw payload before deletion.
		uid := ""
		if v, ok := pt.Payload["user_id"]; ok && v != nil {
			uid = v.GetStringValue()
		}

		if err := deleteFn(ctx, pt.ID); err != nil {
			logger.Errorf("[qdrant] repair: failed to delete damaged memoryID=%s: %v", pt.ID, err)
			continue
		}

		deleted++

		if uid != "" {
			if _, exists := seenUsers[uid]; !exists {
				seenUsers[uid] = struct{}{}
				userIDs = append(userIDs, uid)
			}
			logger.Infof("[qdrant] repair: deleted damaged memoryID=%s userID=%s", pt.ID, uid)
		} else {
			logger.Infof("[qdrant] repair: deleted damaged memoryID=%s (no user_id)", pt.ID)
		}
	}

	return deleted, userIDs
}

// RepairOrDeleteDamagedMemories scans memory points for payload damage,
// deletes damaged points, and returns the count of deleted points plus
// the deduplicated list of affected user IDs.
func (qc *QdrantClient) RepairOrDeleteDamagedMemories(ctx context.Context, limit uint32) (deleted int, userIDs []string, err error) {
	points, err := qc.ScrollMemories(ctx, limit)
	if err != nil {
		return 0, nil, fmt.Errorf("repair damaged memories: %w", err)
	}

	deleted, userIDs = repairOrDeleteDamagedPoints(ctx, points, qc.DeletePoint)
	logger.Infof("[qdrant] repair: scanned %d points, deleted %d damaged, affected %d users", len(points), deleted, len(userIDs))
	return deleted, userIDs, nil
}

// ScrollRelationships scrolls all relationship points with payload, limited to `limit` points.
func (qc *QdrantClient) ScrollRelationships(ctx context.Context, limit uint32) ([]*scrollRelationshipPoint, error) {
	limitPtr := limit
	results, err := qc.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: CollectionRelationships,
		Limit:          &limitPtr,
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("scroll relationships: %w", err)
	}

	points := make([]*scrollRelationshipPoint, 0, len(results))
	for _, pt := range results {
		sp := &scrollRelationshipPoint{
			ID:      pt.Id.GetUuid(),
			Payload: pt.Payload,
		}
		points = append(points, sp)
	}
	return points, nil
}

// DeletePoint deletes a single point by ID from the memories collection.
func (qc *QdrantClient) DeletePoint(ctx context.Context, id string) error {
	_, err := qc.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: CollectionMemories,
		Points:         qdrant.NewPointsSelector(qdrant.NewID(id)),
	})
	if err != nil {
		return fmt.Errorf("delete point %s: %w", id, err)
	}
	return nil
}

// DeleteRelationshipPoint deletes a single point by ID from the relationships collection.
func (qc *QdrantClient) DeleteRelationshipPoint(ctx context.Context, id string) error {
	_, err := qc.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: CollectionRelationships,
		Points:         qdrant.NewPointsSelector(qdrant.NewID(id)),
	})
	if err != nil {
		return fmt.Errorf("delete relationship point %s: %w", id, err)
	}
	return nil
}

// GetMemory retrieves a single memory by ID
func (qc *QdrantClient) GetMemory(ctx context.Context, memoryID string) (*Record, error) {
	logger.Debugf("[qdrant] retrieving memoryID=%s", memoryID)

	points, err := qc.client.Get(ctx, &qdrant.GetPoints{
		CollectionName: CollectionMemories,
		Ids: []*qdrant.PointId{
			qdrant.NewID(memoryID),
		},
		WithPayload: qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("get memory %s: %w", memoryID, err)
	}

	if len(points) == 0 {
		logger.Warnf("[qdrant] memory not found: memoryID=%s", memoryID)
		return nil, fmt.Errorf("memory not found")
	}

	memory, err := qc.payloadToMemory(points[0].Payload, memoryID)
	if err != nil {
		return nil, fmt.Errorf("convert payload for memoryID=%s: %w", memoryID, err)
	}

	logger.Debugf("[qdrant] successfully retrieved memoryID=%s type=%s", memoryID, memory.MemoryType)
	return memory, nil
}

// DeleteMemory deletes a single memory
func (qc *QdrantClient) DeleteMemory(ctx context.Context, memoryID string) error {
	logger.Warnf("[qdrant] deleting memoryID=%s", memoryID)

	_, err := qc.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: CollectionMemories,
		Points:         qdrant.NewPointsSelector(qdrant.NewID(memoryID)),
	})
	if err != nil {
		return fmt.Errorf("delete memory %s: %w", memoryID, err)
	}

	logger.Infof("[qdrant] successfully deleted memoryID=%s", memoryID)
	return nil
}

// DeleteUserMemories deletes all memories for a user
func (qc *QdrantClient) DeleteUserMemories(ctx context.Context, userID string) error {
	logger.Warnf("[qdrant] deleting all memories for userID=%s", userID)

	_, err := qc.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: CollectionMemories,
		Points: qdrant.NewPointsSelectorFilter(&qdrant.Filter{
			Must: []*qdrant.Condition{
				qdrant.NewMatch("user_id", userID),
			},
		}),
	})
	if err != nil {
		return fmt.Errorf("delete user memories for userID=%s: %w", userID, err)
	}

	logger.Infof("[qdrant] successfully deleted all memories for userID=%s", userID)
	return nil
}

// UpsertProfile stores or updates a user profile
func (qc *QdrantClient) UpsertProfile(ctx context.Context, profile *Profile) error {
	profile.LastActiveAt = time.Now()

	logger.Debugf("[qdrant] storing profile for userID=%s messageCount=%d memoryCount=%d",
		profile.UserID, profile.MessageCount, profile.MemoryCount)

	// Prepare all data before retry loop
	payload, err := qc.profileToPayload(profile)
	if err != nil {
		return fmt.Errorf("failed to prepare profile payload: %w", err)
	}

	embedding := profile.Embedding
	var vectors *qdrant.Vectors
	if len(embedding) > 0 {
		vectors = qdrant.NewVectors(embedding...)
	} else {
		// Qdrant requires vectors for all points; use a zero-vector placeholder
		vectors = qdrant.NewVectors(make([]float32, qc.vectorSize)...)
	}

	numID, err := discordIDToUint64(profile.UserID)
	if err != nil {
		return fmt.Errorf("upsert profile: %w", err)
	}

	_, err = retry.Retry(ctx, qc.maxRetries, func(ctx context.Context) (*qdrant.UpdateResult, error) {
		return qc.client.Upsert(ctx, &qdrant.UpsertPoints{
			CollectionName: CollectionProfiles,
			Points: []*qdrant.PointStruct{
				{
					Id:      qdrant.NewIDNum(numID),
					Vectors: vectors,
					Payload: payload,
				},
			},
		})
	}, retry.WithErrorClassifier(isRetryableGrpc), retry.WithBaseDelay(qc.baseBackoff), retry.WithMaxDelay(qc.maxBackoff))
	if err != nil {
		return fmt.Errorf("upsert profile for userID=%s: %w", profile.UserID, err)
	}
	logger.Debugf("[qdrant] successfully stored profile for userID=%s", profile.UserID)
	return nil
}

// GetProfile retrieves a user profile
func (qc *QdrantClient) GetProfile(ctx context.Context, userID string) (*Profile, error) {
	logger.Debugf("[qdrant] getting profile for userID=%s", userID)

	numID, err := discordIDToUint64(userID)
	if err != nil {
		return nil, fmt.Errorf("get profile: %w", err)
	}

	points, err := qc.client.Get(ctx, &qdrant.GetPoints{
		CollectionName: CollectionProfiles,
		Ids: []*qdrant.PointId{
			qdrant.NewIDNum(numID),
		},
		WithPayload: qdrant.NewWithPayload(true),
	})
	if err != nil {
		logger.Debugf("[qdrant] get error: %v", err)
		return nil, fmt.Errorf("failed to get profile: %w", err)
	}

	logger.Debugf("[qdrant] got %d points", len(points))

	if len(points) == 0 {
		return nil, ErrProfileNotFound
	}

	point := points[0]
	logger.Debugf("[qdrant] point ID: %v, payload keys: %v", point.Id, getPayloadKeys(point.Payload))
	return qc.payloadToProfile(point.Payload, userID)
}

// getPayloadKeys returns the keys from a payload map for debugging
func getPayloadKeys(payload map[string]*qdrant.Value) []string {
	keys := make([]string, 0, len(payload))
	for k := range payload {
		keys = append(keys, k)
	}
	return keys
}

// CountCollection returns the exact point count for a collection.
func (qc *QdrantClient) CountCollection(ctx context.Context, collectionName string) (uint64, error) {
	exact := true
	count, err := qc.client.Count(ctx, &qdrant.CountPoints{
		CollectionName: collectionName,
		Exact:          &exact,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to count collection %s: %w", collectionName, err)
	}
	return count, nil
}

// DeleteProfile deletes a user profile
func (qc *QdrantClient) DeleteProfile(ctx context.Context, userID string) error {
	logger.Warnf("[qdrant] deleting profile for userID=%s", userID)

	numID, err := discordIDToUint64(userID)
	if err != nil {
		return fmt.Errorf("delete profile: %w", err)
	}

	_, err = qc.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: CollectionProfiles,
		Points:         qdrant.NewPointsSelector(qdrant.NewIDNum(numID)),
	})
	if err != nil {
		return fmt.Errorf("delete profile for userID=%s: %w", userID, err)
	}

	logger.Infof("[qdrant] successfully deleted profile for userID=%s", userID)
	return nil
}

// UpsertRelationship stores or updates a relationship record.
// If rel.ID is empty, a new UUID is generated. Uses retry with backoff for transient failures.
func (qc *QdrantClient) UpsertRelationship(ctx context.Context, rel *Relationship) error {
	if rel.ID == "" {
		rel.ID = uuid.New().String()
	}

	logger.Debugf("[qdrant] upserting relationship user_a=%s user_b=%s type=%s",
		rel.UserA, rel.UserB, rel.Type)

	payload, err := qc.relationshipToPayload(rel)
	if err != nil {
		return fmt.Errorf("failed to prepare relationship payload: %w", err)
	}

	relID := rel.ID
	vectors := qdrant.NewVectors(make([]float32, qc.vectorSize)...)

	_, err = retry.Retry(ctx, qc.maxRetries, func(ctx context.Context) (*qdrant.UpdateResult, error) {
		return qc.client.Upsert(ctx, &qdrant.UpsertPoints{
			CollectionName: CollectionRelationships,
			Points: []*qdrant.PointStruct{
				{
					Id:      qdrant.NewID(relID),
					Vectors: vectors,
					Payload: payload,
				},
			},
		})
	}, retry.WithErrorClassifier(isRetryableGrpc), retry.WithBaseDelay(qc.baseBackoff), retry.WithMaxDelay(qc.maxBackoff))
	if err != nil {
		return fmt.Errorf("upsert relationship for user_a=%s user_b=%s: %w", rel.UserA, rel.UserB, err)
	}

	logger.Debugf("[qdrant] successfully stored relationship id=%s", relID)
	return nil
}

// GetRelationships retrieves all relationships involving the given user.
// Optionally filters by relationship type.
func (qc *QdrantClient) GetRelationships(ctx context.Context, userID string, relType RelationshipType) ([]*Relationship, error) {
	logger.Debugf("[qdrant] getting relationships for userID=%s type=%s", userID, relType)

	if relType != "" {
		// Must match: (user_a = userID OR user_b = userID) AND type = relType
		filter := &qdrant.Filter{
			Must: []*qdrant.Condition{
				qdrant.NewFilterAsCondition(&qdrant.Filter{
					Should: []*qdrant.Condition{
						qdrant.NewMatch("user_a", userID),
						qdrant.NewMatch("user_b", userID),
					},
				}),
				qdrant.NewMatch("type", string(relType)),
			},
		}

		return qc.scrollRelationships(ctx, filter, userID)
	}

	// Should with user_a OR user_b — acts as OR when Must is empty
	filter := &qdrant.Filter{
		Should: []*qdrant.Condition{
			qdrant.NewMatch("user_a", userID),
			qdrant.NewMatch("user_b", userID),
		},
	}

	return qc.scrollRelationships(ctx, filter, userID)
}

// scrollRelationships executes a scroll query and converts results to Relationship records.
func (qc *QdrantClient) scrollRelationships(ctx context.Context, filter *qdrant.Filter, userID string) ([]*Relationship, error) {
	results, err := qc.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: CollectionRelationships,
		Filter:         filter,
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get relationships for userID=%s: %w", userID, err)
	}

	relationships := []*Relationship{}
	for _, point := range results {
		rel, err := qc.payloadToRelationship(point.Payload, point.Id.GetUuid())
		if err != nil {
			logger.Warnf("[qdrant] failed to convert payload to relationship: %v", err)
			continue
		}
		relationships = append(relationships, rel)
	}

	logger.Debugf("[qdrant] retrieved %d relationships for userID=%s", len(relationships), userID)
	return relationships, nil
}

// GetRelationshipBetween retrieves relationships between two specific users.
// Optionally filters by relationship type.
func (qc *QdrantClient) GetRelationshipBetween(ctx context.Context, userA, userB string, relType RelationshipType) ([]*Relationship, error) {
	logger.Debugf("[qdrant] getting relationship between user_a=%s user_b=%s type=%s", userA, userB, relType)

	// (user_a = A AND user_b = B) OR (user_a = B AND user_b = A)
	userFilter := &qdrant.Filter{
		Should: []*qdrant.Condition{
			qdrant.NewFilterAsCondition(&qdrant.Filter{
				Must: []*qdrant.Condition{
					qdrant.NewMatch("user_a", userA),
					qdrant.NewMatch("user_b", userB),
				},
			}),
			qdrant.NewFilterAsCondition(&qdrant.Filter{
				Must: []*qdrant.Condition{
					qdrant.NewMatch("user_a", userB),
					qdrant.NewMatch("user_b", userA),
				},
			}),
		},
	}

	var filter *qdrant.Filter
	if relType != "" {
		filter = &qdrant.Filter{
			Must: []*qdrant.Condition{
				qdrant.NewFilterAsCondition(userFilter),
				qdrant.NewMatch("type", string(relType)),
			},
		}
	} else {
		filter = userFilter
	}

	results, err := qc.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: CollectionRelationships,
		Filter:         filter,
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get relationship between user_a=%s user_b=%s: %w", userA, userB, err)
	}

	relationships := []*Relationship{}
	for _, point := range results {
		rel, err := qc.payloadToRelationship(point.Payload, point.Id.GetUuid())
		if err != nil {
			logger.Warnf("[qdrant] failed to convert payload to relationship: %v", err)
			continue
		}
		relationships = append(relationships, rel)
	}

	logger.Debugf("[qdrant] retrieved %d relationships between user_a=%s user_b=%s", len(relationships), userA, userB)
	return relationships, nil
}

// RemoveUserFromMentions finds all memory records where mentioned_user_ids
// contains userID and removes it from the array. Logs a warning on failure
// but does not return an error to avoid blocking other operations.
func (qc *QdrantClient) RemoveUserFromMentions(ctx context.Context, userID string) {
	logger.Debugf("[qdrant] removing userID=%s from mentioned_user_ids", userID)

	filter := &qdrant.Filter{
		Must: []*qdrant.Condition{
			qdrant.NewMatch("mentioned_user_ids", userID),
		},
	}

	var offset *qdrant.PointId
	pageLimit := uint32(100)

	var totalRemoved, totalFailed int
	for {
		scrollResult, err := qc.client.Scroll(ctx, &qdrant.ScrollPoints{
			CollectionName: CollectionMemories,
			Filter:         filter,
			Offset:         offset,
			Limit:          &pageLimit,
			WithPayload:    qdrant.NewWithPayload(true),
		})
		if err != nil {
			logger.Warnf("[qdrant] failed to scroll memories for mention removal: %v", err)
			return
		}

		if len(scrollResult) == 0 {
			break
		}

		for _, point := range scrollResult {
			memory, err := qc.payloadToMemory(point.Payload, point.Id.GetUuid())
			if err != nil {
				logger.Warnf("[qdrant] failed to convert payload for mention removal (id=%s): %v", point.Id.GetUuid(), err)
				totalFailed++
				continue
			}

			newMentions := make([]string, 0, len(memory.MentionedUserIDs))
			for _, uid := range memory.MentionedUserIDs {
				if uid != userID {
					newMentions = append(newMentions, uid)
				}
			}

			vals := make([]*qdrant.Value, 0, len(newMentions))
			for _, uid := range newMentions {
				vals = append(vals, &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: uid}})
			}

			updatedPayload := map[string]*qdrant.Value{
				"mentioned_user_ids": {Kind: &qdrant.Value_ListValue{ListValue: &qdrant.ListValue{Values: vals}}},
			}

			_, err = qc.client.SetPayload(ctx, &qdrant.SetPayloadPoints{
				CollectionName: CollectionMemories,
				Payload:        updatedPayload,
				PointsSelector: qdrant.NewPointsSelector(point.Id),
			})
			if err != nil {
				logger.Warnf("[qdrant] failed to update mentions for memoryID=%s: %v", point.Id.GetUuid(), err)
				totalFailed++
				continue
			}
			totalRemoved++
		}

		if scrollResult[len(scrollResult)-1].Id == nil {
			break
		}
		offset = scrollResult[len(scrollResult)-1].Id
	}

	if totalRemoved > 0 || totalFailed > 0 {
		logger.Infof("[qdrant] mention removal complete: %d updated, %d failed", totalRemoved, totalFailed)
	}
}

func computeBM25SparseVector(content string, keywords []string) ([]uint32, []float32) {
	const k1, b = 1.2, 0.75

	tokens := tokenize(strings.ToLower(content))
	for _, kw := range keywords {
		tokens = append(tokens, strings.ToLower(kw))
	}
	if len(tokens) == 0 {
		return nil, nil
	}

	tf := make(map[string]int)
	for _, t := range tokens {
		tf[t]++
	}

	avgdl := 10.0
	dl := float64(len(tokens))

	type entry struct {
		idx uint32
		w   float32
	}
	entries := make([]entry, 0, len(tf))
	for term, freq := range tf {
		tfNorm := (float64(freq) * (k1 + 1)) / (float64(freq) + k1*(1-b+b*dl/avgdl))
		idx := hashToken(term)
		entries = append(entries, entry{idx: idx, w: float32(tfNorm)})
	}

	slices.SortFunc(entries, func(a, b entry) int {
		if a.idx < b.idx {
			return -1
		}
		if a.idx > b.idx {
			return 1
		}
		return 0
	})

	indices := make([]uint32, len(entries))
	values := make([]float32, len(entries))
	for i, e := range entries {
		indices[i] = e.idx
		values[i] = e.w
	}
	return indices, values
}

func tokenize(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func hashToken(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32() % (1 << 24)
}
