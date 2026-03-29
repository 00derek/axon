package google

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/axonframework/axon/kernel"
	"google.golang.org/genai"
)

// --- Schema conversion tests ---

func TestConvertSchema_String(t *testing.T) {
	s := kernel.Schema{Type: "string", Description: "a name"}
	gs := convertSchema(s)
	if gs.Type != genai.TypeString {
		t.Errorf("expected TypeString, got %v", gs.Type)
	}
	if gs.Description != "a name" {
		t.Errorf("expected description %q, got %q", "a name", gs.Description)
	}
}

func TestConvertSchema_Integer(t *testing.T) {
	min := 1.0
	max := 100.0
	s := kernel.Schema{Type: "integer", Minimum: &min, Maximum: &max}
	gs := convertSchema(s)
	if gs.Type != genai.TypeInteger {
		t.Errorf("expected TypeInteger, got %v", gs.Type)
	}
	if gs.Minimum == nil || *gs.Minimum != 1.0 {
		t.Errorf("expected minimum 1.0, got %v", gs.Minimum)
	}
	if gs.Maximum == nil || *gs.Maximum != 100.0 {
		t.Errorf("expected maximum 100.0, got %v", gs.Maximum)
	}
}

func TestConvertSchema_Number(t *testing.T) {
	s := kernel.Schema{Type: "number"}
	gs := convertSchema(s)
	if gs.Type != genai.TypeNumber {
		t.Errorf("expected TypeNumber, got %v", gs.Type)
	}
}

func TestConvertSchema_Boolean(t *testing.T) {
	s := kernel.Schema{Type: "boolean"}
	gs := convertSchema(s)
	if gs.Type != genai.TypeBoolean {
		t.Errorf("expected TypeBoolean, got %v", gs.Type)
	}
}

func TestConvertSchema_Array(t *testing.T) {
	s := kernel.Schema{
		Type:  "array",
		Items: &kernel.Schema{Type: "string"},
	}
	gs := convertSchema(s)
	if gs.Type != genai.TypeArray {
		t.Errorf("expected TypeArray, got %v", gs.Type)
	}
	if gs.Items == nil || gs.Items.Type != genai.TypeString {
		t.Errorf("expected Items to be TypeString, got %v", gs.Items)
	}
}

func TestConvertSchema_Object(t *testing.T) {
	s := kernel.Schema{
		Type: "object",
		Properties: map[string]kernel.Schema{
			"name": {Type: "string", Description: "user name"},
			"age":  {Type: "integer"},
		},
		Required: []string{"name"},
	}
	gs := convertSchema(s)
	if gs.Type != genai.TypeObject {
		t.Errorf("expected TypeObject, got %v", gs.Type)
	}
	if len(gs.Properties) != 2 {
		t.Fatalf("expected 2 properties, got %d", len(gs.Properties))
	}
	nameProp, ok := gs.Properties["name"]
	if !ok {
		t.Fatal("expected 'name' property")
	}
	if nameProp.Type != genai.TypeString {
		t.Errorf("expected name property TypeString, got %v", nameProp.Type)
	}
	if nameProp.Description != "user name" {
		t.Errorf("expected description %q, got %q", "user name", nameProp.Description)
	}
	if len(gs.Required) != 1 || gs.Required[0] != "name" {
		t.Errorf("expected Required [name], got %v", gs.Required)
	}
}

func TestConvertSchema_Enum(t *testing.T) {
	s := kernel.Schema{Type: "string", Enum: []string{"red", "green", "blue"}}
	gs := convertSchema(s)
	if len(gs.Enum) != 3 || gs.Enum[0] != "red" {
		t.Errorf("expected enum [red green blue], got %v", gs.Enum)
	}
}

func TestConvertSchema_Nil(t *testing.T) {
	s := kernel.Schema{}
	gs := convertSchema(s)
	if gs.Type != genai.TypeString {
		t.Errorf("expected default TypeString for empty type, got %v", gs.Type)
	}
}

