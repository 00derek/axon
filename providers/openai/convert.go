package openai

import (
	"encoding/json"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"

	"github.com/axonframework/axon/kernel"
)

// schemaToMap converts a kernel.Schema into a JSON-Schema map. OpenAI's
// function parameters accept shared.FunctionParameters (map[string]any).
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

// convertTools translates kernel tools into OpenAI function tool params.
func convertTools(tools []kernel.Tool) []sdk.ChatCompletionToolUnionParam {
	if len(tools) == 0 {
		return nil
	}
	out := make([]sdk.ChatCompletionToolUnionParam, 0, len(tools))
	for _, t := range tools {
		fn := shared.FunctionDefinitionParam{
			Name:        t.Name(),
			Description: param.NewOpt(t.Description()),
			Parameters:  schemaToMap(t.Schema()),
		}
		out = append(out, sdk.ChatCompletionToolUnionParam{
			OfFunction: &sdk.ChatCompletionFunctionToolParam{Function: fn},
		})
	}
	return out
}

// convertToolChoice translates kernel.ToolChoice into OpenAI's union param.
// For "tool" choice we force a specific function.
func convertToolChoice(tc kernel.ToolChoice) sdk.ChatCompletionToolChoiceOptionUnionParam {
	switch tc.Type {
	case "auto":
		return sdk.ChatCompletionToolChoiceOptionUnionParam{OfAuto: param.NewOpt("auto")}
	case "required":
		return sdk.ChatCompletionToolChoiceOptionUnionParam{OfAuto: param.NewOpt("required")}
	case "none":
		return sdk.ChatCompletionToolChoiceOptionUnionParam{OfAuto: param.NewOpt("none")}
	case "tool":
		return sdk.ChatCompletionToolChoiceOptionUnionParam{
			OfFunctionToolChoice: &sdk.ChatCompletionNamedToolChoiceParam{
				Function: sdk.ChatCompletionNamedToolChoiceFunctionParam{Name: tc.ToolName},
			},
		}
	default:
		return sdk.ChatCompletionToolChoiceOptionUnionParam{}
	}
}

// convertMessages translates a kernel conversation to OpenAI messages. Unlike
// Anthropic/Google, the system prompt stays inline as a system-role message.
// Assistant messages that carry tool-call parts produce an assistant message
// with ToolCalls populated; tool-result messages become role=tool messages
// with the corresponding ToolCallID.
func convertMessages(msgs []kernel.Message) []sdk.ChatCompletionMessageParamUnion {
	out := make([]sdk.ChatCompletionMessageParamUnion, 0, len(msgs))

	for _, msg := range msgs {
		switch msg.Role {
		case kernel.RoleSystem:
			out = append(out, sdk.SystemMessage(msg.TextContent()))

		case kernel.RoleUser:
			// Mixed text/image parts become an array-of-parts user message.
			// Pure text messages use the string-content form.
			if hasOnlyText(msg.Content) {
				out = append(out, sdk.UserMessage(msg.TextContent()))
			} else {
				parts := userContentParts(msg.Content)
				out = append(out, sdk.UserMessage(parts))
			}

		case kernel.RoleAssistant:
			out = append(out, buildAssistantMessage(msg))

		case kernel.RoleTool:
			// kernel represents multiple tool results as separate ContentParts
			// within a single message; OpenAI requires one tool message per call.
			for _, cp := range msg.Content {
				if cp.ToolResult == nil {
					continue
				}
				out = append(out, sdk.ToolMessage(cp.ToolResult.Content, cp.ToolResult.ToolCallID))
			}
		}
	}

	return out
}

// hasOnlyText reports whether all parts are plain text. Used to short-circuit
// user messages to the simpler string-content form.
func hasOnlyText(parts []kernel.ContentPart) bool {
	for _, cp := range parts {
		if cp.Text == nil {
			return false
		}
	}
	return true
}

// userContentParts builds the array-of-parts union for multimodal user input.
func userContentParts(parts []kernel.ContentPart) []sdk.ChatCompletionContentPartUnionParam {
	var out []sdk.ChatCompletionContentPartUnionParam
	for _, cp := range parts {
		switch {
		case cp.Text != nil:
			out = append(out, sdk.ChatCompletionContentPartUnionParam{
				OfText: &sdk.ChatCompletionContentPartTextParam{Text: *cp.Text},
			})
		case cp.Image != nil:
			out = append(out, sdk.ChatCompletionContentPartUnionParam{
				OfImageURL: &sdk.ChatCompletionContentPartImageParam{
					ImageURL: sdk.ChatCompletionContentPartImageImageURLParam{URL: cp.Image.URL},
				},
			})
		}
	}
	return out
}

