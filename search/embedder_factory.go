package search

import (
	"context"
	"errors"
	"fmt"
)

// EmbedderConfig selects an Embedder backend.
//
// Type is one of "ollama" (default), "tei", or "vectorizer". Per-backend
// fields are required only for the selected type; the others are ignored.
type EmbedderConfig struct {
	Type          string // "ollama" | "tei" | "vectorizer" (default "ollama")
	OllamaURL     string
	OllamaModel   string
	TEIURL        string
	TEIModel      string
	VectorizerURL string
}

// ExpectedEmbeddingDim is the vector dimension the menu pipeline expects.
// It matches the menu_items.embedding vector(768) column. If a different
// embedder/model is configured, NewEmbedder's startup ping fails fast rather
// than silently inserting wrong-dim vectors.
const ExpectedEmbeddingDim = 768

// NewEmbedder builds the configured Embedder and validates it at startup by
// embedding a ping string and asserting the returned dimension matches
// ExpectedEmbeddingDim. This runs before any schema/collection init so a
// misconfigured backend fails fast.
func NewEmbedder(ctx context.Context, cfg EmbedderConfig) (Embedder, error) {
	var e Embedder
	switch cfg.Type {
	case "", "ollama":
		if cfg.OllamaURL == "" {
			return nil, errors.New("embedder=ollama requires --ollama-url")
		}
		if cfg.OllamaModel == "" {
			return nil, errors.New("embedder=ollama requires --ollama-model")
		}
		e = NewOllamaEmbedder(cfg.OllamaURL, cfg.OllamaModel)
	case "tei":
		if cfg.TEIURL == "" {
			return nil, errors.New("embedder=tei requires --tei-url")
		}
		e = NewTEIEmbedder(cfg.TEIURL, cfg.TEIModel)
	case "vectorizer":
		if cfg.VectorizerURL == "" {
			return nil, errors.New("embedder=vectorizer requires --vectorizer-url")
		}
		e = NewVectorizerClient(cfg.VectorizerURL)
	default:
		return nil, fmt.Errorf("unknown --embedder %q (want ollama|tei|vectorizer)", cfg.Type)
	}

	// Startup ping + dimension validation. Runs before EnsureMenuSchema so a
	// wrong-model backend can't leave a half-initialized schema behind.
	vec, err := e.EmbedSingle(ctx, "ping")
	if err != nil {
		_ = e.Close()
		return nil, fmt.Errorf("embedder startup ping failed: %w", err)
	}
	if got := len(vec); got != ExpectedEmbeddingDim {
		_ = e.Close()
		return nil, fmt.Errorf(
			"embedder returned %d-dim vectors, expected %d (menu_items.embedding is vector(%d)); "+
				"check the model served by the %q backend",
			got, ExpectedEmbeddingDim, ExpectedEmbeddingDim, cfg.Type)
	}
	return e, nil
}
