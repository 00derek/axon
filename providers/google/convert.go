package google

import (
	"encoding/json"
	"fmt"

	"github.com/axonframework/axon/kernel"
	"google.golang.org/genai"
)

// convertSchema translates a kernel.Schema to a *genai.Schema recursively.
func convertSchema(s kernel.Schema) *genai.Schema {
	gs := &genai.Schema{
		Type:        mapSchemaType(s.Type),
		Description: s.Description,
		Enum:        s.Enum,
		Required:    s.Required,
		Minimum:     s.Minimum,
		Maximum:     s.Maximum,
	}

	if s.Items != nil {
		gs.Items = convertSchema(*s.Items)
	}

	if len(s.Properties) > 0 {
		gs.Properties = make(map[string]*genai.Schema, len(s.Properties))
		for name, prop := range s.Properties {
			gs.Properties[name] = convertSchema(prop)
		}
	}

	return gs
}

// mapSchemaType maps a JSON Schema type string to a genai.Type constant.
func mapSchemaType(t string) genai.Type {
	switch t {
	case "object":
		return genai.TypeObject
	case "string":
		return genai.TypeString
	case "integer":
		return genai.TypeInteger
	case "number":
		return genai.TypeNumber
	case "boolean":
		return genai.TypeBoolean
	case "array":
		return genai.TypeArray
	default:
		return genai.TypeString
	}
}

// convertTools translates a slice of kernel.Tool into genai.Tool wrappers.
// All function declarations are grouped into a single genai.Tool.
func convertTools(tools []kernel.Tool) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}

	decls := make([]*genai.FunctionDeclaration, len(tools))
	for i, t := range tools {
		decls[i] = &genai.FunctionDeclaration{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  convertSchema(t.Schema()),
		}
	}

	return []*genai.Tool{{FunctionDeclarations: decls}}
}

// convertToolChoice translates a kernel.ToolChoice to a *genai.ToolConfig.
// Returns nil for zero-value ToolChoice (no constraint).
func convertToolChoice(tc kernel.ToolChoice) *genai.ToolConfig {
	switch tc.Type {
	case "auto":
		return &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode: genai.FunctionCallingConfigModeAuto,
			},
		}
	case "required":
		return &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode: genai.FunctionCallingConfigModeAny,
			},
		}
	case "none":
		return &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode: genai.FunctionCallingConfigModeNone,
			},
		}
	case "tool":
		return &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode:                 genai.FunctionCallingConfigModeAny,
				AllowedFunctionNames: []string{tc.ToolName},
			},
		}
	default:
		return nil
	}
}

// convertMessages splits kernel messages into genai Contents and an optional
// system instruction. System messages are extracted and merged into a single
// Content for use as GenerateContentConfig.SystemInstruction. Tool result
// messages are mapped to role "user" with FunctionResponse parts, as required
// by the Gemini API.
func convertMessages(msgs []kernel.Message) ([]*genai.Content, *genai.Content) {
	var contents []*genai.Content
	var sysParts []*genai.Part

	for _, msg := range msgs {
		switch msg.Role {
		case kernel.RoleSystem:
			for _, cp := range msg.Content {
				if cp.Text != nil {
					sysParts = append(sysParts, genai.NewPartFromText(*cp.Text))
				}
			}

		case kernel.RoleUser:
			parts := convertContentParts(msg.Content)
			if len(parts) > 0 {
				contents = append(contents, &genai.Content{
					Role:  "user",
					Parts: parts,
				})
			}

		case kernel.RoleAssistant:
			parts := convertContentParts(msg.Content)
			if len(parts) > 0 {
				contents = append(contents, &genai.Content{
					Role:  "model",
					Parts: parts,
				})
			}

		case kernel.RoleTool:
			parts := convertContentParts(msg.Content)
			if len(parts) > 0 {
				contents = append(contents, &genai.Content{
					Role:  "user",
					Parts: parts,
				})
			}
		}
	}

	var sysInstruction *genai.Content
	if len(sysParts) > 0 {
		sysInstruction = &genai.Content{Parts: sysParts}
	}

	return contents, sysInstruction
}

