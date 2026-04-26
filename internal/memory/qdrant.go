package memory

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

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

// retrySleep overrides time.Sleep for tests. Nil means use real time.Sleep.
var retrySleep func(time.Duration)

const (
	maxRetries  = 3
	baseBackoff = 1 * time.Second
	maxBackoff  = 30 * time.Second
)

// retryWithBackoff is a thin compatibility wrapper around retry.Retry,
// preserved for backward compatibility with tests in this package.
func retryWithBackoff(ctx context.Context, operation string, fn func() error) error {
	_, err := retry.Retry(ctx, maxRetries, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, fn()
	}, retry.WithErrorClassifier(isRetryableGrpc),
		retry.WithBaseDelay(baseBackoff),
		retry.WithMaxDelay(maxBackoff))
	if err != nil {
		return fmt.Errorf("%s %w", operation, err)
	}
	return nil
}

// isRetryableGrpc returns true for gRPC status codes that warrant a retry.
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

const (
	// CollectionMemories is the name of the memories collection
	CollectionMemories = "memories"
	// CollectionProfiles is the name of the profiles collection
	CollectionProfiles = "profiles"
)

var ErrProfileNotFound = errors.New("profile not found")

// QdrantClient wraps the Qdrant client
type QdrantClient struct {
	client     *qdrant.Client
	host       string
	port       int
	vectorSize int
}

// NewQdrantClient creates a new Qdrant client using configuration from config package
func NewQdrantClient(cfg *config.QdrantConfig) (*QdrantClient, error) {
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
		client:     client,
		host:       cfg.Host,
		port:       cfg.Port,
		vectorSize: cfg.VectorSize,
	}

	// Initialize collections
	ctx := context.Background()
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

	return nil
}

// createCollectionIfNotExists creates a collection with proper configuration
func (qc *QdrantClient) createCollectionIfNotExists(ctx context.Context, name string) error {
	// Check if collection exists
	exists, err := qc.client.CollectionExists(ctx, name)
	if err != nil {
		return err
	}

	if exists {
		logger.Infof("Collection %s already exists", name)
		return nil
	}

	// Create collection with configured vector size
	err = qc.client.CreateCollection(ctx, &qdrant.CreateCollection{
		CollectionName: name,
		VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
			Size:     uint64(qc.vectorSize),
			Distance: qdrant.Distance_Cosine,
		}),
	})
	if err != nil {
		return fmt.Errorf("failed to create collection: %w", err)
	}

	logger.Infof("Created collection: %s", name)

	// Create payload indexes for filtering
	if err := qc.createPayloadIndexes(ctx, name); err != nil {
		logger.Warnf("Failed to create payload indexes for %s: %v", name, err)
	}

	return nil
}

// createPayloadIndexes creates indexes for payload fields used in filtering
func (qc *QdrantClient) createPayloadIndexes(ctx context.Context, collectionName string) error {
	// Only create indexes for memories collection
	if collectionName != CollectionMemories {
		return nil
	}

	// Create index for user_id field (required for filtering)
	_, err := qc.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
		CollectionName: collectionName,
		FieldName:      "user_id",
		FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
	})
	if err != nil {
		return fmt.Errorf("failed to create user_id index: %w", err)
	}

	// Create index for memory_type field
	_, err = qc.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
		CollectionName: collectionName,
		FieldName:      "memory_type",
		FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
	})
	if err != nil {
		return fmt.Errorf("failed to create memory_type index: %w", err)
	}

	logger.Infof("Created payload indexes for collection: %s", collectionName)
	return nil
}

// UpsertMemory stores or updates a memory
func (qc *QdrantClient) UpsertMemory(ctx context.Context, memory *Record) error {
	// Generate ID BEFORE retry loop to prevent duplicate records on retry
	if memory.ID == "" {
		memory.ID = uuid.New().String()
	}

	// Update timestamps
	if memory.CreatedAt.IsZero() {
		memory.CreatedAt = time.Now()
	}
	memory.UpdatedAt = time.Now()

	logger.Debugf("[UpsertMemory] userID=%s type=%s content=%.50s", memory.UserID, memory.MemoryType, memory.Content)

	// Prepare payload before retry loop (idempotent data only)
	payload := qc.memoryToPayload(memory)
	memID := memory.ID
	embedding := memory.Embedding

	// UUID 必须在重试之前生成以防止重复记录
	_, err := retry.Retry(ctx, maxRetries, func(ctx context.Context) (*qdrant.UpdateResult, error) {
		return qc.client.Upsert(ctx, &qdrant.UpsertPoints{
			CollectionName: CollectionMemories,
			Points: []*qdrant.PointStruct{
				{
					Id:      qdrant.NewID(memID),
					Vectors: qdrant.NewVectors(embedding...),
					Payload: payload,
				},
			},
		})
	}, retry.WithErrorClassifier(isRetryableGrpc), retry.WithBaseDelay(baseBackoff), retry.WithMaxDelay(maxBackoff))
	if err != nil {
		logger.Errorf("[UpsertMemory] failed to upsert memory for userID=%s: %v", memory.UserID, err)
		return fmt.Errorf("failed to upsert memory: %w", err)
	}
	logger.Debugf("[UpsertMemory] successfully stored memoryID=%s for userID=%s", memID, memory.UserID)
	return nil
}

