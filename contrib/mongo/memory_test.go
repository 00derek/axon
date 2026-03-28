// contrib/mongo/memory_test.go
package mongo

import (
	"context"
	"errors"
	"testing"

	"github.com/axonframework/axon/interfaces"
	gomongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// fakeEmbedder is a test double that returns fixed-length vectors.
type fakeEmbedder struct {
	embedFn func(ctx context.Context, texts []string) ([][]float32, error)
}

func (f *fakeEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return f.embedFn(ctx, texts)
}

// newConstantEmbedder returns an embedder that produces a fixed vector for each text.
func newConstantEmbedder(dim int) *fakeEmbedder {
	return &fakeEmbedder{
		embedFn: func(_ context.Context, texts []string) ([][]float32, error) {
			result := make([][]float32, len(texts))
			for i := range texts {
				vec := make([]float32, dim)
				for j := range vec {
					vec[j] = float32(i+1) * 0.1
				}
				result[i] = vec
			}
			return result, nil
		},
	}
}

// newFailingEmbedder returns an embedder that always errors.
func newFailingEmbedder() *fakeEmbedder {
	return &fakeEmbedder{
		embedFn: func(_ context.Context, _ []string) ([][]float32, error) {
			return nil, errors.New("embedding service unavailable")
		},
	}
}

// newTestMemoryDB delegates to newTestDB (defined in history_test.go).
// Tests are skipped if MONGODB_URI is not set.
func newTestMemoryDB(t *testing.T) *gomongo.Database {
	t.Helper()
	return newTestDB(t)
}

// newUnreachableDB creates a *gomongo.Database pointing at a server that is never
// actually dialed. Safe to use when tests fail before any network call (e.g.,
// when the embedder returns an error before any DB operation is attempted).
func newUnreachableDB(t *testing.T) *gomongo.Database {
	t.Helper()
	client, err := gomongo.Connect(
		options.Client().ApplyURI("mongodb://localhost:1").
			SetServerSelectionTimeout(0),
	)
	if err != nil {
		t.Fatalf("newUnreachableDB: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	return client.Database("axon_test_unreachable")
}

func TestMemoryStoreSave(t *testing.T) {
	db := newTestMemoryDB(t)
	collName := "memory_save_" + t.Name()
	t.Cleanup(func() { _ = db.Collection(collName).Drop(context.Background()) })

	embedder := newConstantEmbedder(3)
	store := NewMemoryStore(db, collName, embedder)

	mems := []interfaces.Memory{
		{ID: "m1", Content: "User likes Go programming"},
		{ID: "m2", Content: "User lives in Portland"},
	}

	err := store.Save(context.Background(), "user-1", mems)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func TestMemoryStoreSaveAutoGeneratesID(t *testing.T) {
	db := newTestMemoryDB(t)
	collName := "memory_autoid_" + t.Name()
	t.Cleanup(func() { _ = db.Collection(collName).Drop(context.Background()) })

	embedder := newConstantEmbedder(3)
	store := NewMemoryStore(db, collName, embedder)

	mems := []interfaces.Memory{
		{Content: "Something without an ID"},
	}

	err := store.Save(context.Background(), "user-1", mems)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Fetch all memories and confirm there is one with a generated ID.
	got, err := store.Get(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 memory, got %d", len(got))
	}
	if got[0].ID == "" {
		t.Error("expected auto-generated ID, got empty string")
	}
}

func TestMemoryStoreSaveEmptySlice(t *testing.T) {
	db := newTestMemoryDB(t)
	collName := "memory_empty_" + t.Name()
	t.Cleanup(func() { _ = db.Collection(collName).Drop(context.Background()) })

	embedder := newConstantEmbedder(3)
	store := NewMemoryStore(db, collName, embedder)

	err := store.Save(context.Background(), "user-1", nil)
	if err != nil {
		t.Fatalf("Save with nil: %v", err)
	}

	err = store.Save(context.Background(), "user-1", []interfaces.Memory{})
	if err != nil {
		t.Fatalf("Save with empty slice: %v", err)
	}
}

func TestMemoryStoreSaveEmbedderError(t *testing.T) {
	// Embedder fails before any DB call, so no real MongoDB is needed.
	db := newUnreachableDB(t)
	embedder := newFailingEmbedder()
	store := NewMemoryStore(db, "irrelevant", embedder)

	mems := []interfaces.Memory{
		{ID: "m1", Content: "test"},
	}

	err := store.Save(context.Background(), "user-1", mems)
	if err == nil {
		t.Fatal("expected error from failing embedder, got nil")
	}
	if err.Error() != "embedding service unavailable" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMemoryStoreGet(t *testing.T) {
	db := newTestMemoryDB(t)
	collName := "memory_get_" + t.Name()
	t.Cleanup(func() { _ = db.Collection(collName).Drop(context.Background()) })

	embedder := newConstantEmbedder(3)
	store := NewMemoryStore(db, collName, embedder)

	mems := []interfaces.Memory{
		{ID: "m1", Content: "User likes Go"},
		{ID: "m2", Content: "User lives in Portland"},
	}
	if err := store.Save(context.Background(), "user-1", mems); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 memories, got %d", len(got))
	}

	// Build a map to check both IDs are present (order may vary).
	byID := make(map[string]interfaces.Memory)
	for _, m := range got {
		byID[m.ID] = m
	}
	if _, ok := byID["m1"]; !ok {
		t.Error("expected memory m1 in result")
	}
	if _, ok := byID["m2"]; !ok {
		t.Error("expected memory m2 in result")
	}
	if byID["m1"].Content != "User likes Go" {
		t.Errorf("unexpected content for m1: %q", byID["m1"].Content)
	}
}

func TestMemoryStoreGetEmpty(t *testing.T) {
	db := newTestMemoryDB(t)
	collName := "memory_getempty_" + t.Name()
	t.Cleanup(func() { _ = db.Collection(collName).Drop(context.Background()) })

	embedder := newConstantEmbedder(3)
	store := NewMemoryStore(db, collName, embedder)

	mems, err := store.Get(context.Background(), "unknown-user")
	if err != nil {
		t.Fatalf("Get empty user: %v", err)
	}
	if mems != nil {
		t.Errorf("expected nil for empty user, got %v", mems)
	}
}

func TestMemoryStoreSearchEmbedderError(t *testing.T) {
	// Embedder fails before any DB call, so no real MongoDB is needed.
	db := newUnreachableDB(t)
	embedder := newFailingEmbedder()
	store := NewMemoryStore(db, "irrelevant", embedder)

	_, err := store.Search(context.Background(), "user-1", "anything", 5)
	if err == nil {
		t.Fatal("expected error from failing embedder, got nil")
	}
}

func TestMemoryStoreDelete(t *testing.T) {
	db := newTestMemoryDB(t)
	collName := "memory_delete_" + t.Name()
	t.Cleanup(func() { _ = db.Collection(collName).Drop(context.Background()) })

	embedder := newConstantEmbedder(3)
	store := NewMemoryStore(db, collName, embedder)

	mems := []interfaces.Memory{
		{ID: "m1", Content: "User likes Go"},
		{ID: "m2", Content: "User lives in Portland"},
	}
	if err := store.Save(context.Background(), "user-1", mems); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := store.Delete(context.Background(), "user-1", []string{"m1", "m2"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := store.Get(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil after deleting all memories, got %v", got)
	}
}

func TestMemoryStoreDeleteEmptyIDs(t *testing.T) {
	db := newTestMemoryDB(t)
	collName := "memory_deleteempty_" + t.Name()
	t.Cleanup(func() { _ = db.Collection(collName).Drop(context.Background()) })

	embedder := newConstantEmbedder(3)
	store := NewMemoryStore(db, collName, embedder)

	err := store.Delete(context.Background(), "user-1", nil)
	if err != nil {
		t.Fatalf("Delete with nil IDs: %v", err)
	}

	err = store.Delete(context.Background(), "user-1", []string{})
	if err != nil {
		t.Fatalf("Delete with empty IDs: %v", err)
	}
}

func TestMemoryStoreSaveUpsert(t *testing.T) {
	db := newTestMemoryDB(t)
	collName := "memory_upsert_" + t.Name()
	t.Cleanup(func() { _ = db.Collection(collName).Drop(context.Background()) })

	embedder := newConstantEmbedder(3)
	store := NewMemoryStore(db, collName, embedder)

	// Save a memory.
	mems := []interfaces.Memory{{ID: "m1", Content: "original content"}}
	if err := store.Save(context.Background(), "user-1", mems); err != nil {
		t.Fatalf("Save initial: %v", err)
	}

	// Update the same memory ID with new content.
	mems2 := []interfaces.Memory{{ID: "m1", Content: "updated content"}}
	if err := store.Save(context.Background(), "user-1", mems2); err != nil {
		t.Fatalf("Save update: %v", err)
	}

	got, err := store.Get(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 memory after upsert, got %d", len(got))
	}
	if got[0].Content != "updated content" {
		t.Errorf("expected updated content, got %q", got[0].Content)
	}
}
