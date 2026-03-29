package google

import (
	"context"
	"fmt"
	"iter"
	"time"

	"github.com/axonframework/axon/kernel"
	"google.golang.org/genai"
)

// modelsClient abstracts the genai client methods used by GoogleLLM.
// In production, *genai.Models satisfies this interface.
// In tests, a fake implementation is injected.
type modelsClient interface {
	GenerateContent(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)
	GenerateContentStream(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error]
}

// GoogleLLM implements kernel.LLM for Google Gemini models via the genai SDK.
type GoogleLLM struct {
	mc             modelsClient
	model          string
	safetySettings []*genai.SafetySetting
	cachedContent  *string
}

// Compile-time check that GoogleLLM implements kernel.LLM.
var _ kernel.LLM = (*GoogleLLM)(nil)

// New creates a GoogleLLM using the given genai client and model name.
// The client may be nil in testing scenarios where mc is overridden directly.
func New(client *genai.Client, model string, opts ...Option) *GoogleLLM {
	g := &GoogleLLM{
		model: model,
	}
	if client != nil {
		g.mc = client.Models
	}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// Option configures a GoogleLLM at construction time.
type Option func(*GoogleLLM)

// WithSafetySettings configures safety settings applied to every request.
func WithSafetySettings(settings []*genai.SafetySetting) Option {
	return func(g *GoogleLLM) {
		g.safetySettings = settings
	}
}

// WithCachedContent sets the cached content resource name for requests.
func WithCachedContent(name string) Option {
	return func(g *GoogleLLM) {
		g.cachedContent = &name
	}
}

// Model returns the model name.
func (g *GoogleLLM) Model() string {
	return g.model
}

// Generate makes a non-streaming call to the Gemini API and returns a kernel.Response.
func (g *GoogleLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	contents, sysInstruction := convertMessages(params.Messages)

	cfg := buildConfig(params.Options, g.safetySettings, g.cachedContent)
	cfg.SystemInstruction = sysInstruction

	if len(params.Tools) > 0 {
		cfg.Tools = convertTools(params.Tools)
		if tc := convertToolChoice(params.Options.ToolChoice); tc != nil {
			cfg.ToolConfig = tc
		}
	}

	start := time.Now()
	genaiResp, err := g.mc.GenerateContent(ctx, g.model, contents, cfg)
	latency := time.Since(start)

	if err != nil {
		return kernel.Response{}, fmt.Errorf("google generate: %w", err)
	}

	resp := convertResponse(genaiResp)
	resp.Usage.Latency = latency
	return resp, nil
}

// GenerateStream makes a streaming call to the Gemini API and returns a kernel.Stream.
func (g *GoogleLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	contents, sysInstruction := convertMessages(params.Messages)

	cfg := buildConfig(params.Options, g.safetySettings, g.cachedContent)
	cfg.SystemInstruction = sysInstruction

	if len(params.Tools) > 0 {
		cfg.Tools = convertTools(params.Tools)
		if tc := convertToolChoice(params.Options.ToolChoice); tc != nil {
			cfg.ToolConfig = tc
		}
	}

	iterator := g.mc.GenerateContentStream(ctx, g.model, contents, cfg)

	return newStream(iterator), nil
}
