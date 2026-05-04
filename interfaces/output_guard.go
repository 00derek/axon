// interfaces/output_guard.go
package interfaces

import (
	"context"
	"fmt"
	"strings"
)

// OutputGuard validates the agent's final response text after the agent loop
// completes but before the result is returned to the caller. Returning
// Allowed=false signals that the output should be sanitized, replaced, or
// rejected. The exact rejection behavior is defined by the consumer (typically
// the guards capability package).
type OutputGuard interface {
	// Check evaluates the final response text and returns whether it is allowed.
	Check(ctx context.Context, output string) (GuardResult, error)
}

// simpleOutputGuard rejects output that contains any of the configured phrases.
// Mirrors NewBlocklistGuard, but operates on the agent's final response text.
type simpleOutputGuard struct {
	blocked []string // stored as lowercase
}

// NewSimpleOutputGuard creates an OutputGuard that rejects any final response
// containing one of the given phrases. Matching is case-insensitive substring
// matching. The earliest-matching phrase wins.
func NewSimpleOutputGuard(blocked []string) OutputGuard {
	lower := make([]string, len(blocked))
	for i, b := range blocked {
		lower[i] = strings.ToLower(b)
	}
	return &simpleOutputGuard{blocked: lower}
}

// Check scans the output for any blocked phrase. The match with the earliest
// position in the output causes rejection. Returns Allowed=true when no phrase
// matches.
func (g *simpleOutputGuard) Check(_ context.Context, output string) (GuardResult, error) {
	lowered := strings.ToLower(output)
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
			Reason:  fmt.Sprintf("output contains blocked phrase: %q", bestPhrase),
		}, nil
	}
	return GuardResult{Allowed: true}, nil
}
