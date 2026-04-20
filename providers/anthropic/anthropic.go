// Package anthropic provides a kernel.LLM adapter for Anthropic's Claude
// Messages API via github.com/anthropics/anthropic-sdk-go.
package anthropic

import (
	"context"
	"fmt"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"

	"github.com/axonframework/axon/kernel"
)

// defaultMaxTokens is used when the caller does not configure WithMaxTokens.
// The Anthropic API requires max_tokens on every request.
const defaultMaxTokens int64 = 4096

// messagesClient abstracts the sdk.MessageService methods AnthropicLLM uses.
// In production *sdk.MessageService satisfies this interface. In tests a fake
// implementation is injected to avoid network traffic.
type messagesClient interface {
	New(ctx context.Context, body sdk.MessageNewParams, opts ...option.RequestOption) (*sdk.Message, error)
	NewStreaming(ctx context.Context, body sdk.MessageNewParams, opts ...option.RequestOption) *ssestream.Stream[sdk.MessageStreamEventUnion]
}

// AnthropicLLM implements kernel.LLM for Anthropic Claude models.
type AnthropicLLM struct {
	mc        messagesClient
	model     string
	maxTokens int64
	metadata  *sdk.MetadataParam
}

// Compile-time check that AnthropicLLM implements kernel.LLM.
var _ kernel.LLM = (*AnthropicLLM)(nil)

// New constructs an AnthropicLLM for the given model. The client may be nil in
// test scenarios where mc is injected directly.
func New(client *sdk.Client, model string, opts ...Option) *AnthropicLLM {
	a := &AnthropicLLM{
		model:     model,
		maxTokens: defaultMaxTokens,
	}
	if client != nil {
		a.mc = &client.Messages
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Option configures an AnthropicLLM at construction time.
type Option func(*AnthropicLLM)

// WithMaxTokens overrides the default max_tokens sent with every request.
// Anthropic's Messages API requires a max_tokens value, so a sensible default
// (4096) is used if this option is not supplied.
func WithMaxTokens(n int64) Option {
	return func(a *AnthropicLLM) { a.maxTokens = n }
}

// WithMetadata attaches a request-level metadata object to every call.
func WithMetadata(m sdk.MetadataParam) Option {
	return func(a *AnthropicLLM) { a.metadata = &m }
}

// Model returns the model identifier in use.
func (a *AnthropicLLM) Model() string { return a.model }

// Generate makes a non-streaming call and returns a kernel.Response.
func (a *AnthropicLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	req := buildParams(a.model, a.maxTokens, a.metadata, params)

	start := time.Now()
	msg, err := a.mc.New(ctx, req)
	latency := time.Since(start)

	if err != nil {
		return kernel.Response{}, fmt.Errorf("anthropic generate: %w", err)
	}

	resp := convertResponse(msg)
	resp.Usage.Latency = latency
	return resp, nil
}

// GenerateStream makes a streaming call and returns a kernel.Stream backed by
// the Anthropic SSE stream.
func (a *AnthropicLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	req := buildParams(a.model, a.maxTokens, a.metadata, params)

	sseStream := a.mc.NewStreaming(ctx, req)
	return newStream(sseStream), nil
}
