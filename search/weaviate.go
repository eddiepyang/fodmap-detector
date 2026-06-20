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
	collectionName           = "YelpReview"
	chunkCollectionName      = "YelpReviewChunk"
	fodmapCollectionName     = "FodmapIngredient"
	regulatoryCollectionName = "RegulatoryUpdate"
	topKReviews              = 5
)

// MenuItem is a single scraped menu item stored in the RestaurantMenu
// collection. IDs are deterministic so re-scraping the same URL is idempotent.
type MenuItem struct {
	MenuItemID         string
	BusinessID         string
	MenuSection        string
	RestaurantName     string
	City               string
	State              string
	DishName           string
	Description        string
	StatedIngredients  []string
	HasFullIngredients bool
	SourceURL          string
	ScrapedAtUTC       string
	Vector             []float32
}

// Client wraps the Weaviate client with domain-specific operations.
type Client struct {
	wv       *weaviate.Client
	embedder Embedder
}

// BusinessResult pairs a business ID with its human-readable name.
type BusinessResult struct {
	ID         string
	Name       string
	City       string
	State      string
	Categories string
	Stars      float64
	Score      float64
}

// SearchResult holds the ranked list of businesses returned by a search query.
type SearchResult struct {
	Businesses []BusinessResult
}

// FodmapResult represents a parsed FODMAP ingredient from Weaviate.
type FodmapResult struct {
	Ingredient    string
	Level         string
	Groups        []string
	Notes         string
	Substitutions []string
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

// Chunk holds a single text chunk and its embedding vector.
type Chunk struct {
	Text   string
	Vector []float32
}

// IndexItem pairs a review with its associated business metadata for indexing.
// Vector is the legacy single-vector field (used when Chunks is empty).
// Chunks holds the chunked text and per-chunk vectors for parent-child indexing.
type IndexItem struct {
	Review       schemas.Review
	BusinessName string
	City         string
	State        string
	Categories   string
	Vector       []float32 // legacy: used when Chunks is empty
	Chunks       []Chunk   // chunked text with per-chunk vectors
}

// RankedReview pairs a review with its certainty score.
// MatchedChunk contains the specific chunk text that matched the query,
// while Review.Review.Text always contains the full parent review text.
type RankedReview struct {
	Review       IndexItem
	Score        float64
	MatchedChunk string // the specific chunk text that matched the query (may be empty)
}

// NewClient creates a Weaviate client connected to the given host (e.g. "localhost:8090").
func NewClient(host, scheme, apiKey string, embedder Embedder) (*Client, error) {
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
	return &Client{wv: wv, embedder: embedder}, nil
}

// EnsureSchema creates the YelpReview and YelpReviewChunk collections if they do not exist.
func (c *Client) EnsureSchema(ctx context.Context) error {
	parentExists := false
	if _, err := c.wv.Schema().ClassGetter().WithClassName(collectionName).Do(ctx); err == nil {
		parentExists = true
	}

	if !parentExists {
		class := &models.Class{
			Class:      collectionName,
			Vectorizer: "none",
			Properties: []*models.Property{
				{Name: "reviewId", DataType: []string{"text"}},
				{Name: "businessId", DataType: []string{"text"}},
				{Name: "stars", DataType: []string{"number"}},
				{Name: "city", DataType: []string{"text"}},
				{Name: "state", DataType: []string{"text"}},
				{Name: "categories", DataType: []string{"text"}},
				{Name: "businessName", DataType: []string{"text"}},
				{Name: "text", DataType: []string{"text"}},
			},
		}
		if err := c.wv.Schema().ClassCreator().WithClass(class).Do(ctx); err != nil {
			return fmt.Errorf("creating parent schema: %w", err)
		}
	}

	chunkExists := false
	if _, err := c.wv.Schema().ClassGetter().WithClassName(chunkCollectionName).Do(ctx); err == nil {
		chunkExists = true
	}

	if !chunkExists {
		chunkClass := &models.Class{
			Class:      chunkCollectionName,
			Vectorizer: "none",
			Properties: []*models.Property{
				{Name: "chunkText", DataType: []string{"text"}},
				{
					Name:     "hasParent",
					DataType: []string{collectionName},
				},
			},
		}
		if err := c.wv.Schema().ClassCreator().WithClass(chunkClass).Do(ctx); err != nil {
			return fmt.Errorf("creating chunk schema: %w", err)
		}
	}

	return nil
}

// BatchUpsert inserts or updates a batch of reviews and their chunks in Weaviate.
func (c *Client) BatchUpsert(ctx context.Context, items []IndexItem) error {
	if len(items) == 0 {
		return nil
	}

	// 1. Insert Parents
	parentBatcher := c.wv.Batch().ObjectsBatcher()
	for _, item := range items {
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(item.Review.ReviewID)).String()
		parentBatcher = parentBatcher.WithObjects(&models.Object{
			Class: collectionName,
			ID:    strfmt.UUID(id),
			// Parents don't need vectors, vectors live on chunks
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

	parentResponses, err := parentBatcher.Do(ctx)
	if err != nil {
		return fmt.Errorf("parent batch upsert: %w", err)
	}
	for _, resp := range parentResponses {
		if resp.Result != nil && resp.Result.Errors != nil {
			slog.Warn("parent upsert error", "errors", resp.Result.Errors)
		}
	}

	// 2. Insert Chunks
	chunkBatcher := c.wv.Batch().ObjectsBatcher()
	type refInfo struct {
		from strfmt.UUID
		to   strfmt.UUID
	}
	var refs []refInfo

	for _, item := range items {
		parentUUID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(item.Review.ReviewID)).String()

		chunksToProcess := item.Chunks
		if len(chunksToProcess) == 0 && item.Vector != nil {
			// Legacy fallback
			chunksToProcess = []Chunk{{Text: item.Review.Text, Vector: item.Vector}}
		}

		for i, chunk := range chunksToProcess {
			// Deterministic chunk ID based on review ID and index
			chunkUUID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("%s_chunk_%d", item.Review.ReviewID, i))).String()

			chunkBatcher = chunkBatcher.WithObjects(&models.Object{
				Class:  chunkCollectionName,
				ID:     strfmt.UUID(chunkUUID),
				Vector: models.C11yVector(chunk.Vector),
				Properties: map[string]any{
					"chunkText": chunk.Text,
				},
			})

			refs = append(refs, refInfo{from: strfmt.UUID(chunkUUID), to: strfmt.UUID(parentUUID)})
		}
	}

	chunkResponses, err := chunkBatcher.Do(ctx)
	if err != nil {
		return fmt.Errorf("chunk batch upsert: %w", err)
	}
	for _, resp := range chunkResponses {
		if resp.Result != nil && resp.Result.Errors != nil {
			slog.Warn("chunk upsert error", "errors", resp.Result.Errors)
		}
	}

	// 3. Add Cross-References
	refBatcher := c.wv.Batch().ReferencesBatcher()
	for _, r := range refs {
		refBatcher = refBatcher.WithReferences(
			&models.BatchReference{
				From: strfmt.URI(fmt.Sprintf("weaviate://localhost/%s/%s/hasParent", chunkCollectionName, r.from)),
				To:   strfmt.URI(fmt.Sprintf("weaviate://localhost/%s/%s", collectionName, r.to)),
			},
		)
	}

	refResponses, err := refBatcher.Do(ctx)
	if err != nil {
		return fmt.Errorf("ref batch upsert: %w", err)
	}
	for _, resp := range refResponses {
		if resp.Result != nil && resp.Result.Errors != nil {
			slog.Warn("ref upsert error", "errors", resp.Result.Errors)
		}
	}

	return nil
}

