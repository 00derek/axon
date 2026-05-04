// interfaces/tool_guard.go
package interfaces

import (
	"context"
	"encoding/json"
	"fmt"
)

// ToolGuard validates a tool invocation before the underlying tool executes.
// It receives the tool name and the raw JSON parameters the LLM produced.
// Returning Allowed=false instructs the consumer to skip execution; the exact
// rejection behavior (e.g. report a tool error back to the LLM) is defined by
// the consumer (typically the guards capability package).
type ToolGuard interface {
	// Check evaluates a pending tool invocation and returns whether it is allowed.
	Check(ctx context.Context, toolName string, params json.RawMessage) (GuardResult, error)
}

// simpleToolGuard rejects tool calls whose name appears in the blocklist.
// Tool name comparison is case-sensitive because tool names are exact-match
// identifiers in the agent's tool registry.
type simpleToolGuard struct {
	blocked map[string]struct{}
}

// NewSimpleToolGuard creates a ToolGuard that rejects any tool whose name
// appears in the given list. Comparison is case-sensitive (tool names are
// identifiers, not natural language).
func NewSimpleToolGuard(blocked []string) ToolGuard {
	set := make(map[string]struct{}, len(blocked))
	for _, name := range blocked {
		set[name] = struct{}{}
	}
	return &simpleToolGuard{blocked: set}
}

// Check returns Allowed=false if toolName is in the configured blocklist.
// Params are accepted for interface symmetry but ignored by this simple
// implementation; richer guards may inspect them.
func (g *simpleToolGuard) Check(_ context.Context, toolName string, _ json.RawMessage) (GuardResult, error) {
	if _, ok := g.blocked[toolName]; ok {
		return GuardResult{
			Allowed: false,
			Reason:  fmt.Sprintf("tool %q is blocked", toolName),
		}, nil
	}
	return GuardResult{Allowed: true}, nil
}
