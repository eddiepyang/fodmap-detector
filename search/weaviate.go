package search

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"fodmap/data"
	"fodmap/data/schemas"

	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/weaviate/weaviate-go-client/v4/weaviate"
	"github.com/weaviate/weaviate-go-client/v4/weaviate/auth"
	"github.com/weaviate/weaviate-go-client/v4/weaviate/fault"
	"github.com/weaviate/weaviate-go-client/v4/weaviate/filters"
	"github.com/weaviate/weaviate-go-client/v4/weaviate/graphql"
	"github.com/weaviate/weaviate/entities/models"
)

const (
	collectionName       = "YelpReview"
	fodmapCollectionName = "FodmapIngredient"
	topKReviews          = 5
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
	Stars float64
	Score float64
}

// SearchResult holds the ranked list of businesses returned by a search query.
type SearchResult struct {
	Businesses []BusinessResult
}

// FodmapResult represents a parsed FODMAP ingredient from Weaviate.
type FodmapResult struct {
	Ingredient string
	Level      string
	Groups     []string
	Notes      string
}

// SearchReviews holds the reviews associated with a search result, including their metadata.
type SearchReviews struct {
	BusinessReviews []RankedReview
}

// SearchFilter holds optional filters to narrow the search by category, city, and/or state.
type SearchFilter struct {
	Category   string   // substring match against categories field; empty = no filter
	City       string   // exact match; empty = no filter
	State      string   // exact match; empty = no filter
	BusinessID string   // exact match; empty = no filter
	ReviewIDs  []string // exact match for any of the provided IDs; empty = no filter
	Alpha      float32  // hybrid search balance: 0 = pure vector (nearText), >0 enables hybrid (0=pure BM25, 1=pure vector)
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

// RankedReview pairs a review with its average certainty score across the top K matches for its business.
type RankedReview struct {
	Review IndexItem
	Score  float64
}

// NewClient creates a Weaviate client connected to the given host (e.g. "localhost:8090").
func NewClient(host, scheme, apiKey string) (*Client, error) {
	if scheme == "" {
		scheme = "http"
	}
	cfg := weaviate.Config{
		Host:   host,
		Scheme: scheme,
		ConnectionClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
	if apiKey != "" {
		cfg.AuthConfig = auth.ApiKey{Value: apiKey}
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

// GetBusinesses performs a nearText vector query and returns restaurant IDs ranked by
// Top-K average certainty score (K=topKReviews). Optional filters narrow results
// by category (substring), city (exact), and state (exact).
func (c *Client) GetBusinesses(ctx context.Context, query string, limit int, filter SearchFilter) (SearchResult, error) {
	fields := []graphql.Field{
		{Name: "businessId"},
		{Name: "businessName"},
		{Name: "city"},
		{Name: "state"},
		{Name: "stars"},
		{Name: "_additional { certainty score }"},
	}

	getter := c.wv.GraphQL().Get().
		WithClassName(collectionName).
		WithFields(fields...)

	if query != "" && filter.Alpha > 0 {
		hybrid := c.wv.GraphQL().HybridArgumentBuilder().
			WithQuery(query).
			WithAlpha(filter.Alpha).
			WithFusionType(graphql.RelativeScore)
		getter = getter.WithHybrid(hybrid)
		getter = getter.WithLimit(limit * 20)
	} else if query != "" {
		nearText := c.wv.GraphQL().NearTextArgBuilder().WithConcepts([]string{query})
		getter = getter.WithNearText(nearText)
		getter = getter.WithLimit(limit * 20)
	} else {
		// If no query, we are likely fetching by specific ID or just getting top recent ones.
		// Use a smaller limit if we have an ID filter to avoid scanning everything.
		fetchLimit := 100
		if filter.BusinessID != "" {
			fetchLimit = limit * 20
		}
		getter = getter.WithLimit(fetchLimit)
	}

	if where := buildWhereFilter(filter); where != nil {
		getter = getter.WithWhere(where)
	}

	resp, err := getter.Do(ctx)
	if err != nil {
		return SearchResult{}, fmt.Errorf("graphql query: %w", err)
	}
	if resp.Errors != nil {
		return SearchResult{}, fmt.Errorf("graphql errors: %s", formatGraphQLErrors(resp.Errors))
	}

	return aggregateTopK(resp.Data, limit), nil
}

// GetReviews returns the top reviews from a nearText vector query, sorted by certainty score (descending).
func (c *Client) GetReviews(ctx context.Context, query string, limit int, filter SearchFilter) (SearchReviews, error) {
	fields := []graphql.Field{
		{Name: "reviewId"},
		{Name: "businessId"},
		{Name: "businessName"},
		{Name: "city"},
		{Name: "state"},
		{Name: "text"},
		{Name: "_additional { certainty score }"},
	}

	getter := c.wv.GraphQL().Get().
		WithClassName(collectionName).
		WithFields(fields...)

	if query != "" && filter.Alpha > 0 {
		hybrid := c.wv.GraphQL().HybridArgumentBuilder().
			WithQuery(query).
			WithAlpha(filter.Alpha).
			WithFusionType(graphql.RelativeScore)
		getter = getter.WithHybrid(hybrid)
		getter = getter.WithLimit(limit * 20)
	} else if query != "" {
		nearText := c.wv.GraphQL().NearTextArgBuilder().WithConcepts([]string{query})
		getter = getter.WithNearText(nearText)
		getter = getter.WithLimit(limit * 20)
	} else {
		getter = getter.WithLimit(limit * 10)
	}

	if where := buildWhereFilter(filter); where != nil {
		getter = getter.WithWhere(where)
	}

	resp, err := getter.Do(ctx)
	if err != nil {
		return SearchReviews{}, fmt.Errorf("graphql query: %w", err)
	}
	if resp.Errors != nil {
		return SearchReviews{}, fmt.Errorf("graphql errors: %s", formatGraphQLErrors(resp.Errors))
	}

	return getReviews(resp.Data, limit), nil
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
				WithOperator(filters.Like).
				WithValueText("*"+f.City+"*"))
	}
	if f.State != "" {
		operands = append(operands,
			filters.Where().
				WithPath([]string{"state"}).
				WithOperator(filters.Like).
				WithValueText("*"+f.State+"*"))
	}
	if f.BusinessID != "" {
		operands = append(operands,
			filters.Where().
				WithPath([]string{"businessId"}).
				WithOperator(filters.Equal).
				WithValueText(f.BusinessID))
	}
	if len(f.ReviewIDs) > 0 {
		var idOperands []*filters.WhereBuilder
		for _, id := range f.ReviewIDs {
			idOperands = append(idOperands,
				filters.Where().
					WithPath([]string{"reviewId"}).
					WithOperator(filters.Equal).
					WithValueText(id))
		}
		if len(idOperands) > 0 {
			operands = append(operands,
				filters.Where().
					WithOperator(filters.Or).
					WithOperands(idOperands))
		}
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

// extractScore returns the hybrid score if present, then certainty, then 1.0.
// Weaviate populates _additional.score for hybrid queries and _additional.certainty for nearText queries.
func extractScore(additional map[string]any) float64 {
	if s, ok := additional["score"].(float64); ok && s > 0 {
		return s
	}
	if c, ok := additional["certainty"].(float64); ok && c > 0 {
		return c
	}
	return 1.0
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
		stars  []float64
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
		certainty := extractScore(additional)

		stars, _ := obj["stars"].(float64)

		e := entries[businessID]
		if e == nil {
			name, _ := obj["businessName"].(string)
			city, _ := obj["city"].(string)
			state, _ := obj["state"].(string)
			e = &bizEntry{name: name, city: city, state: state}
			entries[businessID] = e
		}
		e.scores = append(e.scores, certainty)
		if stars > 0 {
			e.stars = append(e.stars, stars)
		}
	}

	// Compute top-K average per business.
	type ranked struct {
		id    string
		name  string
		city  string
		state string
		stars float64
		score float64
	}
	results := make([]ranked, 0, len(entries))
	for id, e := range entries {
		s := e.scores
		sort.Slice(s, func(i, j int) bool { return s[i] > s[j] })
		k := min(topKReviews, len(s))
		var sum float64
		for i := 0; i < k; i++ {
			sum += s[i]
		}
		results = append(results, ranked{
			id:    id,
			name:  e.name,
			city:  e.city,
			state: e.state,
			score: sum / float64(k),
			stars: func() float64 {
				if len(e.stars) == 0 {
					return 0
				}
				var sSum float64
				for _, s := range e.stars {
					sSum += s
				}
				return sSum / float64(len(e.stars))
			}(),
		})
	}

	sort.Slice(results, func(i, j int) bool { return results[i].score > results[j].score })

	out := make([]BusinessResult, 0, min(limit, len(results)))
	for i := 0; i < limit && i < len(results); i++ {
		out = append(out, BusinessResult{
			ID:    results[i].id,
			Name:  results[i].name,
			City:  results[i].city,
			State: results[i].state,
			Stars: results[i].stars,
			Score: results[i].score,
		})
	}
	return SearchResult{Businesses: out}
}

// getReviews returns the top reviews from the search results, sorted by certainty score (descending).
func getReviews(data map[string]models.JSONObject, limit int) SearchReviews {
	getRaw, ok := data["Get"]
	if !ok {
		return SearchReviews{BusinessReviews: []RankedReview{}}
	}
	getMap, ok := getRaw.(map[string]any)
	if !ok {
		return SearchReviews{BusinessReviews: []RankedReview{}}
	}
	rawItems, ok := getMap[collectionName]
	if !ok {
		return SearchReviews{BusinessReviews: []RankedReview{}}
	}
	items, ok := rawItems.([]any)
	if !ok {
		return SearchReviews{BusinessReviews: []RankedReview{}}
	}

	results := make([]RankedReview, 0, len(items))
	for _, raw := range items {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		businessID, _ := obj["businessId"].(string)
		reviewID, _ := obj["reviewId"].(string)
		if businessID == "" || reviewID == "" {
			continue
		}
		additional, _ := obj["_additional"].(map[string]any)
		certainty := extractScore(additional)

		name, _ := obj["businessName"].(string)
		city, _ := obj["city"].(string)
		state, _ := obj["state"].(string)
		text, _ := obj["text"].(string)
		results = append(results, RankedReview{
			Review: IndexItem{
				Review:       schemas.Review{BusinessID: businessID, ReviewID: reviewID, Text: text},
				BusinessName: name,
				City:         city,
				State:        state,
			},
			Score: certainty,
		})
	}

	slices.SortFunc(results, func(a, b RankedReview) int {
		if a.Score > b.Score {
			return -1
		}
		if a.Score < b.Score {
			return 1
		}
		return 0
	})

	if limit < len(results) {
		results = results[:limit]
	}
	return SearchReviews{BusinessReviews: results}
}

// EnsureFodmapSchema creates the FodmapIngredient collection if it does not already exist.
func (c *Client) EnsureFodmapSchema(ctx context.Context) error {
	_, err := c.wv.Schema().ClassGetter().WithClassName(fodmapCollectionName).Do(ctx)
	if err == nil {
		return nil
	}

	skip := map[string]any{
		"text2vec-transformers": map[string]any{"skip": true},
	}
	class := &models.Class{
		Class:      fodmapCollectionName,
		Vectorizer: "text2vec-transformers",
		ModuleConfig: map[string]any{
			"text2vec-transformers": map[string]any{
				"vectorizeClassName": false,
			},
		},
		Properties: []*models.Property{
			{Name: "ingredient", DataType: []string{"text"}},
			{Name: "level", DataType: []string{"text"}, ModuleConfig: skip},
			{Name: "groups", DataType: []string{"text[]"}, ModuleConfig: skip},
			{Name: "notes", DataType: []string{"text"}, ModuleConfig: skip},
		},
	}
	if err := c.wv.Schema().ClassCreator().WithClass(class).Do(ctx); err != nil {
		return fmt.Errorf("creating fodmap schema: %w", err)
	}
	return nil
}

// BatchUpsertFodmap inserts or updates a batch of FODMAP ingredients in Weaviate.
func (c *Client) BatchUpsertFodmap(ctx context.Context, items map[string]data.FodmapEntry) error {
	batcher := c.wv.Batch().ObjectsBatcher()
	for name, entry := range items {
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("fodmap_"+name)).String()
		batcher = batcher.WithObjects(&models.Object{
			Class: fodmapCollectionName,
			ID:    strfmt.UUID(id),
			Properties: map[string]any{
				"ingredient": name,
				"level":      entry.Level,
				"groups":     entry.Groups,
				"notes":      entry.Notes,
			},
		})
	}
	responses, err := batcher.Do(ctx)
	if err != nil {
		var wErr *fault.WeaviateClientError
		if errors.As(err, &wErr) && wErr.DerivedFromError != nil {
			return fmt.Errorf("batch upsert fodmap: %w", wErr.DerivedFromError)
		}
		return fmt.Errorf("batch upsert fodmap: %w", err)
	}
	for _, resp := range responses {
		if resp.Result != nil && resp.Result.Errors != nil {
			slog.Warn("batch upsert fodmap item error", "errors", resp.Result.Errors)
		}
	}
	return nil
}

// SearchFodmap performs a nearText vector query on the FodmapIngredient collection.
func (c *Client) SearchFodmap(ctx context.Context, ingredient string) (FodmapResult, float64, error) {
	fields := []graphql.Field{
		{Name: "ingredient"},
		{Name: "level"},
		{Name: "groups"},
		{Name: "notes"},
		{Name: "_additional { certainty }"},
	}

	nearText := c.wv.GraphQL().NearTextArgBuilder().WithConcepts([]string{ingredient})

	resp, err := c.wv.GraphQL().Get().
		WithClassName(fodmapCollectionName).
		WithFields(fields...).
		WithNearText(nearText).
		WithLimit(1).
		Do(ctx)

	if err != nil {
		return FodmapResult{}, 0, fmt.Errorf("graphql query: %w", err)
	}
	if resp.Errors != nil {
		return FodmapResult{}, 0, fmt.Errorf("graphql errors: %s", formatGraphQLErrors(resp.Errors))
	}

	res, cert, ok := ParseFodmapResult(resp.Data)
	if !ok {
		return FodmapResult{}, 0, errors.New("not found")
	}
	return res, cert, nil
}

// ParseFodmapResult extracts the top FodmapIngredient match from the GraphQL response.
func ParseFodmapResult(result map[string]models.JSONObject) (FodmapResult, float64, bool) {
	getRaw, ok := result["Get"].(map[string]any)
	if !ok {
		return FodmapResult{}, 0, false
	}
	items, ok := getRaw[fodmapCollectionName].([]any)
	if !ok || len(items) == 0 {
		return FodmapResult{}, 0, false
	}
	obj, ok := items[0].(map[string]any)
	if !ok {
		return FodmapResult{}, 0, false
	}

	ingredient, _ := obj["ingredient"].(string)
	level, _ := obj["level"].(string)
	notes, _ := obj["notes"].(string)

	var groups []string
	if groupsSlice, ok := obj["groups"].([]any); ok {
		for _, g := range groupsSlice {
			if s, ok := g.(string); ok {
				groups = append(groups, s)
			}
		}
	}

	certainty := 0.0
	if additional, ok := obj["_additional"].(map[string]any); ok {
		certainty, _ = additional["certainty"].(float64)
	}

	return FodmapResult{
		Ingredient: ingredient,
		Level:      level,
		Groups:     groups,
		Notes:      notes,
	}, certainty, true
}
func formatGraphQLErrors(errs []*models.GraphQLError) string {
	if len(errs) == 0 {
		return ""
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Message
	}
	return strings.Join(msgs, "; ")
}
