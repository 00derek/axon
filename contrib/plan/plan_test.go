// contrib/plan/plan_test.go
package plan

import (
	"testing"
)

func TestNewCreatesWithPendingSteps(t *testing.T) {
	p := New("Booking", "Help user book a restaurant",
		Step{Name: "gather", Description: "Ask preferences"},
		Step{Name: "search", Description: "Find restaurants"},
		Step{Name: "confirm", Description: "Confirm booking", NeedsUserInput: true},
	)

	if p.Name != "Booking" {
		t.Errorf("expected name %q, got %q", "Booking", p.Name)
	}
	if p.Goal != "Help user book a restaurant" {
		t.Errorf("expected goal %q, got %q", "Help user book a restaurant", p.Goal)
	}
	if len(p.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(p.Steps))
	}
	for i, s := range p.Steps {
		if s.Status != StatusPending {
			t.Errorf("step %d: expected status %q, got %q", i, StatusPending, s.Status)
		}
	}
	if p.Notes == nil {
		t.Error("expected Notes map to be initialized, got nil")
	}
}

func TestNewPreservesStepFields(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First", NeedsUserInput: true, CanRepeat: true},
		Step{Name: "s2", Description: "Second"},
	)

	s1 := p.Steps[0]
	if s1.Name != "s1" {
		t.Errorf("expected name %q, got %q", "s1", s1.Name)
	}
	if s1.Description != "First" {
		t.Errorf("expected description %q, got %q", "First", s1.Description)
	}
	if !s1.NeedsUserInput {
		t.Error("expected NeedsUserInput true")
	}
	if !s1.CanRepeat {
		t.Error("expected CanRepeat true")
	}

	s2 := p.Steps[1]
	if s2.NeedsUserInput {
		t.Error("expected NeedsUserInput false for s2")
	}
}

func TestNewNoSteps(t *testing.T) {
	p := New("Empty", "No steps")
	if len(p.Steps) != 0 {
		t.Errorf("expected 0 steps, got %d", len(p.Steps))
	}
	if p.Notes == nil {
		t.Error("expected Notes map to be initialized")
	}
}

func TestStepStatusConstants(t *testing.T) {
	// Verify the string values match spec.
	if StatusPending != "pending" {
		t.Errorf("StatusPending = %q", StatusPending)
	}
	if StatusActive != "active" {
		t.Errorf("StatusActive = %q", StatusActive)
	}
	if StatusDone != "done" {
		t.Errorf("StatusDone = %q", StatusDone)
	}
	if StatusSkipped != "skipped" {
		t.Errorf("StatusSkipped = %q", StatusSkipped)
	}
}
