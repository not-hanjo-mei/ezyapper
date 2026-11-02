package memory

import (
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/qdrant/go-client/qdrant"
)

// Two-tier long-term memory layout
//
//   - Source tier (CollectionMemorySource): one payload-only point per memory
//     record holding the text and all metadata. It has no vectors and therefore
//     no dependency on the embedding model; it is the source of truth.
//   - Vector tier (vectorCollectionName(model, dim)): one point per record with
//     the same ID, holding the dense embedding, the BM25 sparse vector and the
//     few payload fields search filters need (vectorTierPayloadFields).
//
// Search runs on the vector tier and hydrates full records from the source
// tier by ID. Changing the embedding model or dimension selects a new vector
// collection, which is rebuilt from the source tier by re-embedding.

const (
	// CollectionMemorySource is the payload-only source-of-truth collection.
	CollectionMemorySource = "memories_source"
	// vectorCollectionPrefix prefixes every per-model vector collection.
	vectorCollectionPrefix = "memories_vec_"
	// CollectionLegacyMemories is the pre-split single collection that held
	// both vectors and payloads. It is only ever read (for migration).
	CollectionLegacyMemories = "memories"

	// maxModelNameLen bounds the sanitized model part of a vector collection
	// name; Qdrant collection names are limited to 255 bytes.
	maxModelNameLen = 96
)

// vectorTierPayloadFields are the payload keys copied from a source record
// into its vector-tier point. They are exactly the fields used in search
// filters (user/channel/type/mention filters, plus guild for future scoping).
var vectorTierPayloadFields = []string{
	"user_id",
	"guild_id",
	"channel_id",
	"memory_type",
	"mentioned_user_ids",
	"mentioned_channel_ids",
}

// sanitizeModelName turns an embedding model identifier into a string that is
// safe and readable inside a collection name: lowercase ASCII letters and
// digits, with every other run of characters collapsed to a single '_'.
// Overlong names are truncated and suffixed with a short hash of the original
// so distinct models never map to the same truncated name.
func sanitizeModelName(model string) string {
	var b strings.Builder
	lastUnderscore := true // suppress a leading underscore
	for _, r := range strings.ToLower(strings.TrimSpace(model)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	s := strings.TrimRight(b.String(), "_")
	if s == "" {
		s = "model"
	}
	if len(s) > maxModelNameLen {
		h := fnv.New32a()
		h.Write([]byte(model))
		s = fmt.Sprintf("%s_%08x", strings.TrimRight(s[:maxModelNameLen-9], "_"), h.Sum32())
	}
	return s
}

// vectorCollectionName returns the vector-tier collection for a model and
// dimension, e.g. "memories_vec_baai_bge_m3_1024".
func vectorCollectionName(model string, dim int) string {
	return fmt.Sprintf("%s%s_%d", vectorCollectionPrefix, sanitizeModelName(model), dim)
}

// isVectorCollection reports whether name is a per-model vector collection
// (with the given name prefix applied).
func isVectorCollection(name, prefix string) bool {
	return strings.HasPrefix(name, prefix+vectorCollectionPrefix)
}

// vectorPayloadFromSource extracts the vector-tier payload subset from a
// source-tier payload.
func vectorPayloadFromSource(src map[string]*qdrant.Value) map[string]*qdrant.Value {
	out := make(map[string]*qdrant.Value, len(vectorTierPayloadFields))
	for _, k := range vectorTierPayloadFields {
		if v, ok := src[k]; ok && v != nil {
			out[k] = v
		}
	}
	return out
}
