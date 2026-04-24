// examples/09-anthropic/main.go
//
// Minimal Anthropic (Claude) example.
//
// Sends a single prompt to Claude via the Anthropic Messages API. Requires
// the ANTHROPIC_API_KEY environment variable.
//
// Run with:
//
//	cd examples && ANTHROPIC_API_KEY=... go run ./09-anthropic/
package main

import (
	"context"
	"fmt"
	"log"

	sdk "github.com/anthropics/anthropic-sdk-go"

	"github.com/axonframework/axon/kernel"
	"github.com/axonframework/axon/providers/anthropic"
)

func main() {
	// NewClient reads ANTHROPIC_API_KEY from the environment by default.
	client := sdk.NewClient()

	llm := anthropic.New(&client, sdk.ModelClaudeHaiku4_5,
		anthropic.WithMaxTokens(1024),
	)

	agent := kernel.NewAgent(
		kernel.WithModel(llm),
		kernel.WithSystemPrompt("You are a concise assistant."),
	)

	result, err := agent.Run(context.Background(), "Say hello in one short sentence.")
	if err != nil {
		log.Fatalf("agent.Run failed: %v", err)
	}

	fmt.Println("Claude:", result.Text)
	fmt.Printf("Tokens: in=%d out=%d total=%d\n",
		result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.TotalTokens)
}