// --- Tool conversion tests ---

type fakeKernelTool struct {
	name string
	desc string
	sch  kernel.Schema
}

func (f *fakeKernelTool) Name() string                                              { return f.name }
func (f *fakeKernelTool) Description() string                                       { return f.desc }
func (f *fakeKernelTool) Schema() kernel.Schema                                     { return f.sch }
func (f *fakeKernelTool) Execute(_ context.Context, _ json.RawMessage) (any, error) { return nil, nil }

func TestConvertTools(t *testing.T) {
	tools := []kernel.Tool{
		&fakeKernelTool{
			name: "search",
			desc: "Search the web",
			sch: kernel.Schema{
				Type: "object",
				Properties: map[string]kernel.Schema{
					"query": {Type: "string", Description: "search query"},
				},
				Required: []string{"query"},
			},
		},
		&fakeKernelTool{
			name: "calculate",
			desc: "Do math",
			sch: kernel.Schema{
				Type: "object",
				Properties: map[string]kernel.Schema{
					"expression": {Type: "string"},
				},
				Required: []string{"expression"},
			},
		},
	}

	result := convertTools(tools)
	if len(result) != 1 {
		t.Fatalf("expected 1 genai.Tool wrapper, got %d", len(result))
	}
	decls := result[0].FunctionDeclarations
	if len(decls) != 2 {
		t.Fatalf("expected 2 function declarations, got %d", len(decls))
	}
	if decls[0].Name != "search" {
		t.Errorf("expected first decl name 'search', got %q", decls[0].Name)
	}
	if decls[0].Description != "Search the web" {
		t.Errorf("expected description 'Search the web', got %q", decls[0].Description)
	}
	if decls[1].Name != "calculate" {
		t.Errorf("expected second decl name 'calculate', got %q", decls[1].Name)
	}
}

func TestConvertTools_Empty(t *testing.T) {
	result := convertTools(nil)
	if result != nil {
		t.Errorf("expected nil for empty tools, got %v", result)
	}
}

// --- ToolChoice conversion tests ---

func TestConvertToolChoice_Auto(t *testing.T) {
	tc := convertToolChoice(kernel.ToolChoiceAuto)
	if tc == nil {
		t.Fatal("expected non-nil ToolConfig for auto")
	}
	if tc.FunctionCallingConfig.Mode != genai.FunctionCallingConfigModeAuto {
		t.Errorf("expected FunctionCallingConfigModeAuto, got %v", tc.FunctionCallingConfig.Mode)
	}
}

func TestConvertToolChoice_Required(t *testing.T) {
	tc := convertToolChoice(kernel.ToolChoiceRequired)
	if tc == nil {
		t.Fatal("expected non-nil ToolConfig for required")
	}
	if tc.FunctionCallingConfig.Mode != genai.FunctionCallingConfigModeAny {
		t.Errorf("expected FunctionCallingConfigModeAny, got %v", tc.FunctionCallingConfig.Mode)
	}
}

func TestConvertToolChoice_None(t *testing.T) {
	tc := convertToolChoice(kernel.ToolChoiceNone)
	if tc == nil {
		t.Fatal("expected non-nil ToolConfig for none")
	}
	if tc.FunctionCallingConfig.Mode != genai.FunctionCallingConfigModeNone {
		t.Errorf("expected FunctionCallingConfigModeNone, got %v", tc.FunctionCallingConfig.Mode)
	}
}

func TestConvertToolChoice_Force(t *testing.T) {
	tc := convertToolChoice(kernel.ToolChoiceForce("search"))
	if tc == nil {
		t.Fatal("expected non-nil ToolConfig for force")
	}
	if tc.FunctionCallingConfig.Mode != genai.FunctionCallingConfigModeAny {
		t.Errorf("expected FunctionCallingConfigModeAny, got %v", tc.FunctionCallingConfig.Mode)
	}
	if len(tc.FunctionCallingConfig.AllowedFunctionNames) != 1 ||
		tc.FunctionCallingConfig.AllowedFunctionNames[0] != "search" {
		t.Errorf("expected AllowedFunctionNames [search], got %v", tc.FunctionCallingConfig.AllowedFunctionNames)
	}
}

