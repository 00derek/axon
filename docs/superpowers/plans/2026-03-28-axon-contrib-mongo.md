# Axon Contrib/Mongo Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the Axon contrib/mongo package -- MongoDB-backed implementations of `interfaces.HistoryStore` and `interfaces.MemoryStore` with Atlas Vector Search support for semantic memory retrieval. Also adds the `Embedder` interface to the interfaces package.

**Architecture:** The `interfaces/embedder.go` file defines a general-purpose `Embedder` interface (stdlib only, no new deps). The `contrib/mongo/` package provides two implementations: `HistoryStore` uses one-document-per-session with `$push` for message growth and `$slice` projections for limit queries. `MemoryStore` uses one-document-per-memory with batch embedding via `Embedder`, `BulkWrite` upserts, and `$vectorSearch` aggregation for semantic retrieval. Tests use `go.mongodb.org/mongo-driver/v2/mongo/integration/mtest` for unit-level mock testing; integration tests against a real MongoDB instance are gated behind the `MONGODB_URI` environment variable.

**Tech Stack:** Go 1.25.2, `go.mongodb.org/mongo-driver/v2`, `github.com/axonframework/axon/kernel`, `github.com/axonframework/axon/interfaces`

**Source spec:** `docs/superpowers/specs/2026-03-28-providers-google-contrib-design.md`, Section 3

---

## File Structure

```
interfaces/
├── embedder.go          # NEW: Embedder interface

contrib/mongo/
├── go.mod               # module github.com/axonframework/axon/contrib/mongo
├── history.go           # MongoDB HistoryStore
├── history_test.go
├── memory.go            # MongoDB MemoryStore with vector search
├── memory_test.go
```

---

### Task 1: Add Embedder interface to the interfaces package

**Files:**
- Create: `interfaces/embedder.go`

- [ ] **Step 1: Write the Embedder interface**

Write `/Users/derek/repo/axons/interfaces/embedder.go`:

```go
// interfaces/embedder.go
package interfaces

import "context"

// Embedder generates vector embeddings for text.
// Batch-oriented: accepts multiple texts and returns one embedding per text.
// The length of the returned slice must equal the length of the input slice.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}
```

- [ ] **Step 2: Verify the interfaces module still compiles**

Run: `cd /Users/derek/repo/axons/interfaces && go build ./...`
Expected: Clean compile (exit 0). The Embedder interface uses only stdlib types (context, string, float32).

- [ ] **Step 3: Run existing tests to confirm no regression**

Run: `cd /Users/derek/repo/axons/interfaces && go test ./... -count=1`
Expected: PASS -- all existing guard and inmemory tests still pass.

- [ ] **Step 4: Commit**

```bash
git add interfaces/embedder.go
git commit -m "feat(interfaces): add Embedder interface for vector embedding generation"
```

---

### Task 2: Initialize contrib/mongo Go module

**Files:**
- Create: `contrib/mongo/go.mod`

- [ ] **Step 1: Create the directory and go.mod**

Run:
```bash
mkdir -p /Users/derek/repo/axons/contrib/mongo
```

Write `/Users/derek/repo/axons/contrib/mongo/go.mod`:

```
module github.com/axonframework/axon/contrib/mongo

go 1.25.2

require (
	github.com/axonframework/axon/kernel v0.0.0
	github.com/axonframework/axon/interfaces v0.0.0
	go.mongodb.org/mongo-driver/v2 v2.1.0
)

replace (
	github.com/axonframework/axon/kernel => ../../kernel
	github.com/axonframework/axon/interfaces => ../../interfaces
)
```

- [ ] **Step 2: Download dependencies and tidy**

Run:
```bash
cd /Users/derek/repo/axons/contrib/mongo && go mod tidy
```

Expected: `go.sum` is created, indirect dependencies resolved. The mongo-driver version may update; that is fine. If `go mod tidy` adjusts the driver version, accept the result.

- [ ] **Step 3: Verify the module resolves**

