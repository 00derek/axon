# Testing Agents

Axon provides the `axontest` package (`github.com/axonframework/axon/testing`) with utilities for unit testing agents without making real LLM calls. The package covers scripted mock responses, a test runner, chainable assertions, and LLM-as-judge evaluation.

## MockLLM

`MockLLM` implements `kernel.LLM` with per-round scripted responses. Construct one with `NewMockLLM()`, then use `OnRound(n)` to define what the LLM returns on each call. Rounds are 0-indexed and correspond to sequential `Generate` calls during a single agent run.

```go
llm := axontest.NewMockLLM().
    OnRound(0).RespondWithToolCall("search", map[string]any{"query": "hotels in Paris"}).
    OnRound(1).RespondWithText("I found several hotels in Paris for you.")
```

`OnRound` returns a `*MockResponseBuilder` with four terminal methods, each returning the parent `*MockLLM` for fluent chaining:

| Method | Description |
|---|---|
| `RespondWithText(text string)` | Returns a text response with `FinishReason: "stop"` |
| `RespondWithToolCall(name string, params map[string]any)` | Returns a single tool call; params are JSON-marshaled |
| `RespondWithToolCalls(calls ...kernel.ToolCall)` | Returns multiple tool calls in one round |
| `RespondWithError(err error)` | Returns an error from `Generate` |

If a round has no configured response, `MockLLM` returns an empty stop response rather than failing. This means you only need to configure rounds where specific behavior matters.

`MockLLM` does not support streaming — calling `GenerateStream` returns an error.

## Test Runner

`axontest.Run` executes an agent with a given input and returns a `*TestResult` for assertion chaining. Any agent execution error calls `t.Fatal`.

```go
result := axontest.Run(t, agent, "Find me hotels in Paris")
```

### RunOption

Pass options after the input string to configure the test environment:

**`WithHistory(msgs ...kernel.Message)`** — Prepends conversation history before the user input. History messages are inserted after any system prompt, preserving correct message ordering.

```go
history := []kernel.Message{
    kernel.UserMsg("I'm looking for a hotel in Paris for next week."),
    kernel.AssistantMsg("I'd be happy to help you find a hotel in Paris!"),
}

result := axontest.Run(t, agent, "Which one do you recommend?",
    axontest.WithHistory(history...),
)
```

**`MockTool(name string, response any)`** — Replaces a registered tool's execution with a fixed canned response. The tool must already be registered on the agent; `MockTool` intercepts execution while preserving the tool's schema so the LLM still sees it.

```go
result := axontest.Run(t, agent, "What is the weather?",
    axontest.MockTool("get_weather", "72°F and sunny"),
)
```

**`WithMockLLM(llm kernel.LLM)`** — Overrides the agent's LLM for the duration of the test run.

```go
result := axontest.Run(t, agent, "hello",
    axontest.WithMockLLM(axontest.NewMockLLM().OnRound(0).RespondWithText("hi")),
)
```

`Run` creates a cloned agent with the overrides applied, leaving the original agent unchanged.

### TestResult helpers

`*TestResult` exposes two plain accessors before any assertions:

- `result.Text()` — the final response text
- `result.RoundCount()` — the number of agent rounds executed

## Tool Assertions

`result.ExpectTool(name)` begins a `*ToolAssertion` chain for a named tool. Assertions scan tool calls across all rounds.

```go
result.ExpectTool("search").Called(t)
result.ExpectTool("book").NotCalled(t)
result.ExpectTool("search").CalledTimes(t, 1)
```

### Assertion methods

| Method | Description |
|---|---|
| `Called(t)` | Fails if the tool was never called |
| `NotCalled(t)` | Fails if the tool was called at all |
| `CalledTimes(t, n)` | Fails if call count does not equal `n` |

### Parameter filtering

`WithParam` and `WithParamMatch` add filters before the terminal assertion. All filters must match for a call to count.

**`WithParam(key string, value any)`** — Narrows the assertion to calls where the parameter `key` equals `value`. Comparison uses JSON serialization, so numeric types must match exactly.

```go
result.ExpectTool("book").
    WithParam("item", "Acme Hotel").
    WithParam("date", "2025-06-15").
    Called(t)
```

**`WithParamMatch(key string, judge kernel.LLM, criteria string)`** — Uses a judge LLM to evaluate whether the parameter value satisfies free-form criteria. The judge receives a prompt and must respond with `{"reasoning":"...","condition_met":true/false}`.

```go
result.ExpectTool("search").
    WithParamMatch("query", judgeLLM, "query is related to Italian cuisine").
    Called(t)
```

Both methods return `*ToolAssertion` for chaining. The terminal assertion (`Called`, `NotCalled`, `CalledTimes`) must come last.

## Response Assertions

`result.ExpectResponse()` returns a `*ResponseAssertion` for the final text response.

```go
result.ExpectResponse().Contains(t, "confirmed")
result.ExpectResponse().NotContains(t, "error")
```

**`Contains(t, substring)`** — Fails if the response text does not contain `substring`.

**`NotContains(t, substring)`** — Fails if the response text contains `substring`.

**`Satisfies(t, judge kernel.LLM, criteria string)`** — Evaluates the response using a judge LLM. The judge must respond with `{"reasoning":"...","condition_met":true/false}`. When the condition is not met, the test error includes the judge's reasoning.

```go
result.ExpectResponse().Satisfies(t, judgeLLM, "response is polite and mentions the booking details")
```

## Structural Assertions

**`result.ExpectRounds(t, n)`** — Asserts the agent executed exactly `n` rounds. A round corresponds to one `Generate` call; a typical tool-call sequence is two rounds (one for the tool call, one for the final text response).

