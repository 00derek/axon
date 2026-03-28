// examples/05-workflow/main.go
//
// Workflow orchestration example for Axon.
//
// This example demonstrates three workflow composition patterns:
//
//	Part 1 — Sequential workflow:
//	  Two steps run one after another. The first step writes a greeting into
//	  state.Data; the second step reads it and appends an enhancement. The
//	  final value is printed to show the full pipeline output.
//
//	Part 2 — Parallel execution:
//	  Three steps run concurrently, each writing a different key into
//	  state.Data (simulating independent data-fetch operations). A fourth
//	  "summarize" step runs after the parallel group completes and reads
//	  all three keys to produce a combined summary.
//
//	Part 3 — Routing:
//	  An initial "classify" step inspects the input and sets
//	  state.Data["intent"] to either "technical" or "general". A Router
//	  dispatches to a different handler step based on that value, showing
//	  which route was taken.
//
// Run with:
//
//	cd examples && go run ./05-workflow/
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/axonframework/axon/workflow"
)

func main() {
	part1Sequential()
	fmt.Println()
	part2Parallel()
	fmt.Println()
	part3Routing("How do I fix a segfault in my Go program?")
	fmt.Println()
	part3Routing("Tell me a fun fact about penguins.")
}

// part1Sequential demonstrates a two-step pipeline where each step reads and
// writes state.Data, passing results forward via the shared state.
func part1Sequential() {
	fmt.Println("=== Part 1: Sequential Workflow ===")

	greet := workflow.Step("greet", func(_ context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
		s.Data["greeting"] = "Hello, " + s.Input + "!"
		return s, nil
	})

	enhance := workflow.Step("enhance", func(_ context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
		base, _ := s.Data["greeting"].(string)
		s.Data["greeting"] = base + " Welcome to the Axon workflow engine."
		return s, nil
	})

	wf := workflow.NewWorkflow(greet, enhance)

	state, err := wf.Run(context.Background(), &workflow.WorkflowState{Input: "World"})
	if err != nil {
		log.Fatalf("sequential workflow failed: %v", err)
	}

	fmt.Println("Result:", state.Data["greeting"])
}

// part2Parallel demonstrates three concurrent fetch steps followed by a
// summarize step. Each fetch writes to a unique key so there are no conflicts
// during the parallel merge.
func part2Parallel() {
	fmt.Println("=== Part 2: Parallel Execution ===")

	fetchUser := workflow.Step("fetch-user", func(_ context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
		s.Data["user"] = "alice (id=42)"
		fmt.Println("  [fetch-user] fetched user data")
		return s, nil
	})

	fetchPrefs := workflow.Step("fetch-prefs", func(_ context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
		s.Data["prefs"] = "theme=dark, notifications=on"
		fmt.Println("  [fetch-prefs] fetched preferences")
		return s, nil
	})

	fetchHistory := workflow.Step("fetch-history", func(_ context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
		s.Data["history"] = "last login: 2026-03-27, sessions: 14"
		fmt.Println("  [fetch-history] fetched history")
		return s, nil
	})

	summarize := workflow.Step("summarize", func(_ context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
		user, _ := s.Data["user"].(string)
		prefs, _ := s.Data["prefs"].(string)
		history, _ := s.Data["history"].(string)
		s.Data["summary"] = fmt.Sprintf("User: %s | Prefs: %s | History: %s", user, prefs, history)
		return s, nil
	})

	wf := workflow.NewWorkflow(
		workflow.Parallel(fetchUser, fetchPrefs, fetchHistory),
		summarize,
	)

	state, err := wf.Run(context.Background(), &workflow.WorkflowState{Input: "dashboard-load"})
	if err != nil {
		log.Fatalf("parallel workflow failed: %v", err)
	}

	fmt.Println("Summary:", state.Data["summary"])
}

// part3Routing demonstrates a Router that dispatches to a different step
// depending on the intent detected in the input.
func part3Routing(input string) {
	fmt.Printf("=== Part 3: Routing (input: %q) ===\n", input)

	classify := workflow.Step("classify", func(_ context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
		lower := strings.ToLower(s.Input)
		technicalKeywords := []string{"segfault", "debug", "error", "crash", "memory", "leak", "compile", "build", "fix", "bug"}
		isTechnical := false
		for _, kw := range technicalKeywords {
			if strings.Contains(lower, kw) {
				isTechnical = true
				break
			}
		}
		if isTechnical {
			s.Data["intent"] = "technical"
		} else {
			s.Data["intent"] = "general"
		}
		return s, nil
	})

	technicalHandler := workflow.Step("technical-handler", func(_ context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
		s.Data["response"] = "[technical-agent] Routing to specialized debugging assistant for: " + s.Input
		return s, nil
	})

	generalHandler := workflow.Step("general-handler", func(_ context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
		s.Data["response"] = "[general-agent] Routing to general knowledge assistant for: " + s.Input
		return s, nil
	})

	router := workflow.Router(
		func(_ context.Context, s *workflow.WorkflowState) string {
			intent, _ := s.Data["intent"].(string)
			return intent
		},
		map[string]workflow.WorkflowStep{
			"technical": technicalHandler,
			"general":   generalHandler,
		},
	)

	wf := workflow.NewWorkflow(classify, router)

	state, err := wf.Run(context.Background(), &workflow.WorkflowState{Input: input})
	if err != nil {
		log.Fatalf("routing workflow failed: %v", err)
	}

	fmt.Printf("Intent:   %s\n", state.Data["intent"])
	fmt.Printf("Response: %s\n", state.Data["response"])
}
