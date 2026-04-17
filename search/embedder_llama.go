//go:build llamago

package search

import (
	"context"
	"fmt"
	"sync"

	llama "github.com/tcpipuk/llama-go"
)

// LlamaEmbedder implements Embedder using llama-go for in-process embedding
// via llama.cpp. It loads a GGUF model (e.g., nomic-embed-text-v1.5) and
// generates embeddings without any external service or HTTP calls.
//
// Thread safety: The Model is thread-safe and shared. Each goroutine needing
// concurrent access should use a separate Context, managed by the pool below.
type LlamaEmbedder struct {
	model *llama.Model
	pool  sync.Pool // pool of *llama.Context for concurrent access
}

// NewLlamaEmbedder loads the GGUF model at modelPath and prepares it for
// embedding generation. GPU layers are offloaded automatically (Metal on
// Apple Silicon, CUDA on NVIDIA).
func NewLlamaEmbedder(modelPath string) (*LlamaEmbedder, error) {
	model, err := llama.LoadModel(modelPath,
		llama.WithGPULayers(-1),     // offload all layers to GPU
		llama.WithMMap(true),        // memory-map model file for efficiency
		llama.WithSilentLoading(),   // suppress loading progress bar
	)
	if err != nil {
		return nil, fmt.Errorf("loading embedding model %q: %w", modelPath, err)
	}

	e := &LlamaEmbedder{model: model}
	e.pool.New = func() any {
		ctx, err := model.NewContext(
			llama.WithEmbeddings(), // enable embedding mode
			llama.WithContext(2048), // context size sufficient for single-doc embedding
		)
		if err != nil {
			// Pool's New can't return error; we'll handle nil ctx in EmbedSingle.
			return nil
		}
		return ctx
	}
	return e, nil
}

// getContext obtains a llama.Context from the pool, creating one if needed.
func (e *LlamaEmbedder) getContext() (*llama.Context, error) {
	ctx := e.pool.Get()
	if ctx == nil {
		return nil, fmt.Errorf("failed to create llama context for embeddings")
	}
	return ctx.(*llama.Context), nil
}

// putContext returns a llama.Context to the pool for reuse.
func (e *LlamaEmbedder) putContext(ctx *llama.Context) {
	e.pool.Put(ctx)
}

// EmbedSingle returns the embedding for a single text. The text is
// prepended with "search_query: " for query-time usage with nomic-embed-text.
func (e *LlamaEmbedder) EmbedSingle(_ context.Context, text string) ([]float32, error) {
	llamaCtx, err := e.getContext()
	if err != nil {
		return nil, err
	}
	defer e.putContext(llamaCtx)

	vec, err := llamaCtx.GetEmbeddings("search_query: " + text)
	if err != nil {
		return nil, fmt.Errorf("embedding text: %w", err)
	}
	return vec, nil
}

// EmbedBatch returns embeddings for multiple texts. Each text is embedded
// independently. For document indexing, texts should already include the
// "search_document: " prefix if needed.
func (e *LlamaEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		llamaCtx, err := e.getContext()
		if err != nil {
			return nil, err
		}

		vec, err := llamaCtx.GetEmbeddings(text)
		e.putContext(llamaCtx)
		if err != nil {
			return nil, fmt.Errorf("embedding text %d: %w", i, err)
		}
		results[i] = vec
	}
	return results, nil
}

// Close frees the model and all associated resources (GPU memory, etc.).
func (e *LlamaEmbedder) Close() error {
	return e.model.Close()
}
