// Package plan adds structured, auditable multi-step procedures to an agent.
//
// A Plan is an agent-authored list of steps with status tracking. Enable it on
// an agent via [Enable]; the agent gains four tools (create_plan, append_step,
// mark_step, add_note) and the plan text is injected into the system prompt
// every round so the LLM can see its progress.
//
// Use a Plan when the agent needs to coordinate 5+ steps, you want an
// inspectable progress record, or you want to resume across sessions. For
// simple 1–3 tool-call flows, skip it — the LLM handles those naturally.
//
// Plan contrasts with the workflow package: workflow orchestrates human-authored
// sequences of nodes; plan is the agent's own procedure inside a single agent
// run. A plan-capable agent can be used as a workflow node.
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
// It serializes cleanly with encoding/json so it can be persisted and resumed.
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

// New creates a developer-seeded Plan. All steps start as StatusPending.
// Use this when you know the procedure upfront. For agent-authored plans,
// use [Empty] and let the agent populate steps via the create_plan tool.
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

// Empty creates a Plan with no steps, for agent self-planning. The agent
// populates Name, Goal, and Steps via the create_plan tool registered by
// [Enable]. Use this when you want the LLM to reason about the goal and
// propose its own procedure.
func Empty() *Plan {
	return &Plan{
		Notes: make(map[string]any),
	}
}

// IsComplete reports whether every step is done or skipped. Returns false
// for an empty plan (no steps defined yet).
func (p *Plan) IsComplete() bool {
	if len(p.Steps) == 0 {
		return false
	}
	for _, s := range p.Steps {
		if s.Status != StatusDone && s.Status != StatusSkipped {
			return false
		}
	}
	return true
}
