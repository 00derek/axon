// interfaces/inmemory/history.go
package inmemory

import (
	"context"
	"sync"

	"github.com/axonframework/axon/interfaces"
	"github.com/axonframework/axon/kernel"
)

// historyStore is a thread-safe in-memory implementation of interfaces.HistoryStore.
// Intended for development and testing; not suitable for production persistence.
type historyStore struct {
	mu       sync.RWMutex
	sessions map[string][]kernel.Message
}

// NewHistoryStore creates an in-memory HistoryStore.
func NewHistoryStore() interfaces.HistoryStore {
	return &historyStore{
		sessions: make(map[string][]kernel.Message),
	}
}

// SaveMessages appends messages to the given session's history.
func (s *historyStore) SaveMessages(_ context.Context, sessionID string, messages []kernel.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionID] = append(s.sessions[sessionID], messages...)
	return nil
}

// LoadMessages returns the last `limit` messages for the given session.
// If limit <= 0, all messages are returned.
func (s *historyStore) LoadMessages(_ context.Context, sessionID string, limit int) ([]kernel.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	msgs := s.sessions[sessionID]
	if len(msgs) == 0 {
		return nil, nil
	}

	if limit <= 0 || limit > len(msgs) {
		limit = len(msgs)
	}

	// Return the LAST `limit` messages (most recent).
	start := len(msgs) - limit
	result := make([]kernel.Message, limit)
	copy(result, msgs[start:])
	return result, nil
}

// Clear removes all messages for the given session.
func (s *historyStore) Clear(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
	return nil
}
