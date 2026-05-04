// interfaces/tool_guard_test.go
package interfaces

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSimpleToolGuardAllowed(t *testing.T) {
	guard := NewSimpleToolGuard([]string{"shell_exec", "delete_file"})

	result, err := guard.Check(context.Background(), "search_restaurants", json.RawMessage(`{"query":"italian"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Errorf("expected allowed, got blocked with reason: %s", result.Reason)
	}
	if result.Reason != "" {
		t.Errorf("expected empty reason, got %q", result.Reason)
	}
}

func TestSimpleToolGuardBlocked(t *testing.T) {
	guard := NewSimpleToolGuard([]string{"shell_exec", "delete_file"})

	result, err := guard.Check(context.Background(), "shell_exec", json.RawMessage(`{"cmd":"rm -rf /"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Allowed {
		t.Error("expected blocked, got allowed")
	}
	if result.Reason != `tool "shell_exec" is blocked` {
		t.Errorf("unexpected reason: %q", result.Reason)
	}
}

func TestSimpleToolGuardCaseSensitive(t *testing.T) {
	// Tool names are case-sensitive identifiers in code, so the guard must
	// match exactly. "Shell_Exec" should NOT match "shell_exec".
	guard := NewSimpleToolGuard([]string{"shell_exec"})

	result, err := guard.Check(context.Background(), "Shell_Exec", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Error("expected allowed for differently-cased tool name, got blocked")
	}
}

func TestSimpleToolGuardEmptyBlocklist(t *testing.T) {
	guard := NewSimpleToolGuard(nil)

	result, err := guard.Check(context.Background(), "any_tool", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Error("expected allowed with empty blocklist, got blocked")
	}
}

// ToolGuard interface conformance check at compile time.
var _ ToolGuard = (*simpleToolGuard)(nil)
