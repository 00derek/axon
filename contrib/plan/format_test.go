// contrib/plan/format_test.go
package plan

import (
	"strings"
	"testing"
)

func TestFormatAllPending(t *testing.T) {
	p := New("Booking", "Help user book",
		Step{Name: "gather", Description: "Ask preferences"},
		Step{Name: "search", Description: "Find options"},
	)

	got := Format(p)

	assertContains(t, got, "## Current Plan: Booking")
	assertContains(t, got, "Goal: Help user book")
	assertContains(t, got, "[ ] gather")
	assertContains(t, got, "[ ] search")
}

func TestFormatMixedStatuses(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
		Step{Name: "s2", Description: "Second"},
		Step{Name: "s3", Description: "Third"},
		Step{Name: "s4", Description: "Fourth"},
	)
	p.Steps[0].Status = StatusDone
	p.Steps[1].Status = StatusActive
	// s3 stays pending
	p.Steps[3].Status = StatusSkipped

	got := Format(p)

	assertContains(t, got, "[✓] s1 — First")
	assertContains(t, got, "[>] s2 — Second")
	assertContains(t, got, "[ ] s3 — Third")
	assertContains(t, got, "[-] s4 — Fourth")
}

func TestFormatNeedsUserInput(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "confirm", Description: "Get confirmation", NeedsUserInput: true},
	)

	got := Format(p)
	assertContains(t, got, "(needs user input)")
}

func TestFormatCanRepeat(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "retry", Description: "Try again", CanRepeat: true},
	)

	got := Format(p)
	assertContains(t, got, "(repeatable)")
}

func TestFormatBothAnnotations(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s", Description: "Both", NeedsUserInput: true, CanRepeat: true},
	)

	got := Format(p)
	assertContains(t, got, "(needs user input)")
	assertContains(t, got, "(repeatable)")
}

func TestFormatWithNotes(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
	)
	p.Notes["cuisine"] = "Italian"
	p.Notes["party_size"] = 4

	got := Format(p)

	assertContains(t, got, "Notes:")
	assertContains(t, got, "cuisine: Italian")
	assertContains(t, got, "party_size: 4")
}

func TestFormatNoNotesSection(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
	)
	// Notes is empty map

	got := Format(p)

	if strings.Contains(got, "Notes:") {
		t.Error("expected no Notes section when notes map is empty")
	}
}

func TestFormatNoSteps(t *testing.T) {
	p := New("Empty", "No steps")

	got := Format(p)
	assertContains(t, got, "## Current Plan: Empty")
	assertContains(t, got, "Goal: No steps")
}

// assertContains is a test helper that checks if got contains want.
func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("expected output to contain %q\ngot:\n%s", want, got)
	}
}
