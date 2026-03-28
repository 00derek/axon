// examples/02-tools/main.go
//
// Typed tool example for Axon agents.
//
// This example demonstrates how to define tools with typed parameter structs
// using the kernel.NewTool generic constructor:
//   - Three tools: add, multiply, and uppercase
//   - Each tool uses a typed param struct with json and description struct tags
//   - A MockLLM scripted to call "add" in round 0, then return a text answer in round 1
//   - The agent is configured with all three tools via WithTools
//   - After running, the example prints the final response and a full tool call trace
//
// Run with:
//
//	cd examples && go run ./02-tools/
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/axonframework/axon/kernel"
	axontest "github.com/axonframework/axon/testing"
)

// AddParams holds parameters for the "add" tool.
type AddParams struct {
	A float64 `json:"a" description:"First number"`
	B float64 `json:"b" description:"Second number"`
}

// MultiplyParams holds parameters for the "multiply" tool.
type MultiplyParams struct {
	A float64 `json:"a" description:"First number"`
	B float64 `json:"b" description:"Second number"`
}

// UppercaseParams holds parameters for the "uppercase" tool.
type UppercaseParams struct {
	Text string `json:"text" description:"Text to convert to uppercase"`
}

func main() {
	// Define three tools using kernel.NewTool with typed parameter structs.

	addTool := kernel.NewTool[AddParams, float64](
		"add",
		"Add two numbers together and return their sum.",
		func(ctx context.Context, p AddParams) (float64, error) {
			return p.A + p.B, nil
		},
	)

	multiplyTool := kernel.NewTool[MultiplyParams, float64](
		"multiply",
		"Multiply two numbers together and return their product.",
		func(ctx context.Context, p MultiplyParams) (float64, error) {
			return p.A * p.B, nil
		},
	)

	uppercaseTool := kernel.NewTool[UppercaseParams, string](
		"uppercase",
		"Convert a string to uppercase.",
		func(ctx context.Context, p UppercaseParams) (string, error) {
			return strings.ToUpper(p.Text), nil
		},
	)

	// Script the mock LLM:
	//   Round 0 → call "add" with {a: 12, b: 30}
	//   Round 1 → return the final text answer
	llm := axontest.NewMockLLM().
		OnRound(0).RespondWithToolCall("add", map[string]any{"a": 12, "b": 30}).
		OnRound(1).RespondWithText("12 + 30 = 42")

	// Build the agent with all three tools.
	agent := kernel.NewAgent(
		kernel.WithModel(llm),
		kernel.WithSystemPrompt("You are a helpful math assistant with access to calculation tools."),
		kernel.WithTools(addTool, multiplyTool, uppercaseTool),
	)

	// Run the agent.
	result, err := agent.Run(context.Background(), "What is 12 + 30?")
	if err != nil {
		log.Fatalf("agent.Run failed: %v", err)
	}

	// Print the final text response.
	fmt.Println("Agent response:", result.Text)
	fmt.Printf("Rounds completed: %d\n\n", len(result.Rounds))

	// Print the tool call trace from result.Rounds.
	fmt.Println("Tool call trace:")
	for i, round := range result.Rounds {
		if len(round.ToolCalls) == 0 {
			fmt.Printf("  Round %d: (no tool calls — text response)\n", i)
			continue
		}
		for _, tc := range round.ToolCalls {
			// Pretty-print the params for readability.
			var prettyParams any
			_ = json.Unmarshal(tc.Params, &prettyParams)
			paramsJSON, _ := json.Marshal(prettyParams)

			if tc.Error != nil {
				fmt.Printf("  Round %d: tool=%q params=%s error=%v\n",
					i, tc.Name, paramsJSON, tc.Error)
			} else {
				resultJSON, _ := json.Marshal(tc.Result)
				fmt.Printf("  Round %d: tool=%q params=%s result=%s\n",
					i, tc.Name, paramsJSON, resultJSON)
			}
		}
	}
}
