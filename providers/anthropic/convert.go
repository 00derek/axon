package anthropic

import (
	"encoding/json"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/axonframework/axon/kernel"
)

// schemaToMap recursively converts a kernel.Schema into a JSON-Schema-shaped
// map[string]any. Anthropic's tool input_schema accepts arbitrary JSON, so we
// emit the map directly.
func schemaToMap(s kernel.Schema) map[string]any {
	m := map[string]any{}
	if s.Type != "" {
		m["type"] = s.Type
	}
	if s.Description != "" {
		m["description"] = s.Description
	}
	if len(s.Enum) > 0 {
		m["enum"] = s.Enum
	}
	if s.Minimum != nil {
		m["minimum"] = *s.Minimum
	}
	if s.Maximum != nil {
		m["maximum"] = *s.Maximum
	}
	if s.Items != nil {
		m["items"] = schemaToMap(*s.Items)
	}
	if len(s.Properties) > 0 {
		props := make(map[string]any, len(s.Properties))
		for name, p := range s.Properties {
			props[name] = schemaToMap(p)
		}
		m["properties"] = props
	}
	if len(s.Required) > 0 {
		m["required"] = s.Required
	}
	return m
}

// convertTools translates kernel tools into Anthropic ToolUnionParam entries.
// Each kernel tool becomes a client ToolParam with its input_schema populated
// from the tool's Schema.
func convertTools(tools []kernel.Tool) []anthropic.ToolUnionParam {
	if len(tools) == 0 {
		return nil
	}
	result := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		schema := t.Schema()
		props := make(map[string]any, len(schema.Properties))
		for name, p := range schema.Properties {
			props[name] = schemaToMap(p)
		}
		tp := &anthropic.ToolParam{
			Name:        t.Name(),
			Description: anthropic.String(t.Description()),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: props,
				Required:   schema.Required,
			},
		}
		result = append(result, anthropic.ToolUnionParam{OfTool: tp})
	}
	return result
}

// convertToolChoice translates kernel.ToolChoice into Anthropic's union param.
// A zero-valued ToolChoice produces a zero-valued ToolChoiceUnionParam (no
// constraint) which Anthropic treats as implicit "auto".
func convertToolChoice(tc kernel.ToolChoice) anthropic.ToolChoiceUnionParam {
	switch tc.Type {
	case "auto":
		return anthropic.ToolChoiceUnionParam{OfAuto: &anthropic.ToolChoiceAutoParam{}}
	case "required":
		return anthropic.ToolChoiceUnionParam{OfAny: &anthropic.ToolChoiceAnyParam{}}
	case "none":
		return anthropic.ToolChoiceUnionParam{OfNone: &anthropic.ToolChoiceNoneParam{}}
	case "tool":
		return anthropic.ToolChoiceUnionParam{OfTool: &anthropic.ToolChoiceToolParam{Name: tc.ToolName}}
	default:
		return anthropic.ToolChoiceUnionParam{}
	}
}

// convertMessages splits a kernel conversation into Anthropic message params
// plus a separate system-prompt slice. Anthropic requires the system prompt
// as a top-level field on MessageNewParams rather than inline in Messages.
// kernel tool-result messages are sent with role=user, matching Anthropic's
// contract for tool_result content blocks.
func convertMessages(msgs []kernel.Message) ([]anthropic.MessageParam, []anthropic.TextBlockParam) {
	var (
		out    []anthropic.MessageParam
		system []anthropic.TextBlockParam
	)

	for _, msg := range msgs {
		switch msg.Role {
		case kernel.RoleSystem:
			for _, cp := range msg.Content {
				if cp.Text != nil {
					system = append(system, anthropic.TextBlockParam{Text: *cp.Text})
				}
			}

		case kernel.RoleUser:
			blocks := convertContentParts(msg.Content)
			if len(blocks) > 0 {
				out = append(out, anthropic.NewUserMessage(blocks...))
			}

		case kernel.RoleAssistant:
			blocks := convertContentParts(msg.Content)
			if len(blocks) > 0 {
				out = append(out, anthropic.NewAssistantMessage(blocks...))
			}

		case kernel.RoleTool:
			// Tool results are sent as user messages per the Anthropic API.
			blocks := convertContentParts(msg.Content)
			if len(blocks) > 0 {
				out = append(out, anthropic.NewUserMessage(blocks...))
			}
		}
	}

	return out, system
}

