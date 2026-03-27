package search

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// VectorizerClient communicates with the Python vectorizer-proxy.
type VectorizerClient struct {
	BaseURL string
	client  *http.Client
}

// NewVectorizerClient creates a new client for the vectorizer-proxy.
func NewVectorizerClient(baseURL string) *VectorizerClient {
	return &VectorizerClient{
		BaseURL: baseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// VectorizeSingle converts a single text string into a vector.
func (c *VectorizerClient) VectorizeSingle(ctx context.Context, text string) ([]float32, error) {
	reqBody, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/vectors", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("vectorizer error (status %d): %s", resp.StatusCode, string(body))
	}

	var res struct {
		Vector []float32 `json:"vector"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return res.Vector, nil
}

// VectorizeBatch converts multiple text strings into vectors using the binary protocol.
func (c *VectorizerClient) VectorizeBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	reqBody, err := json.Marshal(map[string]any{"texts": texts, "normalize": true})
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/vectors/batch", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("vectorizer error (status %d): %s", resp.StatusCode, string(body))
	}

	// Read binary response.
	// Header: rows (4 bytes), dim (4 bytes), both little-endian uint32.
	header := make([]byte, 8)
	if _, err := io.ReadFull(resp.Body, header); err != nil {
		return nil, fmt.Errorf("reading header: %w", err)
	}

	rows := binary.LittleEndian.Uint32(header[0:4])
	dim := binary.LittleEndian.Uint32(header[4:8])

	if rows != uint32(len(texts)) {
		return nil, fmt.Errorf("unexpected row count: got %d, want %d", rows, len(texts))
	}

	// Read data: rows * dim * 4 bytes (float32).
	data := make([]float32, rows*dim)
	if err := binary.Read(resp.Body, binary.LittleEndian, &data); err != nil {
		return nil, fmt.Errorf("reading vector data: %w", err)
	}

	// Reshape into [][]float32.
	result := make([][]float32, rows)
	for i := uint32(0); i < rows; i++ {
		result[i] = data[i*dim : (i+1)*dim]
	}

	return result, nil
}