// SearchMemories searches for similar memories
func (qc *QdrantClient) SearchMemories(ctx context.Context, userID string, embedding []float32, opts *SearchOptions) ([]*Record, error) {
	if opts == nil {
		opts = &SearchOptions{TopK: 5, MinScore: 0.75}
	}

	limit := uint64(opts.TopK)

	// Build filter for user_id
	filter := &qdrant.Filter{
		Must: []*qdrant.Condition{
			qdrant.NewMatch("user_id", userID),
		},
	}

	// Add memory type filter if specified
	if len(opts.MemoryTypes) > 0 {
		var conditions []*qdrant.Condition
		for _, mt := range opts.MemoryTypes {
			conditions = append(conditions, qdrant.NewMatch("memory_type", mt))
		}
		filter.Should = conditions
	}

	// Perform search using Query API
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

	// Convert results to memories
	var memories []*Record
	logger.Debugf("[SearchMemories] got %d results, min_score=%.4f", len(results), opts.MinScore)
	for i, result := range results {
		logger.Debugf("[SearchMemories] result %d: score=%.4f", i+1, result.Score)
		if result.Score < float32(opts.MinScore) {
			continue
		}
		memory, err := qc.payloadToMemory(result.Payload, result.Id.GetUuid())
		if err != nil {
			logger.Warnf("Failed to convert payload to memory: %v", err)
			continue
		}
		logger.Debugf("[SearchMemories] result %d: score=%.4f type=%s content=%q", i+1, result.Score, memory.MemoryType, memory.Content)
		memories = append(memories, memory)
	}

	return memories, nil
}

// GetMemoriesByUser retrieves all memories for a user
func (qc *QdrantClient) GetMemoriesByUser(ctx context.Context, userID string, limit int) ([]*Record, error) {
	if limit <= 0 {
		logger.Errorf("[GetMemoriesByUser] invalid limit=%d, must be greater than 0", limit)
		return nil, fmt.Errorf("limit must be greater than 0, got: %d", limit)
	}

	logger.Debugf("[GetMemoriesByUser] retrieving memories for userID=%s limit=%d", userID, limit)

	// Use scroll to get all memories for a user
	filter := &qdrant.Filter{
		Must: []*qdrant.Condition{
			qdrant.NewMatch("user_id", userID),
		},
	}

	limitPtr := uint64(limit)
	results, err := qc.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: CollectionMemories,
		Filter:         filter,
		Limit:          &limitPtr,
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		logger.Errorf("[GetMemoriesByUser] failed to query memories for userID=%s: %v", userID, err)
		return nil, fmt.Errorf("failed to query memories: %w", err)
	}

	var memories []*Record
	for _, point := range results {
		memory, err := qc.payloadToMemory(point.Payload, point.Id.GetUuid())
		if err != nil {
			logger.Warnf("[GetMemoriesByUser] failed to convert payload to memory: %v", err)
			continue
		}
		memories = append(memories, memory)
	}

	logger.Debugf("[GetMemoriesByUser] retrieved %d memories for userID=%s", len(memories), userID)
	return memories, nil
}

// GetMemory retrieves a single memory by ID
func (qc *QdrantClient) GetMemory(ctx context.Context, memoryID string) (*Record, error) {
	logger.Debugf("[GetMemory] retrieving memoryID=%s", memoryID)

	points, err := qc.client.Get(ctx, &qdrant.GetPoints{
		CollectionName: CollectionMemories,
		Ids: []*qdrant.PointId{
			qdrant.NewID(memoryID),
		},
		WithPayload: qdrant.NewWithPayload(true),
	})
	if err != nil {
		logger.Errorf("[GetMemory] failed to get memoryID=%s: %v", memoryID, err)
		return nil, fmt.Errorf("failed to get memory: %w", err)
	}

	if len(points) == 0 {
		logger.Warnf("[GetMemory] memory not found: memoryID=%s", memoryID)
		return nil, fmt.Errorf("memory not found")
	}

	memory, err := qc.payloadToMemory(points[0].Payload, memoryID)
	if err != nil {
		logger.Errorf("[GetMemory] failed to convert payload for memoryID=%s: %v", memoryID, err)
		return nil, err
	}

	logger.Debugf("[GetMemory] successfully retrieved memoryID=%s type=%s", memoryID, memory.MemoryType)
	return memory, nil
}

