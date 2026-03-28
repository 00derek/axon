// interfaces/history.go
package interfaces

import (
	"context"

	"github.com/axonframework/axon/kernel"
)

// HistoryStore manages short-term conversation message history.
// Implementations store and retrieve messages scoped to a session.
type HistoryStore interface {
	// SaveMessages appends messages to the given session's history.
	SaveMessages(ctx context.Context, sessionID string, messages []kernel.Message) error

	// LoadMessages returns the last `limit` messages for the given session.
	// If limit <= 0, all messages are returned.
	LoadMessages(ctx context.Context, sessionID string, limit int) ([]kernel.Message, error)

	// Clear removes all messages for the given session.
	Clear(ctx context.Context, sessionID string) error
}
