package plan

import (
	"context"
	"fmt"
	"sync"

	"github.com/axonframework/axon/kernel"
)

// StateKey is the key under which Enable stashes the active Plan into
// AgentContext.State. External code that wants to inspect or persist
// the plan can read ctx.State[plan.StateKey].
const StateKey = "plan"

// enforcementDirective is appended to the system prompt every round,
// directing the LLM to finish the plan before ending.
const enforcementDirective = "Follow the plan. Call create_plan first if no plan exists, then use mark_step to advance as you finish each step. Do not end the conversation until every step is done or skipped."

// createPlanParams is the typed parameter struct for the create_plan tool.
type createPlanParams struct {
	Name  string      `json:"name" description:"Short identifier for the plan"`
	Goal  string      `json:"goal" description:"What the plan is trying to achieve"`
	Steps []stepInput `json:"steps" description:"Ordered list of steps to execute"`
}

// stepInput is the typed step struct accepted by create_plan and append_step.
type stepInput struct {
	Name           string `json:"name" description:"Short step identifier (e.g. 'gather_preferences')"`
	Description    string `json:"description" description:"What this step does"`
	NeedsUserInput bool   `json:"needs_user_input,omitempty" description:"True if this step requires asking the user"`
	CanRepeat      bool   `json:"can_repeat,omitempty" description:"True if this step may run multiple times"`
}

// appendStepParams is the typed parameter struct for the append_step tool.
type appendStepParams struct {
	Name           string `json:"name" description:"Short step identifier"`
	Description    string `json:"description" description:"What this step does"`
	NeedsUserInput bool   `json:"needs_user_input,omitempty"`
	CanRepeat      bool   `json:"can_repeat,omitempty"`
}

// markStepParams is the typed parameter struct for the mark_step tool.
type markStepParams struct {
	StepName string `json:"step_name" description:"Name of the step to update"`
	Status   string `json:"status" description:"New status for the step" enum:"done,skipped,active"`
}

// addNoteParams is the typed parameter struct for the add_note tool.
type addNoteParams struct {
	Key   string `json:"key" description:"Note key"`
	Value string `json:"value" description:"Note value"`
}

