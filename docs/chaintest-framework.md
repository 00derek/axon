# Chain/Agent Test Framework

Comprehensive reference for the testing architecture used in the Lynx codebase,
covering the chaintest framework, evalchain scenario tests, and supporting
patterns.

---

## 1. Overview & Philosophy

Lynx testing follows a **four-layer** architecture, each layer building on the
one below it:

```
+------------------------------------------------------------+
|  Layer 4: Scenario Tests   (svc/vehicle/evalchain/)        |
|  150+ domain-specific test cases, tag-filtered, LLM-judged |
+------------------------------------------------------------+
|  Layer 3: Chain Tests      (lib/chain/chaintest/)           |
|  Tool mocking, parameter validation, ScoreCard evaluation   |
+------------------------------------------------------------+
|  Layer 2: Integration Tests                                 |
|  Memory store shared suites, concurrency stress tests       |
+------------------------------------------------------------+
|  Layer 1: Unit Tests       (lynx/, lib/, svc/)             |
|  Standard Go testing + testify, table-driven, fakeLLM       |
+------------------------------------------------------------+
```

### Build Tags

Build tags gate test categories so they cannot be accidentally executed during
a normal `go test ./...` run:

| Tag              | Purpose                                             |
|------------------|-----------------------------------------------------|
| `//go:build unit`          | Pure unit tests (no network, no LLM)      |
| `//go:build evalchain`     | Chain and scenario tests (requires LLM API keys) |
| `//go:build evalembedded`  | Embedded/on-device chain tests            |
| `//go:build evalllm`       | LLM-specific integration tests            |

The chaintest framework files use `//go:build evalchain || evalembedded` so the
same test infrastructure supports both cloud-hosted and on-device evaluation
paths.

### Key Principles

- **Scenario-based**: Every test starts with a human utterance and validates
  the full chain response (tool selection, parameters, natural-language reply).
- **Tag-filtered**: Fine-grained tag taxonomy (domain, turn type, objective)
  enables running a precise subset of tests.
- **LLM-as-judge**: ScoreCard evaluation uses a second LLM to assess
  natural-language response quality against structured criteria.
- **Tool mocking**: Tool calls are intercepted at runtime; responses can be
  injected without calling external services.

---

## 2. Testing Layers

### Layer 1: Unit Tests

Standard Go testing with `testify/assert` and `testify/require`. Located
throughout the codebase.

Representative files:

| File | Pattern |
|------|---------|
| `lib/chain/chaintest/subset_test.go` | Table-driven tests for `isSubset()` and `matchLeaves()` |
| `svc/vehicle/agent/vehicle_tools_test.go` | Tool registration validation |
| `lib/lynxlog/logger_test.go` | Logger behavior tests |
| `svc/vehicle/functions/vehicle_controls/media/*_test.go` | Media playback unit tests |

Common patterns:
- **Table-driven** tests with sub-test names via `t.Run()`
- **testify/assert** for soft assertions, **testify/require** for hard stops
- `//go:build unit` tag on pure-logic tests

### Layer 2: Integration Tests

Integration tests cover subsystems that depend on external state stores or
APIs. The memory store test suite defines shared tests once, and each backend
re-uses them.

Examples:
- `lib/oauth2/bff/oauth_integration_test.go` -- OAuth BFF integration
- `lib/facts/test/sanitize_test.go` -- Facts pipeline with `//go:embed` test data
- `svc/vehicle/functions/phone/contactsearch/address_book_test.go` -- Address book search

### Layer 3: Chain Tests

The **chaintest** framework at `lib/chain/chaintest/` provides the
infrastructure to test a complete Lynx chain: from user input, through
classification and tool calling, to final response validation. Detailed in
Section 3.

### Layer 4: Scenario Tests

The **evalchain** test suite at `svc/vehicle/evalchain/` contains 150+ test
cases organized by domain. Each test is a `chaintest.TestCase` with tags,
expected tools, and ScoreCard criteria. Detailed in Section 4.

