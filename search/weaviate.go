package search

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"fodmap/data/schemas"

	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/weaviate/weaviate-go-client/v4/weaviate"
	"github.com/weaviate/weaviate-go-client/v4/weaviate/filters"
	"github.com/weaviate/weaviate-go-client/v4/weaviate/graphql"
	"github.com/weaviate/weaviate/entities/models"
)

const (
	collectionName = "YelpReview"
	topKReviews    = 5
)

// Client wraps the Weaviate client with domain-specific operations.
type Client struct {
	wv *weaviate.Client
}

// SearchResult holds the ranked list of business IDs returned by a search query.
type SearchResult struct {
	BusinessIDs []string
}

// SearchFilter holds optional filters to narrow the search by category, city, and/or state.
type SearchFilter struct {
	Category string // substring match against categories field; empty = no filter
	City     string // exact match; empty = no filter
	State    string // exact match; empty = no filter
}

// IndexItem pairs a review with its associated business metadata for indexing.
type IndexItem struct {
	Review     schemas.ReviewSchemaS
	City       string
	State      string
	Categories string
}

// NewClient creates a Weaviate client connected to the given host (e.g. "localhost:8090").
func NewClient(host string) (*Client, error) {
	cfg := weaviate.Config{
		Host:   host,
		Scheme: "http",
	}
	wv, err := weaviate.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("weaviate NewClient: %w", err)
	}
	return &Client{wv: wv}, nil
}

// EnsureSchema creates the YelpReview collection if it does not already exist.
// It is idempotent — safe to call on every startup or before indexing.
func (c *Client) EnsureSchema(ctx context.Context) error {
	_, err := c.wv.Schema().ClassGetter().WithClassName(collectionName).Do(ctx)
	if err == nil {
		// class already exists
		return nil
	}

	skip := map[string]interface{}{
		"text2vec-transformers": map[string]interface{}{"skip": true},
	}
	class := &models.Class{
		Class:      collectionName,
		Vectorizer: "text2vec-transformers",
		ModuleConfig: map[string]interface{}{
			"text2vec-transformers": map[string]interface{}{
				"vectorizeClassName": false,
			},
		},
		Properties: []*models.Property{
			{Name: "reviewId", DataType: []string{"text"}, ModuleConfig: skip},
			{Name: "businessId", DataType: []string{"text"}, ModuleConfig: skip},
			{Name: "stars", DataType: []string{"number"}, ModuleConfig: skip},
			{Name: "city", DataType: []string{"text"}, ModuleConfig: skip},
			{Name: "state", DataType: []string{"text"}, ModuleConfig: skip},
			{Name: "categories", DataType: []string{"text"}, ModuleConfig: skip},
			// text is NOT skipped — this is the field that gets vectorized
			{Name: "text", DataType: []string{"text"}},
		},
	}

	if err := c.wv.Schema().ClassCreator().WithClass(class).Do(ctx); err != nil {
		return fmt.Errorf("creating schema: %w", err)
	}
	return nil
}

// BatchUpsert inserts or updates a batch of reviews in Weaviate.
// Each item is assigned a deterministic UUID from its review_id, making the operation idempotent.
func (c *Client) BatchUpsert(ctx context.Context, items []IndexItem) error {
	batcher := c.wv.Batch().ObjectsBatcher()
	for _, item := range items {
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(item.Review.ReviewId)).String()
		batcher = batcher.WithObjects(&models.Object{
			Class: collectionName,
			ID:    strfmt.UUID(id),
			Properties: map[string]interface{}{
				"reviewId":   item.Review.ReviewId,
				"businessId": item.Review.BusinessId,
				"stars":      item.Review.Stars,
				"text":       item.Review.Text,
				"city":       item.City,
				"state":      item.State,
				"categories": item.Categories,
			},
		})
	}

	responses, err := batcher.Do(ctx)
	if err != nil {
		return fmt.Errorf("batch upsert: %w", err)
	}
	for _, resp := range responses {
		if resp.Result != nil && resp.Result.Errors != nil {
			slog.Warn("batch upsert item error", "errors", resp.Result.Errors)
		}
	}
	return nil
}

