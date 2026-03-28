// contrib/mongo/history_test.go
package mongo

import (
	"context"
	"os"
	"testing"

	"github.com/axonframework/axon/kernel"
	gomongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// newTestDB connects to a MongoDB instance using MONGODB_URI and returns a test database.
// Tests are skipped if MONGODB_URI is not set.
func newTestDB(t *testing.T) *gomongo.Database {
	t.Helper()
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		t.Skip("MONGODB_URI not set; skipping MongoDB integration tests")
	}

	client, err := gomongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("mongo.Connect: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Disconnect(context.Background())
	})
	return client.Database("axon_test")
}

func TestHistoryStoreSaveMessages(t *testing.T) {
	db := newTestDB(t)
	collName := "history_save_" + t.Name()
	store := NewHistoryStore(db, collName)
	t.Cleanup(func() { _ = db.Collection(collName).Drop(context.Background()) })

	msgs := []kernel.Message{
		kernel.UserMsg("hello"),
		kernel.AssistantMsg("hi there"),
	}

	err := store.SaveMessages(context.Background(), "session-1", msgs)
	if err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}
}

func TestHistoryStoreSaveMessagesEmptySlice(t *testing.T) {
	db := newTestDB(t)
	collName := "history_empty_" + t.Name()
	store := NewHistoryStore(db, collName)
	t.Cleanup(func() { _ = db.Collection(collName).Drop(context.Background()) })

	err := store.SaveMessages(context.Background(), "session-1", nil)
	if err != nil {
		t.Fatalf("SaveMessages with nil: %v", err)
	}

	err = store.SaveMessages(context.Background(), "session-1", []kernel.Message{})
	if err != nil {
		t.Fatalf("SaveMessages with empty slice: %v", err)
	}
}

func TestHistoryStoreLoadMessages(t *testing.T) {
	db := newTestDB(t)
	collName := "history_load_" + t.Name()
	store := NewHistoryStore(db, collName)
	t.Cleanup(func() { _ = db.Collection(collName).Drop(context.Background()) })

	text1 := "older message"
	text2 := "newer message"

	msgs := []kernel.Message{
		kernel.UserMsg(text1),
		kernel.AssistantMsg(text2),
	}
	if err := store.SaveMessages(context.Background(), "session-1", msgs); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}

	loaded, err := store.LoadMessages(context.Background(), "session-1", 2)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded))
	}
	if loaded[0].Role != kernel.RoleUser {
		t.Errorf("expected first message role %q, got %q", kernel.RoleUser, loaded[0].Role)
	}
	if loaded[0].TextContent() != text1 {
		t.Errorf("expected first message text %q, got %q", text1, loaded[0].TextContent())
	}
	if loaded[1].Role != kernel.RoleAssistant {
		t.Errorf("expected second message role %q, got %q", kernel.RoleAssistant, loaded[1].Role)
	}
	if loaded[1].TextContent() != text2 {
		t.Errorf("expected second message text %q, got %q", text2, loaded[1].TextContent())
	}
}

func TestHistoryStoreLoadMessagesLimit(t *testing.T) {
	db := newTestDB(t)
	collName := "history_limit_" + t.Name()
	store := NewHistoryStore(db, collName)
	t.Cleanup(func() { _ = db.Collection(collName).Drop(context.Background()) })

	// Save 5 messages, then load with limit=3 — should get the last 3.
	msgs := []kernel.Message{
		kernel.UserMsg("msg1"),
		kernel.AssistantMsg("msg2"),
		kernel.UserMsg("msg3"),
		kernel.AssistantMsg("msg4"),
		kernel.UserMsg("msg5"),
	}
	if err := store.SaveMessages(context.Background(), "sess", msgs); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}

	loaded, err := store.LoadMessages(context.Background(), "sess", 3)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 messages with limit=3, got %d", len(loaded))
	}
	// Last 3 are msg3, msg4, msg5.
	if loaded[0].TextContent() != "msg3" {
		t.Errorf("expected msg3, got %q", loaded[0].TextContent())
	}
	if loaded[2].TextContent() != "msg5" {
		t.Errorf("expected msg5, got %q", loaded[2].TextContent())
	}
}

func TestHistoryStoreLoadMessagesNoLimit(t *testing.T) {
	db := newTestDB(t)
	collName := "history_nolimit_" + t.Name()
	store := NewHistoryStore(db, collName)
	t.Cleanup(func() { _ = db.Collection(collName).Drop(context.Background()) })

	msgs := []kernel.Message{
		kernel.UserMsg("msg1"),
		kernel.UserMsg("msg2"),
		kernel.AssistantMsg("msg3"),
	}
	if err := store.SaveMessages(context.Background(), "session-1", msgs); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}

	loaded, err := store.LoadMessages(context.Background(), "session-1", 0)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 messages (all), got %d", len(loaded))
	}
}

func TestHistoryStoreLoadMessagesNotFound(t *testing.T) {
	db := newTestDB(t)
	collName := "history_notfound_" + t.Name()
	store := NewHistoryStore(db, collName)
	t.Cleanup(func() { _ = db.Collection(collName).Drop(context.Background()) })

	msgs, err := store.LoadMessages(context.Background(), "no-such-session", 10)
	if err != nil {
		t.Fatalf("LoadMessages for missing session: %v", err)
	}
	if msgs != nil {
		t.Errorf("expected nil for missing session, got %v", msgs)
	}
}

func TestHistoryStoreClear(t *testing.T) {
	db := newTestDB(t)
	collName := "history_clear_" + t.Name()
	store := NewHistoryStore(db, collName)
	t.Cleanup(func() { _ = db.Collection(collName).Drop(context.Background()) })

	msgs := []kernel.Message{kernel.UserMsg("hello")}
	if err := store.SaveMessages(context.Background(), "session-1", msgs); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}

	if err := store.Clear(context.Background(), "session-1"); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	// Should return nil after clear.
	loaded, err := store.LoadMessages(context.Background(), "session-1", 0)
	if err != nil {
		t.Fatalf("LoadMessages after clear: %v", err)
	}
	if loaded != nil {
		t.Errorf("expected nil after clear, got %v", loaded)
	}
}

func TestHistoryStoreClearNonexistent(t *testing.T) {
	db := newTestDB(t)
	collName := "history_clearnx_" + t.Name()
	store := NewHistoryStore(db, collName)
	t.Cleanup(func() { _ = db.Collection(collName).Drop(context.Background()) })

	err := store.Clear(context.Background(), "does-not-exist")
	if err != nil {
		t.Fatalf("Clear nonexistent: %v", err)
	}
}
