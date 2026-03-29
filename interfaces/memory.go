// interfaces/memory.go
package interfaces

import (
	"context"
	"time"
)

// Memory represents a long-term knowledge fragment about a user.
type Memory struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MemoryStore manages long-term user knowledge.
// Implementations persist and retrieve user-scoped memory fragments.
type MemoryStore interface {
	// Save persists memories for the given user. If a Memory has an empty ID,
	// the implementation must auto-generate one.
	Save(ctx context.Context, userID string, memories []Memory) error

	// Get returns all memories for the given user.
	Get(ctx context.Context, userID string) ([]Memory, error)

	// Search returns up to topK memories matching the query for the given user.
	Search(ctx context.Context, userID string, query string, topK int) ([]Memory, error)

	// Delete removes memories with the given IDs for the given user.
	Delete(ctx context.Context, userID string, ids []string) error
}