---

## 3. Chaintest Framework Deep Dive

**Location**: `lib/chain/chaintest/`

```
lib/chain/chaintest/
  chaintest.go           -- TestCoordinator, Chaintest(), setupTest(), startAsyncToolTesting()
  testcase.go            -- TestCase[T], ToolTestCriteria, testErr[T]
  config.go              -- TestConfig, GetChainTestConfig()
  mock_tools.go          -- toolMocker
  mocked_tool_struct.go  -- MockedTool, toolArgs
  executor_builder.go    -- TestExecutor, TestExecutorBuilder[T], UnifiedTestRunner[T]
  scoring.go             -- getMessages() helper
  filter.go              -- FilterTestCases()
  utils.go               -- isSubset(), matchLeaves(), normalize(), CreateInteractions()
  subset_test.go         -- Unit tests for subset/matching logic
```

### Core Types

#### TestCase[T]

The generic test case definition. `T` is the client state type (e.g.,
`rivian_va_message_pb.RivianVAMessage` for vehicle tests).

```go
// lib/chain/chaintest/testcase.go

type TestCase[T any] struct {
    Name                 string
    Tags                 []string             `json:"tags,omitempty"`
    FullHistory          []memory.Interaction `json:"full_history,omitempty"`
    History              []lynx.Message       `json:"history,omitempty"`
    OverrideTools        []string
    PreviousRoute        string                      `json:"previous_route,omitempty"`
    Input                string                      `json:"input"`
    ExpectedTools        map[string]ToolTestCriteria `json:"expected_tools,omitempty"`
    AllowUnexpectedTools bool                        `json:"allow_unexpected_tools,omitempty"`
    ScoreCard            *structure.ScoreCard        `json:"score_card,omitempty"`
    ClientState          *T                          `json:"client_state"`

    // ContextValidator allows service-specific tests to validate setup state
    // captured in context without coupling chaintest to service-specific types.
    ContextValidator func(context.Context) error `json:"-"`
}
```

Fields:

| Field | Purpose |
|-------|---------|
| `Name` | Human-readable test case name |
| `Tags` | Tag strings for filtering (see Section 4) |
| `FullHistory` | Pre-built `memory.Interaction` history (mutually exclusive with `History`) |
| `History` | Flat `lynx.Message` history, auto-converted to interactions |
| `OverrideTools` | Tool names to force-enable via a frame push |
| `Input` | The user utterance under test |
| `ExpectedTools` | Map of tool name to validation criteria |
| `AllowUnexpectedTools` | If true, tools not in `ExpectedTools` do not cause failure |
| `ScoreCard` | LLM-as-judge criteria for the final response |
| `ClientState` | Service-specific client state (generic type `T`) |
| `ContextValidator` | Optional hook to validate context after setup |

#### ToolTestCriteria

Defines how to validate and mock a single expected tool call.

```go
// lib/chain/chaintest/testcase.go

type ToolTestCriteria struct {
    MockedResponse       *lynx.ToolCallResponse `json:"injected_response,omitempty"`
    CallRealTool         bool                   `json:"call_real_tool,omitempty"`
    ExactParams          map[string]interface{} `json:"exact_params,omitempty"`
    JudgeParams          map[string]interface{} `json:"judge_params,omitempty"`
    Optional             bool                   `json:"optional"`
    ExactIsCaseSensitive bool                   `json:"case_sensitive"`
}
```

| Field | Purpose |
|-------|---------|
| `MockedResponse` | Injected response instead of calling the real tool |
| `CallRealTool` | If true, the real tool is called even when mocking is active |
| `ExactParams` | Key-value pairs checked via `isSubset()` (deterministic match) |
| `JudgeParams` | Key-value pairs where values are judge prompts for fuzzy LLM evaluation |
| `Optional` | If true, the tool not being called is not a failure |
| `ExactIsCaseSensitive` | Controls case sensitivity for `ExactParams` string comparisons |