// DeleteMemory deletes a single memory
func (qc *QdrantClient) DeleteMemory(ctx context.Context, memoryID string) error {
	logger.Warnf("[DeleteMemory] deleting memoryID=%s", memoryID)

	_, err := qc.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: CollectionMemories,
		Points:         qdrant.NewPointsSelector(qdrant.NewID(memoryID)),
	})
	if err != nil {
		logger.Errorf("[DeleteMemory] failed to delete memoryID=%s: %v", memoryID, err)
		return fmt.Errorf("failed to delete memory: %w", err)
	}

	logger.Infof("[DeleteMemory] successfully deleted memoryID=%s", memoryID)
	return nil
}

// DeleteUserMemories deletes all memories for a user
func (qc *QdrantClient) DeleteUserMemories(ctx context.Context, userID string) error {
	logger.Warnf("[DeleteUserMemories] deleting all memories for userID=%s", userID)

	_, err := qc.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: CollectionMemories,
		Points: qdrant.NewPointsSelectorFilter(&qdrant.Filter{
			Must: []*qdrant.Condition{
				qdrant.NewMatch("user_id", userID),
			},
		}),
	})
	if err != nil {
		logger.Errorf("[DeleteUserMemories] failed to delete memories for userID=%s: %v", userID, err)
		return fmt.Errorf("failed to delete user memories: %w", err)
	}

	logger.Infof("[DeleteUserMemories] successfully deleted all memories for userID=%s", userID)
	return nil
}

// UpsertProfile stores or updates a user profile
func (qc *QdrantClient) UpsertProfile(ctx context.Context, profile *Profile) error {
	profile.LastActiveAt = time.Now()

	logger.Debugf("[UpsertProfile] storing profile for userID=%s messageCount=%d memoryCount=%d",
		profile.UserID, profile.MessageCount, profile.MemoryCount)

	// Prepare all data before retry loop
	payload := qc.profileToPayload(profile)

	embedding := profile.Embedding
	if len(embedding) == 0 {
		embedding = make([]float32, qc.vectorSize)
	}

	numID, err := discordIDToUint64(profile.UserID)
	if err != nil {
		return fmt.Errorf("upsert profile: %w", err)
	}

	_, err = retry.Retry(ctx, maxRetries, func(ctx context.Context) (*qdrant.UpdateResult, error) {
		return qc.client.Upsert(ctx, &qdrant.UpsertPoints{
			CollectionName: CollectionProfiles,
			Points: []*qdrant.PointStruct{
				{
					Id:      qdrant.NewIDNum(numID),
					Vectors: qdrant.NewVectors(embedding...),
					Payload: payload,
				},
			},
		})
	}, retry.WithErrorClassifier(isRetryableGrpc), retry.WithBaseDelay(baseBackoff), retry.WithMaxDelay(maxBackoff))
	if err != nil {
		logger.Errorf("[UpsertProfile] failed to upsert profile for userID=%s: %v", profile.UserID, err)
		return fmt.Errorf("failed to upsert profile: %w", err)
	}
	logger.Debugf("[UpsertProfile] successfully stored profile for userID=%s", profile.UserID)
	return nil
}

// GetProfile retrieves a user profile
func (qc *QdrantClient) GetProfile(ctx context.Context, userID string) (*Profile, error) {
	logger.Debugf("[GetProfile] getting profile for userID=%s", userID)

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
		logger.Debugf("[GetProfile] get error: %v", err)
		return nil, fmt.Errorf("failed to get profile: %w", err)
	}

	logger.Debugf("[GetProfile] got %d points", len(points))

	if len(points) == 0 {
		return nil, ErrProfileNotFound
	}

	point := points[0]
	logger.Debugf("[GetProfile] point ID: %v, payload keys: %v", point.Id, getPayloadKeys(point.Payload))
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

// DeleteProfile deletes a user profile
func (qc *QdrantClient) DeleteProfile(ctx context.Context, userID string) error {
	logger.Warnf("[DeleteProfile] deleting profile for userID=%s", userID)

	numID, err := discordIDToUint64(userID)
	if err != nil {
		return fmt.Errorf("delete profile: %w", err)
	}

	_, err = qc.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: CollectionProfiles,
		Points:         qdrant.NewPointsSelector(qdrant.NewIDNum(numID)),
	})
	if err != nil {
		logger.Errorf("[DeleteProfile] failed to delete profile for userID=%s: %v", userID, err)
		return fmt.Errorf("failed to delete profile: %w", err)
	}

	logger.Infof("[DeleteProfile] successfully deleted profile for userID=%s", userID)
	return nil
}
