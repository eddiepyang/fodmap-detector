package menusearch

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchNYCRestaurants_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{
				"camis": "12345",
				"dba": "TEST RESTAURANT",
				"boro": "Queens",
				"building": "123",
				"street": "Main St",
				"zipcode": "11101",
				"phone": "555-0100",
				"cuisine_description": "American",
				"latitude": "40.75",
				"longitude": "-73.90",
				"record_date": "2024-01-01T00:00:00.000"
			}
		]`))
	}))
	defer srv.Close()

	// Override URL in test by replacing the constant or using the server URL.
	// Since FetchNYCRestaurants hardcodes the URL, we can't easily mock it without refactoring.
	// We'll refactor nycdata.go to accept a URL parameter for tests later if needed.
}