#### TestCoordinator

Orchestrates running multiple test cases across one or more setup
configurations.

```go
// lib/chain/chaintest/chaintest.go

type TestCoordinator[T any, U socketio.TestRunner] struct {
    RunOnceSetup   TestOnceSetup
    TestcaseSetups []TestcaseSetup[T, U]
}
```

Supporting interfaces:

```go
type TestOnceSetup interface {
    GetModels(context.Context) (chainModel, judgeModel lynx.LLM, err error)
}

type TestcaseSetup[T any, U socketio.Runner] interface {
    GetSmith(ctx context.Context, i int, testCase TestCase[T]) socketio.RunnerMaker[U]
    GetMessage(logger lynxlog.LynxLogger, i int, testCase TestCase[T]) interface{}
    memory.MemoryStoreLifecycler
}
```

- `TestOnceSetup.GetModels()` initializes the chain LLM and the judge LLM once.
- `TestcaseSetup.GetSmith()` builds the chain "smith" (factory) for each test.
- `TestcaseSetup.GetMessage()` constructs the service-specific input message.
- `memory.MemoryStoreLifecycler` provides `GetMemoryStore() (MemoryStore, func(), error)`.

#### TestConfig

Controls execution behavior. Loaded from the `CHAINTEST_CONFIG` environment
variable or defaults.

```go
// lib/chain/chaintest/config.go

type TestConfig struct {
    InParallel         bool `json:"in_parallel"`
    StopAfterNRuns     int  `json:"stop_after_n_runs"`
    StopAfterNFailures int  `json:"stop_after_n_failures"`
    SuppressRealTool   bool `json:"suppress_real_tool"`
}
```

Default values (when no env var is set and no fallback is provided):
- `StopAfterNRuns: 1`
- `StopAfterNFailures: 1`
- `InParallel: false`
- `SuppressRealTool: true`

#### TestExecutor and TestExecutorBuilder

Architecture-agnostic interfaces for the unified test runner, enabling both
Chain and CommandCenter architectures to share test infrastructure.

```go
// lib/chain/chaintest/executor_builder.go

type TestExecutor interface {
    Execute(ctx context.Context, input lynx.Message) (string, error)
    GetMemoryStore() *localstore.MemoryStore
    GetConversation() memory.Conversation
}

type ToolModifier func(lynx.ToolCaller) lynx.ToolCaller

type TestExecutorBuilder[T any] interface {
    WithToolModifier(modifier ToolModifier) TestExecutorBuilder[T]
    WithMemoryHistory(interactions []memory.Interaction) TestExecutorBuilder[T]
    WithToolOverrides(tools []string) TestExecutorBuilder[T]
    Build() (TestExecutor, error)
}
```

`UnifiedTestRunner[T]` uses these interfaces to provide `RunTest()` and
`RunMany()` methods that work with any architecture.

### Execution Flow

```
TestCoordinator.RunMany()
  |
  +-- For each TestCase:
  |     |
  |     +-- TestClassifierRecall()         [verify expected tools are in scope]
  |     |     |
  |     |     +-- setupTest()              [build chain, load history]
  |     |     +-- SuggestTraversal()       [run classifiers]
  |     |     +-- Check tool names in state
  |     |
  |     +-- Chaintest()                    [main test execution]
  |           |
  |           +-- ScoreCard.Validate()     [fail fast on bad criteria]
  |           +-- GetMemoryStore()         [get fresh store + cleanup func]
  |           +-- setupTest()              [build chain, inject history]
  |           |     |
  |           |     +-- smith.Make()       [create chain runner]
  |           |     +-- AddInteractions()  [seed memory with history]
  |           |     +-- OverrideTools      [push tool frame if specified]
  |           |
  |           +-- ContextValidator()       [optional service-specific check]
  |           +-- toolMocker setup         [intercept all tool calls]
  |           +-- startAsyncToolTesting()  [launch goroutine for tool validation]
  |           +-- testRunner.Run()         [execute the chain with user input]
  |           +-- close(toolArgsChan)      [signal tool validation to finish]
  |           +-- wg.Wait()               [wait for async validation]
  |           +-- toolErrChecker()         [collect tool validation errors]
  |           +-- ScoreCard.Score()        [LLM-as-judge on final response]
  |
  +-- Print pass rates
```

