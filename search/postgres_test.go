package search

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"fodmap/data"
	"fodmap/data/schemas"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/pgvector/pgvector-go"
)

// mockEmbedder implements Embedder for testing.
type mockEmbedder struct {
	vec []float32
	err error // if non-nil, EmbedSingle/EmbedBatch return this error
}

func (m *mockEmbedder) EmbedSingle(_ context.Context, _ string) ([]float32, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.vec, nil
}

func (m *mockEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	if m.err != nil {
		return nil, m.err
	}
	result := make([][]float32, len(texts))
	for i := range texts {
		result[i] = m.vec
	}
	return result, nil
}

func (m *mockEmbedder) Close() error { return nil }

func TestPostgresClient_EnsureSchema(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	client := &PostgresClient{
		db:       db,
		embedder: nil,
	}

	err = client.EnsureSchema(context.Background())
	if err != nil {
		t.Errorf("EnsureSchema returned error: %v", err)
	}
}

func TestPostgresClient_EnsureFodmapSchema(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	client := &PostgresClient{
		db:       db,
		embedder: nil,
	}

	err = client.EnsureFodmapSchema(context.Background())
	if err != nil {
		t.Errorf("EnsureFodmapSchema returned error: %v", err)
	}
}

func TestPostgresClient_BatchUpsert(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	client := &PostgresClient{
		db:       db,
		embedder: nil,
	}

	items := []IndexItem{
		{
			Review: schemas.Review{
				ReviewID:   "rev1",
				BusinessID: "bus1",
				Stars:      4.5,
				Text:       "Great food!",
			},
			BusinessName: "Tasty Bites",
			City:         "New York",
			State:        "NY",
			Categories:   "Restaurant, Food",
			Vector:       []float32{0.1, 0.2, 0.3},
		},
	}

	mock.ExpectBegin()
	prepRev := mock.ExpectPrepare("INSERT INTO reviews")
	prepDel := mock.ExpectPrepare("DELETE FROM review_chunks")
	prepChunk := mock.ExpectPrepare("INSERT INTO review_chunks")

	prepRev.ExpectExec().
		WithArgs(
			"rev1",
			"bus1",
			"Tasty Bites",
			"New York",
			"NY",
			"Restaurant, Food",
			4.5,
			"Great food!",
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	prepDel.ExpectExec().
		WithArgs("rev1").
		WillReturnResult(sqlmock.NewResult(0, 0))

	prepChunk.ExpectExec().
		WithArgs("rev1", "Great food!", pgvector.NewVector([]float32{0.1, 0.2, 0.3})).
		WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectCommit()

	err = client.BatchUpsert(context.Background(), items)
	if err != nil {
		t.Errorf("BatchUpsert returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}

func TestPostgresClient_BatchUpsertFodmap(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	mockEmb := &mockEmbedder{vec: []float32{0.1, 0.2, 0.3}}

	client := &PostgresClient{
		db:       db,
		embedder: mockEmb,
	}

	items := map[string]data.FodmapEntry{
		"garlic": {
			Level:         "high",
			Groups:        []string{"Fructans"},
			Notes:         "High in fructans",
			Substitutions: []string{"garlic-infused olive oil", "garlic chives"},
		},
	}

	mock.ExpectBegin()
	mock.ExpectPrepare("INSERT INTO fodmap_ingredients").ExpectExec().
		WithArgs(
			"garlic",
			"high",
			pq.Array([]string{"Fructans"}),
			"High in fructans",
			pq.Array([]string{"garlic-infused olive oil", "garlic chives"}),
			pgvector.NewVector([]float32{0.1, 0.2, 0.3}),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err = client.BatchUpsertFodmap(context.Background(), items)
	if err != nil {
		t.Errorf("BatchUpsertFodmap returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}

func TestPostgresClient_SearchFodmap(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	mockEmb := &mockEmbedder{vec: []float32{0.1, 0.2, 0.3}}

	client := &PostgresClient{
		db:       db,
		embedder: mockEmb,
	}

	mock.ExpectQuery("SELECT ingredient, level, groups, notes, substitutions, \\(1 - \\(embedding <=> \\$1\\)\\) AS certainty FROM fodmap_ingredients").
		WithArgs(pgvector.NewVector([]float32{0.1, 0.2, 0.3})).
		WillReturnRows(sqlmock.NewRows([]string{"ingredient", "level", "groups", "notes", "substitutions", "certainty"}).
			AddRow("garlic", "high", "{Fructans}", "High in fructans", "{garlic-infused olive oil,garlic chives}", 0.95))

	res, certainty, err := client.SearchFodmap(context.Background(), "garlic")
	if err != nil {
		t.Fatalf("SearchFodmap returned error: %v", err)
	}

	if res.Ingredient != "garlic" || res.Level != "high" || res.Groups[0] != "Fructans" {
		t.Errorf("Unexpected result: %+v", res)
	}
	if len(res.Substitutions) != 2 || res.Substitutions[0] != "garlic-infused olive oil" {
		t.Errorf("Unexpected substitutions: %v", res.Substitutions)
	}
	if certainty != 0.95 {
		t.Errorf("Unexpected certainty: %v", certainty)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}

func TestPostgresClient_Businesses(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	mockEmb := &mockEmbedder{vec: []float32{0.1, 0.2, 0.3}}

	client := &PostgresClient{
		db:       db,
		embedder: mockEmb,
	}

	mock.ExpectQuery("WITH chunk_scores AS \\(").
		WithArgs(pgvector.NewVector([]float32{0.1, 0.2, 0.3}), "%Pizza%", "%New York%", "NY", 10).
		WillReturnRows(sqlmock.NewRows([]string{"business_id", "name", "city", "state", "categories", "avg_stars", "avg_certainty"}).
			AddRow("bus1", "Joe's Pizza", "New York", "NY", "Pizza", 4.5, 0.9))

	res, err := client.Businesses(context.Background(), "good pizza", 10, SearchFilter{
		Category: "Pizza",
		City:     "New York",
		State:    "NY",
	})
	if err != nil {
		t.Fatalf("Businesses returned error: %v", err)
	}

	if len(res.Businesses) != 1 || res.Businesses[0].ID != "bus1" {
		t.Errorf("Unexpected businesses: %+v", res.Businesses)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}

func TestPostgresClient_Reviews(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	mockEmb := &mockEmbedder{vec: []float32{0.1, 0.2, 0.3}}

	client := &PostgresClient{
		db:       db,
		embedder: mockEmb,
	}

	mock.ExpectQuery("WITH best_chunks AS \\(").
		WithArgs(pgvector.NewVector([]float32{0.1, 0.2, 0.3}), "bus1", pq.Array([]string{"rev1"}), 5).
		WillReturnRows(sqlmock.NewRows([]string{"review_id", "business_id", "business_name", "city", "state", "text", "chunk_text", "certainty"}).
			AddRow("rev1", "bus1", "Joe's Pizza", "New York", "NY", "Great pizza!", "Great pizza chunk", 0.95))

	res, err := client.Reviews(context.Background(), "good pizza", 5, SearchFilter{
		BusinessID: "bus1",
		ReviewIDs:  []string{"rev1"},
	})
	if err != nil {
		t.Fatalf("Reviews returned error: %v", err)
	}

	if len(res.BusinessReviews) != 1 || res.BusinessReviews[0].Review.Review.ReviewID != "rev1" {
		t.Errorf("Unexpected reviews: %+v", res.BusinessReviews)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}

// pgxStringArray Test
func TestPgxStringArray_Scan(t *testing.T) {
	var arr pgxStringArray

	// Test fallback parsing
	err := arr.Scan("{a,b,c}")
	if err != nil {
		t.Errorf("Scan error: %v", err)
	}
	expected := []string{"a", "b", "c"}
	if !reflect.DeepEqual([]string(arr), expected) {
		t.Errorf("Expected %v, got %v", expected, arr)
	}

	// Test slice directly
	err = arr.Scan([]string{"x", "y"})
	if err != nil {
		t.Errorf("Scan error: %v", err)
	}
	expected2 := []string{"x", "y"}
	if !reflect.DeepEqual([]string(arr), expected2) {
		t.Errorf("Expected %v, got %v", expected2, arr)
	}

	// Test nil
	err = arr.Scan(nil)
	if err != nil {
		t.Errorf("Scan error: %v", err)
	}
	if arr != nil {
		t.Errorf("Expected nil, got %v", arr)
	}
}

func TestPostgresClient_BatchUpsert_BeginError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to open mock db: %v", err)
	}
	defer func() { _ = db.Close() }()
	client := &PostgresClient{db: db}

	items := []IndexItem{
		{
			Review: schemas.Review{
				ReviewID:   "rev1",
				BusinessID: "bus1",
				Stars:      4.5,
				Text:       "Great food!",
			},
		},
	}

	mock.ExpectBegin().WillReturnError(fmt.Errorf("tx begin error"))

	err = client.BatchUpsert(context.Background(), items)
	if err == nil || !strings.Contains(err.Error(), "begin tx") {
		t.Errorf("Expected begin tx error, got: %v", err)
	}
}

func TestPostgresClient_BatchUpsert_ExecError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to open mock db: %v", err)
	}
	defer func() { _ = db.Close() }()
	client := &PostgresClient{db: db}

	items := []IndexItem{
		{
			Review: schemas.Review{
				ReviewID:   "rev1",
				BusinessID: "bus1",
				Stars:      4.5,
				Text:       "Great food!",
			},
		},
	}

	mock.ExpectBegin()
	prepRev := mock.ExpectPrepare("INSERT INTO reviews")
	mock.ExpectPrepare("DELETE FROM review_chunks")
	mock.ExpectPrepare("INSERT INTO review_chunks")

	prepRev.ExpectExec().WillReturnError(fmt.Errorf("insert review error"))

	err = client.BatchUpsert(context.Background(), items)
	if err == nil || !strings.Contains(err.Error(), "insert review") {
		t.Errorf("Expected insert review error, got: %v", err)
	}
}

func TestPostgresClient_Businesses_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to open mock db: %v", err)
	}
	defer func() { _ = db.Close() }()
	client := &PostgresClient{db: db, embedder: &mockEmbedder{vec: []float32{0.1, 0.2, 0.3}}}

	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("query error"))

	_, err = client.Businesses(context.Background(), "pizza", 10, SearchFilter{})
	if err == nil || !strings.Contains(err.Error(), "query error") {
		t.Errorf("Expected query error, got: %v", err)
	}
}

func TestPostgresClient_Reviews_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to open mock db: %v", err)
	}
	defer func() { _ = db.Close() }()
	client := &PostgresClient{db: db, embedder: &mockEmbedder{vec: []float32{0.1, 0.2, 0.3}}}

	mock.ExpectQuery("SELECT").WillReturnError(fmt.Errorf("query error"))

	_, err = client.Reviews(context.Background(), "pizza", 10, SearchFilter{})
	if err == nil || !strings.Contains(err.Error(), "query error") {
		t.Errorf("Expected query error, got: %v", err)
	}
}

func TestNewPostgresClient(t *testing.T) {
	_, err := NewPostgresClient("postgres://invalid:invalid@localhost:0/invalid", nil)
	if err == nil {
		t.Error("expected error due to bad connection string")
	}
}

// ── MenuStore tests ───────────────────────────────────────────────────────────

func TestPostgresClient_EnsureMenuSchema(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	client := &PostgresClient{db: db, embedder: nil}

	if err := client.EnsureMenuSchema(context.Background()); err != nil {
		t.Errorf("EnsureMenuSchema returned unexpected error: %v", err)
	}
}

func TestPostgresClient_BatchUpsertMenu_success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	mockEmb := &mockEmbedder{vec: []float32{0.1, 0.2, 0.3}}
	client := &PostgresClient{db: db, embedder: mockEmb}

	payload1 := json.RawMessage(`{"k":"v1"}`)
	payload2 := json.RawMessage(`{"k":"v2"}`)

	items := []MenuItem{
		{
			MenuItemID:         "item-1",
			BusinessID:         "bus-1",
			MenuSection:        "Mains",
			RestaurantName:     "Test Restaurant",
			City:               "San Francisco",
			State:              "CA",
			DishName:           "Grilled Chicken",
			Description:        "Juicy grilled chicken",
			StatedIngredients:  []string{"chicken", "herbs"},
			HasFullIngredients: true,
			SourceURL:          "https://example.com/menu",
			ScrapedAtUTC:       "2024-01-01T00:00:00Z",
			Payload:            payload1,
		},
		{
			MenuItemID:         "item-2",
			BusinessID:         "bus-1",
			MenuSection:        "Sides",
			RestaurantName:     "Test Restaurant",
			City:               "San Francisco",
			State:              "CA",
			DishName:           "Caesar Salad",
			Description:        "Classic caesar salad",
			StatedIngredients:  []string{"lettuce", "croutons"},
			HasFullIngredients: false,
			SourceURL:          "https://example.com/menu",
			ScrapedAtUTC:       "2024-01-01T00:00:00Z",
			Payload:            payload2,
		},
	}

	mock.ExpectBegin()
	prep := mock.ExpectPrepare("INSERT INTO restaurant_menu")
	vec := pgvector.NewVector([]float32{0.1, 0.2, 0.3})

	prep.ExpectExec().
		WithArgs(
			"item-1", "bus-1", "Mains", "Test Restaurant",
			"San Francisco", "CA", "Grilled Chicken", "Juicy grilled chicken",
			pq.Array([]string{"chicken", "herbs"}), true,
			"https://example.com/menu", "2024-01-01T00:00:00Z",
			vec, payload1,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	prep.ExpectExec().
		WithArgs(
			"item-2", "bus-1", "Sides", "Test Restaurant",
			"San Francisco", "CA", "Caesar Salad", "Classic caesar salad",
			pq.Array([]string{"lettuce", "croutons"}), false,
			"https://example.com/menu", "2024-01-01T00:00:00Z",
			vec, payload2,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectCommit()

	if err := client.BatchUpsertMenu(context.Background(), items); err != nil {
		t.Errorf("BatchUpsertMenu returned unexpected error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %s", err)
	}
}

func TestPostgresClient_BatchUpsertMenu_empty(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	client := &PostgresClient{db: db, embedder: &mockEmbedder{vec: []float32{0.1}}}

	if err := client.BatchUpsertMenu(context.Background(), nil); err != nil {
		t.Errorf("BatchUpsertMenu(nil) returned unexpected error: %v", err)
	}
	if err := client.BatchUpsertMenu(context.Background(), []MenuItem{}); err != nil {
		t.Errorf("BatchUpsertMenu([]) returned unexpected error: %v", err)
	}

	// No DB interactions expected.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unexpected DB interactions: %s", err)
	}
}

func TestPostgresClient_SearchMenu_success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	mockEmb := &mockEmbedder{vec: []float32{0.1, 0.2, 0.3}}
	client := &PostgresClient{db: db, embedder: mockEmb}

	payload := json.RawMessage(`{"source":"test"}`)

	rows := sqlmock.NewRows([]string{
		"menu_item_id", "business_id", "menu_section", "restaurant_name",
		"city", "state", "dish_name", "description", "stated_ingredients",
		"has_full_ingredients", "source_url", "scraped_at", "payload",
	}).
		AddRow(
			"item-1", "bus-1", "Mains", "Test Restaurant",
			"San Francisco", "CA", "Grilled Chicken", "Juicy chicken",
			"{chicken,herbs}", true, "https://example.com", "2024-01-01T00:00:00Z", payload,
		).
		AddRow(
			"item-2", "bus-1", "Sides", "Test Restaurant",
			"San Francisco", "CA", "Caesar Salad", "Classic salad",
			"{lettuce,croutons}", false, "https://example.com", "2024-01-01T00:00:00Z", payload,
		)

	mock.ExpectQuery("SELECT").
		WithArgs(pgvector.NewVector([]float32{0.1, 0.2, 0.3}), 5).
		WillReturnRows(rows)

	results, err := client.SearchMenu(context.Background(), "chicken", 5)
	if err != nil {
		t.Fatalf("SearchMenu returned unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].DishName != "Grilled Chicken" {
		t.Errorf("expected first dish 'Grilled Chicken', got %q", results[0].DishName)
	}
	if results[0].Payload == nil {
		t.Errorf("expected non-nil Payload for first result")
	}
	if len(results[0].StatedIngredients) == 0 {
		t.Errorf("expected non-empty StatedIngredients for first result")
	}
	if results[1].DishName != "Caesar Salad" {
		t.Errorf("expected second dish 'Caesar Salad', got %q", results[1].DishName)
	}
	if results[1].Payload == nil {
		t.Errorf("expected non-nil Payload for second result")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %s", err)
	}
}

func TestPostgresClient_SearchMenu_embedError(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	mockEmb := &mockEmbedder{err: fmt.Errorf("embedding service unavailable")}
	client := &PostgresClient{db: db, embedder: mockEmb}

	_, err = client.SearchMenu(context.Background(), "chicken", 5)
	if err == nil {
		t.Fatal("expected error from SearchMenu when embedder fails")
	}
	if !strings.Contains(err.Error(), "vectorize query") {
		t.Errorf("expected error to contain 'vectorize query', got: %v", err)
	}
}