Run: `cd /Users/derek/repo/axons/contrib/mongo && go build ./...`
Expected: Clean compile (nothing to build yet, but module resolution succeeds).

- [ ] **Step 4: Commit**

```bash
git add contrib/mongo/go.mod contrib/mongo/go.sum
git commit -m "feat(contrib/mongo): initialize Go module with mongo-driver dependency"
```

---

### Task 3: MongoDB HistoryStore -- tests first

**Files:**
- Create: `contrib/mongo/history_test.go`

- [ ] **Step 1: Write the test file**

Write `/Users/derek/repo/axons/contrib/mongo/history_test.go`:

```go
// contrib/mongo/history_test.go
package mongo

import (
	"context"
	"testing"
	"time"

	"github.com/axonframework/axon/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/integration/mtest"
)

func TestHistoryStoreSaveMessages(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))

	mt.Run("appends messages with upsert", func(mt *mtest.T) {
		store := NewHistoryStore(mt.DB, mt.Coll.Name())

		mt.AddMockResponses(bson.D{
			{Key: "ok", Value: 1},
			{Key: "nModified", Value: 1},
		})

		msgs := []kernel.Message{
			kernel.UserMsg("hello"),
			kernel.AssistantMsg("hi there"),
		}

		err := store.SaveMessages(context.Background(), "session-1", msgs)
		if err != nil {
			t.Fatalf("SaveMessages: %v", err)
		}
	})
}

func TestHistoryStoreSaveMessagesEmptySlice(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))

	mt.Run("no-op for empty messages", func(mt *mtest.T) {
		store := NewHistoryStore(mt.DB, mt.Coll.Name())

		err := store.SaveMessages(context.Background(), "session-1", nil)
		if err != nil {
			t.Fatalf("SaveMessages with nil: %v", err)
		}

		err = store.SaveMessages(context.Background(), "session-1", []kernel.Message{})
		if err != nil {
			t.Fatalf("SaveMessages with empty slice: %v", err)
		}
	})
}

func TestHistoryStoreLoadMessages(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))

	mt.Run("returns messages with limit", func(mt *mtest.T) {
		store := NewHistoryStore(mt.DB, mt.Coll.Name())

		text1 := "older message"
		text2 := "newer message"

		mt.AddMockResponses(mtest.CreateCursorResponse(1, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch, bson.D{
			{Key: "session_id", Value: "session-1"},
			{Key: "messages", Value: bson.A{
				bson.D{
					{Key: "role", Value: "user"},
					{Key: "content", Value: bson.A{bson.D{{Key: "text", Value: text1}}}},
				},
				bson.D{
					{Key: "role", Value: "assistant"},
					{Key: "content", Value: bson.A{bson.D{{Key: "text", Value: text2}}}},
				},
			}},
			{Key: "updated_at", Value: time.Now()},
		}))

		msgs, err := store.LoadMessages(context.Background(), "session-1", 2)
		if err != nil {
			t.Fatalf("LoadMessages: %v", err)
		}
		if len(msgs) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(msgs))
		}
		if msgs[0].Role != kernel.RoleUser {
			t.Errorf("expected first message role %q, got %q", kernel.RoleUser, msgs[0].Role)
		}
		if msgs[0].TextContent() != text1 {
			t.Errorf("expected first message text %q, got %q", text1, msgs[0].TextContent())
		}
		if msgs[1].Role != kernel.RoleAssistant {
			t.Errorf("expected second message role %q, got %q", kernel.RoleAssistant, msgs[1].Role)
		}
	})
}

func TestHistoryStoreLoadMessagesNoLimit(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))

	mt.Run("limit <= 0 returns all", func(mt *mtest.T) {
		store := NewHistoryStore(mt.DB, mt.Coll.Name())

		mt.AddMockResponses(mtest.CreateCursorResponse(1, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch, bson.D{
			{Key: "session_id", Value: "session-1"},
			{Key: "messages", Value: bson.A{
				bson.D{
					{Key: "role", Value: "user"},
					{Key: "content", Value: bson.A{bson.D{{Key: "text", Value: "msg1"}}}},
				},
				bson.D{
					{Key: "role", Value: "user"},
					{Key: "content", Value: bson.A{bson.D{{Key: "text", Value: "msg2"}}}},
				},
				bson.D{
					{Key: "role", Value: "assistant"},
					{Key: "content", Value: bson.A{bson.D{{Key: "text", Value: "msg3"}}}},
				},
			}},
		}))

		msgs, err := store.LoadMessages(context.Background(), "session-1", 0)
		if err != nil {
			t.Fatalf("LoadMessages: %v", err)
		}
		if len(msgs) != 3 {
			t.Fatalf("expected 3 messages (all), got %d", len(msgs))
		}
	})
}

func TestHistoryStoreLoadMessagesNotFound(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))

	mt.Run("returns nil for nonexistent session", func(mt *mtest.T) {
		store := NewHistoryStore(mt.DB, mt.Coll.Name())

		// Return empty cursor (no documents)
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch))

		msgs, err := store.LoadMessages(context.Background(), "no-such-session", 10)
		if err != nil {
			t.Fatalf("LoadMessages for missing session: %v", err)
		}
		if msgs != nil {
			t.Errorf("expected nil for missing session, got %v", msgs)
		}
	})
}

func TestHistoryStoreClear(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))

	mt.Run("deletes session document", func(mt *mtest.T) {
		store := NewHistoryStore(mt.DB, mt.Coll.Name())

		mt.AddMockResponses(bson.D{
			{Key: "ok", Value: 1},
			{Key: "n", Value: 1},
		})

		err := store.Clear(context.Background(), "session-1")
		if err != nil {
			t.Fatalf("Clear: %v", err)
		}
	})
}

func TestHistoryStoreClearNonexistent(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))

	mt.Run("no error for nonexistent session", func(mt *mtest.T) {
		store := NewHistoryStore(mt.DB, mt.Coll.Name())

		mt.AddMockResponses(bson.D{
			{Key: "ok", Value: 1},
			{Key: "n", Value: 0},
		})

		err := store.Clear(context.Background(), "does-not-exist")
		if err != nil {
			t.Fatalf("Clear nonexistent: %v", err)
		}
	})
}
```

