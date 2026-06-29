package search

import "context"

// Embedder generates vector embeddings from text.
// Implementations include OllamaEmbedder (local Ollama server), TEIEmbedder
// (HuggingFace Text Embeddings Inference service), and VectorizerClient (HTTP
// proxy fallback).
//
// Contract: implementations MUST preserve input order in EmbedBatch — the
// i-th returned vector must be the embedding of the i-th input text.
// pipeline.ToMenuItems attaches vectors to menu items positionally; silently
// reordered vectors would corrupt the index without raising an error.
type Embedder interface {
	// EmbedSingle returns the embedding vector for a single text string.
	// For query-time usage, implementations should prepend the appropriate
	// task prefix (e.g., "search_query: " for nomic-embed-text).
	EmbedSingle(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch returns embeddings for multiple texts. Each text is treated
	// independently. Implementations may batch the work for efficiency.
	// Vectors MUST be returned in input order.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)

	// Close releases any resources held by the embedder (model weights, GPU memory, etc.).
	Close() error
}