// buildAssistantMessage constructs an assistant message param, splitting text
// (into Content) and tool-call parts (into ToolCalls).
func buildAssistantMessage(msg kernel.Message) sdk.ChatCompletionMessageParamUnion {
	var text string
	var toolCalls []sdk.ChatCompletionMessageToolCallUnionParam
	for _, cp := range msg.Content {
		switch {
		case cp.Text != nil:
			text += *cp.Text
		case cp.ToolCall != nil:
			args := string(cp.ToolCall.Params)
			if args == "" {
				args = "{}"
			}
			toolCalls = append(toolCalls, sdk.ChatCompletionMessageToolCallUnionParam{
				OfFunction: &sdk.ChatCompletionMessageFunctionToolCallParam{
					ID: cp.ToolCall.ID,
					Function: sdk.ChatCompletionMessageFunctionToolCallFunctionParam{
						Name:      cp.ToolCall.Name,
						Arguments: args,
					},
				},
			})
		}
	}

	p := sdk.ChatCompletionAssistantMessageParam{}
	if text != "" {
		p.Content.OfString = param.NewOpt(text)
	}
	if len(toolCalls) > 0 {
		p.ToolCalls = toolCalls
	}
	return sdk.ChatCompletionMessageParamUnion{OfAssistant: &p}
}

// convertResponse translates a ChatCompletion into a kernel.Response. Tool
// call IDs are preserved from the API; Function.Arguments is already a JSON
// string so we assign it directly to json.RawMessage.
func convertResponse(resp *sdk.ChatCompletion) kernel.Response {
	var kr kernel.Response
	if resp == nil || len(resp.Choices) == 0 {
		kr.FinishReason = "stop"
		return kr
	}
	choice := resp.Choices[0]
	msg := choice.Message

	kr.Text = msg.Content

	for _, tc := range msg.ToolCalls {
		// Only function tool calls map to kernel.ToolCall; custom tools are
		// skipped (callers using kernel.NewTool produce "function" shape).
		if tc.Type != "function" {
			continue
		}
		args := tc.Function.Arguments
		if args == "" {
			args = "{}"
		}
		kr.ToolCalls = append(kr.ToolCalls, kernel.ToolCall{
			ID:     tc.ID,
			Name:   tc.Function.Name,
			Params: json.RawMessage(args),
		})
	}

	if len(kr.ToolCalls) > 0 {
		kr.FinishReason = "tool_calls"
	} else {
		kr.FinishReason = normalizeFinishReason(choice.FinishReason)
	}

	kr.Usage = kernel.Usage{
		InputTokens:  int(resp.Usage.PromptTokens),
		OutputTokens: int(resp.Usage.CompletionTokens),
		TotalTokens:  int(resp.Usage.TotalTokens),
	}

	return kr
}

// normalizeFinishReason passes through recognized OpenAI finish reasons and
// defaults unknown/empty values to "stop".
func normalizeFinishReason(fr string) string {
	switch fr {
	case "", "stop":
		return "stop"
	case "length", "tool_calls", "content_filter", "function_call":
		return fr
	default:
		return fr
	}
}

// buildParams assembles a ChatCompletionNewParams from kernel params and
// provider-level options. If `forStream` is true, StreamOptions.IncludeUsage
// is enabled so the final chunk carries token counts.
func buildParams(
	model string,
	user, reasoningEffort string,
	store *bool,
	p kernel.GenerateParams,
	forStream bool,
) sdk.ChatCompletionNewParams {
	params := sdk.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: convertMessages(p.Messages),
	}

	if len(p.Tools) > 0 {
		params.Tools = convertTools(p.Tools)
		params.ToolChoice = convertToolChoice(p.Options.ToolChoice)
	}

	if p.Options.Temperature != nil {
		params.Temperature = param.NewOpt(float64(*p.Options.Temperature))
	}
	if p.Options.MaxTokens != nil {
		params.MaxCompletionTokens = param.NewOpt(int64(*p.Options.MaxTokens))
	}
	if len(p.Options.StopSequences) > 0 {
		params.Stop = sdk.ChatCompletionNewParamsStopUnion{
			OfStringArray: p.Options.StopSequences,
		}
	}

	if user != "" {
		params.User = param.NewOpt(user)
	}
	if reasoningEffort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(reasoningEffort)
	}
	if store != nil {
		params.Store = param.NewOpt(*store)
	}

	if forStream {
		params.StreamOptions = sdk.ChatCompletionStreamOptionsParam{
			IncludeUsage: param.NewOpt(true),
		}
	}

	return params
}