- [ ] **Step 2: Verify tests fail to compile**

Run: `cd /Users/derek/repo/axons/contrib/mongo && go test ./... -count=1`
Expected: Compilation failure -- `NewHistoryStore` is not defined yet.

---

### Task 4: MongoDB HistoryStore -- implementation

**Files:**
- Create: `contrib/mongo/history.go`

- [ ] **Step 1: Write the implementation**

Write `/Users/derek/repo/axons/contrib/mongo/history.go`:

```go
// contrib/mongo/history.go
package mongo

import (
	"context"
	"errors"
	"time"

	"github.com/axonframework/axon/interfaces"
	"github.com/axonframework/axon/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// historyDoc is the MongoDB document structure for a conversation session.
type historyDoc struct {
	SessionID string           `bson:"session_id"`
	Messages  []messageDoc     `bson:"messages"`
	UpdatedAt time.Time        `bson:"updated_at"`
}

// messageDoc is the BSON representation of a kernel.Message.
type messageDoc struct {
	Role     string           `bson:"role"`
	Content  []contentPartDoc `bson:"content"`
	Metadata map[string]any   `bson:"metadata,omitempty"`
}

// contentPartDoc is the BSON representation of a kernel.ContentPart.
type contentPartDoc struct {
	Text       *string        `bson:"text,omitempty"`
	Image      *imageDoc      `bson:"image,omitempty"`
	ToolCall   *toolCallDoc   `bson:"tool_call,omitempty"`
	ToolResult *toolResultDoc `bson:"tool_result,omitempty"`
}

type imageDoc struct {
	URL      string `bson:"url"`
	MimeType string `bson:"mime_type,omitempty"`
}

type toolCallDoc struct {
	ID     string `bson:"id"`
	Name   string `bson:"name"`
	Params []byte `bson:"params,omitempty"`
}

type toolResultDoc struct {
	ToolCallID string `bson:"tool_call_id"`
	Name       string `bson:"name"`
	Content    string `bson:"content"`
	IsError    bool   `bson:"is_error,omitempty"`
}

// historyStore is a MongoDB-backed implementation of interfaces.HistoryStore.
// Each session is stored as a single document with a growing messages array.
type historyStore struct {
	coll *mongo.Collection
}

// NewHistoryStore creates a MongoDB-backed HistoryStore.
// The collection should have a unique index on {session_id: 1}.
func NewHistoryStore(db *mongo.Database, collection string) interfaces.HistoryStore {
	return &historyStore{
		coll: db.Collection(collection),
	}
}

// SaveMessages appends messages to the given session's history using
// $push with upsert. If the session document does not exist, it is created.
func (s *historyStore) SaveMessages(ctx context.Context, sessionID string, messages []kernel.Message) error {
	if len(messages) == 0 {
		return nil
	}

	docs := make([]messageDoc, len(messages))
	for i, msg := range messages {
		docs[i] = toMessageDoc(msg)
	}

	filter := bson.D{{Key: "session_id", Value: sessionID}}
	update := bson.D{
		{Key: "$push", Value: bson.D{
			{Key: "messages", Value: bson.D{
				{Key: "$each", Value: docs},
			}},
		}},
		{Key: "$set", Value: bson.D{
			{Key: "updated_at", Value: time.Now()},
		}},
	}

	opts := options.UpdateOne().SetUpsert(true)
	_, err := s.coll.UpdateOne(ctx, filter, update, opts)
	return err
}

// LoadMessages returns the last `limit` messages for the given session.
// If limit <= 0, all messages are returned.
func (s *historyStore) LoadMessages(ctx context.Context, sessionID string, limit int) ([]kernel.Message, error) {
	filter := bson.D{{Key: "session_id", Value: sessionID}}

	var opts *options.FindOneOptionsBuilder
	if limit > 0 {
		opts = options.FindOne().SetProjection(bson.D{
			{Key: "messages", Value: bson.D{{Key: "$slice", Value: -limit}}},
		})
	}

	var doc historyDoc
	var err error
	if opts != nil {
		err = s.coll.FindOne(ctx, filter, opts).Decode(&doc)
	} else {
		err = s.coll.FindOne(ctx, filter).Decode(&doc)
	}
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}

	messages := make([]kernel.Message, len(doc.Messages))
	for i, md := range doc.Messages {
		messages[i] = fromMessageDoc(md)
	}
	return messages, nil
}

// Clear removes all messages for the given session by deleting the document.
func (s *historyStore) Clear(ctx context.Context, sessionID string) error {
	filter := bson.D{{Key: "session_id", Value: sessionID}}
	_, err := s.coll.DeleteOne(ctx, filter)
	return err
}

// toMessageDoc converts a kernel.Message to a BSON-friendly messageDoc.
func toMessageDoc(msg kernel.Message) messageDoc {
	doc := messageDoc{
		Role:     string(msg.Role),
		Content:  make([]contentPartDoc, len(msg.Content)),
		Metadata: msg.Metadata,
	}
	for i, part := range msg.Content {
		doc.Content[i] = toContentPartDoc(part)
	}
	return doc
}

func toContentPartDoc(part kernel.ContentPart) contentPartDoc {
	doc := contentPartDoc{
		Text: part.Text,
	}
	if part.Image != nil {
		doc.Image = &imageDoc{
			URL:      part.Image.URL,
			MimeType: part.Image.MimeType,
		}
	}
	if part.ToolCall != nil {
		doc.ToolCall = &toolCallDoc{
			ID:     part.ToolCall.ID,
			Name:   part.ToolCall.Name,
			Params: part.ToolCall.Params,
		}
	}
	if part.ToolResult != nil {
		doc.ToolResult = &toolResultDoc{
			ToolCallID: part.ToolResult.ToolCallID,
			Name:       part.ToolResult.Name,
			Content:    part.ToolResult.Content,
			IsError:    part.ToolResult.IsError,
		}
	}
	return doc
}

// fromMessageDoc converts a BSON messageDoc back to a kernel.Message.
func fromMessageDoc(doc messageDoc) kernel.Message {
	msg := kernel.Message{
		Role:     kernel.Role(doc.Role),
		Content:  make([]kernel.ContentPart, len(doc.Content)),
		Metadata: doc.Metadata,
	}
	for i, part := range doc.Content {
		msg.Content[i] = fromContentPartDoc(part)
	}
	return msg
}

func fromContentPartDoc(doc contentPartDoc) kernel.ContentPart {
	part := kernel.ContentPart{
		Text: doc.Text,
	}
	if doc.Image != nil {
		part.Image = &kernel.ImageContent{
			URL:      doc.Image.URL,
			MimeType: doc.Image.MimeType,
		}
	}
	if doc.ToolCall != nil {
		part.ToolCall = &kernel.ToolCall{
			ID:     doc.ToolCall.ID,
			Name:   doc.ToolCall.Name,
			Params: doc.ToolCall.Params,
		}
	}
	if doc.ToolResult != nil {
		part.ToolResult = &kernel.ToolResult{
			ToolCallID: doc.ToolResult.ToolCallID,
			Name:       doc.ToolResult.Name,
			Content:    doc.ToolResult.Content,
			IsError:    doc.ToolResult.IsError,
		}
	}
	return part
}
```