```go
// search → final text = 2 rounds
result.ExpectRounds(t, 2)

// search → book → final text = 3 rounds
result.ExpectRounds(t, 3)
```

## ScoreCard (LLM-as-Judge)

`ScoreCard` evaluates a full conversation against a set of weighted criteria using a judge LLM. It is appropriate for measuring response quality when simple string matching is insufficient.

```go
sc := axontest.ScoreCard{
    Criteria: []axontest.Criterion{
        {Condition: "The assistant confirms the reservation", Score: 30},
        {Condition: "The assistant mentions the hotel name", Score: 20},
        {Condition: "The assistant mentions the check-in date", Score: 20},
        {Condition: "The response is polite and professional", Score: 30},
    },
    PassingScore: 70,
}
```

**`Criterion`** fields:
- `Condition string` — a human-readable description of what to check
- `Score int` — points awarded when the condition is met

**`ScoreCard`** fields:
- `Criteria []Criterion` — the evaluation rubric
- `PassingScore int` — minimum total score to pass

### Evaluate

```go
messages := []kernel.Message{
    kernel.UserMsg("Book me a room at the Grand Inn for June 15th."),
    kernel.AssistantMsg("Your reservation at the Grand Inn on 2025-06-15 is confirmed!"),
}

scoreResult, err := sc.Evaluate(ctx, judgeLLM, messages)
if err != nil {
    t.Fatal(err)
}
if !scoreResult.Passed {
    t.Errorf("scored %d/%d", scoreResult.TotalScore, scoreResult.MaxScore)
}
```

`Evaluate` sends a single prompt to the judge with all criteria and the full conversation. The judge is instructed to provide step-by-step reasoning before each verdict — this ordering reduces evaluation errors on ambiguous criteria.

### ScoreResult

| Field | Type | Description |
|---|---|---|
| `TotalScore` | `int` | Sum of scores for all met criteria |
| `MaxScore` | `int` | Sum of all criteria scores |
| `Passed` | `bool` | `TotalScore >= PassingScore` |
| `Details` | `[]CriterionResult` | Per-criterion results with `Met`, `Score`, and `Reasoning` |

## Batch Evaluation

`axontest.Eval` runs a slice of `Case` values as Go subtests, each under `t.Run`. It is the recommended entry point for evaluation suites.

```go
axontest.Eval(t, agent, judgeLLM, []axontest.Case{
    {
        Name:  "basic search",
        Input: "Find Italian restaurants downtown",
        Expect: &axontest.Expectation{
            ToolCalled:       []string{"search_restaurants"},
            ResponseContains: []string{"Bella Trattoria"},
        },
    },
    {
        Name:  "reservation flow",
        Input: "Book a table for 2 at 7 PM",
        Expect: &axontest.Expectation{
            ToolCalled: []string{"make_reservation"},
            Rounds:     intPtr(2),
        },
    },
})
```

### Case

| Field | Type | Description |
|---|---|---|
| `Name` | `string` | Subtest name; falls back to `Input` when empty |
| `Input` | `string` | User message sent to the agent |
| `History` | `[]kernel.Message` | Optional prior conversation (equivalent to `WithHistory`) |
| `Expect` | `*Expectation` | Assertions to run; nil means run without assertions |

### Expectation

All fields are optional. Only populated fields are checked.

| Field | Type | Description |
|---|---|---|
| `ResponseContains` | `[]string` | Each substring must appear in the response |
| `ResponseNotContains` | `[]string` | None of these substrings may appear |
| `ToolCalled` | `[]string` | Each named tool must have been called at least once |
| `ToolNotCalled` | `[]string` | None of these tools may have been called |
| `Rounds` | `*int` | Exact round count (use a pointer literal or helper) |
| `ScoreCard` | `*ScoreCard` | Run scorecard evaluation; requires a non-nil `judge` |

When `judge` is `nil` and a case has a `ScoreCard`, the scorecard evaluation is silently skipped. This lets you run fast structural tests without a live LLM.

## Test Organization

Structure tests using standard Go patterns. Keep unit tests (MockLLM only) and evaluation tests (live judge LLM) in separate functions so the `-short` flag can skip expensive calls.

```go
func TestSearchToolIsCalled(t *testing.T) {
    // Fast: MockLLM, no network calls
    llm := axontest.NewMockLLM().
        OnRound(0).RespondWithToolCall("search", map[string]any{"query": "hotels in Paris"}).
        OnRound(1).RespondWithText("I found several hotels in Paris for you.")

    agent := newTestAgent(llm)
    result := axontest.Run(t, agent, "Find me hotels in Paris")

    result.ExpectTool("search").WithParam("query", "hotels in Paris").Called(t)
    result.ExpectResponse().Contains(t, "hotels")
    result.ExpectRounds(t, 2)
}

func TestResponseQuality(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping live LLM judge evaluation in short mode")
    }

    // Slower: live judge LLM required
    sc := axontest.ScoreCard{
        Criteria: []axontest.Criterion{
            {Condition: "Response names at least one hotel", Score: 50},
            {Condition: "Response is helpful and informative", Score: 50},
        },
        PassingScore: 80,
    }
    // ... run agent, call sc.Evaluate
}
```

Use `-run` to target specific tests during development:

```sh
# Run all tests in a package
go test ./...

# Run only tests matching a pattern
go test -run TestSearch ./...

# Skip expensive evaluation tests
go test -short ./...

# Run a specific subtest from Eval
go test -run TestEval/basic_search ./...
```

For examples of these patterns applied to a complete agent, see:
- `examples/06-testing/agent_test.go` — MockLLM, tool assertions, response assertions, `WithHistory`, and `ScoreCard` construction
- `examples/07-restaurant-bot/agent_test.go` — multi-tool sequence tests, `WithHistory` for multi-turn context, guard/middleware testing with `Eval`
