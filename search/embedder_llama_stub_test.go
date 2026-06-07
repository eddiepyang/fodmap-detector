package search

import (
	"context"
	"testing"
)

func TestLlamaEmbedderStub_Errors(t *testing.T) {
	_, err := NewLlamaEmbedder("model.bin")
	if err == nil || err.Error() != "in-process embedding not available: binary built without '-tags llamago' (requires llama.cpp)" {
		t.Errorf("unexpected error: %v", err)
	}

	stub := &LlamaEmbedderStub{}
	
	_, err = stub.EmbedSingle(context.Background(), "test")
	if err == nil || err.Error() != "llama embedder not available" {
		t.Errorf("unexpected error: %v", err)
	}

	_, err = stub.EmbedBatch(context.Background(), []string{"one"})
	if err == nil || err.Error() != "llama embedder not available" {
		t.Errorf("unexpected error: %v", err)
	}

	if err := stub.Close(); err != nil {
		t.Errorf("unexpected close error: %v", err)
	}
}