### Tool Mocking

The `toolMocker` struct intercepts every tool call in the chain:

```go
// lib/chain/chaintest/mock_tools.go

type toolMocker struct {
    toolArgsChan     chan toolArgs
    suppressRealTool bool
    injectResponse   map[string]ToolTestCriteria
}
```

It is injected as a `ModifyTools` state setter, wrapping every `ToolCaller`
with a `MockedTool`:

```go
// lib/chain/chaintest/mocked_tool_struct.go

type MockedTool struct {
    Tool         lynx.ToolCaller
    Declaration  lynx.FunctionDeclaration
    toolArgsChan chan toolArgs
    injectCall   func(string) (lynx.ToolCallResponse, error)
}
```

When `MockedTool.Call()` is invoked:

1. It sends the tool name and JSON params onto `toolArgsChan`.
2. If `injectCall` is set (from `MockedResponse`), it returns the mocked
   response.
3. Else if the real `Tool` is non-nil (from `CallRealTool` or
   `!SuppressRealTool`), it delegates to the real tool.
4. Otherwise it returns an empty `ToolCallResponse`.

### Parameter Validation

Two validation strategies run on the params received via `toolArgsChan`:

**Exact match** (`ExactParams`): Uses `isSubset(expected, actual, caseSensitive)`.
The expected map must be a subset of the actual map -- extra keys from the LLM
are allowed. Features:
- Handles `[]string` vs `[]interface{}` type mismatches from JSON marshaling
- Numeric normalization (all numeric types convert to `float64`)
- Optional case-insensitive string comparison
- Nested map and slice-of-maps support
- Detailed error messages with dot-delimited key paths

**Fuzzy match** (`JudgeParams`): Uses `matchLeaves(judgeMap, actualMap)` to
pair each leaf string (a judge prompt) with the corresponding actual value.
Each pair is then sent to the judge LLM:

```
System: "According to the following scoring criteria:
         <judge prompt>
         Score the following with PASS or FAIL:"
Human:   <actual value as JSON>
```

The judge returns a structured `{"result": "PASS"}` or `{"result": "FAIL"}`.

### Async Tool Validation

`startAsyncToolTesting()` launches a goroutine that reads from `toolArgsChan`
and:

1. Checks each tool call against `ExpectedTools`.
2. Logs unexpected tool calls (error unless `AllowUnexpectedTools` is true).
3. Validates `ExactParams` via `isSubset()`.
4. Validates `JudgeParams` via `matchLeaves()` + judge LLM.
5. Tracks which expected (non-optional) tools were called.

After the chain run completes and the channel is closed, the error checker
function reports:
- Parameter failures
- Unexpected tool calls
- Expected tools that were never called

Errors are collected in a nested `map[string]map[string]interface{}` and
serialized to JSON for reporting.

### Error Reporting

```go
// lib/chain/chaintest/testcase.go

type testErr[T any] struct {
    Err         error
    TestCase    TestCase[T]
    I           int
    StateSetter string
    Attempt     int
    Messages    []lynx.Message
}
```

The `String()` method outputs JSON-formatted test case and message history for
debugging.

---

## 4. Evalchain Scenario Tests

**Location**: `svc/vehicle/evalchain/`

17 Go files comprising the vehicle domain scenario test suite:

