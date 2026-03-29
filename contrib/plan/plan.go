// contrib/plan/plan.go
package plan

// StepStatus represents the current state of a plan step.
type StepStatus string

const (
	StatusPending StepStatus = "pending"
	StatusActive  StepStatus = "active"
	StatusDone    StepStatus = "done"
	StatusSkipped StepStatus = "skipped"
)

// Plan is a structured multi-step procedure for an agent to follow.
// It is a plain struct that serializes cleanly with encoding/json.
type Plan struct {
	Name  string         `json:"name"`
	Goal  string         `json:"goal"`
	Steps []Step         `json:"steps"`
	Notes map[string]any `json:"notes"`
}

// Step is a single step in a Plan.
type Step struct {
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	Status         StepStatus `json:"status"`
	NeedsUserInput bool       `json:"needs_user_input,omitempty"`
	CanRepeat      bool       `json:"can_repeat,omitempty"`
}

// New creates a Plan with the given steps. All steps start as StatusPending.
// Notes is initialized to an empty map.
func New(name, goal string, steps ...Step) *Plan {
	s := make([]Step, len(steps))
	for i, step := range steps {
		step.Status = StatusPending
		s[i] = step
	}
	return &Plan{
		Name:  name,
		Goal:  goal,
		Steps: s,
		Notes: make(map[string]any),
	}
}