- [ ] **Step 2: Run go mod tidy to resolve any new imports**

Run: `cd /Users/derek/repo/axons/contrib/mongo && go mod tidy`
Expected: Clean (no new deps needed beyond what go.mod already has).

- [ ] **Step 3: Run tests to verify they pass**

Run: `cd /Users/derek/repo/axons/contrib/mongo && go test -v -run TestHistoryStore -count=1`
Expected: PASS -- all 7 history tests pass.

- [ ] **Step 4: Verify clean build**

Run: `cd /Users/derek/repo/axons/contrib/mongo && go vet ./...`
Expected: No issues.

- [ ] **Step 5: Commit**

```bash
git add contrib/mongo/history.go contrib/mongo/history_test.go
git commit -m "feat(contrib/mongo): add MongoDB HistoryStore implementation"
```

---

### Task 5: MongoDB MemoryStore -- tests first

**Files:**
- Create: `contrib/mongo/memory_test.go`

- [ ] **Step 1: Write the test file**

Write `/Users/derek/repo/axons/contrib/mongo/memory_test.go`:

```go
// contrib/mongo/memory_test.go
package mongo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/axonframework/axon/interfaces"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/integration/mtest"
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

func TestMemoryStoreSave(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))

	mt.Run("saves memories with embeddings via bulk write", func(mt *mtest.T) {
		embedder := newConstantEmbedder(3)
		store := NewMemoryStore(mt.DB, mt.Coll.Name(), embedder)

		mt.AddMockResponses(bson.D{
			{Key: "ok", Value: 1},
			{Key: "nUpserted", Value: 2},
		})

		mems := []interfaces.Memory{
			{ID: "m1", Content: "User likes Go programming"},
			{ID: "m2", Content: "User lives in Portland"},
		}

		err := store.Save(context.Background(), "user-1", mems)
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
	})
}

func TestMemoryStoreSaveAutoGeneratesID(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))

	mt.Run("generates ID for memories with empty ID", func(mt *mtest.T) {
		embedder := newConstantEmbedder(3)
		store := NewMemoryStore(mt.DB, mt.Coll.Name(), embedder)

		mt.AddMockResponses(bson.D{
			{Key: "ok", Value: 1},
			{Key: "nUpserted", Value: 1},
		})

		mems := []interfaces.Memory{
			{Content: "Something without an ID"},
		}

		err := store.Save(context.Background(), "user-1", mems)
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
	})
}

func TestMemoryStoreSaveEmptySlice(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))

	mt.Run("no-op for empty memories", func(mt *mtest.T) {
		embedder := newConstantEmbedder(3)
		store := NewMemoryStore(mt.DB, mt.Coll.Name(), embedder)

		err := store.Save(context.Background(), "user-1", nil)
		if err != nil {
			t.Fatalf("Save with nil: %v", err)
		}

		err = store.Save(context.Background(), "user-1", []interfaces.Memory{})
		if err != nil {
			t.Fatalf("Save with empty slice: %v", err)
		}
	})
}

func TestMemoryStoreSaveEmbedderError(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))

	mt.Run("returns error when embedder fails", func(mt *mtest.T) {
		embedder := newFailingEmbedder()
		store := NewMemoryStore(mt.DB, mt.Coll.Name(), embedder)

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
	})
}

func TestMemoryStoreGet(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))

	mt.Run("returns memories for user without embeddings", func(mt *mtest.T) {
		embedder := newConstantEmbedder(3)
		store := NewMemoryStore(mt.DB, mt.Coll.Name(), embedder)

		now := time.Now()
		first := mtest.CreateCursorResponse(1, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch, bson.D{
			{Key: "user_id", Value: "user-1"},
			{Key: "memory_id", Value: "m1"},
			{Key: "content", Value: "User likes Go"},
			{Key: "created_at", Value: now},
			{Key: "updated_at", Value: now},
		})
		second := mtest.CreateCursorResponse(1, mt.DB.Name()+"."+mt.Coll.Name(), mtest.NextBatch, bson.D{
			{Key: "user_id", Value: "user-1"},
			{Key: "memory_id", Value: "m2"},
			{Key: "content", Value: "User lives in Portland"},
			{Key: "created_at", Value: now},
			{Key: "updated_at", Value: now},
		})
		killCursors := mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.NextBatch)
		mt.AddMockResponses(first, second, killCursors)

		mems, err := store.Get(context.Background(), "user-1")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if len(mems) != 2 {
			t.Fatalf("expected 2 memories, got %d", len(mems))
		}
		if mems[0].ID != "m1" {
			t.Errorf("expected first memory ID %q, got %q", "m1", mems[0].ID)
		}
		if mems[0].Content != "User likes Go" {
			t.Errorf("expected first memory content %q, got %q", "User likes Go", mems[0].Content)
		}
		if mems[1].ID != "m2" {
			t.Errorf("expected second memory ID %q, got %q", "m2", mems[1].ID)
		}
	})
}

func TestMemoryStoreGetEmpty(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))

	mt.Run("returns nil for user with no memories", func(mt *mtest.T) {
		embedder := newConstantEmbedder(3)
		store := NewMemoryStore(mt.DB, mt.Coll.Name(), embedder)

		// Empty cursor
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch))

		mems, err := store.Get(context.Background(), "unknown-user")
		if err != nil {
			t.Fatalf("Get empty user: %v", err)
		}
		if mems != nil {
			t.Errorf("expected nil for empty user, got %v", mems)
		}
	})
}

func TestMemoryStoreSearch(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))

	mt.Run("returns vector search results", func(mt *mtest.T) {
		embedder := newConstantEmbedder(3)
		store := NewMemoryStore(mt.DB, mt.Coll.Name(), embedder)

		now := time.Now()
		first := mtest.CreateCursorResponse(1, mt.DB.Name()+"."+mt.Coll.Name(), mtest.FirstBatch, bson.D{
			{Key: "user_id", Value: "user-1"},
			{Key: "memory_id", Value: "m1"},
			{Key: "content", Value: "User likes Go"},
			{Key: "created_at", Value: now},
			{Key: "updated_at", Value: now},
		})
		killCursors := mtest.CreateCursorResponse(0, mt.DB.Name()+"."+mt.Coll.Name(), mtest.NextBatch)
		mt.AddMockResponses(first, killCursors)

		mems, err := store.Search(context.Background(), "user-1", "programming languages", 5)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(mems) != 1 {
			t.Fatalf("expected 1 memory, got %d", len(mems))
		}
		if mems[0].ID != "m1" {
			t.Errorf("expected memory ID %q, got %q", "m1", mems[0].ID)
		}
		if mems[0].Content != "User likes Go" {
			t.Errorf("expected content %q, got %q", "User likes Go", mems[0].Content)
		}
	})
}

func TestMemoryStoreSearchEmbedderError(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))

	mt.Run("returns error when embedder fails during search", func(mt *mtest.T) {
		embedder := newFailingEmbedder()
		store := NewMemoryStore(mt.DB, mt.Coll.Name(), embedder)

		_, err := store.Search(context.Background(), "user-1", "anything", 5)
		if err == nil {
			t.Fatal("expected error from failing embedder, got nil")
		}
	})
}

func TestMemoryStoreDelete(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))

	mt.Run("deletes memories by user and IDs", func(mt *mtest.T) {
		embedder := newConstantEmbedder(3)
		store := NewMemoryStore(mt.DB, mt.Coll.Name(), embedder)

		mt.AddMockResponses(bson.D{
			{Key: "ok", Value: 1},
			{Key: "n", Value: 2},
		})

		err := store.Delete(context.Background(), "user-1", []string{"m1", "m2"})
		if err != nil {
			t.Fatalf("Delete: %v", err)
		}
	})
}

func TestMemoryStoreDeleteEmptyIDs(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))

	mt.Run("no-op for empty ID slice", func(mt *mtest.T) {
		embedder := newConstantEmbedder(3)
		store := NewMemoryStore(mt.DB, mt.Coll.Name(), embedder)

		err := store.Delete(context.Background(), "user-1", nil)
		if err != nil {
			t.Fatalf("Delete with nil IDs: %v", err)
		}

		err = store.Delete(context.Background(), "user-1", []string{})
		if err != nil {
			t.Fatalf("Delete with empty IDs: %v", err)
		}
	})
}
```

