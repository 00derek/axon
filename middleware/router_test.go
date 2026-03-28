// middleware/router_test.go
package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/axonframework/axon/kernel"
)

func TestRouterFallback(t *testing.T) {
	fallback := newFakeLLM("fallback-model")
	fallback.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "fallback"}, nil
	}

	// No routes -- always falls back.
	router := NewRouter(fallback)
	resp, err := router.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "fallback" {
		t.Errorf("expected %q, got %q", "fallback", resp.Text)
	}
}

func TestRouterFirstMatchWins(t *testing.T) {
	fallback := newFakeLLM("fallback")
	modelA := newFakeLLM("model-a")
	modelA.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "from A"}, nil
	}
	modelB := newFakeLLM("model-b")
	modelB.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "from B"}, nil
	}

	router := NewRouter(fallback,
		Route{
			Model: modelA,
			Condition: func(rc RouteContext) bool {
				return len(rc.Params.Messages) > 5
			},
		},
		Route{
			Model: modelB,
			Condition: func(rc RouteContext) bool {
				return len(rc.Params.Messages) > 0
			},
		},
	)

	// 3 messages: matches B (not A, which requires >5).
	params := kernel.GenerateParams{
		Messages: make([]kernel.Message, 3),
	}
	resp, err := router.Generate(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "from B" {
		t.Errorf("expected %q, got %q", "from B", resp.Text)
	}

	// 10 messages: matches A first.
	params.Messages = make([]kernel.Message, 10)
	resp, err = router.Generate(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "from A" {
		t.Errorf("expected %q, got %q", "from A", resp.Text)
	}
}

func TestRouterStream(t *testing.T) {
	fallback := newFakeLLM("fallback")
	modelA := newFakeLLM("model-a")
	modelA.streamFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
		return &fakeStream{resp: kernel.Response{Text: "stream from A"}}, nil
	}

	router := NewRouter(fallback,
		Route{
			Model: modelA,
			Condition: func(rc RouteContext) bool {
				return true // Always matches.
			},
		},
	)

	stream, err := router.GenerateStream(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stream.Response().Text != "stream from A" {
		t.Errorf("expected %q, got %q", "stream from A", stream.Response().Text)
	}
}

func TestRouterContextPassthrough(t *testing.T) {
	type ctxKey string
	key := ctxKey("test-key")

	fallback := newFakeLLM("fallback")
	modelA := newFakeLLM("model-a")

	var capturedCtx context.Context
	modelA.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		capturedCtx = ctx
		return kernel.Response{Text: "ok"}, nil
	}

	router := NewRouter(fallback,
		Route{
			Model: modelA,
			Condition: func(rc RouteContext) bool {
				// Verify context is available in the condition.
				return rc.Ctx.Value(key) == "hello"
			},
		},
	)

	ctx := context.WithValue(context.Background(), key, "hello")
	_, err := router.Generate(ctx, kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedCtx.Value(key) != "hello" {
		t.Error("expected context value to be passed through to the matched model")
	}
}

func TestRouterPropagatesErrors(t *testing.T) {
	fallback := newFakeLLM("fallback")
	fallback.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{}, errors.New("fallback error")
	}

	router := NewRouter(fallback)
	_, err := router.Generate(context.Background(), kernel.GenerateParams{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "fallback error" {
		t.Errorf("expected %q, got %q", "fallback error", err.Error())
	}
}

func TestRouterModel(t *testing.T) {
	fallback := newFakeLLM("fallback-model")
	router := NewRouter(fallback)

	// Router's Model() returns "router" since it routes to multiple models.
	if router.Model() != "router" {
		t.Errorf("expected model %q, got %q", "router", router.Model())
	}
}