// Enable returns AgentOptions that give an agent the planning capability.
// Spread into NewAgent: kernel.NewAgent(append(baseOpts, plan.Enable(p)...)...)
//
// The attached plan is stashed into AgentContext.State[plan.StateKey] on
// start. The agent gains four tools:
//
//   - create_plan(name, goal, steps[]) — populate an empty plan's steps
//   - append_step(name, description, ...) — add a step mid-flight
//   - mark_step(step_name, status) — advance a step (auto-activates next pending)
//   - add_note(key, value) — record intermediate data
//
// Two flows are supported:
//
//   - Dev-seeded: pass plan.New(name, goal, steps...) when you know the
//     procedure upfront.
//   - Agent-seeded: pass plan.Empty() and let the LLM call create_plan.
//
// Enforcement is prompt-based: a directive is injected every round urging
// the LLM to finish all steps. If the LLM stops early anyway,
// plan.IsComplete returns false and the caller can decide what to do.
func Enable(p *Plan) []kernel.AgentOption {
	var mu sync.Mutex
	var basePrompt string

	createPlan := kernel.NewTool("create_plan",
		"Propose the plan. Provide a short name, the user's goal, and an ordered list of steps you will take. Call this only once, at the start, when no plan exists yet. Errors if the plan already has steps.",
		func(ctx context.Context, params createPlanParams) (string, error) {
			mu.Lock()
			defer mu.Unlock()

			if len(p.Steps) > 0 {
				return "", fmt.Errorf("plan already has %d steps; use append_step to add more", len(p.Steps))
			}
			if len(params.Steps) == 0 {
				return "", fmt.Errorf("create_plan requires at least one step")
			}

			p.Name = params.Name
			p.Goal = params.Goal
			p.Steps = make([]Step, len(params.Steps))
			for i, s := range params.Steps {
				p.Steps[i] = Step{
					Name:           s.Name,
					Description:    s.Description,
					Status:         StatusPending,
					NeedsUserInput: s.NeedsUserInput,
					CanRepeat:      s.CanRepeat,
				}
			}
			first := activateNextPending(p)
			if first != "" {
				return fmt.Sprintf("Plan created with %d steps. Active: '%s'.", len(p.Steps), first), nil
			}
			return fmt.Sprintf("Plan created with %d steps.", len(p.Steps)), nil
		},
	)

	appendStep := kernel.NewTool("append_step",
		"Append a new pending step to the end of the plan. Use this when you discover work you didn't anticipate. The step starts as pending; it does not auto-activate.",
		func(ctx context.Context, params appendStepParams) (string, error) {
			mu.Lock()
			defer mu.Unlock()

			if params.Name == "" {
				return "", fmt.Errorf("step name is required")
			}
			for _, s := range p.Steps {
				if s.Name == params.Name {
					return "", fmt.Errorf("step %q already exists", params.Name)
				}
			}

			p.Steps = append(p.Steps, Step{
				Name:           params.Name,
				Description:    params.Description,
				Status:         StatusPending,
				NeedsUserInput: params.NeedsUserInput,
				CanRepeat:      params.CanRepeat,
			})
			return fmt.Sprintf("Step '%s' appended (pending). Plan now has %d steps.", params.Name, len(p.Steps)), nil
		},
	)

	markStep := kernel.NewTool("mark_step",
		"Update a plan step's status. When a step is marked done or skipped, the next pending step is automatically activated.",
		func(ctx context.Context, params markStepParams) (string, error) {
			mu.Lock()
			defer mu.Unlock()

			status := StepStatus(params.Status)

			// Find the step by name.
			idx := -1
			for i, s := range p.Steps {
				if s.Name == params.StepName {
					idx = i
					break
				}
			}
			if idx == -1 {
				return "", fmt.Errorf("step %q not found in plan %q", params.StepName, p.Name)
			}

			p.Steps[idx].Status = status

			// Auto-advance: if done or skipped, activate the next pending step.
			if status == StatusDone || status == StatusSkipped {
				next := activateNextPending(p)
				if next != "" {
					return fmt.Sprintf("Step '%s' marked as %s. Next: '%s'", params.StepName, status, next), nil
				}
				return fmt.Sprintf("Step '%s' marked as %s. All steps complete.", params.StepName, status), nil
			}

			return fmt.Sprintf("Step '%s' marked as %s.", params.StepName, status), nil
		},
	)

	addNote := kernel.NewTool("add_note",
		"Store a key-value note in the plan for reference in later steps.",
		func(ctx context.Context, params addNoteParams) (string, error) {
			mu.Lock()
			defer mu.Unlock()

			p.Notes[params.Key] = params.Value
			return fmt.Sprintf("Note added: %s = %s", params.Key, params.Value), nil
		},
	)

	onStart := kernel.OnStart(func(tc *kernel.TurnContext) {
		mu.Lock()
		defer mu.Unlock()
		// Capture the base system prompt so PrepareRound can preserve it.
		basePrompt = tc.AgentCtx.SystemPrompt()
		// Stash plan in state so external code can inspect/persist it.
		if tc.AgentCtx.State == nil {
			tc.AgentCtx.State = make(map[string]any)
		}
		tc.AgentCtx.State[StateKey] = p
		// If the plan is already seeded, activate the first pending step.
		activateNextPending(p)
	})

	prepareRound := kernel.PrepareRound(func(rc *kernel.RoundContext) {
		mu.Lock()
		text := Format(p)
		base := basePrompt
		mu.Unlock()

		// Compose: base prompt (if any) + plan text + enforcement directive.
		// Written in-place so only a single up-to-date system message exists.
		combined := text + "\n" + enforcementDirective
		if base != "" {
			combined = base + "\n\n" + combined
		}
		rc.AgentCtx.SetSystemPrompt(combined)
	})

	return []kernel.AgentOption{
		onStart,
		prepareRound,
		kernel.WithTools(createPlan, appendStep, markStep, addNote),
	}
}

// activateNextPending finds the first pending step and sets it to active.
// Returns the name of the activated step, or empty string if none found.
// Caller must hold the mutex.
func activateNextPending(p *Plan) string {
	for i, s := range p.Steps {
		if s.Status == StatusPending {
			p.Steps[i].Status = StatusActive
			return s.Name
		}
	}
	return ""
}
