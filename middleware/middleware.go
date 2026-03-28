// middleware/middleware.go
package middleware

import "github.com/axonframework/axon/kernel"

// Middleware wraps a kernel.LLM, returning a new kernel.LLM with added behavior.
// Middleware is applied at construction time. The agent does not know it is
// talking to a wrapped LLM.
type Middleware func(kernel.LLM) kernel.LLM

// Wrap applies the given middleware to an LLM in order. The first middleware
// in the list is the outermost wrapper (called first on each request).
//
// Example:
//
//	wrapped := Wrap(llm, WithRetry(3, time.Second), WithLogging(logger))
//	// Call flow: WithRetry -> WithLogging -> llm
func Wrap(llm kernel.LLM, mw ...Middleware) kernel.LLM {
	// Apply in reverse so that mw[0] is outermost.
	for i := len(mw) - 1; i >= 0; i-- {
		llm = mw[i](llm)
	}
	return llm
}
