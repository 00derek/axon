// contrib/mongo/memory.go
package mongo

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/axonframework/axon/interfaces"
	"go.mongodb.org/mongo-driver/v2/bson"
	gomongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// memoryIDCounter provides unique auto-generated IDs for memories without an explicit ID.
var memoryIDCounter atomic.Uint64

func generateMemoryID() string {
	return fmt.Sprintf("mem-%d", memoryIDCounter.Add(1))
}

// memoryDoc is the MongoDB document structure for a single memory.
type memoryDoc struct {
	UserID    string    `bson:"user_id"`
	MemoryID  string    `bson:"memory_id"`
	Content   string    `bson:"content"`
	Embedding []float32 `bson:"embedding"`
	CreatedAt time.Time `bson:"created_at"`
	UpdatedAt time.Time `bson:"updated_at"`
}

// memoryStore is a MongoDB-backed implementation of interfaces.MemoryStore.
// Uses Atlas Vector Search for semantic retrieval via $vectorSearch aggregation.
type memoryStore struct {
	coll     *gomongo.Collection
	embedder interfaces.Embedder
}

// NewMemoryStore creates a MongoDB-backed MemoryStore with vector search.
// The collection should have:
//   - A unique compound index on {user_id: 1, memory_id: 1}
//   - An Atlas Vector Search index on the embedding field
//
// The embedder is called to generate vector embeddings for Save and Search operations.
func NewMemoryStore(db *gomongo.Database, collection string, embedder interfaces.Embedder) interfaces.MemoryStore {
	return &memoryStore{
		coll:     db.Collection(collection),
		embedder: embedder,
	}
}

// Save persists memories for the given user. Each memory is embedded via the
// Embedder and upserted using BulkWrite keyed on (user_id, memory_id).
// Memories with empty IDs get auto-generated IDs.
func (s *memoryStore) Save(ctx context.Context, userID string, memories []interfaces.Memory) error {
	if len(memories) == 0 {
		return nil
	}

	now := time.Now()

	// Auto-generate IDs for memories without one.
	for i := range memories {
		if memories[i].ID == "" {
			memories[i].ID = generateMemoryID()
		}
	}

	// Batch embed all memory contents.
	texts := make([]string, len(memories))
	for i, m := range memories {
		texts[i] = m.Content
	}

	embeddings, err := s.embedder.Embed(ctx, texts)
	if err != nil {
		return err
	}

	// Build bulk write models: one upsert per memory.
	models := make([]gomongo.WriteModel, len(memories))
	for i, mem := range memories {
		filter := bson.D{
			{Key: "user_id", Value: userID},
			{Key: "memory_id", Value: mem.ID},
		}

		createdAt := mem.CreatedAt
		if createdAt.IsZero() {
			createdAt = now
		}

		update := bson.D{
			{Key: "$set", Value: bson.D{
				{Key: "content", Value: mem.Content},
				{Key: "embedding", Value: embeddings[i]},
				{Key: "updated_at", Value: now},
			}},
			{Key: "$setOnInsert", Value: bson.D{
				{Key: "created_at", Value: createdAt},
			}},
		}

		models[i] = gomongo.NewUpdateOneModel().
			SetFilter(filter).
			SetUpdate(update).
			SetUpsert(true)
	}

	opts := options.BulkWrite().SetOrdered(false)
	_, err = s.coll.BulkWrite(ctx, models, opts)
	return err
}

// Get returns all memories for the given user. Embedding vectors are excluded
// from the projection to reduce payload size.
func (s *memoryStore) Get(ctx context.Context, userID string) ([]interfaces.Memory, error) {
	filter := bson.D{{Key: "user_id", Value: userID}}
	projection := bson.D{{Key: "embedding", Value: 0}}

	opts := options.Find().SetProjection(projection)
	cursor, err := s.coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var docs []memoryDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}

	if len(docs) == 0 {
		return nil, nil
	}

	memories := make([]interfaces.Memory, len(docs))
	for i, doc := range docs {
		memories[i] = interfaces.Memory{
			ID:        doc.MemoryID,
			Content:   doc.Content,
			CreatedAt: doc.CreatedAt,
			UpdatedAt: doc.UpdatedAt,
		}
	}
	return memories, nil
}

// Search performs a $vectorSearch aggregation to find the topK most similar
// memories for the given user. The query text is first embedded via the Embedder,
// then used as the query vector for Atlas Vector Search.
func (s *memoryStore) Search(ctx context.Context, userID string, query string, topK int) ([]interfaces.Memory, error) {
	// Embed the query text.
	embeddings, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	queryVector := embeddings[0]

	// Build the $vectorSearch aggregation pipeline.
	pipeline := bson.A{
		bson.D{{Key: "$vectorSearch", Value: bson.D{
			{Key: "index", Value: "vector_index"},
			{Key: "path", Value: "embedding"},
			{Key: "queryVector", Value: queryVector},
			{Key: "numCandidates", Value: topK * 10},
			{Key: "limit", Value: topK},
			{Key: "filter", Value: bson.D{
				{Key: "user_id", Value: bson.D{{Key: "$eq", Value: userID}}},
			}},
		}}},
		bson.D{{Key: "$project", Value: bson.D{
			{Key: "embedding", Value: 0},
		}}},
	}

	cursor, err := s.coll.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var docs []memoryDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}

	if len(docs) == 0 {
		return nil, nil
	}

	memories := make([]interfaces.Memory, len(docs))
	for i, doc := range docs {
		memories[i] = interfaces.Memory{
			ID:        doc.MemoryID,
			Content:   doc.Content,
			CreatedAt: doc.CreatedAt,
			UpdatedAt: doc.UpdatedAt,
		}
	}
	return memories, nil
}

// Delete removes memories with the given IDs for the given user.
// Uses DeleteMany with user_id and memory_id $in filter.
func (s *memoryStore) Delete(ctx context.Context, userID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	filter := bson.D{
		{Key: "user_id", Value: userID},
		{Key: "memory_id", Value: bson.D{{Key: "$in", Value: ids}}},
	}

	_, err := s.coll.DeleteMany(ctx, filter)
	return err
}
