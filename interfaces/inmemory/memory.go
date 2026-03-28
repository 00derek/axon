// interfaces/inmemory/memory.go
package inmemory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/axonframework/axon/interfaces"
)

// idCounter provides unique auto-generated IDs for memories without an explicit ID.
var idCounter atomic.Uint64

func generateID() string {
	return fmt.Sprintf("mem-%d", idCounter.Add(1))
}

// memoryStore is a thread-safe in-memory implementation of interfaces.MemoryStore.
// Search uses naive case-insensitive substring matching on Memory.Content.
// Intended for development and testing; not suitable for production persistence.
type memoryStore struct {
	mu    sync.RWMutex
	users map[string][]interfaces.Memory
}

// NewMemoryStore creates an in-memory MemoryStore.
func NewMemoryStore() interfaces.MemoryStore {
	return &memoryStore{
		users: make(map[string][]interfaces.Memory),
	}
}

// Save persists memories for the given user. Memories with empty IDs get
// auto-generated IDs. CreatedAt and UpdatedAt are set to the current time
// if they are zero.
func (s *memoryStore) Save(_ context.Context, userID string, memories []interfaces.Memory) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for i := range memories {
		if memories[i].ID == "" {
			memories[i].ID = generateID()
		}
		if memories[i].CreatedAt.IsZero() {
			memories[i].CreatedAt = now
		}
		if memories[i].UpdatedAt.IsZero() {
			memories[i].UpdatedAt = now
		}
	}

	s.users[userID] = append(s.users[userID], memories...)
	return nil
}

// Get returns all memories for the given user.
func (s *memoryStore) Get(_ context.Context, userID string) ([]interfaces.Memory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mems := s.users[userID]
	if len(mems) == 0 {
		return nil, nil
	}

	result := make([]interfaces.Memory, len(mems))
	copy(result, mems)
	return result, nil
}

// Search returns up to topK memories whose Content contains the query string.
// Matching is case-insensitive. Results are returned in storage order.
func (s *memoryStore) Search(_ context.Context, userID string, query string, topK int) ([]interfaces.Memory, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mems := s.users[userID]
	loweredQuery := strings.ToLower(query)

	var results []interfaces.Memory
	for _, m := range mems {
		if strings.Contains(strings.ToLower(m.Content), loweredQuery) {
			results = append(results, m)
			if len(results) >= topK {
				break
			}
		}
	}
	return results, nil
}

// Delete removes memories with the given IDs for the given user.
// IDs that do not exist are silently ignored.
func (s *memoryStore) Delete(_ context.Context, userID string, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mems := s.users[userID]
	if len(mems) == 0 {
		return nil
	}

	toDelete := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		toDelete[id] = struct{}{}
	}

	filtered := make([]interfaces.Memory, 0, len(mems))
	for _, m := range mems {
		if _, found := toDelete[m.ID]; !found {
			filtered = append(filtered, m)
		}
	}

	s.users[userID] = filtered
	return nil
}
