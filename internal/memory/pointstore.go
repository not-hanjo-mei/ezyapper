package memory

import (
	"context"
	"fmt"

	"github.com/qdrant/go-client/qdrant"
)

// tierPoint is a collection-agnostic view of a Qdrant point used by the
// two-tier memory store. Dense is empty for source-tier points.
type tierPoint struct {
	ID            string
	Payload       map[string]*qdrant.Value
	Dense         []float32
	SparseIndices []uint32
	SparseValues  []float32
}

// scoredID is a search hit from the vector tier: the record ID and its score.
// Records are hydrated from the source tier by this ID.
type scoredID struct {
	ID    string
	Score float32
}

// collectionInfo describes an existing collection.
type collectionInfo struct {
	// DenseSize is the size of the default (unnamed) dense vector, or 0 when
	// the collection has no default dense vector (e.g. a payload-only source
	// collection).
	DenseSize  int
	PointCount uint64
	Metadata   map[string]*qdrant.Value
}

// vectorQuery is a search request against a vector-tier collection.
// When SparseIndices is non-empty and Hybrid is true, the dense and sparse
// result lists are fused with RRF.
type vectorQuery struct {
	Dense         []float32
	SparseIndices []uint32
	SparseValues  []float32
	Hybrid        bool
	Filter        *qdrant.Filter
	Limit         uint64
}

// pointStore is the minimal set of Qdrant collection/point primitives the
// two-tier memory store is built on. It exists so the tier logic (search +
// hydrate, migration, re-embedding, dual-tier deletes) can be unit tested
// against an in-memory fake instead of a live Qdrant.
type pointStore interface {
	ListCollections(ctx context.Context) ([]string, error)
	// CollectionInfo returns (nil, nil) when the collection does not exist.
	CollectionInfo(ctx context.Context, name string) (*collectionInfo, error)
	CreateSourceCollection(ctx context.Context, name string) error
	CreateVectorCollection(ctx context.Context, name string, dim int) error
	// SetCollectionMetadata merges the given keys into the collection metadata.
	SetCollectionMetadata(ctx context.Context, name string, md map[string]*qdrant.Value) error

	Upsert(ctx context.Context, collection string, points []*tierPoint) error
	Get(ctx context.Context, collection string, ids []string, withPayload, withVectors bool) ([]*tierPoint, error)
	// Scroll returns one page starting at offset ("" for the first page) and
	// the offset of the next page ("" when there are no more pages).
	Scroll(ctx context.Context, collection string, filter *qdrant.Filter, offset string, limit uint32, withPayload, withVectors bool) ([]*tierPoint, string, error)
	Query(ctx context.Context, collection string, q *vectorQuery) ([]scoredID, error)
	SetPayload(ctx context.Context, collection string, ids []string, payload map[string]*qdrant.Value) error
	Delete(ctx context.Context, collection string, ids []string) error
	DeleteByFilter(ctx context.Context, collection string, filter *qdrant.Filter) error
	Count(ctx context.Context, collection string) (uint64, error)
}

// sparseVectorName is the named sparse vector holding BM25-style keyword
// weights. It is derived from the record text, not from the embedding model,
// but lives in the vector tier next to the dense vector so hybrid queries can
// fuse both in a single request.
const sparseVectorName = "bm25_keywords"

// grpcPointStore implements pointStore on top of the official Qdrant client.
type grpcPointStore struct {
	client *qdrant.Client
}

func (s *grpcPointStore) ListCollections(ctx context.Context) ([]string, error) {
	return s.client.ListCollections(ctx)
}

func (s *grpcPointStore) CollectionInfo(ctx context.Context, name string) (*collectionInfo, error) {
	exists, err := s.client.CollectionExists(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("check collection %s: %w", name, err)
	}
	if !exists {
		return nil, nil
	}
	info, err := s.client.GetCollectionInfo(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("get collection info %s: %w", name, err)
	}
	out := &collectionInfo{
		PointCount: info.GetPointsCount(),
		Metadata:   info.GetConfig().GetMetadata(),
	}
	if params := info.GetConfig().GetParams().GetVectorsConfig().GetParams(); params != nil {
		out.DenseSize = int(params.GetSize())
	} else if m := info.GetConfig().GetParams().GetVectorsConfig().GetParamsMap().GetMap(); m != nil {
		if p, ok := m[""]; ok && p != nil {
			out.DenseSize = int(p.GetSize())
		}
	}
	return out, nil
}

