package search

import (
	"context"
	"reflect"
	"testing"

	"fodmap/data"
	"fodmap/data/schemas"

	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/pgvector/pgvector-go"
)

// mockVectorizer is a simple vectorizer client for testing purposes.

// mockVectorizerServer creates an httptest.Server that returns a mock vector.
func getMockVectorizerServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vectors/batch":
			// Mock batch response (not strictly needed right now, but good to have)
			w.WriteHeader(http.StatusOK)
		case "/vectors":
			w.WriteHeader(http.StatusOK)
			res := struct {
				Vector []float32 `json:"vector"`
			}{Vector: []float32{0.1, 0.2, 0.3}}
			_ = json.NewEncoder(w).Encode(res)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestPostgresClient_EnsureSchema(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer db.Close()

	client := &PostgresClient{
		db:         db,
		vectorizer: nil,
	}

	mock.ExpectExec("CREATE EXTENSION IF NOT EXISTS vector").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS reviews").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_reviews_embedding").WillReturnResult(sqlmock.NewResult(0, 0))

	err = client.EnsureSchema(context.Background())
	if err != nil {
		t.Errorf("EnsureSchema returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}

func TestPostgresClient_EnsureFodmapSchema(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer db.Close()

	client := &PostgresClient{
		db:         db,
		vectorizer: nil,
	}

	mock.ExpectExec("CREATE EXTENSION IF NOT EXISTS vector").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS fodmap_ingredients").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_fodmap_embedding").WillReturnResult(sqlmock.NewResult(0, 0))

	err = client.EnsureFodmapSchema(context.Background())
	if err != nil {
		t.Errorf("EnsureFodmapSchema returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}

func TestPostgresClient_BatchUpsert(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer db.Close()

	client := &PostgresClient{
		db:         db,
		vectorizer: nil,
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
	mock.ExpectPrepare("INSERT INTO reviews").ExpectExec().
		WithArgs(
			"rev1",
			"bus1",
			"Tasty Bites",
			"New York",
			"NY",
			"Restaurant, Food",
			4.5,
			"Great food!",
			pgvector.NewVector([]float32{0.1, 0.2, 0.3}),
		).
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
	defer db.Close()

	ts := getMockVectorizerServer()
	defer ts.Close()
	vecClient := NewVectorizerClient(ts.URL)

	client := &PostgresClient{
		db:         db,
		vectorizer: vecClient,
	}

	items := map[string]data.FodmapEntry{
		"garlic": {
			Level:  "high",
			Groups: []string{"Fructans"},
			Notes:  "High in fructans",
		},
	}

	mock.ExpectBegin()
	mock.ExpectPrepare("INSERT INTO fodmap_ingredients").ExpectExec().
		WithArgs(
			"garlic",
			"high",
			pq.Array([]string{"Fructans"}),
			"High in fructans",
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
	defer db.Close()

	ts := getMockVectorizerServer()
	defer ts.Close()
	vecClient := NewVectorizerClient(ts.URL)

	client := &PostgresClient{
		db:         db,
		vectorizer: vecClient,
	}

	mock.ExpectQuery("SELECT ingredient, level, groups, notes, \\(1 - \\(embedding <=> \\$1\\)\\) AS certainty FROM fodmap_ingredients").
		WithArgs(pgvector.NewVector([]float32{0.1, 0.2, 0.3})).
		WillReturnRows(sqlmock.NewRows([]string{"ingredient", "level", "groups", "notes", "certainty"}).
			AddRow("garlic", "high", "{Fructans}", "High in fructans", 0.95))

	res, certainty, err := client.SearchFodmap(context.Background(), "garlic")
	if err != nil {
		t.Fatalf("SearchFodmap returned error: %v", err)
	}

	if res.Ingredient != "garlic" || res.Level != "high" || res.Groups[0] != "Fructans" {
		t.Errorf("Unexpected result: %+v", res)
	}
	if certainty != 0.95 {
		t.Errorf("Unexpected certainty: %v", certainty)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}

func TestPostgresClient_GetBusinesses(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer db.Close()

	ts := getMockVectorizerServer()
	defer ts.Close()
	vecClient := NewVectorizerClient(ts.URL)

	client := &PostgresClient{
		db:         db,
		vectorizer: vecClient,
	}

	mock.ExpectQuery("WITH top_reviews AS \\(").
		WithArgs(pgvector.NewVector([]float32{0.1, 0.2, 0.3}), "%Pizza%", "%New York%", "NY", 10).
		WillReturnRows(sqlmock.NewRows([]string{"business_id", "name", "city", "state", "avg_stars", "avg_certainty"}).
			AddRow("bus1", "Joe's Pizza", "New York", "NY", 4.5, 0.9))

	res, err := client.GetBusinesses(context.Background(), "good pizza", 10, SearchFilter{
		Category: "Pizza",
		City:     "New York",
		State:    "NY",
	})
	if err != nil {
		t.Fatalf("GetBusinesses returned error: %v", err)
	}

	if len(res.Businesses) != 1 || res.Businesses[0].ID != "bus1" {
		t.Errorf("Unexpected businesses: %+v", res.Businesses)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}

func TestPostgresClient_GetReviews(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer db.Close()

	ts := getMockVectorizerServer()
	defer ts.Close()
	vecClient := NewVectorizerClient(ts.URL)

	client := &PostgresClient{
		db:         db,
		vectorizer: vecClient,
	}

	mock.ExpectQuery("SELECT review_id, business_id, business_name, city, state, text, \\(1 - \\(embedding <=> \\$1\\)\\) AS certainty FROM reviews").
		WithArgs(pgvector.NewVector([]float32{0.1, 0.2, 0.3}), "bus1", pq.Array([]string{"rev1"}), 5).
		WillReturnRows(sqlmock.NewRows([]string{"review_id", "business_id", "business_name", "city", "state", "text", "certainty"}).
			AddRow("rev1", "bus1", "Joe's Pizza", "New York", "NY", "Great pizza!", 0.95))

	res, err := client.GetReviews(context.Background(), "good pizza", 5, SearchFilter{
		BusinessID: "bus1",
		ReviewIDs:  []string{"rev1"},
	})
	if err != nil {
		t.Fatalf("GetReviews returned error: %v", err)
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
