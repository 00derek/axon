// Example 08: Agent Self-Planning
//
// Demonstrates the plan package. An agent receives a goal, drafts its own
// plan via the create_plan tool, executes each step, appends a step it
// didn't anticipate, and finishes.
//
// plan is the agent-authored counterpart to the workflow package. Use plan
// when:
//   - the agent needs to coordinate 5+ steps
//   - you want an auditable record of progress (the Plan struct is inspectable)
//   - you may want to resume across sessions (Plan serializes cleanly)
//
// Skip it for 1–3 round tool-call flows; the LLM handles those naturally.
//
// Two flows are supported by plan.Enable:
//   - Dev-seeded (plan.New(name, goal, steps...)) — you know the procedure upfront.
//   - Agent-seeded (plan.Empty()) — the LLM drafts its own steps; shown here.
//
// This example uses MockLLM, so the "agent's" plan is actually scripted into
// tool-call responses. The mechanics (plan injected into system prompt,
// create_plan populating the plan, mark_step advancing it, append_step
// adding mid-flight work) are identical with a real provider — swap MockLLM
// for a live model and the same flow runs from LLM reasoning.
//
// Run: cd examples && go run ./08-plan/
package main

import (
	"context"
	"fmt"

	"github.com/axonframework/axon/axontest"
	"github.com/axonframework/axon/kernel"
	"github.com/axonframework/axon/plan"
)

func main() {
	// Start empty. The agent will draft its own plan in round 0.
	p := plan.Empty()

	fmt.Println("=== Initial Plan ===")
	fmt.Print(plan.Format(p))

	llm := axontest.NewMockLLM()

	// Round 0: agent reasons about the goal and proposes a plan.
	// With a real LLM this is emitted from the model's own reasoning.
	llm.OnRound(0).RespondWithToolCalls(
		kernel.ToolCall{
			ID:   "c1",
			Name: "create_plan",
			Params: []byte(`{
				"name": "trip-booking",
				"goal": "Help the user book a trip from NYC to Paris",
				"steps": [
					{"name": "gather", "description": "Ask the user about travel dates and budget"},
					{"name": "search", "description": "Search for flights matching preferences"},
					{"name": "present", "description": "Present the top 3 options to the user", "needs_user_input": true}
				]
			}`),
		},
	)

	// Round 1: gather preferences — note them, mark step done.
	llm.OnRound(1).RespondWithToolCalls(
		kernel.ToolCall{ID: "c2", Name: "add_note", Params: []byte(`{"key":"dates","value":"June 15-22, 2025"}`)},
		kernel.ToolCall{ID: "c3", Name: "add_note", Params: []byte(`{"key":"budget","value":"under $300"}`)},
		kernel.ToolCall{ID: "c4", Name: "mark_step", Params: []byte(`{"step_name":"gather","status":"done"}`)},
	)

	// Round 2: search completed. The agent realizes it also needs a confirm
	// step, so it appends one mid-flight.
	llm.OnRound(2).RespondWithToolCalls(
		kernel.ToolCall{ID: "c5", Name: "add_note", Params: []byte(`{"key":"best_flight","value":"StarWings $189, departs 6:15 AM"}`)},
		kernel.ToolCall{ID: "c6", Name: "append_step", Params: []byte(`{"name":"confirm","description":"Confirm the user's selection and finalize booking","needs_user_input":true}`)},
		kernel.ToolCall{ID: "c7", Name: "mark_step", Params: []byte(`{"step_name":"search","status":"done"}`)},
	)

	// Round 3: present options, capture choice, advance.
	llm.OnRound(3).RespondWithToolCalls(
		kernel.ToolCall{ID: "c8", Name: "add_note", Params: []byte(`{"key":"user_choice","value":"StarWings confirmed"}`)},
		kernel.ToolCall{ID: "c9", Name: "mark_step", Params: []byte(`{"step_name":"present","status":"done"}`)},
	)

	// Round 4: finalize the appended step.
	llm.OnRound(4).RespondWithToolCalls(
		kernel.ToolCall{ID: "c10", Name: "mark_step", Params: []byte(`{"step_name":"confirm","status":"done"}`)},
	)

	// Round 5: final summary.
	llm.OnRound(5).RespondWithText(
		"Your trip is booked! StarWings flight NYC → Paris on June 15th, " +
			"departing 6:15 AM, $189. Confirmation code: SW-8472. Have a wonderful trip!")

	// plan.Enable(p) returns []AgentOption that wires in:
	//   - OnStart hook — stashes plan in ctx.State["plan"]; activates first pending step (if any)
	//   - PrepareRound hook — renders the plan into the system prompt every round
	//     along with an enforcement directive urging the LLM to finish
	//   - Four tools — create_plan, append_step, mark_step, add_note
	agent := kernel.NewAgent(append(
		plan.Enable(p),
		kernel.WithModel(llm),
		kernel.WithSystemPrompt("You are a travel booking assistant."),
	)...)

	result, err := agent.Run(context.Background(), "I want to fly from NYC to Paris in mid-June, budget under $300")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Println("\n=== Final Plan (agent-authored) ===")
	fmt.Print(plan.Format(p))

	fmt.Println("\n=== Agent Response ===")
	fmt.Println(result.Text)
	fmt.Printf("\nCompleted in %d rounds. Plan complete: %v\n", len(result.Rounds), p.IsComplete())

	fmt.Println("\n=== Plan Notes ===")
	for k, v := range p.Notes {
		fmt.Printf("  %s: %v\n", k, v)
	}
}