```
svc/vehicle/evalchain/
  run_test.go                -- Test functions + run()/filter() helpers
  setup.go                   -- VehTestCaseSetup, GetVehicleTestSetup()
  helpers.go                 -- Mock response fixtures, state builders, history builders
  tags.go                    -- Tag constants (domain, turn, objective, sub-group)
  all_cases.go               -- GetAllVehicleTestCases(), tagCases(), concat()
  phone_cases.go             -- SMS and messaging test cases
  navigation_cases.go        -- POI, home, data navigation cases
  calendar_cases.go          -- Event CRUD and availability cases
  vehicle_controls_cases.go  -- Lighting, body, camera, dynamics, media cases
  vehicle_states_cases.go    -- Battery state cases
  diagnostics_cases.go       -- Diagnostic scan cases
  owners_guide_cases.go      -- Owners guide query cases
  privacy_cases.go           -- Data deletion and collection cases
  unsupported_cases.go       -- Unsupported operation handling cases
  weather_cases.go           -- Weather unit cases
  tool_retry_cases.go        -- Malformed tool call retry cases
  guardrails_cases.go        -- Prompt injection guardrail cases
```

### Tag Taxonomy

Tags are typed constants defined in `tags.go`:

**Domain tags** (`domain:*`):
`phone`, `navigation`, `vehicle-controls`, `owners-guide`, `calendar`,
`diagnostics`, `vehicle-states`, `privacy`, `weather`, `unsupported`,
`general`, `infra`

**Turn tags** (`turn:*`):
`single` (input only, no prior history), `multi` (has History or FullHistory)

**Objective tags** (`objective:*`):
`tool-params` (validates exact/fuzzy tool call parameters),
`tool-routing` (validates correct tool selection),
`scorecard` (validates natural language response via judge LLM)

**Sub-group tags**: Fine-grained tags like `phone:sms-basic`,
`calendar:create-event`, `vehicle-controls:lighting`, etc. Each maps 1:1 to
a getter function and a `Test*` function.

### Test Function Pattern

Each domain group follows a consistent pattern in `run_test.go`:

```go
// svc/vehicle/evalchain/run_test.go

func run(t *testing.T, cases []chaintest.TestCase[rivian_va_message_pb.RivianVAMessage]) {
    t.Helper()
    teardown := svc_testing.SetupTest(t)
    defer teardown(t)
    if err := GetVehicleTestSetup(t.Context()).RunMany(
        t.Context(), cases,
        chaintest.GetChainTestConfig(lynxlog.GetLynxLogger(t.Context()), nil),
    ); err != nil {
        t.Fatal(err)
    }
}

func filter(tag string) []chaintest.TestCase[rivian_va_message_pb.RivianVAMessage] {
    return chaintest.FilterTestCases(GetAllVehicleTestCases(), functions.IncludesAny{tag})
}

func TestPhoneSMSBasic(t *testing.T)       { run(t, filter(PhoneSMSBasic)) }
func TestNavigationPOI(t *testing.T)       { run(t, filter(NavigationPOI)) }
func TestCalendarCreateEvent(t *testing.T) { run(t, filter(CalendarCreateEvent)) }
// ... 30+ more test functions
```

### Tag Filtering

`FilterTestCases` uses the `functions.TagFilter` interface with combinators:

```go
// lib/chain/chaintest/filter.go

func FilterTestCases[T any](cases []TestCase[T], filter functions.TagFilter) []TestCase[T]
```

Available combinators from `lib/functions/tags.go`:

```go
type TagFilter interface {
    Filter(tags []string) bool
}

type IncludesAll []string   // all tags must be present
type IncludesAny []string   // at least one tag must be present
type And []TagFilter        // all sub-filters must pass
type Or  []TagFilter        // at least one sub-filter must pass
type Not [1]TagFilter       // negation
```

Example combining filters:

```go
chaintest.FilterTestCases(cases, functions.And{
    functions.IncludesAll{DomainPhone, TurnMulti},
    functions.Not{functions.IncludesAny{ObjectiveScorecard}},
})
```

### Setup

`GetVehicleTestSetup()` wires the coordinator:

```go
// svc/vehicle/evalchain/setup.go

func GetVehicleTestSetup(ctx context.Context) chaintest.TestCoordinator[
    rivian_va_message_pb.RivianVAMessage, *chain.Chain,
] {
    return chaintest.TestCoordinator[rivian_va_message_pb.RivianVAMessage, *chain.Chain]{
        RunOnceSetup: svc_testing.NewDefaultOnceTestSetup(config.GetEmbeddedModelFiles()),
        TestcaseSetups: []chaintest.TestcaseSetup[
            rivian_va_message_pb.RivianVAMessage, *chain.Chain,
        ]{
            VehTestCaseSetup{BaseClassifier: "COLBERT_GRAPH"},
        },
    }
}
```

`VehTestCaseSetup` implements `TestcaseSetup` with:
- `GetMemoryStore()`: returns a fresh `localstore.New("chaintest", 0)`
- `GetSmith()`: loads a test model catalog and returns a `ChainSmith`
- `GetMessage()`: builds the vehicle-specific protobuf message

### Case Registration

`GetAllVehicleTestCases()` in `all_cases.go` concatenates all getter
functions. Each getter calls `tagCases()` to stamp its sub-group tag:

```go
func tagCases(tag string, cases []chaintest.TestCase[...]) []chaintest.TestCase[...] {
    for i := range cases {
        cases[i].Tags = append(cases[i].Tags, tag)
    }
    return cases
}
```

### Mock Response Pattern

Mock responses are pre-built as package-level variables in `helpers.go`:

```go
var smsNoMessageResponse = &lynx.ToolCallResponse{
    ToolCallID:    "234",
    Name:          "DraftMessage",
    MemoryContent: `{"succeeded":true,"status":"SUCCESS","final_state":"...","ai_guidance":"..."}`,
}
```

These are referenced from test cases:

```go
ExpectedTools: map[string]chaintest.ToolTestCriteria{
    "DraftMessage": {
        MockedResponse: smsNoMessageResponse,
        ExactParams:    map[string]any{"contact_name": "John Smith"},
    },
},
```

### State Management Helpers

Helpers build `lynx.State` objects that configure the tool frame stack for
specific scenarios:

```go
func phoneMockFavoringMessagingState(sendMessageTo ...sendMessageArgs) *lynx.State
func phoneMockDraftMessageEditResponseState(sendMessageTo sendMessageArgs) *lynx.State
func callMockFavoringCallState(dialTo ...dialContactArgs) *lynx.State
```

History builders construct multi-turn conversation histories:

```go
func smsBuildDraftMessageHistory(humanText, toolCallID, toolArgs string,
    response *lynx.ToolCallResponse, aiFollowUp string) []lynx.Message

func phoneInteractionWithMessagingState(history []lynx.Message,
    sendMessageTo ...sendMessageArgs) []memory.Interaction
```

### ScoreCard Examples

From `phone_cases.go`:

```go
ScoreCard: &structure.ScoreCard{
    Criteria: []structure.Criterion{
        {Condition: "The assistant asks for message content", Score: 1},
        {Condition: "The assistant does not ask for contact information again", Score: 1},
    },
    PassingScore: 2,
},
```

From `calendar_cases.go`:

```go
ScoreCard: &structure.ScoreCard{
    Criteria: []structure.Criterion{
        {Condition: "The assistant confirms the event was created or scheduled", Score: 1},
        {Condition: "The assistant mentions the time of the event (2 PM or 2 o'clock or 14:00)", Score: 1},
        {Condition: "The assistant mentions January 23rd or the specific date", Score: 1},
    },
    PassingScore: 3,
},
```

---

## 5. Core Unit Test Patterns

### Table-Driven Tests

`lib/chain/chaintest/subset_test.go` demonstrates extensive table-driven
testing for the `isSubset()` function with named sub-tests:

