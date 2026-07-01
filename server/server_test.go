package server

import (
	"context"
	"net/http"
	"testing"
)

type mockEmbedder struct{}

func (m *mockEmbedder) EmbedSingle(ctx context.Context, text string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}

func (m *mockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	res := make([][]float32, len(texts))
	for i := range texts {
		res[i] = []float32{0.1, 0.2, 0.3}
	}
	return res, nil
}

func (m *mockEmbedder) Close() error { return nil }

func TestServerNew(t *testing.T) {
	ctx := context.Background()

	cfg1 := Config{
		Port:              8080,
		PineconeAPIKey:    "test-pinecone",
		PineconeIndexHost: "https://test-pinecone.io",
		Embedder:          &mockEmbedder{},
		ChatRateLimit:     10,
		ChatRateBurst:     20,
		ChatMaxConcurrent: 5,
	}

	s1, err := New(ctx, cfg1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s1.Searcher() == nil {
		t.Error("expected Searcher to be set for Pinecone config")
	}

	if s1.ChatBackend() != nil {
		t.Error("expected ChatBackend to be nil")
	}

	s1.SetMenutrackingAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if s1.menutrackingAdmin == nil {
		t.Error("expected menutrackingAdmin to be set")
	}

	cfg2 := Config{
		Port:               8081,
		GoogleCloudProject: "fake-project",
		ChatAPIKey:         "fake-chat-key",
		Embedder:           &mockEmbedder{},
	}
	s2, err := New(ctx, cfg2)
	if err != nil {
		// Vertex AI client construction needs Application Default Credentials.
		// In environments without ADC (e.g. CI), skip rather than fail.
		t.Skipf("vertex ai client construction failed (no ADC?): %v", err)
	}
	if s2.ChatBackend() == nil {
		t.Error("expected ChatBackend to be set for Gemini config")
	}

	s2.port = -1
	err = s2.Start()
	if err == nil {
		t.Error("expected error from Start() with invalid port")
	}
}

func TestServerMockStoreClose(t *testing.T) {
	ms := newStubStore()
	err := ms.Close()
	if err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}