- [ ] **Step 2: Verify tests fail to compile**

Run: `cd /Users/derek/repo/axons/contrib/mongo && go test ./... -count=1`
Expected: Compilation failure -- `NewMemoryStore` is not defined yet.

---

### Task 6: MongoDB MemoryStore -- implementation

**Files:**
- Create: `contrib/mongo/memory.go`

- [ ] **Step 1: Write the implementation**

Write `/Users/derek/repo/axons/contrib/mongo/memory.go`:

```go
// contrib/mongo/memory.go
package mongo

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/axonframework/axon/interfaces"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
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
	coll     *mongo.Collection
	embedder interfaces.Embedder
}

// NewMemoryStore creates a MongoDB-backed MemoryStore with vector search.
// The collection should have:
//   - A unique compound index on {user_id: 1, memory_id: 1}
//   - An Atlas Vector Search index on the embedding field (see package docs)
//
// The embedder is called to generate vector embeddings for Save and Search operations.
func NewMemoryStore(db *mongo.Database, collection string, embedder interfaces.Embedder) interfaces.MemoryStore {
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
	models := make([]mongo.WriteModel, len(memories))
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

		models[i] = mongo.NewUpdateOneModel().
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
```

- [ ] **Step 2: Run go mod tidy**

Run: `cd /Users/derek/repo/axons/contrib/mongo && go mod tidy`
Expected: Clean.

