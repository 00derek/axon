package anthropic

import (
	"context"
	"encoding/json"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"

	"github.com/axonframework/axon/kernel"
)

func TestSchemaToMap_PrimitiveAndNested(t *testing.T) {
	s := kernel.Schema{
		Type: "object",
		Properties: map[string]kernel.Schema{
			"name": {Type: "string", Description: "user name"},
			"tags": {Type: "array", Items: &kernel.Schema{Type: "string"}},
		},
		Required: []string{"name"},
	}
	m := schemaToMap(s)
	if m["type"] != "object" {
		t.Errorf("expected type object, got %v", m["type"])
	}
	props, ok := m["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties to be a map, got %T", m["properties"])
	}
	name, _ := props["name"].(map[string]any)
	if name["type"] != "string" || name["description"] != "user name" {
		t.Errorf("unexpected name schema: %v", name)
	}
	tags, _ := props["tags"].(map[string]any)
	items, _ := tags["items"].(map[string]any)
	if items["type"] != "string" {
		t.Errorf("unexpected items schema: %v", items)
	}
	req, _ := m["required"].([]string)
	if len(req) != 1 || req[0] != "name" {
		t.Errorf("unexpected required: %v", req)
	}
}

// dummyTool is a minimal kernel.Tool for convert tests.
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

func TestConvertTools(t *testing.T) {
	tool := &dummyTool{
		name: "search",
		desc: "search the web",
		schema: kernel.Schema{
			Type: "object",
			Properties: map[string]kernel.Schema{
				"query": {Type: "string"},
			},
			Required: []string{"query"},
		},
	}

	out := convertTools([]kernel.Tool{tool})
	if len(out) != 1 {
		t.Fatalf("expected 1 tool param, got %d", len(out))
	}
	if out[0].OfTool == nil {
		t.Fatal("expected OfTool populated")
	}
	tp := out[0].OfTool
	if tp.Name != "search" {
		t.Errorf("expected name 'search', got %q", tp.Name)
	}
	if !tp.Description.Valid() || tp.Description.Value != "search the web" {
		t.Errorf("expected description set")
	}
	props, ok := tp.InputSchema.Properties.(map[string]any)
	if !ok {
		t.Fatalf("expected Properties to be map, got %T", tp.InputSchema.Properties)
	}
	if _, ok := props["query"]; !ok {
		t.Error("expected 'query' property")
	}
	if len(tp.InputSchema.Required) != 1 || tp.InputSchema.Required[0] != "query" {
		t.Errorf("unexpected required: %v", tp.InputSchema.Required)
	}
}

func TestConvertToolChoice(t *testing.T) {
	cases := []struct {
		tc        kernel.ToolChoice
		expectSet func(u sdk.ToolChoiceUnionParam) bool
	}{
		{kernel.ToolChoiceAuto, func(u sdk.ToolChoiceUnionParam) bool { return u.OfAuto != nil }},
		{kernel.ToolChoiceRequired, func(u sdk.ToolChoiceUnionParam) bool { return u.OfAny != nil }},
		{kernel.ToolChoiceNone, func(u sdk.ToolChoiceUnionParam) bool { return u.OfNone != nil }},
		{kernel.ToolChoiceForce("search"), func(u sdk.ToolChoiceUnionParam) bool {
			return u.OfTool != nil && u.OfTool.Name == "search"
		}},
	}
	for _, c := range cases {
		out := convertToolChoice(c.tc)
		if !c.expectSet(out) {
			t.Errorf("unexpected conversion for %q", c.tc.Type)
		}
	}
}

func TestConvertMessages_SystemExtraction(t *testing.T) {
	msgs := []kernel.Message{
		kernel.SystemMsg("be brief"),
		kernel.UserMsg("hi"),
		kernel.AssistantMsg("hello"),
		kernel.ToolResultMsg("toolu_1", "search", "weather sunny"),
	}

	out, system := convertMessages(msgs)

	if len(system) != 1 || system[0].Text != "be brief" {
		t.Errorf("expected single system block 'be brief', got %v", system)
	}
	// 3 non-system messages: user, assistant, tool-result(as user)
	if len(out) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(out))
	}
	if out[0].Role != sdk.MessageParamRoleUser {
		t.Errorf("expected first message role user, got %q", out[0].Role)
	}
	if out[1].Role != sdk.MessageParamRoleAssistant {
		t.Errorf("expected second message role assistant, got %q", out[1].Role)
	}
	if out[2].Role != sdk.MessageParamRoleUser {
		t.Errorf("expected tool-result to become user, got %q", out[2].Role)
	}
	// The tool-result block must be a ToolResult variant.
	toolBlock := out[2].Content[0]
	if toolBlock.OfToolResult == nil {
		t.Error("expected tool-result block variant on message 3")
	} else if toolBlock.OfToolResult.ToolUseID != "toolu_1" {
		t.Errorf("expected tool use id 'toolu_1', got %q", toolBlock.OfToolResult.ToolUseID)
	}
}

func TestConvertContentParts_ToolCallAndImage(t *testing.T) {
	parts := []kernel.ContentPart{
		{ToolCall: &kernel.ToolCall{ID: "toolu_1", Name: "search", Params: json.RawMessage(`{"q":"x"}`)}},
		{Image: &kernel.ImageContent{URL: "https://example.com/cat.jpg"}},
	}
	out := convertContentParts(parts)
	if len(out) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(out))
	}
	if out[0].OfToolUse == nil || out[0].OfToolUse.ID != "toolu_1" {
		t.Error("expected tool-use block with preserved ID")
	}
	if out[1].OfImage == nil {
		t.Error("expected image block")
	}
}

func TestConvertResponse_StopReasonMapping(t *testing.T) {
	cases := map[sdk.StopReason]string{
		"end_turn":      "stop",
		"stop_sequence": "stop",
		"max_tokens":    "length",
		"tool_use":      "tool_calls",
		"refusal":       "refusal",
		"pause_turn":    "pause",
	}
	for sr, expected := range cases {
		if got := convertStopReason(sr); got != expected {
			t.Errorf("stop reason %q: expected %q got %q", sr, expected, got)
		}
	}
}

func TestConvertResponse_NilMessage(t *testing.T) {
	resp := convertResponse(nil)
	if resp.FinishReason != "stop" {
		t.Errorf("expected 'stop' on nil message, got %q", resp.FinishReason)
	}
}

func TestBuildParams_DefaultsAndTools(t *testing.T) {
	tool := &dummyTool{
		name: "greet",
		desc: "say hi",
		schema: kernel.Schema{
			Type:       "object",
			Properties: map[string]kernel.Schema{"name": {Type: "string"}},
			Required:   []string{"name"},
		},
	}

	temp := float32(0.7)
	params := buildParams("claude-opus-4-5", 1000, nil, kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("hi")},
		Tools:    []kernel.Tool{tool},
		Options: kernel.GenerateOptions{
			Temperature: &temp,
			ToolChoice:  kernel.ToolChoiceAuto,
		},
	})

	if params.Model != "claude-opus-4-5" {
		t.Errorf("unexpected model")
	}
	if params.MaxTokens != 1000 {
		t.Errorf("expected max_tokens passthrough")
	}
	if len(params.Tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(params.Tools))
	}
	if params.ToolChoice.OfAuto == nil {
		t.Errorf("expected auto tool-choice")
	}
	if !params.Temperature.Valid() {
		t.Errorf("expected temperature set")
	}
}