// convertContentParts translates kernel content parts into Anthropic's content
// block params. Text, Image, ToolCall, and ToolResult are supported.
func convertContentParts(parts []kernel.ContentPart) []anthropic.ContentBlockParamUnion {
	var result []anthropic.ContentBlockParamUnion
	for _, cp := range parts {
		switch {
		case cp.Text != nil:
			result = append(result, anthropic.NewTextBlock(*cp.Text))

		case cp.Image != nil:
			result = append(result, anthropic.NewImageBlock(
				anthropic.URLImageSourceParam{URL: cp.Image.URL},
			))

		case cp.ToolCall != nil:
			// Input is marshaled as JSON by the SDK. Passing json.RawMessage
			// preserves the parameters verbatim.
			var input any = cp.ToolCall.Params
			if len(cp.ToolCall.Params) == 0 {
				input = map[string]any{}
			}
			result = append(result, anthropic.NewToolUseBlock(
				cp.ToolCall.ID, input, cp.ToolCall.Name,
			))

		case cp.ToolResult != nil:
			result = append(result, anthropic.NewToolResultBlock(
				cp.ToolResult.ToolCallID,
				cp.ToolResult.Content,
				cp.ToolResult.IsError,
			))
		}
	}
	return result
}

// convertResponse translates an Anthropic *Message into a kernel.Response.
// Text blocks are concatenated; tool_use blocks become kernel.ToolCalls. Tool
// call IDs are preserved from the API (do NOT regenerate). When tool calls
// are present, FinishReason is forced to "tool_calls" regardless of StopReason.
func convertResponse(msg *anthropic.Message) kernel.Response {
	var kr kernel.Response
	if msg == nil {
		kr.FinishReason = "stop"
		return kr
	}

	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			tb := block.AsText()
			kr.Text += tb.Text
		case "tool_use":
			tub := block.AsToolUse()
			params := tub.Input
			if len(params) == 0 {
				params = json.RawMessage("{}")
			}
			kr.ToolCalls = append(kr.ToolCalls, kernel.ToolCall{
				ID:     tub.ID,
				Name:   tub.Name,
				Params: params,
			})
		}
	}

	if len(kr.ToolCalls) > 0 {
		kr.FinishReason = "tool_calls"
	} else {
		kr.FinishReason = convertStopReason(msg.StopReason)
	}

	kr.Usage = kernel.Usage{
		InputTokens:  int(msg.Usage.InputTokens),
		OutputTokens: int(msg.Usage.OutputTokens),
		TotalTokens:  int(msg.Usage.InputTokens + msg.Usage.OutputTokens),
	}

	return kr
}

// convertStopReason maps Anthropic StopReason values onto kernel finish reasons.
func convertStopReason(sr anthropic.StopReason) string {
	switch sr {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "pause_turn":
		return "pause"
	case "refusal":
		return "refusal"
	default:
		if sr == "" {
			return "stop"
		}
		return string(sr)
	}
}

// buildParams constructs a MessageNewParams ready for client.Messages.New or
// NewStreaming. Model and MaxTokens are required by the Anthropic API.
func buildParams(
	model string,
	maxTokens int64,
	metadata *anthropic.MetadataParam,
	p kernel.GenerateParams,
) anthropic.MessageNewParams {
	msgs, system := convertMessages(p.Messages)

	params := anthropic.MessageNewParams{
		Model:     model,
		MaxTokens: maxTokens,
		Messages:  msgs,
	}

	if len(system) > 0 {
		params.System = system
	}

	if len(p.Tools) > 0 {
		params.Tools = convertTools(p.Tools)
		params.ToolChoice = convertToolChoice(p.Options.ToolChoice)
	}

	if p.Options.Temperature != nil {
		params.Temperature = anthropic.Float(float64(*p.Options.Temperature))
	}
	if len(p.Options.StopSequences) > 0 {
		params.StopSequences = p.Options.StopSequences
	}
	if metadata != nil {
		params.Metadata = *metadata
	}

	return params
}