- [ ] **Step 3: Run all tests**

Run: `cd /Users/derek/repo/axons/contrib/mongo && go test -v -count=1 ./...`
Expected: PASS -- all history and memory tests pass.

- [ ] **Step 4: Verify clean build**

Run: `cd /Users/derek/repo/axons/contrib/mongo && go vet ./...`
Expected: No issues.

- [ ] **Step 5: Commit**

```bash
git add contrib/mongo/memory.go contrib/mongo/memory_test.go
git commit -m "feat(contrib/mongo): add MongoDB MemoryStore with vector search"
```

---

### Task 7: Final verification and cleanup

- [ ] **Step 1: Run all tests across the entire contrib/mongo module**

Run: `cd /Users/derek/repo/axons/contrib/mongo && go test ./... -v -count=1`
Expected: PASS -- all tests across history_test.go and memory_test.go.

- [ ] **Step 2: Run with race detector**

Run: `cd /Users/derek/repo/axons/contrib/mongo && go test ./... -race -count=1`
Expected: PASS with no data races detected.

- [ ] **Step 3: Verify the interfaces package still passes**

Run: `cd /Users/derek/repo/axons/interfaces && go test ./... -count=1`
Expected: PASS -- the new embedder.go introduces no regressions.

- [ ] **Step 4: Verify go vet on both packages**

