package openai

import (
	"context"
	"encoding/json"
	"testing"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"

	"github.com/axonframework/axon/kernel"
)

type dummyTool struct {
	name, desc string
	schema     kernel.Schema
}

func (d *dummyTool) Name() string          { return d.name }
func (d *dummyTool) Description() string   { return d.desc }
func (d *dummyTool) Schema() kernel.Schema { return d.schema }
func (d *dummyTool) Execute(_ context.Context, _ json.RawMessage) (any, error) {
	return nil, nil
}

func TestSchemaToMap(t *testing.T) {
	s := kernel.Schema{
		Type: "object",
		Properties: map[string]kernel.Schema{
			"q": {Type: "string", Description: "query"},
		},
		Required: []string{"q"},
	}
	m := schemaToMap(s)
	if m["type"] != "object" {
		t.Errorf("unexpected type %v", m["type"])
	}
	props, _ := m["properties"].(map[string]any)
	if props == nil || props["q"] == nil {
		t.Error("missing q property")
	}
}

func TestConvertTools(t *testing.T) {
	tool := &dummyTool{
		name: "search", desc: "search",
		schema: kernel.Schema{
			Type:       "object",
			Properties: map[string]kernel.Schema{"q": {Type: "string"}},
			Required:   []string{"q"},
		},
	}
	out := convertTools([]kernel.Tool{tool})
	if len(out) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(out))
	}
	fn := out[0].OfFunction
	if fn == nil {
		t.Fatal("expected OfFunction")
	}
	if fn.Function.Name != "search" {
		t.Errorf("expected name 'search'")
	}
	if fn.Function.Parameters == nil {
		t.Error("expected parameters set")
	}
}

func TestConvertToolChoice(t *testing.T) {
	auto := convertToolChoice(kernel.ToolChoiceAuto)
	if !auto.OfAuto.Valid() || auto.OfAuto.Value != "auto" {
		t.Errorf("expected OfAuto='auto', got %+v", auto)
	}
	req := convertToolChoice(kernel.ToolChoiceRequired)
	if !req.OfAuto.Valid() || req.OfAuto.Value != "required" {
		t.Errorf("expected OfAuto='required', got %+v", req)
	}
	none := convertToolChoice(kernel.ToolChoiceNone)
	if !none.OfAuto.Valid() || none.OfAuto.Value != "none" {
		t.Errorf("expected OfAuto='none', got %+v", none)
	}
	force := convertToolChoice(kernel.ToolChoiceForce("search"))
	if force.OfFunctionToolChoice == nil || force.OfFunctionToolChoice.Function.Name != "search" {
		t.Errorf("expected forced function, got %+v", force)
	}
}

func TestConvertMessages_SystemInline(t *testing.T) {
	out := convertMessages([]kernel.Message{
		kernel.SystemMsg("be brief"),
		kernel.UserMsg("hi"),
	})
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
	// Role fields carry `default` JSON tags that only materialize at marshal
	// time, so inspect the variant pointers directly.
	if out[0].OfSystem == nil {
		t.Error("expected first message to be system variant")
	}
	if out[1].OfUser == nil {
		t.Error("expected second message to be user variant")
	}
}

func TestConvertMessages_AssistantToolCallsAndResults(t *testing.T) {
	// Assistant produces a tool call.
	assistantMsg := kernel.Message{
		Role: kernel.RoleAssistant,
		Content: []kernel.ContentPart{
			{ToolCall: &kernel.ToolCall{ID: "call_1", Name: "search", Params: json.RawMessage(`{"q":"x"}`)}},
		},
	}
	toolResultMsg := kernel.Message{
		Role: kernel.RoleTool,
		Content: []kernel.ContentPart{
			{ToolResult: &kernel.ToolResult{ToolCallID: "call_1", Name: "search", Content: "results"}},
		},
	}

	out := convertMessages([]kernel.Message{assistantMsg, toolResultMsg})
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
	// Assistant message carries tool calls.
	asst := out[0].OfAssistant
	if asst == nil {
		t.Fatal("expected assistant message")
	}
	if len(asst.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(asst.ToolCalls))
	}
	fn := asst.ToolCalls[0].OfFunction
	if fn == nil || fn.ID != "call_1" || fn.Function.Name != "search" {
		t.Errorf("unexpected tool call: %+v", asst.ToolCalls[0])
	}
	if fn.Function.Arguments != `{"q":"x"}` {
		t.Errorf("expected JSON string args, got %q", fn.Function.Arguments)
	}
	// Tool-result is a tool-role message.
	tool := out[1].OfTool
	if tool == nil {
		t.Fatal("expected tool message")
	}
	if tool.ToolCallID != "call_1" {
		t.Errorf("expected tool_call_id 'call_1', got %q", tool.ToolCallID)
	}
}

