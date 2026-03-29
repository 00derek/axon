// interfaces/inmemory/memory_test.go
package inmemory

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/axonframework/axon/interfaces"
)

func TestMemoryStoreSaveAndGet(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	mems := []interfaces.Memory{
		{Content: "User has a daughter named Emma"},
		{Content: "User likes soccer"},
	}

	if err := store.Save(ctx, "user-1", mems); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get(ctx, "user-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 memories, got %d", len(got))
	}
	if got[0].Content != "User has a daughter named Emma" {
		t.Errorf("unexpected content: %q", got[0].Content)
	}
	if got[1].Content != "User likes soccer" {
		t.Errorf("unexpected content: %q", got[1].Content)
	}
}

func TestMemoryStoreAutoGeneratesID(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	mems := []interfaces.Memory{
		{Content: "test memory with no ID"},
	}

	if err := store.Save(ctx, "user-1", mems); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, _ := store.Get(ctx, "user-1")
	if len(got) != 1 {
		t.Fatalf("expected 1 memory, got %d", len(got))
	}
	if got[0].ID == "" {
		t.Error("expected auto-generated ID, got empty string")
	}
}

func TestMemoryStorePreservesExistingID(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	mems := []interfaces.Memory{
		{ID: "custom-id-42", Content: "memory with explicit ID"},
	}

	if err := store.Save(ctx, "user-1", mems); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, _ := store.Get(ctx, "user-1")
	if got[0].ID != "custom-id-42" {
		t.Errorf("expected ID %q, got %q", "custom-id-42", got[0].ID)
	}
}

func TestMemoryStoreSetsTimestamps(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	before := time.Now()
	_ = store.Save(ctx, "user-1", []interfaces.Memory{{Content: "timestamped"}})
	after := time.Now()

	got, _ := store.Get(ctx, "user-1")
	if got[0].CreatedAt.Before(before) || got[0].CreatedAt.After(after) {
		t.Errorf("CreatedAt %v not in range [%v, %v]", got[0].CreatedAt, before, after)
	}
	if got[0].UpdatedAt.Before(before) || got[0].UpdatedAt.After(after) {
		t.Errorf("UpdatedAt %v not in range [%v, %v]", got[0].UpdatedAt, before, after)
	}
}

func TestMemoryStoreSearchNaive(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_ = store.Save(ctx, "user-1", []interfaces.Memory{
		{Content: "User has a daughter named Emma who likes soccer"},
		{Content: "User works at Google as a software engineer"},
		{Content: "User prefers dark mode in all applications"},
		{Content: "Emma scored a goal in her soccer match last week"},
	})

	// Search for "soccer" -- should match 2 memories
	results, err := store.Search(ctx, "user-1", "soccer", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results for 'soccer', got %d", len(results))
	}
}

func TestMemoryStoreSearchCaseInsensitive(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_ = store.Save(ctx, "user-1", []interfaces.Memory{
		{Content: "User likes SOCCER"},
	})

	results, err := store.Search(ctx, "user-1", "soccer", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for case-insensitive search, got %d", len(results))
	}
}

func TestMemoryStoreSearchTopK(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_ = store.Save(ctx, "user-1", []interfaces.Memory{
		{Content: "fact one"},
		{Content: "fact two"},
		{Content: "fact three"},
	})

	// All match "fact", but topK=2 limits results
	results, err := store.Search(ctx, "user-1", "fact", 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results with topK=2, got %d", len(results))
	}
}

func TestMemoryStoreSearchNoMatch(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_ = store.Save(ctx, "user-1", []interfaces.Memory{
		{Content: "User likes cats"},
	})

	results, err := store.Search(ctx, "user-1", "dogs", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestMemoryStoreSearchEmptyUser(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	results, err := store.Search(ctx, "nobody", "anything", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for unknown user, got %d", len(results))
	}
}

func TestMemoryStoreDelete(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_ = store.Save(ctx, "user-1", []interfaces.Memory{
		{ID: "m1", Content: "keep this"},
		{ID: "m2", Content: "delete this"},
		{ID: "m3", Content: "keep this too"},
	})

	if err := store.Delete(ctx, "user-1", []string{"m2"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, _ := store.Get(ctx, "user-1")
	if len(got) != 2 {
		t.Fatalf("expected 2 memories after delete, got %d", len(got))
	}
	for _, m := range got {
		if m.ID == "m2" {
			t.Error("deleted memory m2 still present")
		}
	}
}

func TestMemoryStoreDeleteNonexistent(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_ = store.Save(ctx, "user-1", []interfaces.Memory{
		{ID: "m1", Content: "exists"},
	})

	// Deleting a nonexistent ID should not error
	if err := store.Delete(ctx, "user-1", []string{"does-not-exist"}); err != nil {
		t.Fatalf("Delete nonexistent: %v", err)
	}

	got, _ := store.Get(ctx, "user-1")
	if len(got) != 1 {
		t.Errorf("expected 1 memory unchanged, got %d", len(got))
	}
}

func TestMemoryStoreUserIsolation(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_ = store.Save(ctx, "user-1", []interfaces.Memory{{Content: "user 1 data"}})
	_ = store.Save(ctx, "user-2", []interfaces.Memory{{Content: "user 2 data"}})

	got1, _ := store.Get(ctx, "user-1")
	got2, _ := store.Get(ctx, "user-2")

	if len(got1) != 1 || got1[0].Content != "user 1 data" {
		t.Errorf("user 1 isolation broken: %+v", got1)
	}
	if len(got2) != 1 || got2[0].Content != "user 2 data" {
		t.Errorf("user 2 isolation broken: %+v", got2)
	}
}

func TestMemoryStoreGetEmptyUser(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	got, err := store.Get(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Get for empty user: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 memories for unknown user, got %d", len(got))
	}
}

func TestMemoryStoreConcurrency(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			uid := fmt.Sprintf("user-%d", n%5)
			_ = store.Save(ctx, uid, []interfaces.Memory{{Content: fmt.Sprintf("mem-%d", n)}})
			_, _ = store.Get(ctx, uid)
			_, _ = store.Search(ctx, uid, "mem", 5)
		}(i)
	}
	wg.Wait()

	// Verify no panics and data is consistent
	for i := 0; i < 5; i++ {
		uid := fmt.Sprintf("user-%d", i)
		got, err := store.Get(ctx, uid)
		if err != nil {
			t.Errorf("user %s: %v", uid, err)
		}
		if len(got) != 10 {
			t.Errorf("user %s: expected 10 memories, got %d", uid, len(got))
		}
	}
}
