// contrib/plan/attach.go
package plan

import (
	"context"
	"fmt"
	"sync"

	"github.com/axonframework/axon/kernel"
)

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

// Attach returns AgentOptions that wire a Plan into an agent via hooks and tools.
// Spread into NewAgent: kernel.NewAgent(append(baseOpts, plan.Attach(p)...)...)
func Attach(p *Plan) []kernel.AgentOption {
	var mu sync.Mutex
	var basePrompt string

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
		// Register plan tools into the live context rather than at construction
		// time. This prevents kernel.WithTools(userTools) from clobbering plan
		// tools when it appears after plan.Attach in the options slice.
		tc.AgentCtx.AddTools(markStep, addNote)
		activateNextPending(p)
	})

	prepareRound := kernel.PrepareRound(func(rc *kernel.RoundContext) {
		mu.Lock()
		text := Format(p)
		base := basePrompt
		mu.Unlock()

		// Combine base system prompt with plan text, replacing in-place so
		// only a single up-to-date plan message exists in the conversation.
		combined := text
		if base != "" {
			combined = base + "\n\n" + text
		}
		rc.AgentCtx.SetSystemPrompt(combined)
	})

	return []kernel.AgentOption{
		onStart,
		prepareRound,
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