func TestConvertMessages_ImageUser(t *testing.T) {
	msg := kernel.Message{
		Role: kernel.RoleUser,
		Content: []kernel.ContentPart{
			{Text: strPtr("look at this")},
			{Image: &kernel.ImageContent{URL: "https://x/cat.jpg"}},
		},
	}
	out := convertMessages([]kernel.Message{msg})
	if len(out) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(out))
	}
	u := out[0].OfUser
	if u == nil {
		t.Fatal("expected user message")
	}
	// Should be an array-of-parts form, not string.
	if u.Content.OfString.Valid() {
		t.Error("expected array-of-parts content for multimodal input")
	}
	if len(u.Content.OfArrayOfContentParts) != 2 {
		t.Errorf("expected 2 parts, got %d", len(u.Content.OfArrayOfContentParts))
	}
}

func strPtr(s string) *string { return &s }

func TestConvertResponse_MultiToolPreservesOrder(t *testing.T) {
	resp := &sdk.ChatCompletion{}
	if err := resp.UnmarshalJSON([]byte(`{
		"id":"c","object":"chat.completion","created":0,"model":"gpt-5",
		"choices":[{"index":0,"message":{"role":"assistant","content":"","refusal":"",
			"tool_calls":[
				{"id":"call_a","type":"function","function":{"name":"first","arguments":"{}"}},
				{"id":"call_b","type":"function","function":{"name":"second","arguments":"{}"}}
			]},"finish_reason":"tool_calls","logprobs":null}],
		"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
	}`)); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	kr := convertResponse(resp)
	if len(kr.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(kr.ToolCalls))
	}
	if kr.ToolCalls[0].Name != "first" || kr.ToolCalls[1].Name != "second" {
		t.Errorf("order not preserved: %v", kr.ToolCalls)
	}
}

func TestConvertResponse_NilOrEmpty(t *testing.T) {
	if convertResponse(nil).FinishReason != "stop" {
		t.Error("nil response should produce finish=stop")
	}
	empty := &sdk.ChatCompletion{}
	if convertResponse(empty).FinishReason != "stop" {
		t.Error("empty choices should produce finish=stop")
	}
}

func TestBuildParams_StreamFlagEnablesUsage(t *testing.T) {
	params := buildParams("gpt-5", "", "", nil, kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("hi")},
	}, true)
	if !params.StreamOptions.IncludeUsage.Valid() || !params.StreamOptions.IncludeUsage.Value {
		t.Error("expected StreamOptions.IncludeUsage=true when forStream")
	}
}

func TestBuildParams_NoStreamNoUsage(t *testing.T) {
	params := buildParams("gpt-5", "", "", nil, kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("hi")},
	}, false)
	if params.StreamOptions.IncludeUsage.Valid() {
		t.Error("expected StreamOptions.IncludeUsage unset when not streaming")
	}
}

func TestBuildParams_Temperature(t *testing.T) {
	temp := float32(0.3)
	params := buildParams("gpt-5", "", "", nil, kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("hi")},
		Options:  kernel.GenerateOptions{Temperature: &temp},
	}, false)
	if !params.Temperature.Valid() {
		t.Error("expected temperature set")
	}
	// Sanity check — param.NewOpt round-trip.
	_ = param.NewOpt(0.0)
}
