// middleware/cascade_test.go
package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/axonframework/axon/kernel"
)

// --- RouteByTokenCount ---

func TestRouteByTokenCountSmall(t *testing.T) {
	small := newFakeLLM("small")
	small.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "small response"}, nil
	}
	large := newFakeLLM("large")
	large.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "large response"}, nil
	}

	router := RouteByTokenCount(100, small, large)

	// Short message: under threshold.
	params := kernel.GenerateParams{
		Messages: []kernel.Message{
			{Content: []kernel.ContentPart{{Text: strPtr("hi")}}},
		},
	}
	resp, err := router.Generate(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "small response" {
		t.Errorf("expected %q, got %q", "small response", resp.Text)
	}
}

func TestRouteByTokenCountLarge(t *testing.T) {
	small := newFakeLLM("small")
	small.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "small response"}, nil
	}
	large := newFakeLLM("large")
	large.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "large response"}, nil
	}

	router := RouteByTokenCount(10, small, large)

	// Long message: over threshold.
	longText := "this is a long message that exceeds the token threshold easily"
	params := kernel.GenerateParams{
		Messages: []kernel.Message{
			{Content: []kernel.ContentPart{{Text: &longText}}},
		},
	}
	resp, err := router.Generate(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "large response" {
		t.Errorf("expected %q, got %q", "large response", resp.Text)
	}
}

// --- RouteByToolCount ---

func TestRouteByToolCountSimple(t *testing.T) {
	simple := newFakeLLM("simple")
	simple.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "simple response"}, nil
	}
	complex := newFakeLLM("complex")
	complex.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "complex response"}, nil
	}

	router := RouteByToolCount(3, simple, complex)

	// 1 tool: under threshold.
	params := kernel.GenerateParams{
		Tools: make([]kernel.Tool, 1),
	}
	resp, err := router.Generate(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "simple response" {
		t.Errorf("expected %q, got %q", "simple response", resp.Text)
	}
}

func TestRouteByToolCountComplex(t *testing.T) {
	simple := newFakeLLM("simple")
	simple.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "simple response"}, nil
	}
	complex := newFakeLLM("complex")
	complex.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "complex response"}, nil
	}

	router := RouteByToolCount(3, simple, complex)

	// 5 tools: over threshold.
	params := kernel.GenerateParams{
		Tools: make([]kernel.Tool, 5),
	}
	resp, err := router.Generate(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "complex response" {
		t.Errorf("expected %q, got %q", "complex response", resp.Text)
	}
}

// --- RoundRobin ---

func TestRoundRobinDistribution(t *testing.T) {
	models := make([]*fakeLLM, 3)
	llms := make([]kernel.LLM, 3)
	for i := range models {
		m := newFakeLLM("")
		idx := i // capture for closure
		m.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
			return kernel.Response{Text: string(rune('A' + idx))}, nil
		}
		models[i] = m
		llms[i] = m
	}

	rr := RoundRobin(llms...)

	// Calls should cycle through A, B, C, A, B, C...
	expected := []string{"A", "B", "C", "A", "B", "C"}
	for i, exp := range expected {
		resp, err := rr.Generate(context.Background(), kernel.GenerateParams{})
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		if resp.Text != exp {
			t.Errorf("call %d: expected %q, got %q", i, exp, resp.Text)
		}
	}
}

func TestRoundRobinConcurrentSafety(t *testing.T) {
	var callCount atomic.Int64
	models := make([]kernel.LLM, 3)
	for i := range models {
		m := newFakeLLM("")
		m.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
			callCount.Add(1)
			return kernel.Response{Text: "ok"}, nil
		}
		models[i] = m
	}

	rr := RoundRobin(models...)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = rr.Generate(context.Background(), kernel.GenerateParams{})
		}()
	}
	wg.Wait()

	if callCount.Load() != 100 {
		t.Errorf("expected 100 total calls, got %d", callCount.Load())
	}
}

func TestRoundRobinModel(t *testing.T) {
	m := newFakeLLM("test")
	rr := RoundRobin(m)
	if rr.Model() != "round-robin" {
		t.Errorf("expected model %q, got %q", "round-robin", rr.Model())
	}
}

// --- Cascade ---

func TestCascadeNoEscalation(t *testing.T) {
	primary := newFakeLLM("primary")
	primary.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "primary response"}, nil
	}
	fallback := newFakeLLM("fallback")
	fallback.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "fallback response"}, nil
	}

	c := Cascade(primary, fallback, func(r kernel.Response) bool {
		return false // Never escalate.
	})

	resp, err := c.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "primary response" {
		t.Errorf("expected %q, got %q", "primary response", resp.Text)
	}
}

func TestCascadeWithEscalation(t *testing.T) {
	primary := newFakeLLM("primary")
	primary.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "low quality"}, nil
	}
	fallback := newFakeLLM("fallback")
	fallback.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "high quality"}, nil
	}

	c := Cascade(primary, fallback, func(r kernel.Response) bool {
		return r.Text == "low quality" // Escalate on low quality.
	})

	resp, err := c.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "high quality" {
		t.Errorf("expected %q, got %q", "high quality", resp.Text)
	}
}

func TestCascadePrimaryError(t *testing.T) {
	primary := newFakeLLM("primary")
	primary.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{}, errors.New("primary failed")
	}
	fallback := newFakeLLM("fallback")
	fallback.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "fallback saved us"}, nil
	}

	c := Cascade(primary, fallback, func(r kernel.Response) bool {
		return false
	})

	// When primary errors, cascade should escalate to fallback.
	resp, err := c.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "fallback saved us" {
		t.Errorf("expected %q, got %q", "fallback saved us", resp.Text)
	}
}

func TestCascadeBothFail(t *testing.T) {
	primary := newFakeLLM("primary")
	primary.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{}, errors.New("primary failed")
	}
	fallback := newFakeLLM("fallback")
	fallback.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{}, errors.New("fallback also failed")
	}

	c := Cascade(primary, fallback, func(r kernel.Response) bool { return false })

	_, err := c.Generate(context.Background(), kernel.GenerateParams{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "fallback also failed" {
		t.Errorf("expected %q, got %q", "fallback also failed", err.Error())
	}
}

func TestCascadeStream(t *testing.T) {
	primary := newFakeLLM("primary")
	primary.streamFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
		return &fakeStream{resp: kernel.Response{Text: "streamed from primary"}}, nil
	}
	fallback := newFakeLLM("fallback")

	c := Cascade(primary, fallback, func(r kernel.Response) bool { return false })

	stream, err := c.GenerateStream(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stream.Response().Text != "streamed from primary" {
		t.Errorf("expected %q, got %q", "streamed from primary", stream.Response().Text)
	}
}

func TestCascadeModel(t *testing.T) {
	primary := newFakeLLM("primary")
	fallback := newFakeLLM("fallback")
	c := Cascade(primary, fallback, func(r kernel.Response) bool { return false })
	if c.Model() != "cascade" {
		t.Errorf("expected model %q, got %q", "cascade", c.Model())
	}
}

// strPtr is a test helper.
func strPtr(s string) *string {
	return &s
}

// suppress unused import warnings
var _ = json.Marshal
