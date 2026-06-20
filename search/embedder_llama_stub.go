//go:build !llamago

package search

import (
	"context"
	"fmt"
)

// NewLlamaEmbedder returns an error when the binary is built without the
// 'llamago' build tag. Build with: go build -tags llamago
func NewLlamaEmbedder(modelPath string) (*LlamaEmbedderStub, error) {
	return nil, fmt.Errorf("in-process embedding not available: binary built without '-tags llamago' (requires llama.cpp)")
}

// LlamaEmbedderStub is a placeholder that satisfies the Embedder interface
// when the llamago build tag is not set.
type LlamaEmbedderStub struct{}

// EmbedSingle always returns an error because the llamago build tag is not set.
func (e *LlamaEmbedderStub) EmbedSingle(_ context.Context, _ string) ([]float32, error) {
	return nil, fmt.Errorf("llama embedder not available")
}

// EmbedBatch always returns an error because the llamago build tag is not set.
func (e *LlamaEmbedderStub) EmbedBatch(_ context.Context, _ []string) ([][]float32, error) {
	return nil, fmt.Errorf("llama embedder not available")
}

// Close is a no-op on the stub.
func (e *LlamaEmbedderStub) Close() error { return nil }