func TestConvertToolChoice_Zero(t *testing.T) {
	tc := convertToolChoice(kernel.ToolChoice{})
	if tc != nil {
		t.Errorf("expected nil for zero-value ToolChoice, got %v", tc)
	}
}

// --- Message conversion tests ---

func strPtr(s string) *string { return &s }

func TestConvertMessages_SystemExtracted(t *testing.T) {
	msgs := []kernel.Message{
		{Role: kernel.RoleSystem, Content: []kernel.ContentPart{{Text: strPtr("Be helpful")}}},
		{Role: kernel.RoleUser, Content: []kernel.ContentPart{{Text: strPtr("Hello")}}},
	}

	contents, sysInstruction := convertMessages(msgs)

	if sysInstruction == nil {
		t.Fatal("expected system instruction to be extracted")
	}
	if len(sysInstruction.Parts) != 1 {
		t.Fatalf("expected 1 system instruction part, got %d", len(sysInstruction.Parts))
	}
	if sysInstruction.Parts[0].Text != "Be helpful" {
		t.Errorf("expected system text 'Be helpful', got %q", sysInstruction.Parts[0].Text)
	}

	if len(contents) != 1 {
		t.Fatalf("expected 1 content (user), got %d", len(contents))
	}
	if contents[0].Role != "user" {
		t.Errorf("expected role 'user', got %q", contents[0].Role)
	}
}

func TestConvertMessages_MultipleSystemMerged(t *testing.T) {
	msgs := []kernel.Message{
		{Role: kernel.RoleSystem, Content: []kernel.ContentPart{{Text: strPtr("Rule 1")}}},
		{Role: kernel.RoleSystem, Content: []kernel.ContentPart{{Text: strPtr("Rule 2")}}},
		{Role: kernel.RoleUser, Content: []kernel.ContentPart{{Text: strPtr("Hi")}}},
	}

	_, sysInstruction := convertMessages(msgs)
	if sysInstruction == nil {
		t.Fatal("expected system instruction")
	}
	if len(sysInstruction.Parts) != 2 {
		t.Fatalf("expected 2 system parts, got %d", len(sysInstruction.Parts))
	}
}

func TestConvertMessages_UserText(t *testing.T) {
	msgs := []kernel.Message{
		{Role: kernel.RoleUser, Content: []kernel.ContentPart{
			{Text: strPtr("part one")},
			{Text: strPtr("part two")},
		}},
	}

	contents, _ := convertMessages(msgs)
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}
	if len(contents[0].Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(contents[0].Parts))
	}
	if contents[0].Parts[0].Text != "part one" {
		t.Errorf("expected 'part one', got %q", contents[0].Parts[0].Text)
	}
}

func TestConvertMessages_AssistantWithToolCall(t *testing.T) {
	params := json.RawMessage(`{"query":"test"}`)
	msgs := []kernel.Message{
		{Role: kernel.RoleAssistant, Content: []kernel.ContentPart{
			{ToolCall: &kernel.ToolCall{ID: "call-1", Name: "search", Params: params}},
		}},
	}

	contents, _ := convertMessages(msgs)
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}
	if contents[0].Role != "model" {
		t.Errorf("expected role 'model', got %q", contents[0].Role)
	}
	fc := contents[0].Parts[0].FunctionCall
	if fc == nil {
		t.Fatalf("expected FunctionCall part, got nil")
	}
	if fc.Name != "search" {
		t.Errorf("expected function name 'search', got %q", fc.Name)
	}
	if fc.Args["query"] != "test" {
		t.Errorf("expected args query='test', got %v", fc.Args)
	}
}