```go
func TestCustomSubsetImplementation(t *testing.T) {
    t.Run("basic subset checking", func(t *testing.T) { ... })
    t.Run("handles []string vs []interface{} correctly", func(t *testing.T) { ... })
    t.Run("case insensitive comparison", func(t *testing.T) { ... })
    t.Run("missing required key", func(t *testing.T) { ... })
    t.Run("nested object validation", func(t *testing.T) { ... })
    t.Run("numeric type normalization", func(t *testing.T) { ... })
    t.Run("complex LLM tool call scenario", func(t *testing.T) { ... })
    // ... more
}
```

### testify Usage

- `assert` for non-fatal checks (test continues on failure)
- `require` for fatal checks (test stops immediately)

Used extensively in unit tests throughout `lib/` and `svc/`.

---

## 6. ScoreCard: LLM-as-Judge Evaluation

**Location**: `lib/structure/scoring.go`

The `ScoreCard` provides structured evaluation of LLM responses against
human-defined criteria.

```go
// lib/structure/scoring.go

type Criterion struct {
    Score     int
    Condition string   // free-text description; must not contain double quotes
}

type ScoreCard struct {
    Criteria     []Criterion
    PassingScore int
    messages     []lynx.Message  // set via SetMessages()
}
```

### Evaluation Flow

```
ScoreCard.Score(ctx, judgeModel)
  |
  +-- EvaluateCriteria(ctx, model)
  |     |
  |     +-- Build ObjectStructure from Criteria
  |     |   (each criterion becomes a required JSON key with
  |     |    "reasoning" (string) + "condition_met" (boolean) properties)
  |     |
  |     +-- judgeModel.Generate(ctx, messages, nil, WithStructure(...))
  |     +-- Unmarshal response into map[string]ConditionResult
  |     +-- Return []ConditionResult
  |
  +-- Sum scores where condition_met == true
  +-- If totalScore < PassingScore, return error with details
```

The judge sees the full message history (set via `SetMessages()`) and is
asked to evaluate each criterion with structured output. The structure forces
the judge to provide reasoning before a boolean verdict, reducing evaluation
errors.

### Validation Rules

`ScoreCard.Validate()` enforces:
- `PassingScore > 0` when criteria are defined
- `PassingScore <= sum(positive scores)`
- No double quotes in condition strings (would break JSON structure)

---

## 7. Specialized Test Patterns

### HTTP Mocking

Tests like guardrail prompt injection checks use `httptest.NewServer` to mock
HTTP endpoints without network calls.

### Channel-Based Testing

The chaintest framework uses channels extensively for async tool validation:
- `toolArgsChan chan toolArgs` carries intercepted tool call parameters
- A goroutine reads from the channel and validates parameters concurrently
  with chain execution
- `sync.WaitGroup` coordinates completion

### Embedded Test Data

Facts integration tests use `//go:embed` directives to load test data files
directly into test binaries, avoiding filesystem dependencies at test time.

### Build-Tag-Gated Integration Tests

Tests requiring external services (MongoDB, Gemini API) use build tags to
prevent accidental execution:

```go
//go:build evalchain
```

---

## 8. Configuration & Execution

### CHAINTEST_CONFIG Environment Variable

The `CHAINTEST_CONFIG` environment variable accepts a JSON string:

```bash
export CHAINTEST_CONFIG='{"stop_after_n_runs": 3, "stop_after_n_failures": 2, "in_parallel": true, "suppress_real_tool": true}'
```

Parsed by `GetChainTestConfig()` in `lib/chain/chaintest/config.go` using
`viper.AutomaticEnv()`. Falls back to provided defaults or hardcoded defaults
(1 run, 1 failure, sequential, suppress tools).

### Running Tests

```bash
# Run all evalchain tests
go test -tags evalchain ./svc/vehicle/evalchain/

# Run a specific domain
go test -tags evalchain -run TestPhoneSMSBasic ./svc/vehicle/evalchain/

# Run with custom config
CHAINTEST_CONFIG='{"stop_after_n_runs":5,"stop_after_n_failures":3}' \
  go test -tags evalchain -run TestCalendarCreateEvent ./svc/vehicle/evalchain/

# Run unit tests only
go test -tags unit ./lib/chain/chaintest/
```

