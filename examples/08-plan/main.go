// Example 08: Multi-Step Plans
//
// Demonstrates the contrib/plan package for structured multi-step procedures.
// The agent follows a plan to book a trip, using the auto-injected mark_step
// tool to track progress and add_note to store intermediate data. The plan
// text is automatically injected into the system prompt each round so the LLM
// can see exactly where it is in the procedure.
//
// Use contrib/plan when your agent needs to follow a 5+ step flow with
// auditable progress tracking. For simple 1-3 round tool interactions,
// you don't need it — the LLM handles those naturally.
//
// Run: go run ./examples/08-plan/
package main

import (
	"context"
	"fmt"

	"github.com/axonframework/axon/contrib/plan"
	"github.com/axonframework/axon/kernel"
	axontest "github.com/axonframework/axon/testing"
)

func main() {
	// --- Create the plan ---
	// Define the steps the agent should follow. All start as "pending".
	// Steps with NeedsUserInput hint the LLM to ask before proceeding.
	p := plan.New("trip-booking", "Help the user book a trip from NYC to Paris",
		plan.Step{Name: "gather", Description: "Ask the user about travel dates and budget"},
		plan.Step{Name: "search", Description: "Search for flights matching the user's preferences"},
		plan.Step{Name: "present", Description: "Present the top 3 flight options to the user", NeedsUserInput: true},
		plan.Step{Name: "confirm", Description: "Confirm the user's selection and finalize booking", NeedsUserInput: true},
	)

	// Print the initial plan — all steps pending.
	fmt.Println("=== Initial Plan ===")
	fmt.Print(plan.Format(p))

	// --- Script the MockLLM ---
	// In production, a real LLM reads the plan from the system prompt and
	// decides which step to work on. Here we script the behavior with MockLLM.
	//
	// plan.Attach() gives the agent two tools:
	//   mark_step(step_name, status) — update a step to "done", "skipped", or "active"
	//   add_note(key, value) — store data in the plan's Notes map
	llm := axontest.NewMockLLM()

	// Round 0: Gather preferences — note them down, mark step done.
	// When a step is marked "done", the next pending step auto-activates.
	llm.OnRound(0).RespondWithToolCalls(
		kernel.ToolCall{ID: "c1", Name: "add_note", Params: []byte(`{"key":"dates","value":"June 15-22, 2025"}`)},
		kernel.ToolCall{ID: "c2", Name: "add_note", Params: []byte(`{"key":"budget","value":"under $300"}`)},
		kernel.ToolCall{ID: "c3", Name: "mark_step", Params: []byte(`{"step_name":"gather","status":"done"}`)},
	)

	// Round 1: Search complete — note the best option, mark step done.
	llm.OnRound(1).RespondWithToolCalls(
		kernel.ToolCall{ID: "c4", Name: "add_note", Params: []byte(`{"key":"best_flight","value":"StarWings $189, departs 6:15 AM"}`)},
		kernel.ToolCall{ID: "c5", Name: "mark_step", Params: []byte(`{"step_name":"search","status":"done"}`)},
	)

	// Round 2: Present options to user — note their choice, mark step done.
	llm.OnRound(2).RespondWithToolCalls(
		kernel.ToolCall{ID: "c6", Name: "add_note", Params: []byte(`{"key":"user_choice","value":"StarWings confirmed"}`)},
		kernel.ToolCall{ID: "c7", Name: "mark_step", Params: []byte(`{"step_name":"present","status":"done"}`)},
	)

	// Round 3: Confirm booking — mark final step done.
	llm.OnRound(3).RespondWithToolCalls(
		kernel.ToolCall{ID: "c8", Name: "mark_step", Params: []byte(`{"step_name":"confirm","status":"done"}`)},
	)

	// Round 4: All steps done — final summary.
	llm.OnRound(4).RespondWithText(
		"Your trip is booked! StarWings flight NYC → Paris on June 15th, " +
			"departing 6:15 AM, $189. Confirmation code: SW-8472. Have a wonderful trip!")

	// --- Build the agent ---
	// plan.Attach(p) returns []AgentOption that wire in:
	//   1. OnStart hook — activates the first pending step
	//   2. PrepareRound hook — injects the formatted plan into the system prompt
	//   3. WithTools — registers mark_step and add_note tools
	//
	// Spread the options into NewAgent with append.
	agent := kernel.NewAgent(append(
		plan.Attach(p),
		kernel.WithModel(llm),
		kernel.WithSystemPrompt("You are a travel booking assistant. Follow the plan step by step."),
	)...)

	// --- Run ---
	result, err := agent.Run(context.Background(), "I want to fly from NYC to Paris in mid-June, budget under $300")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	// --- Show results ---
	fmt.Println("\n=== Final Plan ===")
	fmt.Print(plan.Format(p))

	fmt.Println("\n=== Agent Response ===")
	fmt.Println(result.Text)
	fmt.Printf("\nCompleted in %d rounds\n", len(result.Rounds))

	// Notes the agent collected along the way — accessible in Go code.
	fmt.Println("\n=== Plan Notes ===")
	for k, v := range p.Notes {
		fmt.Printf("  %s: %v\n", k, v)
	}
}