func TestConvertMessages_ToolResult(t *testing.T) {
	msgs := []kernel.Message{
		{Role: kernel.RoleTool, Content: []kernel.ContentPart{
			{ToolResult: &kernel.ToolResult{
				ToolCallID: "call-1",
				Name:       "search",
				Content:    `{"results":["a","b"]}`,
			}},
		}},
	}

	contents, _ := convertMessages(msgs)
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}
	if contents[0].Role != "user" {
		t.Errorf("expected role 'user' for tool results, got %q", contents[0].Role)
	}
	fr := contents[0].Parts[0].FunctionResponse
	if fr == nil {
		t.Fatalf("expected FunctionResponse part, got nil")
	}
	if fr.Name != "search" {
		t.Errorf("expected function name 'search', got %q", fr.Name)
	}
}

func TestConvertMessages_ImageURL(t *testing.T) {
	msgs := []kernel.Message{
		{Role: kernel.RoleUser, Content: []kernel.ContentPart{
			{Image: &kernel.ImageContent{URL: "gs://bucket/image.png", MimeType: "image/png"}},
		}},
	}

	contents, _ := convertMessages(msgs)
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}
	fd := contents[0].Parts[0].FileData
	if fd == nil {
		t.Fatalf("expected FileData part, got nil")
	}
	if fd.FileURI != "gs://bucket/image.png" {
		t.Errorf("expected URI 'gs://bucket/image.png', got %q", fd.FileURI)
	}
	if fd.MIMEType != "image/png" {
		t.Errorf("expected MIME 'image/png', got %q", fd.MIMEType)
	}
}

func TestConvertMessages_Empty(t *testing.T) {
	contents, sysInstruction := convertMessages(nil)
	if len(contents) != 0 {
		t.Errorf("expected 0 contents, got %d", len(contents))
	}
	if sysInstruction != nil {
		t.Errorf("expected nil system instruction, got %v", sysInstruction)
	}
}

// --- Response conversion tests ---

func TestConvertResponse_TextOnly(t *testing.T) {
	genaiResp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{
				Parts: []*genai.Part{
					{Text: "Hello "},
					{Text: "world"},
				},
			},
			FinishReason: genai.FinishReasonStop,
		}},
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     genai.Ptr[int32](10),
			CandidatesTokenCount: genai.Ptr[int32](5),
			TotalTokenCount:      15,
		},
	}

	resp := convertResponse(genaiResp)
	if resp.Text != "Hello world" {
		t.Errorf("expected text 'Hello world', got %q", resp.Text)
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls, got %d", len(resp.ToolCalls))
	}
	if resp.FinishReason != "stop" {
		t.Errorf("expected finish reason 'stop', got %q", resp.FinishReason)
	}
	if resp.Usage.InputTokens != 10 {
		t.Errorf("expected input tokens 10, got %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 5 {
		t.Errorf("expected output tokens 5, got %d", resp.Usage.OutputTokens)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("expected total tokens 15, got %d", resp.Usage.TotalTokens)
	}
}

func TestConvertResponse_WithToolCalls(t *testing.T) {
	genaiResp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{
				Parts: []*genai.Part{
					{FunctionCall: &genai.FunctionCall{
						Name: "search",
						Args: map[string]any{"query": "golang"},
					}},
					{FunctionCall: &genai.FunctionCall{
						Name: "calculate",
						Args: map[string]any{"expr": "2+2"},
					}},
				},
			},
			FinishReason: genai.FinishReasonStop,
		}},
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{},
	}

	resp := convertResponse(genaiResp)
	if resp.FinishReason != "tool_calls" {
		t.Errorf("expected finish reason 'tool_calls', got %q", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "search" {
		t.Errorf("expected tool call name 'search', got %q", resp.ToolCalls[0].Name)
	}
	if resp.ToolCalls[0].ID == "" {
		t.Error("expected non-empty tool call ID")
	}

	var params map[string]any
	if err := json.Unmarshal(resp.ToolCalls[0].Params, &params); err != nil {
		t.Fatalf("failed to unmarshal tool call params: %v", err)
	}
	if params["query"] != "golang" {
		t.Errorf("expected query 'golang', got %v", params["query"])
	}
}

func TestConvertResponse_NoCandidates(t *testing.T) {
	genaiResp := &genai.GenerateContentResponse{
		Candidates:    nil,
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{TotalTokenCount: 5},
	}

	resp := convertResponse(genaiResp)
	if resp.Text != "" {
		t.Errorf("expected empty text, got %q", resp.Text)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("expected finish reason 'stop', got %q", resp.FinishReason)
	}
}

func TestConvertResponse_NilUsageMetadata(t *testing.T) {
	genaiResp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content:      &genai.Content{Parts: []*genai.Part{{Text: "hi"}}},
			FinishReason: genai.FinishReasonStop,
		}},
		UsageMetadata: nil,
	}

	resp := convertResponse(genaiResp)
	if resp.Usage.InputTokens != 0 {
		t.Errorf("expected 0 input tokens, got %d", resp.Usage.InputTokens)
	}
}

