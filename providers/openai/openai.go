// Package openai provides a kernel.LLM adapter for the OpenAI Chat Completions
// API via github.com/openai/openai-go/v3.
package openai

import (
	"context"
	"fmt"
	"time"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/ssestream"

	"github.com/axonframework/axon/kernel"
)

// chatCompletionsClient abstracts the sdk.ChatCompletionService methods used
// by OpenAILLM. In production *sdk.ChatCompletionService satisfies this
// interface; tests inject a fake.
type chatCompletionsClient interface {
	New(ctx context.Context, body sdk.ChatCompletionNewParams, opts ...option.RequestOption) (*sdk.ChatCompletion, error)
	NewStreaming(ctx context.Context, body sdk.ChatCompletionNewParams, opts ...option.RequestOption) *ssestream.Stream[sdk.ChatCompletionChunk]
}

// OpenAILLM implements kernel.LLM for OpenAI Chat Completions models.
type OpenAILLM struct {
	cc              chatCompletionsClient
	model           string
	user            string
	reasoningEffort string
	store           *bool
}

// Compile-time check.
var _ kernel.LLM = (*OpenAILLM)(nil)

// New constructs an OpenAILLM for the given model. The client may be nil in
// tests where cc is injected directly.
func New(client *sdk.Client, model string, opts ...Option) *OpenAILLM {
	o := &OpenAILLM{model: model}
	if client != nil {
		o.cc = &client.Chat.Completions
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// Option configures an OpenAILLM at construction time.
type Option func(*OpenAILLM)

// WithUser attaches a stable end-user identifier to every request. Helps
// OpenAI detect abuse and improves prompt-cache hit rates.
func WithUser(u string) Option {
	return func(o *OpenAILLM) { o.user = u }
}

// WithReasoningEffort sets the reasoning effort for reasoning-capable models.
// Valid values: "none", "minimal", "low", "medium", "high", "xhigh".
func WithReasoningEffort(e string) Option {
	return func(o *OpenAILLM) { o.reasoningEffort = e }
}

// WithStore toggles server-side storage of completions (for distillation/evals).
func WithStore(b bool) Option {
	return func(o *OpenAILLM) { o.store = &b }
}

// Model returns the model identifier in use.
func (o *OpenAILLM) Model() string { return o.model }

// Generate makes a non-streaming call and returns a kernel.Response.
func (o *OpenAILLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	req := buildParams(o.model, o.user, o.reasoningEffort, o.store, params, false)

	start := time.Now()
	resp, err := o.cc.New(ctx, req)
	latency := time.Since(start)

	if err != nil {
		return kernel.Response{}, fmt.Errorf("openai generate: %w", err)
	}

	kr := convertResponse(resp)
	kr.Usage.Latency = latency
	return kr, nil
}

// GenerateStream makes a streaming call and returns a kernel.Stream backed by
// the OpenAI SSE stream. IncludeUsage is enabled so the final chunk carries
// token counts.
func (o *OpenAILLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	req := buildParams(o.model, o.user, o.reasoningEffort, o.store, params, true)
	sseStream := o.cc.NewStreaming(ctx, req)
	return newStream(sseStream), nil
}