Run:
```bash
cd /Users/derek/repo/axons/interfaces && go vet ./...
cd /Users/derek/repo/axons/contrib/mongo && go vet ./...
```
Expected: No issues on either package.

- [ ] **Step 5: Commit any remaining unstaged changes (if any)**

```bash
git add interfaces/ contrib/mongo/
git commit -m "feat(contrib/mongo): complete MongoDB contrib package with all tests passing"
```

---

## Self-Review

### Interface compliance

- `historyStore` satisfies `interfaces.HistoryStore`:
  - `SaveMessages(ctx, sessionID, messages)` -- uses `$push` + `$each` with upsert, no-op for empty slice
  - `LoadMessages(ctx, sessionID, limit)` -- uses `FindOne` with `$slice: -N` projection; limit <= 0 returns all; returns nil for missing session
  - `Clear(ctx, sessionID)` -- uses `DeleteOne`; no error for missing session

- `memoryStore` satisfies `interfaces.MemoryStore`:
  - `Save(ctx, userID, memories)` -- batch embeds via `Embedder`, `BulkWrite` with upserts on `(user_id, memory_id)`, auto-generates IDs, no-op for empty slice
  - `Get(ctx, userID)` -- `Find` by user_id with projection excluding embedding, returns nil for empty result
  - `Search(ctx, userID, query, topK)` -- embeds query, `$vectorSearch` aggregation with user_id filter, returns nil for empty result
  - `Delete(ctx, userID, ids)` -- `DeleteMany` with `$in` filter, no-op for empty IDs

