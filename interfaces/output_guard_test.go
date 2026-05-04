// interfaces/output_guard_test.go
package interfaces

import (
	"context"
	"testing"
)

func TestSimpleOutputGuardAllowed(t *testing.T) {
	guard := NewSimpleOutputGuard([]string{"social security number", "credit card"})

	result, err := guard.Check(context.Background(), "Here is your restaurant recommendation: Bella Trattoria.")
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

func TestSimpleOutputGuardBlocked(t *testing.T) {
	guard := NewSimpleOutputGuard([]string{"social security number", "credit card"})

	result, err := guard.Check(context.Background(), "Your social security number is 123-45-6789")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Allowed {
		t.Error("expected blocked, got allowed")
	}
	if result.Reason != `output contains blocked phrase: "social security number"` {
		t.Errorf("unexpected reason: %q", result.Reason)
	}
}

func TestSimpleOutputGuardCaseInsensitive(t *testing.T) {
	guard := NewSimpleOutputGuard([]string{"top secret"})

	result, err := guard.Check(context.Background(), "This is TOP SECRET information.")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Allowed {
		t.Error("expected blocked for uppercase output, got allowed")
	}
}

func TestSimpleOutputGuardEmptyBlocklist(t *testing.T) {
	guard := NewSimpleOutputGuard(nil)

	result, err := guard.Check(context.Background(), "anything goes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Error("expected allowed with empty blocklist, got blocked")
	}
}

func TestSimpleOutputGuardEmptyOutput(t *testing.T) {
	guard := NewSimpleOutputGuard([]string{"bad"})

	result, err := guard.Check(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Error("expected allowed for empty output, got blocked")
	}
}

// OutputGuard interface conformance check at compile time.
var _ OutputGuard = (*simpleOutputGuard)(nil)
