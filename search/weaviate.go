package search

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"fodmap/data/schemas"

	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/weaviate/weaviate-go-client/v4/weaviate"
	"github.com/weaviate/weaviate-go-client/v4/weaviate/fault"
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

// BusinessResult pairs a business ID with its human-readable name.
type BusinessResult struct {
	ID    string
	Name  string
	City  string
	State string
	Score float64
}

// SearchResult holds the ranked list of businesses returned by a search query.
type SearchResult struct {
	Businesses []BusinessResult
}

// SearchFilter holds optional filters to narrow the search by category, city, and/or state.
type SearchFilter struct {
	Category string // substring match against categories field; empty = no filter
	City     string // exact match; empty = no filter
	State    string // exact match; empty = no filter
}

// IndexItem pairs a review with its associated business metadata for indexing.
// If Vector is non-nil it is sent directly to Weaviate, bypassing the
// transformer sidecar's per-object sequential vectorization.
type IndexItem struct {
	Review       schemas.Review
	BusinessName string
	City         string
	State        string
	Categories   string
	Vector       []float32
}

// NewClient creates a Weaviate client connected to the given host (e.g. "localhost:8090").
func NewClient(host string) (*Client, error) {
	cfg := weaviate.Config{
		Host:   host,
		Scheme: "http",
		ConnectionClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
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

	skip := map[string]any{
		"text2vec-transformers": map[string]any{"skip": true},
	}
	class := &models.Class{
		Class:      collectionName,
		Vectorizer: "text2vec-transformers",
		ModuleConfig: map[string]any{
			"text2vec-transformers": map[string]any{
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
			{Name: "businessName", DataType: []string{"text"}, ModuleConfig: skip},
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
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(item.Review.ReviewID)).String()
		batcher = batcher.WithObjects(&models.Object{
			Class:  collectionName,
			ID:     strfmt.UUID(id),
			Vector: models.C11yVector(item.Vector),
			Properties: map[string]any{
				"reviewId":     item.Review.ReviewID,
				"businessId":   item.Review.BusinessID,
				"businessName": item.BusinessName,
				"stars":        item.Review.Stars,
				"text":         item.Review.Text,
				"city":         item.City,
				"state":        item.State,
				"categories":   item.Categories,
			},
		})
	}

	responses, err := batcher.Do(ctx)
	if err != nil {
		var wErr *fault.WeaviateClientError
		if errors.As(err, &wErr) && wErr.DerivedFromError != nil {
			return fmt.Errorf("batch upsert: %w", wErr.DerivedFromError)
		}
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
		{Name: "businessName"},
		{Name: "city"},
		{Name: "state"},
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
// then returns the top `limit` businesses sorted by that average (descending).
func aggregateTopK(data map[string]models.JSONObject, limit int) SearchResult {
	getRaw, ok := data["Get"]
	if !ok {
		return SearchResult{Businesses: []BusinessResult{}}
	}
	getMap, ok := getRaw.(map[string]any)
	if !ok {
		return SearchResult{Businesses: []BusinessResult{}}
	}
	rawItems, ok := getMap[collectionName]
	if !ok {
		return SearchResult{Businesses: []BusinessResult{}}
	}
	items, ok := rawItems.([]any)
	if !ok {
		return SearchResult{Businesses: []BusinessResult{}}
	}

	// Collect certainty scores and name per business.
	type bizEntry struct {
		name   string
		city   string
		state  string
		scores []float64
	}
	entries := make(map[string]*bizEntry)
	for _, raw := range items {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		businessID, _ := obj["businessId"].(string)
		if businessID == "" {
			continue
		}
		additional, _ := obj["_additional"].(map[string]any)
		if additional == nil {
			continue
		}
		certainty, _ := additional["certainty"].(float64)
		e := entries[businessID]
		if e == nil {
			name, _ := obj["businessName"].(string)
			city, _ := obj["city"].(string)
			state, _ := obj["state"].(string)
			e = &bizEntry{name: name, city: city, state: state}
			entries[businessID] = e
		}
		e.scores = append(e.scores, certainty)
	}

	// Compute top-K average per business.
	type ranked struct {
		id    string
		name  string
		city  string
		state string
		score float64
	}
	results := make([]ranked, 0, len(entries))
	for id, e := range entries {
		s := e.scores
		sort.Slice(s, func(i, j int) bool { return s[i] > s[j] })
		k := min(topKReviews, len(s))
		var sum float64
		for i := range k {
			sum += s[i]
		}
		results = append(results, ranked{id: id, name: e.name, city: e.city, state: e.state, score: sum / float64(k)})
	}

	sort.Slice(results, func(i, j int) bool { return results[i].score > results[j].score })

	out := make([]BusinessResult, 0, min(limit, len(results)))
	for i := 0; i < limit && i < len(results); i++ {
		out = append(out, BusinessResult{ID: results[i].id, Name: results[i].name, City: results[i].city, State: results[i].state, Score: results[i].score})
	}
	return SearchResult{Businesses: out}
}