// CreateSourceCollection creates a payload-only collection. Qdrant accepts an
// empty named-vectors map as the vectors config; points are then upserted with
// an empty vectors map. Verified against Qdrant 1.20 with go-client v1.17.
func (s *grpcPointStore) CreateSourceCollection(ctx context.Context, name string) error {
	if err := s.client.CreateCollection(ctx, &qdrant.CreateCollection{
		CollectionName: name,
		VectorsConfig:  qdrant.NewVectorsConfigMap(map[string]*qdrant.VectorParams{}),
		OnDiskPayload:  qdrant.PtrOf(true),
	}); err != nil {
		return fmt.Errorf("create source collection %s: %w", name, err)
	}
	return createSourcePayloadIndexes(ctx, s.client, name)
}

func (s *grpcPointStore) CreateVectorCollection(ctx context.Context, name string, dim int) error {
	if dim <= 0 {
		return fmt.Errorf("create vector collection %s: invalid dimension %d", name, dim)
	}
	if err := s.client.CreateCollection(ctx, &qdrant.CreateCollection{
		CollectionName: name,
		VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
			Size:     uint64(dim),
			Distance: qdrant.Distance_Cosine,
		}),
		// Tenant-aware HNSW: no global graph, per-user (payload) graphs.
		HnswConfig: &qdrant.HnswConfigDiff{
			M:        qdrant.PtrOf(uint64(0)),
			PayloadM: qdrant.PtrOf(uint64(16)),
		},
		QuantizationConfig: qdrant.NewQuantizationScalar(&qdrant.ScalarQuantization{
			Type:      qdrant.QuantizationType_Int8,
			Quantile:  qdrant.PtrOf(float32(0.99)),
			AlwaysRam: qdrant.PtrOf(true),
		}),
		SparseVectorsConfig: qdrant.NewSparseVectorsConfig(map[string]*qdrant.SparseVectorParams{
			sparseVectorName: {},
		}),
	}); err != nil {
		return fmt.Errorf("create vector collection %s: %w", name, err)
	}
	return createVectorPayloadIndexes(ctx, s.client, name)
}

func (s *grpcPointStore) SetCollectionMetadata(ctx context.Context, name string, md map[string]*qdrant.Value) error {
	if err := s.client.UpdateCollection(ctx, &qdrant.UpdateCollection{
		CollectionName: name,
		Metadata:       md,
	}); err != nil {
		return fmt.Errorf("update metadata of %s: %w", name, err)
	}
	return nil
}

func toPointStruct(p *tierPoint) *qdrant.PointStruct {
	var vectors *qdrant.Vectors
	switch {
	case len(p.Dense) > 0 && len(p.SparseIndices) > 0:
		vectors = qdrant.NewVectorsMap(map[string]*qdrant.Vector{
			"":               qdrant.NewVectorDense(p.Dense),
			sparseVectorName: qdrant.NewVectorSparse(p.SparseIndices, p.SparseValues),
		})
	case len(p.Dense) > 0:
		vectors = qdrant.NewVectors(p.Dense...)
	default:
		// Payload-only point (source tier): Qdrant requires a vectors field,
		// an empty named map is accepted.
		vectors = qdrant.NewVectorsMap(map[string]*qdrant.Vector{})
	}
	return &qdrant.PointStruct{
		Id:      qdrant.NewID(p.ID),
		Vectors: vectors,
		Payload: p.Payload,
	}
}

func (s *grpcPointStore) Upsert(ctx context.Context, collection string, points []*tierPoint) error {
	if len(points) == 0 {
		return nil
	}
	structs := make([]*qdrant.PointStruct, len(points))
	for i, p := range points {
		structs[i] = toPointStruct(p)
	}
	_, err := s.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: collection,
		Wait:           qdrant.PtrOf(true),
		Points:         structs,
	})
	if err != nil {
		return fmt.Errorf("upsert %d points into %s: %w", len(points), collection, err)
	}
	return nil
}

func pointIDString(id *qdrant.PointId) string {
	if id == nil {
		return ""
	}
	if u := id.GetUuid(); u != "" {
		return u
	}
	return fmt.Sprintf("%d", id.GetNum())
}

