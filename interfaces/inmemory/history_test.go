// interfaces/inmemory/history_test.go
package inmemory

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/axonframework/axon/kernel"
)

func TestHistoryStoreSaveAndLoad(t *testing.T) {
	store := NewHistoryStore()
	ctx := context.Background()

	msgs := []kernel.Message{
		kernel.UserMsg("Hello"),
		kernel.AssistantMsg("Hi there"),
	}

	if err := store.SaveMessages(ctx, "session-1", msgs); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}

	loaded, err := store.LoadMessages(ctx, "session-1", 0)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded))
	}
	if loaded[0].TextContent() != "Hello" {
		t.Errorf("expected first message %q, got %q", "Hello", loaded[0].TextContent())
	}
	if loaded[1].TextContent() != "Hi there" {
		t.Errorf("expected second message %q, got %q", "Hi there", loaded[1].TextContent())
	}
}

func TestHistoryStoreLoadLimit(t *testing.T) {
	store := NewHistoryStore()
	ctx := context.Background()

	// Save 5 messages
	msgs := make([]kernel.Message, 5)
	for i := range msgs {
		msgs[i] = kernel.UserMsg(fmt.Sprintf("msg-%d", i))
	}
	if err := store.SaveMessages(ctx, "session-1", msgs); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}

	// Load last 2 -- should return the LAST 2 (most recent)
	loaded, err := store.LoadMessages(ctx, "session-1", 2)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded))
	}
	if loaded[0].TextContent() != "msg-3" {
		t.Errorf("expected %q, got %q", "msg-3", loaded[0].TextContent())
	}
	if loaded[1].TextContent() != "msg-4" {
		t.Errorf("expected %q, got %q", "msg-4", loaded[1].TextContent())
	}
}

func TestHistoryStoreLimitExceedsTotal(t *testing.T) {
	store := NewHistoryStore()
	ctx := context.Background()

	msgs := []kernel.Message{kernel.UserMsg("only one")}
	if err := store.SaveMessages(ctx, "s1", msgs); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}

	// Limit larger than stored count returns all
	loaded, err := store.LoadMessages(ctx, "s1", 100)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 message, got %d", len(loaded))
	}
}

func TestHistoryStoreAppendAcrossCalls(t *testing.T) {
	store := NewHistoryStore()
	ctx := context.Background()

	_ = store.SaveMessages(ctx, "s1", []kernel.Message{kernel.UserMsg("first")})
	_ = store.SaveMessages(ctx, "s1", []kernel.Message{kernel.AssistantMsg("second")})

	loaded, err := store.LoadMessages(ctx, "s1", 0)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages after two saves, got %d", len(loaded))
	}
}

func TestHistoryStoreClear(t *testing.T) {
	store := NewHistoryStore()
	ctx := context.Background()

	_ = store.SaveMessages(ctx, "s1", []kernel.Message{kernel.UserMsg("hello")})

	if err := store.Clear(ctx, "s1"); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	loaded, err := store.LoadMessages(ctx, "s1", 0)
	if err != nil {
		t.Fatalf("LoadMessages after Clear: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("expected 0 messages after clear, got %d", len(loaded))
	}
}

func TestHistoryStoreSessionIsolation(t *testing.T) {
	store := NewHistoryStore()
	ctx := context.Background()

	_ = store.SaveMessages(ctx, "s1", []kernel.Message{kernel.UserMsg("session 1")})
	_ = store.SaveMessages(ctx, "s2", []kernel.Message{kernel.UserMsg("session 2")})

	loaded1, _ := store.LoadMessages(ctx, "s1", 0)
	loaded2, _ := store.LoadMessages(ctx, "s2", 0)

	if len(loaded1) != 1 || loaded1[0].TextContent() != "session 1" {
		t.Errorf("session 1 isolation broken: %+v", loaded1)
	}
	if len(loaded2) != 1 || loaded2[0].TextContent() != "session 2" {
		t.Errorf("session 2 isolation broken: %+v", loaded2)
	}
}

func TestHistoryStoreLoadEmptySession(t *testing.T) {
	store := NewHistoryStore()
	ctx := context.Background()

	loaded, err := store.LoadMessages(ctx, "nonexistent", 10)
	if err != nil {
		t.Fatalf("LoadMessages for empty session: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("expected 0 messages for empty session, got %d", len(loaded))
	}
}

func TestHistoryStoreConcurrency(t *testing.T) {
	store := NewHistoryStore()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sid := fmt.Sprintf("session-%d", n%5) // 5 sessions, 10 writers each
			_ = store.SaveMessages(ctx, sid, []kernel.Message{kernel.UserMsg(fmt.Sprintf("msg-%d", n))})
			_, _ = store.LoadMessages(ctx, sid, 5)
		}(i)
	}
	wg.Wait()

	// Verify no panics occurred and data is consistent
	for i := 0; i < 5; i++ {
		sid := fmt.Sprintf("session-%d", i)
		loaded, err := store.LoadMessages(ctx, sid, 0)
		if err != nil {
			t.Errorf("session %s: %v", sid, err)
		}
		if len(loaded) != 10 {
			t.Errorf("session %s: expected 10 messages, got %d", sid, len(loaded))
		}
	}
}