### Spec adherence

- HistoryStore uses one-document-per-session with `$push` growth as specified
- MemoryStore uses one-document-per-memory with `BulkWrite` upserts as specified
- `$vectorSearch` uses `numCandidates: topK * 10` for quality recall as is standard practice
- Vector search index name is `"vector_index"` -- users create this in Atlas
- Embedding exclusion in `Get` and post-`$vectorSearch` `$project` matches spec
- `$setOnInsert` for `created_at` preserves original creation time on upserts
- Required indexes documented: `{session_id: 1}` unique, `{user_id: 1, memory_id: 1}` unique compound

### TDD adherence

- Task 3 writes history tests before Task 4 writes history implementation
- Task 5 writes memory tests before Task 6 writes memory implementation
- Each test step includes a "verify tests fail" checkpoint before implementation

### Edge cases handled

- Empty message/memory slices: early return, no MongoDB call
- Empty ID slices in Delete: early return
- Missing session in LoadMessages: returns `(nil, nil)` not an error
- Missing user in Get: returns `(nil, nil)`
- Embedder failures: propagated as errors from Save and Search
- Auto-generated memory IDs: `mem-N` counter pattern matching inmemory package
- `created_at` preservation: `$setOnInsert` prevents overwriting on upsert updates
- `updated_at` always set to `now` on save

### What this does NOT do (per spec)

- No automatic index creation (user responsibility)
- No TTL/expiry on messages
- No transactions
- No connection pooling (user provides `*mongo.Database`)
- No reference Embedder implementation
- No migration tooling
