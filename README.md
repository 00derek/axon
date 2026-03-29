# Axon

A minimal, composable framework for building AI agents in Go.

Axon gives you a tiny kernel (~6 types, stdlib-only) for building agents with typed tools,
composable middleware, workflow orchestration, and first-class testing — without framework bloat.

## Quick Start

```go
package main

import (
	"context"
	"fmt"

	"github.com/axonframework/axon/kernel"
	"github.com/axonframework/axon/providers/google"
	"google.golang.org/genai"
)

func main() {
	client, _ := genai.NewClient(context.Background(), nil)
	llm := google.New(client, "gemini-2.0-flash")

	agent := kernel.NewAgent(
		kernel.WithModel(llm),
		kernel.WithSystemPrompt("You are a helpful assistant."),
		kernel.WithTools(
			kernel.NewTool("greet", "Say hello to someone", func(ctx context.Context, p struct {
				Name string `json:"name" description:"Person to greet"`
			}) (string, error) {
				return fmt.Sprintf("Hello, %s!", p.Name), nil
			}),
		),
	)

	result, err := agent.Run(context.Background(), "Say hi to Alice")
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Text)
}
```

## Packages

| Package | Import | Description |
|---------|--------|-------------|
| **kernel** | `github.com/axonframework/axon/kernel` | Agent, Tool, LLM, Message — the core. Zero dependencies. |
| **middleware** | `github.com/axonframework/axon/middleware` | LLM wrappers: retry, logging, timeout, cost tracking, routing, cascade. |
| **workflow** | `github.com/axonframework/axon/workflow` | Compose agents and functions: sequential, parallel, routing, retry loops. |
| **testing** | `github.com/axonframework/axon/testing` | MockLLM, assertion helpers, ScoreCard evaluation, batch testing. |
| **interfaces** | `github.com/axonframework/axon/interfaces` | HistoryStore, MemoryStore, Guard contracts + in-memory implementations. |
| **providers/google** | `github.com/axonframework/axon/providers/google` | Google Gemini LLM adapter. |
| **contrib/plan** | `github.com/axonframework/axon/contrib/plan` | Multi-step procedure tracking for complex agent flows. |
| **contrib/mongo** | `github.com/axonframework/axon/contrib/mongo` | MongoDB-backed HistoryStore and MemoryStore. |

Each package is a separate Go module. Import only what you need — `kernel/` has zero external dependencies.

## Design Principles

1. **Kernel is tiny.** ~6 types, ~10 files. Portable, auditable, no magic.
2. **Everything else is optional.** Use `kernel/` alone or add packages as needed.
3. **No framework abstractions where plain functions work.** No StateSetter, no Component, no Scheduler.
4. **Typed where it matters.** Tool params are schema-validated via generics. No manual JSON parsing.
5. **One repo, separate Go modules.** Importing Axon doesn't pull in every provider SDK.

## Progressive Complexity

| Level | Concepts |
|-------|----------|
| Minimal | Agent, Tool, LLM, Message (~4 concepts) |
| With hooks | + OnStart, OnFinish, PrepareRound, AgentContext (~12) |
| With orchestration | + Workflow, Step, Parallel, Router (~16) |
| Full framework | + Middleware, HistoryStore, MemoryStore, Guard (~20) |

## Examples

See [`examples/`](examples/) for runnable code:

| Example | What it demonstrates |
|---------|---------------------|
| [01-minimal](examples/01-minimal/) | Bare minimum agent |
| [02-tools](examples/02-tools/) | Typed tool definitions |
| [03-streaming](examples/03-streaming/) | Streaming responses and events |
| [04-middleware](examples/04-middleware/) | Retry, logging, cost tracking, routing |
| [05-workflow](examples/05-workflow/) | Parallel steps, routing, retry loops |
| [06-testing](examples/06-testing/) | MockLLM, assertions, ScoreCard |
| [07-restaurant-bot](examples/07-restaurant-bot/) | Full toy application tying all packages together |
| [08-plan](examples/08-plan/) | Multi-step procedure tracking with contrib/plan |

## Documentation

- [Getting Started](docs/getting-started.md) — installation and first agent
- [Agents Guide](docs/guides/agents.md) — lifecycle, hooks, stop conditions
- [Tools Guide](docs/guides/tools.md) — typed tools, schemas, guided responses
- [Streaming Guide](docs/guides/streaming.md) — Stream vs Run, events
- [Middleware Guide](docs/guides/middleware.md) — retry, logging, routing, cascade
- [Workflow Guide](docs/guides/workflow.md) — orchestration patterns
- [Testing Guide](docs/guides/testing.md) — mocking, assertions, evaluation
- [Interfaces Guide](docs/guides/interfaces.md) — persistence and guards
- [Contrib Guide](docs/guides/contrib.md) — plan tracking and MongoDB storage
