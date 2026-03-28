// interfaces/guard.go
package interfaces

import (
	"context"
	"fmt"
	"strings"
)

// GuardResult is the outcome of a Guard check.
type GuardResult struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

// Guard validates input before it reaches the agent.
type Guard interface {
	// Check evaluates the input and returns whether it is allowed.
	Check(ctx context.Context, input string) (GuardResult, error)
}

// blocklistGuard is a Guard that blocks input containing any of the blocked phrases.
type blocklistGuard struct {
	blocked []string // stored as lowercase
}

// NewBlocklistGuard creates a Guard that rejects input containing any of the
// given phrases. Matching is case-insensitive substring matching.
func NewBlocklistGuard(blocked []string) Guard {
	lower := make([]string, len(blocked))
	for i, b := range blocked {
		lower[i] = strings.ToLower(b)
	}
	return &blocklistGuard{blocked: lower}
}

// Check scans the input for any blocked phrase. The match with the earliest
// position in the input string causes rejection. Returns (Allowed=true) if no phrase matches.
func (g *blocklistGuard) Check(_ context.Context, input string) (GuardResult, error) {
	lowered := strings.ToLower(input)
	bestIdx := -1
	bestPhrase := ""
	for _, phrase := range g.blocked {
		idx := strings.Index(lowered, phrase)
		if idx >= 0 && (bestIdx < 0 || idx < bestIdx) {
			bestIdx = idx
			bestPhrase = phrase
		}
	}
	if bestIdx >= 0 {
		return GuardResult{
			Allowed: false,
			Reason:  fmt.Sprintf("input contains blocked phrase: %q", bestPhrase),
		}, nil
	}
	return GuardResult{Allowed: true}, nil
}