// convertContentParts translates kernel ContentParts to genai Parts.
func convertContentParts(parts []kernel.ContentPart) []*genai.Part {
	var result []*genai.Part
	for _, cp := range parts {
		switch {
		case cp.Text != nil:
			result = append(result, genai.NewPartFromText(*cp.Text))

		case cp.Image != nil:
			result = append(result, genai.NewPartFromURI(cp.Image.URL, cp.Image.MimeType))

		case cp.ToolCall != nil:
			args := make(map[string]any)
			if len(cp.ToolCall.Params) > 0 {
				_ = json.Unmarshal(cp.ToolCall.Params, &args)
			}
			result = append(result, genai.NewPartFromFunctionCall(cp.ToolCall.Name, args))

		case cp.ToolResult != nil:
			response := make(map[string]any)
			if err := json.Unmarshal([]byte(cp.ToolResult.Content), &response); err != nil {
				// If content is not valid JSON, wrap it as a string value.
				response = map[string]any{"result": cp.ToolResult.Content}
			}
			result = append(result, genai.NewPartFromFunctionResponse(cp.ToolResult.Name, response))
		}
	}
	return result
}

// convertResponse translates a genai response into a kernel.Response.
// Text parts are concatenated. FunctionCall parts become kernel.ToolCalls
// with generated IDs. If tool calls are present, FinishReason is set to
// "tool_calls" regardless of what the API returned.
func convertResponse(resp *genai.GenerateContentResponse) kernel.Response {
	var kr kernel.Response

	if len(resp.Candidates) > 0 {
		candidate := resp.Candidates[0]

		if candidate.Content != nil {
			for i, part := range candidate.Content.Parts {
				if part.Text != "" {
					kr.Text += part.Text
				}
				if part.FunctionCall != nil {
					params, _ := json.Marshal(part.FunctionCall.Args)
					kr.ToolCalls = append(kr.ToolCalls, kernel.ToolCall{
						ID:     fmt.Sprintf("call_%d", i),
						Name:   part.FunctionCall.Name,
						Params: params,
					})
				}
			}
		}

		if len(kr.ToolCalls) > 0 {
			kr.FinishReason = "tool_calls"
		} else {
			kr.FinishReason = convertFinishReason(candidate.FinishReason)
		}
	} else {
		kr.FinishReason = "stop"
	}

	if resp.UsageMetadata != nil {
		kr.Usage = kernel.Usage{
			InputTokens:  derefInt32(resp.UsageMetadata.PromptTokenCount),
			OutputTokens: derefInt32(resp.UsageMetadata.CandidatesTokenCount),
			TotalTokens:  int(resp.UsageMetadata.TotalTokenCount),
		}
	}

	return kr
}

// derefInt32 safely dereferences an *int32, returning 0 if nil.
func derefInt32(p *int32) int {
	if p == nil {
		return 0
	}
	return int(*p)
}

// convertFinishReason maps a genai.FinishReason to a kernel finish reason string.
func convertFinishReason(fr genai.FinishReason) string {
	switch fr {
	case genai.FinishReasonStop:
		return "stop"
	case genai.FinishReasonMaxTokens:
		return "max_tokens"
	case genai.FinishReasonSafety, genai.FinishReasonRecitation:
		return "safety"
	default:
		return "stop"
	}
}

// buildConfig constructs a GenerateContentConfig from kernel options and
// provider-level settings (safety settings, cached content name).
func buildConfig(
	opts kernel.GenerateOptions,
	safetySettings []*genai.SafetySetting,
	cachedContent *string,
) *genai.GenerateContentConfig {
	cfg := &genai.GenerateContentConfig{
		SafetySettings: safetySettings,
	}

	if opts.Temperature != nil {
		cfg.Temperature = genai.Ptr(float32(*opts.Temperature))
	}

	if opts.MaxTokens != nil {
		cfg.MaxOutputTokens = genai.Ptr(int32(*opts.MaxTokens))
	}

	if len(opts.StopSequences) > 0 {
		cfg.StopSequences = opts.StopSequences
	}

	if opts.OutputSchema != nil {
		cfg.ResponseMIMEType = "application/json"
		cfg.ResponseSchema = convertSchema(*opts.OutputSchema)
	}

	if cachedContent != nil {
		cfg.CachedContent = *cachedContent
	}

	return cfg
}