func fromRetrieved(pt *qdrant.RetrievedPoint) *tierPoint {
	tp := &tierPoint{ID: pointIDString(pt.GetId()), Payload: pt.GetPayload()}
	if vo := pt.GetVectors(); vo != nil {
		if v := vo.GetVector(); v != nil {
			if dense, ok := v.GetVector().(*qdrant.VectorOutput_Dense); ok {
				tp.Dense = dense.Dense.GetData()
			}
		} else if m := vo.GetVectors().GetVectors(); m != nil {
			if v, ok := m[""]; ok && v != nil {
				if dense, ok := v.GetVector().(*qdrant.VectorOutput_Dense); ok {
					tp.Dense = dense.Dense.GetData()
				}
			}
			if v, ok := m[sparseVectorName]; ok && v != nil {
				if sp, ok := v.GetVector().(*qdrant.VectorOutput_Sparse); ok {
					tp.SparseIndices = sp.Sparse.GetIndices()
					tp.SparseValues = sp.Sparse.GetValues()
				}
			}
		}
	}
	return tp
}

func toPointIDs(ids []string) []*qdrant.PointId {
	out := make([]*qdrant.PointId, len(ids))
	for i, id := range ids {
		out[i] = qdrant.NewID(id)
	}
	return out
}

func (s *grpcPointStore) Get(ctx context.Context, collection string, ids []string, withPayload, withVectors bool) ([]*tierPoint, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	pts, err := s.client.Get(ctx, &qdrant.GetPoints{
		CollectionName: collection,
		Ids:            toPointIDs(ids),
		WithPayload:    qdrant.NewWithPayload(withPayload),
		WithVectors:    qdrant.NewWithVectors(withVectors),
	})
	if err != nil {
		return nil, fmt.Errorf("get %d points from %s: %w", len(ids), collection, err)
	}
	out := make([]*tierPoint, 0, len(pts))
	for _, pt := range pts {
		out = append(out, fromRetrieved(pt))
	}
	return out, nil
}

func (s *grpcPointStore) Scroll(ctx context.Context, collection string, filter *qdrant.Filter, offset string, limit uint32, withPayload, withVectors bool) ([]*tierPoint, string, error) {
	req := &qdrant.ScrollPoints{
		CollectionName: collection,
		Filter:         filter,
		Limit:          &limit,
		WithPayload:    qdrant.NewWithPayload(withPayload),
		WithVectors:    qdrant.NewWithVectors(withVectors),
	}
	if offset != "" {
		req.Offset = qdrant.NewID(offset)
	}
	pts, next, err := s.client.ScrollAndOffset(ctx, req)
	if err != nil {
		return nil, "", fmt.Errorf("scroll %s: %w", collection, err)
	}
	out := make([]*tierPoint, 0, len(pts))
	for _, pt := range pts {
		out = append(out, fromRetrieved(pt))
	}
	return out, pointIDString(next), nil
}

func (s *grpcPointStore) Query(ctx context.Context, collection string, q *vectorQuery) ([]scoredID, error) {
	limit := q.Limit
	req := &qdrant.QueryPoints{
		CollectionName: collection,
		Filter:         q.Filter,
		Limit:          &limit,
		WithPayload:    qdrant.NewWithPayload(false),
	}
	if q.Hybrid && len(q.SparseIndices) > 0 {
		req.Prefetch = []*qdrant.PrefetchQuery{
			{
				Query:  qdrant.NewQuery(q.Dense...),
				Using:  qdrant.PtrOf(""),
				Filter: q.Filter,
				Limit:  qdrant.PtrOf(limit * 2),
			},
			{
				Query:  qdrant.NewQuerySparse(q.SparseIndices, q.SparseValues),
				Using:  qdrant.PtrOf(sparseVectorName),
				Filter: q.Filter,
				Limit:  qdrant.PtrOf(limit),
			},
		}
		req.Query = qdrant.NewQueryFusion(qdrant.Fusion_RRF)
	} else {
		req.Query = qdrant.NewQuery(q.Dense...)
	}
	res, err := s.client.Query(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", collection, err)
	}
	out := make([]scoredID, 0, len(res))
	for _, r := range res {
		out = append(out, scoredID{ID: pointIDString(r.GetId()), Score: r.GetScore()})
	}
	return out, nil
}