func TestConvertFinishReason(t *testing.T) {
	tests := []struct {
		in  genai.FinishReason
		out string
	}{
		{genai.FinishReasonStop, "stop"},
		{genai.FinishReasonMaxTokens, "max_tokens"},
		{genai.FinishReasonSafety, "safety"},
		{genai.FinishReasonRecitation, "safety"},
		{genai.FinishReason("UNKNOWN"), "stop"},
	}
	for _, tt := range tests {
		got := convertFinishReason(tt.in)
		if got != tt.out {
			t.Errorf("convertFinishReason(%v) = %q, want %q", tt.in, got, tt.out)
		}
	}
}

// --- GenerateOptions conversion tests ---

func TestBuildConfig_Temperature(t *testing.T) {
	temp := float32(0.7)
	opts := kernel.GenerateOptions{Temperature: &temp}
	cfg := buildConfig(opts, nil, nil)
	if cfg.Temperature == nil || *cfg.Temperature != 0.7 {
		t.Errorf("expected temperature 0.7, got %v", cfg.Temperature)
	}
}

func TestBuildConfig_MaxTokens(t *testing.T) {
	max := 1024
	opts := kernel.GenerateOptions{MaxTokens: &max}
	cfg := buildConfig(opts, nil, nil)
	if cfg.MaxOutputTokens == nil || *cfg.MaxOutputTokens != int32(1024) {
		t.Errorf("expected max output tokens 1024, got %v", cfg.MaxOutputTokens)
	}
}

func TestBuildConfig_StopSequences(t *testing.T) {
	opts := kernel.GenerateOptions{StopSequences: []string{"END", "STOP"}}
	cfg := buildConfig(opts, nil, nil)
	if len(cfg.StopSequences) != 2 || cfg.StopSequences[0] != "END" {
		t.Errorf("expected stop sequences [END STOP], got %v", cfg.StopSequences)
	}
}

func TestBuildConfig_OutputSchema(t *testing.T) {
	schema := &kernel.Schema{
		Type: "object",
		Properties: map[string]kernel.Schema{
			"answer": {Type: "string"},
		},
		Required: []string{"answer"},
	}
	opts := kernel.GenerateOptions{OutputSchema: schema}
	cfg := buildConfig(opts, nil, nil)
	if cfg.ResponseMIMEType != "application/json" {
		t.Errorf("expected ResponseMIMEType 'application/json', got %q", cfg.ResponseMIMEType)
	}
	if cfg.ResponseSchema == nil {
		t.Fatal("expected ResponseSchema to be set")
	}
	if cfg.ResponseSchema.Type != genai.TypeObject {
		t.Errorf("expected TypeObject, got %v", cfg.ResponseSchema.Type)
	}
}

func TestBuildConfig_CachedContent(t *testing.T) {
	cfg := buildConfig(kernel.GenerateOptions{}, nil, ptrStr("cached-abc"))
	if cfg.CachedContent != "cached-abc" {
		t.Errorf("expected cached content 'cached-abc', got %q", cfg.CachedContent)
	}
}

