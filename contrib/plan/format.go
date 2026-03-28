// contrib/plan/format.go
package plan

import (
	"fmt"
	"sort"
	"strings"
)

// Format renders a Plan as structured text for injection into the LLM context.
//
// Output format:
//
//	## Current Plan: {Name}
//	Goal: {Goal}
//
//	[✓] step_name — Description
//	[>] active_step — Description
//	[ ] pending_step — Description (needs user input)
//	[-] skipped_step — Description
//
//	Notes:
//	- key: value
func Format(p *Plan) string {
	var b strings.Builder

	fmt.Fprintf(&b, "## Current Plan: %s\n", p.Name)
	fmt.Fprintf(&b, "Goal: %s\n", p.Goal)

	if len(p.Steps) > 0 {
		b.WriteString("\n")
		for _, s := range p.Steps {
			marker := statusMarker(s.Status)
			fmt.Fprintf(&b, "%s %s — %s", marker, s.Name, s.Description)

			if s.NeedsUserInput {
				b.WriteString(" (needs user input)")
			}
			if s.CanRepeat {
				b.WriteString(" (repeatable)")
			}
			b.WriteString("\n")
		}
	}

	if len(p.Notes) > 0 {
		b.WriteString("\nNotes:\n")
		// Sort keys for deterministic output.
		keys := make([]string, 0, len(p.Notes))
		for k := range p.Notes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "- %s: %v\n", k, p.Notes[k])
		}
	}

	return b.String()
}

// statusMarker returns the checkbox marker for a given StepStatus.
func statusMarker(s StepStatus) string {
	switch s {
	case StatusDone:
		return "[✓]"
	case StatusActive:
		return "[>]"
	case StatusSkipped:
		return "[-]"
	default:
		return "[ ]"
	}
}