// Businesses performs a nearText vector query and returns restaurant IDs ranked by
// Top-K average certainty score (K=topKReviews). Optional filters narrow results
// by category (substring), city (exact), and state (exact).
func (c *Client) Businesses(ctx context.Context, query string, limit int, filter SearchFilter) (SearchResult, error) {
	fields := []graphql.Field{
		{Name: "chunkText"},
		{
			Name: "hasParent",
			Fields: []graphql.Field{
				{
					Name: "... on " + collectionName,
					Fields: []graphql.Field{
						{Name: "reviewId"},
						{Name: "businessId"},
						{Name: "businessName"},
						{Name: "city"},
						{Name: "state"},
						{Name: "categories"},
						{Name: "stars"},
					},
				},
			},
		},
		{Name: "_additional { certainty score }"},
	}

	getter := c.wv.GraphQL().Get().
		WithClassName(chunkCollectionName).
		WithFields(fields...)

	if query != "" && filter.Alpha > 0 {
		if c.embedder == nil {
			return SearchResult{}, errors.New("embedder is not configured (required for hybrid search)")
		}
		vec, err := c.embedder.EmbedSingle(ctx, query)
		if err != nil {
			return SearchResult{}, fmt.Errorf("embedding query: %w", err)
		}
		hybrid := c.wv.GraphQL().HybridArgumentBuilder().
			WithQuery(query).
			WithVector(vec).
			WithAlpha(filter.Alpha).
			WithFusionType(graphql.RelativeScore)
		getter = getter.WithHybrid(hybrid)
		getter = getter.WithLimit(limit * 20)
	} else if query != "" {
		if c.embedder == nil {
			return SearchResult{}, errors.New("embedder is not configured (required for semantic search)")
		}
		vec, err := c.embedder.EmbedSingle(ctx, query)
		if err != nil {
			return SearchResult{}, fmt.Errorf("embedding query: %w", err)
		}
		nearVector := c.wv.GraphQL().NearVectorArgBuilder().WithVector(vec)
		getter = getter.WithNearVector(nearVector)
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

// Reviews returns the top reviews from a nearText vector query, sorted by certainty score (descending).
func (c *Client) Reviews(ctx context.Context, query string, limit int, filter SearchFilter) (SearchReviews, error) {
	fields := []graphql.Field{
		{Name: "chunkText"},
		{
			Name: "hasParent",
			Fields: []graphql.Field{
				{
					Name: "... on " + collectionName,
					Fields: []graphql.Field{
						{Name: "reviewId"},
						{Name: "businessId"},
						{Name: "businessName"},
						{Name: "city"},
						{Name: "state"},
						{Name: "text"},
					},
				},
			},
		},
		{Name: "_additional { certainty score }"},
	}

	getter := c.wv.GraphQL().Get().
		WithClassName(chunkCollectionName).
		WithFields(fields...)

	if query != "" && filter.Alpha > 0 {
		if c.embedder == nil {
			return SearchReviews{}, errors.New("embedder is not configured (required for hybrid search)")
		}
		vec, err := c.embedder.EmbedSingle(ctx, query)
		if err != nil {
			return SearchReviews{}, fmt.Errorf("embedding query: %w", err)
		}
		hybrid := c.wv.GraphQL().HybridArgumentBuilder().
			WithQuery(query).
			WithVector(vec).
			WithAlpha(filter.Alpha).
			WithFusionType(graphql.RelativeScore)
		getter = getter.WithHybrid(hybrid)
		getter = getter.WithLimit(limit * 20)
	} else if query != "" {
		if c.embedder == nil {
			return SearchReviews{}, errors.New("embedder is not configured (required for semantic search)")
		}
		vec, err := c.embedder.EmbedSingle(ctx, query)
		if err != nil {
			return SearchReviews{}, fmt.Errorf("embedding query: %w", err)
		}
		nearVector := c.wv.GraphQL().NearVectorArgBuilder().WithVector(vec)
		getter = getter.WithNearVector(nearVector)
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
				WithPath([]string{"hasParent", collectionName, "categories"}).
				WithOperator(filters.Like).
				WithValueText("*"+f.Category+"*"))
	}
	if f.City != "" {
		operands = append(operands,
			filters.Where().
				WithPath([]string{"hasParent", collectionName, "city"}).
				WithOperator(filters.Like).
				WithValueText("*"+f.City+"*"))
	}
	if f.State != "" {
		operands = append(operands,
			filters.Where().
				WithPath([]string{"hasParent", collectionName, "state"}).
				WithOperator(filters.Like).
				WithValueText("*"+f.State+"*"))
	}
	if f.BusinessID != "" {
		operands = append(operands,
			filters.Where().
				WithPath([]string{"hasParent", collectionName, "businessId"}).
				WithOperator(filters.Equal).
				WithValueText(f.BusinessID))
	}
	if len(f.ReviewIDs) > 0 {
		var idOperands []*filters.WhereBuilder
		for _, id := range f.ReviewIDs {
			idOperands = append(idOperands,
				filters.Where().
					WithPath([]string{"hasParent", collectionName, "reviewId"}).
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
	rawItems, ok := getMap[chunkCollectionName]
	if !ok {
		return SearchResult{Businesses: []BusinessResult{}}
	}
	items, ok := rawItems.([]any)
	if !ok {
		return SearchResult{Businesses: []BusinessResult{}}
	}

	// Collect certainty scores and name per business.
	// deduplicate by review ID to take only the best chunk per review.
	type bizEntry struct {
		name        string
		city        string
		state       string
		categories  string
		scores      []float64
		stars       []float64
		seenReviews map[string]bool
	}
	entries := make(map[string]*bizEntry)

	for _, raw := range items {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		// Extract parent object
		parentObj, parentExists := extractParent(obj)
		if !parentExists {
			continue
		}

		businessID, _ := parentObj["businessId"].(string)
		reviewID, _ := parentObj["reviewId"].(string)
		if businessID == "" || reviewID == "" {
			continue
		}

		additional, _ := obj["_additional"].(map[string]any)
		certainty := extractScore(additional)

		e := entries[businessID]
		if e == nil {
			name, _ := parentObj["businessName"].(string)
			city, _ := parentObj["city"].(string)
			state, _ := parentObj["state"].(string)
			categories, _ := parentObj["categories"].(string)
			e = &bizEntry{
				name: name, city: city, state: state, categories: categories,
				seenReviews: make(map[string]bool),
			}
			entries[businessID] = e
		}

		// Keep only the best chunk per review. The GraphQL query returns results
		// sorted by vector distance, so the first time we see a reviewID it's the best chunk.
		if !e.seenReviews[reviewID] {
			e.seenReviews[reviewID] = true
			e.scores = append(e.scores, certainty)

			stars, _ := parentObj["stars"].(float64)
			if stars > 0 {
				e.stars = append(e.stars, stars)
			}
		}
	}

	// Compute top-K average per business.
	type ranked struct {
		id         string
		name       string
		city       string
		state      string
		categories string
		stars      float64
		score      float64
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
			id:         id,
			name:       e.name,
			city:       e.city,
			state:      e.state,
			categories: e.categories,
			score:      sum / float64(k),
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
			ID:         results[i].id,
			Name:       results[i].name,
			City:       results[i].city,
			State:      results[i].state,
			Categories: results[i].categories,
			Stars:      results[i].stars,
			Score:      results[i].score,
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
	rawItems, ok := getMap[chunkCollectionName]
	if !ok {
		return SearchReviews{BusinessReviews: []RankedReview{}}
	}
	items, ok := rawItems.([]any)
	if !ok {
		return SearchReviews{BusinessReviews: []RankedReview{}}
	}

	results := make([]RankedReview, 0, len(items))
	seenReviews := make(map[string]bool)

	for _, raw := range items {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		parentObj, parentExists := extractParent(obj)
		if !parentExists {
			continue
		}

		businessID, _ := parentObj["businessId"].(string)
		reviewID, _ := parentObj["reviewId"].(string)
		if businessID == "" || reviewID == "" {
			continue
		}

		// Deduplicate: keep only the best chunk per review
		if seenReviews[reviewID] {
			continue
		}
		seenReviews[reviewID] = true

		chunkText, _ := obj["chunkText"].(string)

		additional, _ := obj["_additional"].(map[string]any)
		certainty := extractScore(additional)

		name, _ := parentObj["businessName"].(string)
		city, _ := parentObj["city"].(string)
		state, _ := parentObj["state"].(string)
		text, _ := parentObj["text"].(string)

		results = append(results, RankedReview{
			Review: IndexItem{
				Review:       schemas.Review{BusinessID: businessID, ReviewID: reviewID, Text: text},
				BusinessName: name,
				City:         city,
				State:        state,
			},
			Score:        certainty,
			MatchedChunk: chunkText,
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

// EnsureFodmapSchema creates the FodmapIngredient collection if it does not
// already exist, and ensures all properties have the correct data types. If a
// property has the wrong type (e.g. "text" instead of "text[]"), the class is
// deleted and recreated to fix the schema.
func (c *Client) EnsureFodmapSchema(ctx context.Context) error {
	desiredProps := []*models.Property{
		{Name: "ingredient", DataType: []string{"text"}},
		{Name: "level", DataType: []string{"text"}},
		{Name: "groups", DataType: []string{"text[]"}},
		{Name: "notes", DataType: []string{"text"}},
		{Name: "substitutions", DataType: []string{"text[]"}},
	}

	existing, err := c.wv.Schema().ClassGetter().WithClassName(fodmapCollectionName).Do(ctx)
	if err != nil {
		// Class doesn't exist yet — create it.
		class := &models.Class{
			Class:      fodmapCollectionName,
			Vectorizer: "none",
			Properties: desiredProps,
		}
		if createErr := c.wv.Schema().ClassCreator().WithClass(class).Do(ctx); createErr != nil {
			return fmt.Errorf("creating fodmap schema: %w", createErr)
		}
		return nil
	}

	// Class exists — verify property types match. If any mismatch, drop and
	// recreate because Weaviate does not support altering property data types.
	propMap := make(map[string][]string, len(existing.Properties))
	for _, p := range existing.Properties {
		propMap[p.Name] = p.DataType
	}
	needsRecreate := false
	for _, dp := range desiredProps {
		existingTypes, ok := propMap[dp.Name]
		if !ok {
			needsRecreate = true
			break
		}
		if len(existingTypes) != len(dp.DataType) {
			needsRecreate = true
			break
		}
		for i, t := range dp.DataType {
			if existingTypes[i] != t {
				needsRecreate = true
				break
			}
		}
	}
	if !needsRecreate && len(existing.Properties) != len(desiredProps) {
		needsRecreate = true
	}

	if needsRecreate {
		slog.Warn("recreating FodmapIngredient class to fix schema", "existing_props", len(existing.Properties), "desired_props", len(desiredProps))
		if err := c.wv.Schema().ClassDeleter().WithClassName(fodmapCollectionName).Do(ctx); err != nil {
			return fmt.Errorf("deleting fodmap class for schema fix: %w", err)
		}
		class := &models.Class{
			Class:      fodmapCollectionName,
			Vectorizer: "none",
			Properties: desiredProps,
		}
		if err := c.wv.Schema().ClassCreator().WithClass(class).Do(ctx); err != nil {
			return fmt.Errorf("recreating fodmap schema: %w", err)
		}
	}

	return nil
}

// BatchUpsertFodmap inserts or updates a batch of FODMAP ingredients in Weaviate.
// Vectors are pre-computed using the embedder since the vectorizer is set to "none".
func (c *Client) BatchUpsertFodmap(ctx context.Context, items map[string]data.FodmapEntry) error {
	if c.embedder == nil {
		return errors.New("embedder is not configured (required for fodmap ingredients)")
	}
	batcher := c.wv.Batch().ObjectsBatcher()
	for name, entry := range items {
		groups := entry.Groups
		if groups == nil {
			groups = []string{}
		}
		subs := entry.Substitutions
		if subs == nil {
			subs = []string{}
		}
		vec, err := c.embedder.EmbedSingle(ctx, "search_document: "+name)
		if err != nil {
			return fmt.Errorf("embedding fodmap %q: %w", name, err)
		}
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("fodmap_"+name)).String()
		batcher = batcher.WithObjects(&models.Object{
			Class:  fodmapCollectionName,
			ID:     strfmt.UUID(id),
			Vector: models.C11yVector(vec),
			Properties: map[string]any{
				"ingredient":    name,
				"level":         entry.Level,
				"groups":        groups,
				"notes":         entry.Notes,
				"substitutions": subs,
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

// UpsertFodmapItem embeds and upserts a single ingredient into Weaviate.
func (c *Client) UpsertFodmapItem(ctx context.Context, name string, entry data.FodmapEntry) error {
	if c.embedder == nil {
		return errors.New("embedder is not configured")
	}
	groups := entry.Groups
	if groups == nil {
		groups = []string{}
	}
	subs := entry.Substitutions
	if subs == nil {
		subs = []string{}
	}
	vec, err := c.embedder.EmbedSingle(ctx, "search_document: "+name)
	if err != nil {
		return fmt.Errorf("embedding fodmap %q: %w", name, err)
	}
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("fodmap_"+name)).String()
	_, err = c.wv.Data().Creator().
		WithClassName(fodmapCollectionName).
		WithID(id).
		WithVector(models.C11yVector(vec)).
		WithProperties(map[string]any{
			"ingredient":    name,
			"level":         entry.Level,
			"groups":        groups,
			"notes":         entry.Notes,
			"substitutions": subs,
		}).Do(ctx)
	if err != nil {
		return fmt.Errorf("upsert fodmap item %q: %w", name, err)
	}
	return nil
}

// DeleteFodmapItem removes a single ingredient from Weaviate. It uses the
// deterministic ID convention and does not treat "not found" as an error.
func (c *Client) DeleteFodmapItem(ctx context.Context, name string) error {
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("fodmap_"+name)).String()
	err := c.wv.Data().Deleter().
		WithClassName(fodmapCollectionName).
		WithID(id).
		Do(ctx)
	if err != nil {
		var wErr *fault.WeaviateClientError
		if errors.As(err, &wErr) && wErr.DerivedFromError != nil {
			return fmt.Errorf("delete fodmap item %q: %w", name, wErr.DerivedFromError)
		}
		return fmt.Errorf("delete fodmap item %q: %w", name, err)
	}
	return nil
}

// SearchFodmap performs a nearVector query on the FodmapIngredient collection.
func (c *Client) SearchFodmap(ctx context.Context, ingredient string) (FodmapResult, float64, error) {
	if c.embedder == nil {
		return FodmapResult{}, 0, errors.New("embedder is not configured")
	}
	vec, err := c.embedder.EmbedSingle(ctx, ingredient)
	if err != nil {
		return FodmapResult{}, 0, fmt.Errorf("embedding ingredient: %w", err)
	}

	fields := []graphql.Field{
		{Name: "ingredient"},
		{Name: "level"},
		{Name: "groups"},
		{Name: "notes"},
		{Name: "substitutions"},
		{Name: "_additional { certainty }"},
	}

	nearVector := c.wv.GraphQL().NearVectorArgBuilder().WithVector(vec)

	resp, err := c.wv.GraphQL().Get().
		WithClassName(fodmapCollectionName).
		WithFields(fields...).
		WithNearVector(nearVector).
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

	var substitutions []string
	if subSlice, ok := obj["substitutions"].([]any); ok {
		for _, s := range subSlice {
			if str, ok := s.(string); ok {
				substitutions = append(substitutions, str)
			}
		}
	}

	certainty := 0.0
	if additional, ok := obj["_additional"].(map[string]any); ok {
		certainty, _ = additional["certainty"].(float64)
	}

	return FodmapResult{
		Ingredient:    ingredient,
		Level:         level,
		Groups:        groups,
		Notes:         notes,
		Substitutions: substitutions,
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

// ─── RestaurantMenu collection ────────────────────────────────────────────────

const menuCollectionName = "RestaurantMenu"

// EnsureMenuSchema creates the RestaurantMenu Weaviate collection if absent.
// It is idempotent — safe to call on every scrape-command startup.
func (c *Client) EnsureMenuSchema(ctx context.Context) error {
	_, err := c.wv.Schema().ClassGetter().WithClassName(menuCollectionName).Do(ctx)
	if err == nil {
		return nil
	}
	class := &models.Class{
		Class:      menuCollectionName,
		Vectorizer: "none",
		Properties: []*models.Property{
			{Name: "menuItemId", DataType: []string{"text"}},
			{Name: "businessId", DataType: []string{"text"}},
			{Name: "menuSection", DataType: []string{"text"}},
			{Name: "restaurantName", DataType: []string{"text"}},
			{Name: "city", DataType: []string{"text"}},
			{Name: "state", DataType: []string{"text"}},
			{Name: "dishName", DataType: []string{"text"}},
			{Name: "description", DataType: []string{"text"}},
			{Name: "statedIngredients", DataType: []string{"text[]"}},
			{Name: "hasFullIngredients", DataType: []string{"boolean"}},
			{Name: "sourceUrl", DataType: []string{"text"}},
			{Name: "scrapedAtUtc", DataType: []string{"text"}},
		},
	}
	if err := c.wv.Schema().ClassCreator().WithClass(class).Do(ctx); err != nil {
		return fmt.Errorf("creating menu schema: %w", err)
	}
	return nil
}

// BatchUpsertMenu inserts or updates scraped menu items. Each item carries a
// pre-computed Vector and a deterministic MenuItemID for idempotent upserts.
func (c *Client) BatchUpsertMenu(ctx context.Context, items []MenuItem) error {
	batcher := c.wv.Batch().ObjectsBatcher()
	for _, item := range items {
		batcher = batcher.WithObjects(&models.Object{
			Class:  menuCollectionName,
			ID:     strfmt.UUID(item.MenuItemID),
			Vector: models.C11yVector(item.Vector),
			Properties: map[string]any{
				"menuItemId":         item.MenuItemID,
				"businessId":         item.BusinessID,
				"menuSection":        item.MenuSection,
				"restaurantName":     item.RestaurantName,
				"city":               item.City,
				"state":              item.State,
				"dishName":           item.DishName,
				"description":        item.Description,
				"statedIngredients":  item.StatedIngredients,
				"hasFullIngredients": item.HasFullIngredients,
				"sourceUrl":          item.SourceURL,
				"scrapedAtUtc":       item.ScrapedAtUTC,
			},
		})
	}
	responses, err := batcher.Do(ctx)
	if err != nil {
		var wErr *fault.WeaviateClientError
		if errors.As(err, &wErr) && wErr.DerivedFromError != nil {
			return fmt.Errorf("batch upsert menu: %w", wErr.DerivedFromError)
		}
		return fmt.Errorf("batch upsert menu: %w", err)
	}
	for _, resp := range responses {
		if resp.Result != nil && resp.Result.Errors != nil {
			slog.Warn("batch upsert menu item error", "errors", resp.Result.Errors)
		}
	}
	return nil
}

// SearchMenu performs a nearVector semantic search over the RestaurantMenu collection.
func (c *Client) SearchMenu(ctx context.Context, query string, limit int) ([]MenuItem, error) {
	if c.embedder == nil {
		return nil, errors.New("embedder is not configured (required for menu search)")
	}
	vec, err := c.embedder.EmbedSingle(ctx, "search_query: "+query)
	if err != nil {
		return nil, fmt.Errorf("embedding query: %w", err)
	}
	fields := []graphql.Field{
		{Name: "menuItemId"}, {Name: "businessId"}, {Name: "restaurantName"},
		{Name: "dishName"}, {Name: "description"}, {Name: "statedIngredients"},
		{Name: "hasFullIngredients"}, {Name: "sourceUrl"}, {Name: "city"}, {Name: "state"},
		{Name: "_additional { certainty }"},
	}
	resp, err := c.wv.GraphQL().Get().
		WithClassName(menuCollectionName).
		WithFields(fields...).
		WithNearVector(c.wv.GraphQL().NearVectorArgBuilder().WithVector(vec)).
		WithLimit(limit).
		Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("menu search: %w", err)
	}
	if resp.Errors != nil {
		return nil, fmt.Errorf("menu search graphql: %s", formatGraphQLErrors(resp.Errors))
	}
	raw, ok := resp.Data["Get"].(map[string]any)
	if !ok {
		return nil, nil
	}
	rawItems, ok := raw[menuCollectionName].([]any)
	if !ok {
		return nil, nil
	}
	var results []MenuItem
	for _, ri := range rawItems {
		m, ok := ri.(map[string]any)
		if !ok {
			continue
		}
		results = append(results, MenuItem{
			MenuItemID:         stringField(m, "menuItemId"),
			BusinessID:         stringField(m, "businessId"),
			RestaurantName:     stringField(m, "restaurantName"),
			DishName:           stringField(m, "dishName"),
			Description:        stringField(m, "description"),
			StatedIngredients:  stringSliceField(m, "statedIngredients"),
			HasFullIngredients: boolField(m, "hasFullIngredients"),
			SourceURL:          stringField(m, "sourceUrl"),
			City:               stringField(m, "city"),
			State:              stringField(m, "state"),
		})
	}
	return results, nil
}

func stringField(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// RegulatoryUpdate is a single regulatory update stored in the
// RegulatoryUpdate Weaviate collection for semantic search.
type RegulatoryUpdate struct {
	ID            string
	SourceID      string
	SourceURL     string
	CASNumber     string
	SubstanceName string
	ChangeType    string
	Description   string
	EffectiveDate string
	Vector        []float32
}

// EnsureRegulatorySchema creates the RegulatoryUpdate Weaviate collection if
// absent. It follows the same one-function-per-collection pattern as
// EnsureFodmapSchema and EnsureMenuSchema.
func (c *Client) EnsureRegulatorySchema(ctx context.Context) error {
	_, err := c.wv.Schema().ClassGetter().WithClassName(regulatoryCollectionName).Do(ctx)
	if err == nil {
		return nil
	}
	class := &models.Class{
		Class:      regulatoryCollectionName,
		Vectorizer: "none",
		Properties: []*models.Property{
			{Name: "sourceId", DataType: []string{"text"}},
			{Name: "sourceUrl", DataType: []string{"text"}},
			{Name: "casNumber", DataType: []string{"text"}},
			{Name: "substanceName", DataType: []string{"text"}},
			{Name: "changeType", DataType: []string{"text"}},
			{Name: "description", DataType: []string{"text"}},
			{Name: "effectiveDate", DataType: []string{"text"}},
		},
	}
	if err := c.wv.Schema().ClassCreator().WithClass(class).Do(ctx); err != nil {
		return fmt.Errorf("creating regulatory update schema: %w", err)
	}
	return nil
}

// BatchUpsertRegulatory inserts or updates regulatory updates in Weaviate.
// Each item carries a pre-computed Vector and a deterministic ID for idempotent
// upserts, matching the BatchUpsertFodmap pattern.
func (c *Client) BatchUpsertRegulatory(ctx context.Context, items []RegulatoryUpdate) error {
	if c.embedder == nil {
		return errors.New("embedder is not configured (required for regulatory update upsert)")
	}
	var regUpdateNS = uuid.MustParse("a3c8e6d0-7f2b-4b1a-9c5d-3e8f1a2b4c6d")
	batcher := c.wv.Batch().ObjectsBatcher()
	for _, item := range items {
		vec := item.Vector
		if len(vec) == 0 {
			var err error
			vec, err = c.embedder.EmbedSingle(ctx, "search_document: "+item.SubstanceName)
			if err != nil {
				return fmt.Errorf("embedding regulatory update %q: %w", item.SubstanceName, err)
			}
		}
		id := uuid.NewSHA1(regUpdateNS, []byte(item.ID)).String()
		batcher = batcher.WithObjects(&models.Object{
			Class:  regulatoryCollectionName,
			ID:     strfmt.UUID(id),
			Vector: models.C11yVector(vec),
			Properties: map[string]any{
				"sourceId":      item.SourceID,
				"sourceUrl":     item.SourceURL,
				"casNumber":     item.CASNumber,
				"substanceName": item.SubstanceName,
				"changeType":    item.ChangeType,
				"description":   item.Description,
				"effectiveDate": item.EffectiveDate,
			},
		})
	}
	responses, err := batcher.Do(ctx)
	if err != nil {
		var wErr *fault.WeaviateClientError
		if errors.As(err, &wErr) && wErr.DerivedFromError != nil {
			return fmt.Errorf("batch upsert regulatory: %w", wErr.DerivedFromError)
		}
		return fmt.Errorf("batch upsert regulatory: %w", err)
	}
	for _, resp := range responses {
		if resp.Result != nil && resp.Result.Errors != nil {
			slog.Warn("batch upsert regulatory update error", "errors", resp.Result.Errors)
		}
	}
	return nil
}

func stringSliceField(m map[string]any, key string) []string {
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func boolField(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}

// extractParent is a helper to pull the parent YelpReview object out of the hasParent reference
func extractParent(obj map[string]any) (map[string]any, bool) {
	hasParentRaw, ok := obj["hasParent"]
	if !ok || hasParentRaw == nil {
		return nil, false
	}
	hasParentArr, ok := hasParentRaw.([]any)
	if !ok || len(hasParentArr) == 0 {
		return nil, false
	}
	parentWrapper, ok := hasParentArr[0].(map[string]any)
	if !ok {
		return nil, false
	}
	// Weaviate returns refs grouped by type, e.g., "... on YelpReview": {...}
	parentRaw, ok := parentWrapper["... on "+collectionName]
	if ok {
		if parentObj, ok := parentRaw.(map[string]any); ok {
			return parentObj, true
		}
	}
	// Fall back to returning parentWrapper directly if it has the required fields
	if _, ok := parentWrapper["businessId"]; ok {
		return parentWrapper, true
	}
	return nil, false
}