func ptrStr(s string) *string { return &s }

// --- Edge case tests ---

func TestConvertSchema_NestedObject(t *testing.T) {
	s := kernel.Schema{
		Type: "object",
		Properties: map[string]kernel.Schema{
			"address": {
				Type: "object",
				Properties: map[string]kernel.Schema{
					"street": {Type: "string"},
					"city":   {Type: "string"},
					"zip":    {Type: "string"},
				},
				Required: []string{"street", "city"},
			},
		},
		Required: []string{"address"},
	}
	gs := convertSchema(s)
	addr, ok := gs.Properties["address"]
	if !ok {
		t.Fatal("expected 'address' property")
	}
	if addr.Type != genai.TypeObject {
		t.Errorf("expected TypeObject for address, got %v", addr.Type)
	}
	if len(addr.Properties) != 3 {
		t.Errorf("expected 3 address properties, got %d", len(addr.Properties))
	}
	street, ok := addr.Properties["street"]
	if !ok {
		t.Fatal("expected 'street' sub-property")
	}
	if street.Type != genai.TypeString {
		t.Errorf("expected TypeString for street, got %v", street.Type)
	}
}

func TestConvertMessages_MixedParts(t *testing.T) {
	msgs := []kernel.Message{
		{Role: kernel.RoleUser, Content: []kernel.ContentPart{
			{Text: strPtr("Describe this image:")},
			{Image: &kernel.ImageContent{URL: "gs://bucket/photo.jpg", MimeType: "image/jpeg"}},
		}},
	}

	contents, _ := convertMessages(msgs)
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}
	if len(contents[0].Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(contents[0].Parts))
	}
	if contents[0].Parts[0].Text == "" {
		t.Errorf("expected first part to have Text, got empty")
	}
	if contents[0].Parts[1].FileData == nil {
		t.Errorf("expected second part to have FileData, got nil")
	}
}

func TestConvertMessages_ToolResultNonJSON(t *testing.T) {
	msgs := []kernel.Message{
		{Role: kernel.RoleTool, Content: []kernel.ContentPart{
			{ToolResult: &kernel.ToolResult{
				ToolCallID: "call-1",
				Name:       "read_file",
				Content:    "plain text result, not JSON",
			}},
		}},
	}

	contents, _ := convertMessages(msgs)
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}
	fr := contents[0].Parts[0].FunctionResponse
	if fr == nil {
		t.Fatal("expected FunctionResponse part")
	}
	// Non-JSON content should be wrapped in {"result": "..."} map
	if fr.Response["result"] != "plain text result, not JSON" {
		t.Errorf("expected wrapped result, got %v", fr.Response)
	}
}

func TestBuildConfig_SafetySettings(t *testing.T) {
	settings := []*genai.SafetySetting{
		{Category: genai.HarmCategoryHateSpeech, Threshold: genai.HarmBlockThresholdBlockNone},
	}
	cfg := buildConfig(kernel.GenerateOptions{}, settings, nil)
	if len(cfg.SafetySettings) != 1 {
		t.Fatalf("expected 1 safety setting, got %d", len(cfg.SafetySettings))
	}
	if cfg.SafetySettings[0].Category != genai.HarmCategoryHateSpeech {
		t.Errorf("expected HarmCategoryHateSpeech, got %v", cfg.SafetySettings[0].Category)
	}
}

func TestBuildConfig_Empty(t *testing.T) {
	cfg := buildConfig(kernel.GenerateOptions{}, nil, nil)
	if cfg.Temperature != nil {
		t.Errorf("expected nil temperature, got %v", cfg.Temperature)
	}
	if cfg.MaxOutputTokens != nil {
		t.Errorf("expected nil max output tokens, got %v", cfg.MaxOutputTokens)
	}
	if cfg.ResponseMIMEType != "" {
		t.Errorf("expected empty response MIME type, got %q", cfg.ResponseMIMEType)
	}
}
