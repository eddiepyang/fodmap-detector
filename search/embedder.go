package search

import "context"

// Embedder generates vector embeddings from text.
// Implementations include LlamaEmbedder (in-process via llama-go) and
// VectorizerClient (HTTP proxy fallback).
type Embedder interface {
	// EmbedSingle returns the embedding vector for a single text string.
	// For query-time usage, implementations should prepend the appropriate
	// task prefix (e.g., "search_query: " for nomic-embed-text).
	EmbedSingle(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch returns embeddings for multiple texts. Each text is treated
	// independently. Implementations may batch the work for efficiency.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)

	// Close releases any resources held by the embedder (model weights, GPU memory, etc.).
	Close() error
}
