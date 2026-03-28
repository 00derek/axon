package google

import (
	"context"
	"fmt"
	"iter"
	"testing"

	"github.com/axonframework/axon/kernel"
	"google.golang.org/genai"
)

// fakeModelsClient implements modelsClient for testing.
type fakeModelsClient struct {
	generateFn       func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)
	generateStreamFn func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error]
}

func (f *fakeModelsClient) GenerateContent(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	if f.generateFn != nil {
		return f.generateFn(ctx, model, contents, config)
	}
	return nil, fmt.Errorf("GenerateContent not configured")
}

func (f *fakeModelsClient) GenerateContentStream(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error] {
	if f.generateStreamFn != nil {
		return f.generateStreamFn(ctx, model, contents, config)
	}
	return func(yield func(*genai.GenerateContentResponse, error) bool) {
		yield(nil, fmt.Errorf("GenerateContentStream not configured"))
	}
}

// --- New tests ---

func TestNew(t *testing.T) {
	llm := New(nil, "gemini-2.0-flash")
	if llm.Model() != "gemini-2.0-flash" {
		t.Errorf("expected model 'gemini-2.0-flash', got %q", llm.Model())
	}
}

func TestNew_WithOptions(t *testing.T) {
	settings := []*genai.SafetySetting{
		{Category: genai.HarmCategoryHateSpeech, Threshold: genai.HarmBlockThresholdBlockNone},
	}
	llm := New(nil, "gemini-2.0-flash",
		WithSafetySettings(settings),
		WithCachedContent("cache-xyz"),
	)
	if llm.safetySettings[0].Category != genai.HarmCategoryHateSpeech {
		t.Error("expected safety setting to be applied")
	}
	if llm.cachedContent == nil || *llm.cachedContent != "cache-xyz" {
		t.Error("expected cached content to be set")
	}
}

// --- Generate tests ---

func TestGenerate_TextResponse(t *testing.T) {
	fake := &fakeModelsClient{
		generateFn: func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
			if model != "gemini-2.0-flash" {
				t.Errorf("expected model 'gemini-2.0-flash', got %q", model)
			}
			return &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{
					Content: &genai.Content{
						Parts: []*genai.Part{{Text: "Hello from Gemini"}},
					},
					FinishReason: genai.FinishReasonStop,
				}},
				UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
					PromptTokenCount:     genai.Ptr[int32](8),
					CandidatesTokenCount: genai.Ptr[int32](4),
					TotalTokenCount:      12,
				},
			}, nil
		},
	}

	llm := &GoogleLLM{model: "gemini-2.0-flash", mc: fake}
	resp, err := llm.Generate(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("Hi")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "Hello from Gemini" {
		t.Errorf("expected text 'Hello from Gemini', got %q", resp.Text)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("expected finish reason 'stop', got %q", resp.FinishReason)
	}
	if resp.Usage.InputTokens != 8 {
		t.Errorf("expected 8 input tokens, got %d", resp.Usage.InputTokens)
	}
}

func TestGenerate_ToolCallResponse(t *testing.T) {
	fake := &fakeModelsClient{
		generateFn: func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
			return &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{
					Content: &genai.Content{
						Parts: []*genai.Part{
							{FunctionCall: &genai.FunctionCall{
								Name: "search",
								Args: map[string]any{"query": "weather"},
							}},
						},
					},
					FinishReason: genai.FinishReasonStop,
				}},
				UsageMetadata: &genai.GenerateContentResponseUsageMetadata{},
			}, nil
		},
	}

	llm := &GoogleLLM{model: "gemini-2.0-flash", mc: fake}
	resp, err := llm.Generate(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("What is the weather?")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("expected finish reason 'tool_calls', got %q", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "search" {
		t.Errorf("expected tool name 'search', got %q", resp.ToolCalls[0].Name)
	}
}

func TestGenerate_APIError(t *testing.T) {
	fake := &fakeModelsClient{
		generateFn: func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
			return nil, fmt.Errorf("quota exceeded")
		},
	}

	llm := &GoogleLLM{model: "gemini-2.0-flash", mc: fake}
	_, err := llm.Generate(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("Hi")},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "google generate: quota exceeded" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestGenerate_SystemMessageExtracted(t *testing.T) {
	var capturedConfig *genai.GenerateContentConfig
	var capturedContents []*genai.Content

	fake := &fakeModelsClient{
		generateFn: func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
			capturedConfig = config
			capturedContents = contents
			return &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{
					Content:      &genai.Content{Parts: []*genai.Part{{Text: "ok"}}},
					FinishReason: genai.FinishReasonStop,
				}},
				UsageMetadata: &genai.GenerateContentResponseUsageMetadata{},
			}, nil
		},
	}

	llm := &GoogleLLM{model: "gemini-2.0-flash", mc: fake}
	_, err := llm.Generate(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{
			kernel.SystemMsg("You are a helpful assistant"),
			kernel.UserMsg("Hello"),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedConfig.SystemInstruction == nil {
		t.Fatal("expected system instruction in config")
	}
	if len(capturedContents) != 1 {
		t.Fatalf("expected 1 content (user only), got %d", len(capturedContents))
	}
	if capturedContents[0].Role != "user" {
		t.Errorf("expected user role, got %q", capturedContents[0].Role)
	}
}

func TestGenerate_WithTools(t *testing.T) {
	var capturedConfig *genai.GenerateContentConfig

	fake := &fakeModelsClient{
		generateFn: func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
			capturedConfig = config
			return &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{
					Content:      &genai.Content{Parts: []*genai.Part{{Text: "ok"}}},
					FinishReason: genai.FinishReasonStop,
				}},
				UsageMetadata: &genai.GenerateContentResponseUsageMetadata{},
			}, nil
		},
	}

	tool := &fakeKernelTool{
		name: "lookup",
		desc: "Look things up",
		sch:  kernel.Schema{Type: "object", Properties: map[string]kernel.Schema{"q": {Type: "string"}}, Required: []string{"q"}},
	}

	llm := &GoogleLLM{model: "gemini-2.0-flash", mc: fake}
	_, err := llm.Generate(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("find X")},
		Tools:    []kernel.Tool{tool},
		Options:  kernel.GenerateOptions{ToolChoice: kernel.ToolChoiceRequired},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(capturedConfig.Tools) != 1 {
		t.Fatalf("expected 1 tool group, got %d", len(capturedConfig.Tools))
	}
	if capturedConfig.ToolConfig == nil {
		t.Fatal("expected ToolConfig to be set")
	}
	if capturedConfig.ToolConfig.FunctionCallingConfig.Mode != genai.FunctionCallingConfigModeAny {
		t.Errorf("expected FunctionCallingConfigModeAny, got %v", capturedConfig.ToolConfig.FunctionCallingConfig.Mode)
	}
}