func (s *grpcPointStore) SetPayload(ctx context.Context, collection string, ids []string, payload map[string]*qdrant.Value) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.client.SetPayload(ctx, &qdrant.SetPayloadPoints{
		CollectionName: collection,
		Wait:           qdrant.PtrOf(true),
		Payload:        payload,
		PointsSelector: qdrant.NewPointsSelector(toPointIDs(ids)...),
	})
	if err != nil {
		return fmt.Errorf("set payload in %s: %w", collection, err)
	}
	return nil
}

func (s *grpcPointStore) Delete(ctx context.Context, collection string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: collection,
		Wait:           qdrant.PtrOf(true),
		Points:         qdrant.NewPointsSelector(toPointIDs(ids)...),
	})
	if err != nil {
		return fmt.Errorf("delete %d points from %s: %w", len(ids), collection, err)
	}
	return nil
}

func (s *grpcPointStore) DeleteByFilter(ctx context.Context, collection string, filter *qdrant.Filter) error {
	_, err := s.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: collection,
		Wait:           qdrant.PtrOf(true),
		Points:         qdrant.NewPointsSelectorFilter(filter),
	})
	if err != nil {
		return fmt.Errorf("delete by filter from %s: %w", collection, err)
	}
	return nil
}

func (s *grpcPointStore) Count(ctx context.Context, collection string) (uint64, error) {
	exact := true
	n, err := s.client.Count(ctx, &qdrant.CountPoints{CollectionName: collection, Exact: &exact})
	if err != nil {
		return 0, fmt.Errorf("count %s: %w", collection, err)
	}
	return n, nil
}

func createKeywordIndex(ctx context.Context, c *qdrant.Client, collection, field string, tenant bool) error {
	req := &qdrant.CreateFieldIndexCollection{
		CollectionName: collection,
		FieldName:      field,
		FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
	}
	if tenant {
		req.FieldIndexParams = qdrant.NewPayloadIndexParamsKeyword(&qdrant.KeywordIndexParams{
			IsTenant: qdrant.PtrOf(true),
		})
	}
	if _, err := c.CreateFieldIndex(ctx, req); err != nil {
		return fmt.Errorf("create %s index on %s: %w", field, collection, err)
	}
	return nil
}

// createSourcePayloadIndexes indexes the fields the source tier is filtered
// or scrolled by (per-user listing, type listing, mention cleanup, text).
func createSourcePayloadIndexes(ctx context.Context, c *qdrant.Client, collection string) error {
	if err := createKeywordIndex(ctx, c, collection, "user_id", true); err != nil {
		return err
	}
	for _, f := range []string{"memory_type", "channel_id", "guild_id", "mentioned_user_ids", "mentioned_channel_ids"} {
		if err := createKeywordIndex(ctx, c, collection, f, false); err != nil {
			return err
		}
	}
	// created_at is stored as a float (unix seconds), so index it as float.
	if _, err := c.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
		CollectionName: collection,
		FieldName:      "created_at",
		FieldType:      qdrant.FieldType_FieldTypeFloat.Enum(),
	}); err != nil {
		return fmt.Errorf("create created_at index on %s: %w", collection, err)
	}
	if _, err := c.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
		CollectionName: collection,
		FieldName:      "content",
		FieldType:      qdrant.FieldType_FieldTypeText.Enum(),
		FieldIndexParams: qdrant.NewPayloadIndexParamsText(&qdrant.TextIndexParams{
			Tokenizer: qdrant.TokenizerType_Word,
			Lowercase: qdrant.PtrOf(true),
		}),
	}); err != nil {
		return fmt.Errorf("create content index on %s: %w", collection, err)
	}
	return nil
}

// createVectorPayloadIndexes indexes the filter fields mirrored into the
// vector tier so filtered vector search stays efficient.
func createVectorPayloadIndexes(ctx context.Context, c *qdrant.Client, collection string) error {
	if err := createKeywordIndex(ctx, c, collection, "user_id", true); err != nil {
		return err
	}
	for _, f := range vectorTierPayloadFields {
		if f == "user_id" {
			continue
		}
		if err := createKeywordIndex(ctx, c, collection, f, false); err != nil {
			return err
		}
	}
	return nil
}
