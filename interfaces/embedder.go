// interfaces/embedder.go
package interfaces

import "context"

// Embedder generates vector embeddings for text.
// Batch-oriented: accepts multiple texts and returns one embedding per text.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}