func TestGenerate_WithOptions(t *testing.T) {
	var capturedConfig *genai.GenerateContentConfig

	fake := &fakeModelsClient{
		generateFn: func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
			capturedConfig = config
			return &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{
					Content:      &genai.Content{Parts: []*genai.Part{{Text: "ok"}}},
					FinishReason: genai.FinishReasonStop,
				}},
				UsageMetadata: &genai.GenerateContentResponseUsageMetadata{},
			}, nil
		},
	}

	temp := float32(0.5)
	maxTok := 2048

	llm := &GoogleLLM{model: "gemini-2.0-flash", mc: fake}
	_, err := llm.Generate(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("test")},
		Options: kernel.GenerateOptions{
			Temperature:   &temp,
			MaxTokens:     &maxTok,
			StopSequences: []string{"DONE"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedConfig.Temperature == nil || *capturedConfig.Temperature != 0.5 {
		t.Errorf("expected temperature 0.5, got %v", capturedConfig.Temperature)
	}
	if capturedConfig.MaxOutputTokens == nil || *capturedConfig.MaxOutputTokens != 2048 {
		t.Errorf("expected max tokens 2048, got %v", capturedConfig.MaxOutputTokens)
	}
}

// --- GenerateStream tests ---

func TestGenerateStream_TextOnly(t *testing.T) {
	fake := &fakeModelsClient{
		generateStreamFn: func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error] {
			return makeTestIterator([]*genai.GenerateContentResponse{
				{
					Candidates: []*genai.Candidate{{
						Content: &genai.Content{Parts: []*genai.Part{{Text: "Hello "}}},
					}},
				},
				{
					Candidates: []*genai.Candidate{{
						Content:      &genai.Content{Parts: []*genai.Part{{Text: "world"}}},
						FinishReason: genai.FinishReasonStop,
					}},
					UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
						PromptTokenCount:     genai.Ptr[int32](5),
						CandidatesTokenCount: genai.Ptr[int32](3),
						TotalTokenCount:      8,
					},
				},
			}, nil)
		},
	}

	llm := &GoogleLLM{model: "gemini-2.0-flash", mc: fake}
	stream, err := llm.GenerateStream(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("Hi")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Drain events
	var texts []string
	for txt := range stream.Text() {
		texts = append(texts, txt)
	}

	if len(texts) != 2 {
		t.Fatalf("expected 2 text chunks, got %d", len(texts))
	}

	resp := stream.Response()
	if resp.Text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", resp.Text)
	}
	if resp.Usage.TotalTokens != 8 {
		t.Errorf("expected 8 total tokens, got %d", resp.Usage.TotalTokens)
	}
	if stream.Err() != nil {
		t.Errorf("unexpected error: %v", stream.Err())
	}
}

func TestGenerateStream_Error(t *testing.T) {
	fake := &fakeModelsClient{
		generateStreamFn: func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error] {
			return makeTestIterator(nil, fmt.Errorf("stream error"))
		},
	}

	llm := &GoogleLLM{model: "gemini-2.0-flash", mc: fake}
	stream, err := llm.GenerateStream(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("Hi")},
	})
	if err != nil {
		t.Fatalf("unexpected error from GenerateStream: %v", err)
	}

	// Drain
	for range stream.Events() {
	}

	if stream.Err() == nil {
		t.Fatal("expected stream error, got nil")
	}
}

func TestGenerateStream_WithSystemAndTools(t *testing.T) {
	var capturedConfig *genai.GenerateContentConfig

	fake := &fakeModelsClient{
		generateStreamFn: func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error] {
			capturedConfig = config
			return makeTestIterator([]*genai.GenerateContentResponse{
				{
					Candidates: []*genai.Candidate{{
						Content:      &genai.Content{Parts: []*genai.Part{{Text: "ok"}}},
						FinishReason: genai.FinishReasonStop,
					}},
					UsageMetadata: &genai.GenerateContentResponseUsageMetadata{},
				},
			}, nil)
		},
	}

	tool := &fakeKernelTool{
		name: "lookup",
		desc: "Look things up",
		sch:  kernel.Schema{Type: "object"},
	}

	llm := &GoogleLLM{model: "gemini-2.0-flash", mc: fake}
	stream, err := llm.GenerateStream(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{
			kernel.SystemMsg("Be concise"),
			kernel.UserMsg("test"),
		},
		Tools: []kernel.Tool{tool},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for range stream.Events() {
	}

	if capturedConfig.SystemInstruction == nil {
		t.Error("expected system instruction in streaming config")
	}
	if len(capturedConfig.Tools) != 1 {
		t.Errorf("expected 1 tool group, got %d", len(capturedConfig.Tools))
	}
}