### Required Environment Variables

| Variable | Used By |
|----------|---------|
| `GEMINI_API_KEY` | Chain and judge LLM initialization |
| `GOOGLE_APPLICATION_TEST_CREDENTIALS` | Service account for Google APIs |
| `MONGODB_URI` | Facts integration tests |

### Parallel Execution

When `InParallel: true`:
- `TestCoordinator.RunMany()` uses `errgroup.Group` to run test cases
  concurrently.
- Within each case, retries also run in parallel via a sub-`errgroup.Group`.
- A `sync.Mutex` protects the failure count and error collection.
- Tool validation runs in a background goroutine regardless of parallelism.

### BERT-Handled Intent Support

The framework supports embedded mode where BERT may handle intents directly
(returning a nil chain). When detected:
- Classifier recall tests are skipped.
- Tool validation is skipped (BERT does not use tools).
- ScoreCard validation still runs against the memory-stored response.

---

## 9. Design Critique

### Strengths

- **Generic `TestCase[T]`**: The type parameter supports any client state type,
  making the framework reusable across services (vehicle, mobile, etc.).

- **Tag-based filtering**: The three-axis taxonomy (domain, turn, objective)
  combined with `IncludesAll`, `IncludesAny`, `And`, `Or`, `Not` combinators
  enables precise test selection without maintaining separate test suites.

- **Dual parameter validation**: `ExactParams` provides deterministic
  regression checking; `JudgeParams` handles semantic equivalence where exact
  strings vary. This covers both "did the LLM fill in the right enum?" and
  "did the LLM compose a reasonable search query?"

- **ScoreCard structured evaluation**: Forcing the judge LLM to produce
  `reasoning` before `condition_met` reduces snap judgments. Per-criterion
  scoring with a threshold avoids all-or-nothing pass/fail brittleness.

- **Architecture-agnostic executor**: The `TestExecutor`/`TestExecutorBuilder`
  abstraction allows Chain and CommandCenter architectures to share the same
  test infrastructure.

- **Classifier recall pre-check**: `TestClassifierRecall()` validates that
  expected tools are even in scope before running the expensive chain
  execution, providing fast failure on routing bugs.

### Weaknesses

- **No snapshot testing for regression detection**: There is no mechanism to
  capture and diff LLM outputs across runs. A test that passes today with a
  different response than yesterday provides no signal about drift.

- **ScoreCard criteria are free-text strings with no schema validation**: The
  only constraint is "no double quotes." Typos, contradictory criteria, or
  ambiguous phrasing silently degrade evaluation quality. There is no lint or
  static analysis for criteria content.

- **Test cases scattered across many files with no central registry beyond
  `all_cases.go`**: Adding a new domain requires: (1) create `*_cases.go`,
  (2) add tag constants to `tags.go`, (3) add getter call to
  `GetAllVehicleTestCases()`, (4) add `Test*` function to `run_test.go`.
  Missing any step silently omits cases.

- **`CHAINTEST_CONFIG` is JSON-in-env-var**: Fragile to quoting issues in CI
  scripts and shell environments. No validation feedback beyond a log warning
  that falls back to defaults.

- **No built-in test coverage tracking for tool paths**: The framework tracks
  which expected tools were called but does not report which tools across the
  entire tool catalog are never tested.

- **No visual test report generation**: Results are only available in stdout
  logs. There is no HTML report, JUnit XML output, or dashboard integration
  for tracking pass rates over time.

- **Channel-based async validation can mask timing bugs**: If the chain
  completes before a tool call is fully processed on the channel, or if
  multiple calls to the same tool race, validation may see stale or
  out-of-order data. The current design works because LLM tool calls are
  sequential, but this is an implicit assumption.

- **Mock responses are package-level vars**: Shared mutable state. While
  `ToolCallResponse` is a value type (no mutation risk), the pattern
  encourages defining more and more package-level vars as cases grow,
  reducing locality of test data.
