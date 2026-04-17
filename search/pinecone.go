package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"fodmap/data"
	"fodmap/data/schemas"
)

const (
	pineconeReviewNamespace = "yelp-reviews"
	pineconeFodmapNamespace = "fodmap-ingredients"
)

// PineconeClient implements Searcher for Pinecone.
type PineconeClient struct {
	APIKey    string
	IndexHost string
	embedder  Embedder
	client    *http.Client
}

// NewPineconeClient creates a new PineconeClient.
func NewPineconeClient(apiKey, indexHost string, e Embedder) *PineconeClient {
	return &PineconeClient{
		APIKey:    apiKey,
		IndexHost: indexHost,
		embedder:  e,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

// PineconeQueryResponse is the low-level response from Pinecone /query endpoint.
type PineconeQueryResponse struct {
	Matches []struct {
		ID       string         `json:"id"`
		Score    float64        `json:"score"`
		Metadata map[string]any `json:"metadata"`
	} `json:"matches"`
}

// EnsureSchema is a no-op for Pinecone as it uses implicit namespaces.
func (c *PineconeClient) EnsureSchema(ctx context.Context) error {
	return nil
}

// EnsureFodmapSchema is a no-op for Pinecone as it uses implicit namespaces.
func (c *PineconeClient) EnsureFodmapSchema(ctx context.Context) error {
	return nil
}

// GetBusinesses performs an aggregation-like search by querying reviews and grouping by business.
func (c *PineconeClient) GetBusinesses(ctx context.Context, query string, limit int, filter SearchFilter) (SearchResult, error) {
	vec, err := c.embedder.EmbedSingle(ctx, query)
	if err != nil {
		return SearchResult{}, fmt.Errorf("vectorizing query: %w", err)
	}

	// query Pinecone for top matches in yelp-reviews namespace
	payload := map[string]any{
		"vector":          vec,
		"topK":            limit * 10, // overkill for grouping
		"includeMetadata": true,
		"namespace":       pineconeReviewNamespace,
	}

	// apply filters if present
	if filter.City != "" || filter.State != "" || filter.Category != "" {
		f := map[string]any{}
		if filter.City != "" {
			f["city"] = map[string]string{"$eq": filter.City}
		}
		if filter.State != "" {
			f["state"] = map[string]string{"$eq": filter.State}
		}
		if filter.Category != "" {
			f["categories"] = map[string]string{"$contains": filter.Category}
		}
		payload["filter"] = f
	}

	res, err := c.doQuery(ctx, payload)
	if err != nil {
		return SearchResult{}, err
	}

	// Group results by business.
	seen := make(map[string]bool)
	var businesses []BusinessResult
	for _, m := range res.Matches {
		bizID, _ := m.Metadata["business_id"].(string)
		if bizID == "" || seen[bizID] {
			continue
		}
		seen[bizID] = true
		businesses = append(businesses, BusinessResult{
			ID:    bizID,
			Name:  m.Metadata["business_name"].(string),
			City:  m.Metadata["city"].(string),
			State: m.Metadata["state"].(string),
			Score: m.Score,
		})
		if len(businesses) >= limit {
			break
		}
	}

	return SearchResult{Businesses: businesses}, nil
}

// GetReviews retrieves top reviews for a query, filtered by business if specified.
func (c *PineconeClient) GetReviews(ctx context.Context, query string, limit int, filter SearchFilter) (SearchReviews, error) {
	vec, err := c.embedder.EmbedSingle(ctx, query)
	if err != nil {
		return SearchReviews{}, fmt.Errorf("vectorizing query: %w", err)
	}

	payload := map[string]any{
		"vector":          vec,
		"topK":            limit,
		"includeMetadata": true,
		"namespace":       pineconeReviewNamespace,
	}

	// filter by business ID if provided
	if filter.BusinessID != "" {
		payload["filter"] = map[string]any{"business_id": map[string]string{"$eq": filter.BusinessID}}
	} else if len(filter.ReviewIDs) > 0 {
		payload["filter"] = map[string]any{"review_id": map[string]any{"$in": filter.ReviewIDs}}
	}

	res, err := c.doQuery(ctx, payload)
	if err != nil {
		return SearchReviews{}, err
	}

	var reviews []RankedReview
	for _, m := range res.Matches {
		text, _ := m.Metadata["text"].(string)
		score := blendScore(query, text, m.Score, filter.Alpha)
		reviews = append(reviews, RankedReview{
			Score: score,
			Review: IndexItem{
				BusinessName: m.Metadata["business_name"].(string),
				City:         m.Metadata["city"].(string),
				State:        m.Metadata["state"].(string),
				Review: schemas.Review{
					ReviewID: m.Metadata["review_id"].(string),
					Text:     text,
					Stars:    metadataFloat32(m.Metadata, "stars"),
				},
			},
		})
	}

	// Re-sort by blended score when hybrid is active.
	if filter.Alpha > 0 && filter.Alpha < 1 {
		sort.Slice(reviews, func(i, j int) bool { return reviews[i].Score > reviews[j].Score })
	}

	return SearchReviews{BusinessReviews: reviews}, nil
}

// SearchFodmap looks up an ingredient in the fodmap-ingredients namespace.
func (c *PineconeClient) SearchFodmap(ctx context.Context, ingredient string) (FodmapResult, float64, error) {
	vec, err := c.embedder.EmbedSingle(ctx, ingredient)
	if err != nil {
		return FodmapResult{}, 0, fmt.Errorf("vectorizing query: %w", err)
	}

	payload := map[string]any{
		"vector":          vec,
		"topK":            1,
		"includeMetadata": true,
		"namespace":       pineconeFodmapNamespace,
	}

	res, err := c.doQuery(ctx, payload)
	if err != nil {
		return FodmapResult{}, 0, err
	}

	if len(res.Matches) == 0 {
		return FodmapResult{}, 0, fmt.Errorf("not found")
	}

	m := res.Matches[0]
	var groups []string
	if gSlice, ok := m.Metadata["groups"].([]any); ok {
		for _, g := range gSlice {
			if s, ok := g.(string); ok {
				groups = append(groups, s)
			}
		}
	}

	return FodmapResult{
		Ingredient: m.Metadata["ingredient"].(string),
		Level:      m.Metadata["level"].(string),
		Groups:     groups,
		Notes:      m.Metadata["notes"].(string),
	}, m.Score, nil
}

// BatchUpsertFodmap vectorizes and uploads FODMAP data to Pinecone.
func (c *PineconeClient) BatchUpsert(ctx context.Context, items []IndexItem) error {
	if len(items) == 0 {
		return nil
	}
	var pineconeVectors []map[string]any
	for _, item := range items {
		pineconeVectors = append(pineconeVectors, map[string]any{
			"id":     item.Review.ReviewID,
			"values": item.Vector,
			"metadata": map[string]any{
				"business_id":   item.Review.BusinessID,
				"business_name": item.BusinessName,
				"city":          item.City,
				"state":         item.State,
				"categories":    item.Categories,
				"stars":         item.Review.Stars,
				"text":          item.Review.Text,
			},
		})
	}
	return c.doUpsert(ctx, pineconeVectors, pineconeReviewNamespace)
}

func (c *PineconeClient) BatchUpsertFodmap(ctx context.Context, items map[string]data.FodmapEntry) error {
	if len(items) == 0 {
		return nil
	}

	var texts []string
	for name := range items {
		texts = append(texts, name)
	}

	vectors, err := c.embedder.EmbedBatch(ctx, texts)
	if err != nil {
		return fmt.Errorf("batch vectorize: %w", err)
	}

	var pineconeVectors []map[string]any
	i := 0
	for name, entry := range items {
		pineconeVectors = append(pineconeVectors, map[string]any{
			"id":     fmt.Sprintf("fodmap-%s", name),
			"values": vectors[i],
			"metadata": map[string]any{
				"ingredient": name,
				"level":      entry.Level,
				"groups":     entry.Groups,
				"notes":      entry.Notes,
			},
		})
		i++
	}

	// Pinecone upsert limit is typically 2MB or 1000 vectors; we'll do all at once as fodmap is small.
	return c.doUpsert(ctx, pineconeVectors, pineconeFodmapNamespace)
}

func (c *PineconeClient) doQuery(ctx context.Context, payload map[string]any) (PineconeQueryResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return PineconeQueryResponse{}, fmt.Errorf("marshalling query payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.IndexHost+"/query", bytes.NewReader(body))
	if err != nil {
		return PineconeQueryResponse{}, fmt.Errorf("creating query request: %w", err)
	}
	req.Header.Set("Api-Key", c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return PineconeQueryResponse{}, fmt.Errorf("executing pinecone query: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		out, _ := io.ReadAll(resp.Body)
		return PineconeQueryResponse{}, fmt.Errorf("pinecone query error (status %d): %s", resp.StatusCode, string(out))
	}

	var res PineconeQueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return PineconeQueryResponse{}, fmt.Errorf("decoding pinecone response: %w", err)
	}
	return res, nil
}

func (c *PineconeClient) doUpsert(ctx context.Context, vectors []map[string]any, namespace string) error {
	body, err := json.Marshal(map[string]any{"vectors": vectors, "namespace": namespace})
	if err != nil {
		return fmt.Errorf("marshalling upsert payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.IndexHost+"/vectors/upsert", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating upsert request: %w", err)
	}
	req.Header.Set("Api-Key", c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("executing pinecone upsert: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		out, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pinecone upsert error (status %d): %s", resp.StatusCode, string(out))
	}
	return nil
}

// metadataFloat32 safely extracts a float32 from a metadata map, returning 0 if missing.
func metadataFloat32(m map[string]any, key string) float32 {
	v, ok := m[key].(float64)
	if !ok {
		return 0
	}
	return float32(v)
}