// Search performs a nearText vector query and returns restaurant IDs ranked by
// Top-K average certainty score (K=topKReviews). Optional filters narrow results
// by category (substring), city (exact), and state (exact).
func (c *Client) Search(ctx context.Context, query string, limit int, filter SearchFilter) (SearchResult, error) {
	fields := []graphql.Field{
		{Name: "businessId"},
		{Name: "_additional { certainty }"},
	}

	nearText := c.wv.GraphQL().NearTextArgBuilder().WithConcepts([]string{query})

	getter := c.wv.GraphQL().Get().
		WithClassName(collectionName).
		WithFields(fields...).
		WithNearText(nearText).
		WithLimit(limit * 20)

	if where := buildWhereFilter(filter); where != nil {
		getter = getter.WithWhere(where)
	}

	resp, err := getter.Do(ctx)
	if err != nil {
		return SearchResult{}, fmt.Errorf("graphql query: %w", err)
	}
	if resp.Errors != nil {
		return SearchResult{}, fmt.Errorf("graphql errors: %v", resp.Errors)
	}

	return aggregateTopK(resp.Data, limit), nil
}

// buildWhereFilter constructs a Weaviate where filter from the non-empty fields of SearchFilter.
// Returns nil when all fields are empty (no filtering).
func buildWhereFilter(f SearchFilter) *filters.WhereBuilder {
	var operands []*filters.WhereBuilder

	if f.Category != "" {
		operands = append(operands,
			filters.Where().
				WithPath([]string{"categories"}).
				WithOperator(filters.Like).
				WithValueText("*"+f.Category+"*"))
	}
	if f.City != "" {
		operands = append(operands,
			filters.Where().
				WithPath([]string{"city"}).
				WithOperator(filters.Equal).
				WithValueText(f.City))
	}
	if f.State != "" {
		operands = append(operands,
			filters.Where().
				WithPath([]string{"state"}).
				WithOperator(filters.Equal).
				WithValueText(f.State))
	}

	switch len(operands) {
	case 0:
		return nil
	case 1:
		return operands[0]
	default:
		return filters.Where().WithOperator(filters.And).WithOperands(operands)
	}
}

// aggregateTopK groups review certainty scores by businessId, averages the top K per restaurant,
// then returns the top `limit` business IDs sorted by that average (descending).
func aggregateTopK(data map[string]models.JSONObject, limit int) SearchResult {
	getRaw, ok := data["Get"]
	if !ok {
		return SearchResult{BusinessIDs: []string{}}
	}
	getMap, ok := getRaw.(map[string]interface{})
	if !ok {
		return SearchResult{BusinessIDs: []string{}}
	}
	rawItems, ok := getMap[collectionName]
	if !ok {
		return SearchResult{BusinessIDs: []string{}}
	}
	items, ok := rawItems.([]interface{})
	if !ok {
		return SearchResult{BusinessIDs: []string{}}
	}

	// Collect certainty scores per business.
	scores := make(map[string][]float64)
	for _, raw := range items {
		obj, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		businessID, _ := obj["businessId"].(string)
		if businessID == "" {
			continue
		}
		additional, _ := obj["_additional"].(map[string]interface{})
		if additional == nil {
			continue
		}
		certainty, _ := additional["certainty"].(float64)
		scores[businessID] = append(scores[businessID], certainty)
	}

	// Compute top-K average per business.
	type ranked struct {
		id    string
		score float64
	}
	results := make([]ranked, 0, len(scores))
	for id, s := range scores {
		sort.Slice(s, func(i, j int) bool { return s[i] > s[j] })
		k := min(topKReviews, len(s))
		var sum float64
		for i := 0; i < k; i++ {
			sum += s[i]
		}
		results = append(results, ranked{id: id, score: sum / float64(k)})
	}

	sort.Slice(results, func(i, j int) bool { return results[i].score > results[j].score })

	out := make([]string, 0, min(limit, len(results)))
	for i := 0; i < limit && i < len(results); i++ {
		out = append(out, results[i].id)
	}
	return SearchResult{BusinessIDs: out}
}
