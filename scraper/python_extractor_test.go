package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// canned JSON responses used across tests.

const inspectResp2Pages = `{
	"page_count": 2,
	"content_type": "application/pdf",
	"pages": [
		{"page": 1, "route": "text", "text_chars": 450, "image_coverage": null},
		{"page": 2, "route": "ocr",  "text_chars": 0,   "image_coverage": 0.9}
	]
}`

const extractPage1Resp = `{
	"page": 1,
	"route": "text",
	"backend": "text-layer",
	"text": "Starters section text",
	"ocr_text": null,
	"ocr_layout": null
}`

const extractPage2Resp = `{
	"page": 2,
	"route": "ocr",
	"backend": "tesseract",
	"text": null,
	"ocr_text": "Mains section text",
	"ocr_layout": "layout block"
}`

const structureResp = `{
	"schema_revision": "v1",
	"backend": "openai-compat",
	"menu": {
		"schema_revision": "v1",
		"restaurant_name": "Chez Alice",
		"city": "San Francisco",
		"state": "CA",
		"sections": [
			{
				"name": "Starters",
				"items": [
					{
						"name": "Bruschetta",
						"description": "Toasted bread with tomatoes",
						"price": 8.50,
						"stated_ingredients": ["bread", "tomatoes"],
						"has_full_ingredients": true,
						"modifiers": []
					}
				]
			},
			{
				"name": "Mains",
				"items": [
					{
						"name": "Pasta",
						"description": "Fresh pasta",
						"price": 14.00,
						"stated_ingredients": ["pasta", "sauce"],
						"has_full_ingredients": false,
						"modifiers": []
					}
				]
			}
		]
	}
}`

// newPythonExtractorTestServer builds an httptest.Server that handles the three
// Python-service endpoints. Each handler function receives the request and
// writes a response; nil means use a standard canned response.
func newPythonExtractorTestServer(
	t *testing.T,
	inspectHandler func(w http.ResponseWriter, r *http.Request),
	extractHandler func(w http.ResponseWriter, r *http.Request),
	structureHandler func(w http.ResponseWriter, r *http.Request),
) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ":inspect"):
			if inspectHandler != nil {
				inspectHandler(w, r)
			} else {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, inspectResp2Pages)
			}
		case strings.Contains(r.URL.Path, "/pages/") && strings.HasSuffix(r.URL.Path, ":extract"):
			if extractHandler != nil {
				extractHandler(w, r)
			} else {
				// Serve page-specific response based on path.
				if strings.Contains(r.URL.Path, "/pages/1:") {
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprint(w, extractPage1Resp)
				} else {
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprint(w, extractPage2Resp)
				}
			}
		case strings.HasSuffix(r.URL.Path, ":structure"):
			if structureHandler != nil {
				structureHandler(w, r)
			} else {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, structureResp)
			}
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestPythonVisionExtractor_success(t *testing.T) {
	srv := newPythonExtractorTestServer(t, nil, nil, nil)
	defer srv.Close()

	ex := &PythonVisionExtractor{BaseURL: srv.URL}
	result, payload, err := ex.ExtractDocument(context.Background(), []byte("%PDF-fake"), "application/pdf")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Two items across two sections.
	if len(result.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(result.Items))
	}

	// has_full_ingredients propagated for first item.
	if !result.Items[0].HasFullIngredients {
		t.Errorf("expected Items[0].HasFullIngredients to be true")
	}
	if result.Items[1].HasFullIngredients {
		t.Errorf("expected Items[1].HasFullIngredients to be false")
	}

	// Payload is non-nil and contains schema_revision.
	if payload == nil {
		t.Fatal("expected non-nil payload")
	}
	if !strings.Contains(string(payload), `"schema_revision"`) {
		t.Errorf("payload missing schema_revision, got: %s", string(payload))
	}
}

func TestPythonVisionExtractor_inspectHTTPError(t *testing.T) {
	srv := newPythonExtractorTestServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unprocessable", http.StatusUnprocessableEntity)
		},
		nil, nil,
	)
	defer srv.Close()

	ex := &PythonVisionExtractor{BaseURL: srv.URL}
	_, _, err := ex.ExtractDocument(context.Background(), []byte("%PDF-fake"), "application/pdf")
	if err == nil {
		t.Fatal("expected error for 422 inspect response")
	}
}

func TestPythonVisionExtractor_structureHTTPError(t *testing.T) {
	srv := newPythonExtractorTestServer(t, nil, nil,
		func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "bad gateway", http.StatusBadGateway)
		},
	)
	defer srv.Close()

	ex := &PythonVisionExtractor{BaseURL: srv.URL}
	_, _, err := ex.ExtractDocument(context.Background(), []byte("%PDF-fake"), "application/pdf")
	if err == nil {
		t.Fatal("expected error for 502 structure response")
	}
}

// TestPythonVisionExtractor_largeStructureResponse verifies that doPost does not
// truncate 2xx responses. It serves a structure payload larger than 4096 bytes
// (100 menu items ≈ 6 KB) and asserts the full JSON is preserved.
func TestPythonVisionExtractor_largeStructureResponse(t *testing.T) {
	// Build a structure response with 100 items so the JSON exceeds 4096 bytes.
	type itemJSON struct {
		Name               string   `json:"name"`
		Description        string   `json:"description"`
		StatedIngredients  []string `json:"stated_ingredients"`
		HasFullIngredients bool     `json:"has_full_ingredients"`
	}
	type sectionJSON struct {
		Name  string     `json:"name"`
		Items []itemJSON `json:"items"`
	}
	type menuJSON struct {
		SchemaRevision string        `json:"schema_revision"`
		RestaurantName string        `json:"restaurant_name"`
		City           string        `json:"city"`
		State          string        `json:"state"`
		Sections       []sectionJSON `json:"sections"`
	}
	type largeResp struct {
		SchemaRevision string   `json:"schema_revision"`
		Backend        string   `json:"backend"`
		Menu           menuJSON `json:"menu"`
	}

	items := make([]itemJSON, 100)
	for i := range items {
		items[i] = itemJSON{
			Name:               fmt.Sprintf("Dish Number %03d with a longer descriptive name", i+1),
			Description:        fmt.Sprintf("A detailed description for dish %d including preparation notes", i+1),
			StatedIngredients:  []string{"ingredient-a", "ingredient-b", "ingredient-c"},
			HasFullIngredients: i%2 == 0,
		}
	}
	resp := largeResp{
		SchemaRevision: "v1",
		Backend:        "openai-compat",
		Menu: menuJSON{
			SchemaRevision: "v1",
			RestaurantName: "Big Menu Restaurant",
			City:           "Los Angeles",
			State:          "CA",
			Sections:       []sectionJSON{{Name: "All Items", Items: items}},
		},
	}
	largeJSON, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshalling large response: %v", err)
	}
	if len(largeJSON) <= 4096 {
		t.Fatalf("fixture is only %d bytes; must exceed 4096 to test truncation guard", len(largeJSON))
	}

	srv := newPythonExtractorTestServer(t, nil, nil,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(largeJSON)
		},
	)
	defer srv.Close()

	ex := &PythonVisionExtractor{BaseURL: srv.URL}
	result, payload, err := ex.ExtractDocument(context.Background(), []byte("%PDF-fake"), "application/pdf")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Items) != 100 {
		t.Errorf("expected 100 items, got %d", len(result.Items))
	}
	if len(payload) != len(largeJSON) {
		t.Errorf("payload length %d does not match fixture length %d; likely truncated", len(payload), len(largeJSON))
	}
}

func TestOpenAIVisionAdapter_nilPayload(t *testing.T) {
	// Serve a valid OpenAI-compat /chat/completions response.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := makeChoiceResp(
			`{"restaurant_name":"Test","items":[{"dish":"Soup","description":"hot","stated_ingredients":[],"has_full_ingredients":false}]}`,
			"", "", "stop",
		)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	inner, err := NewOpenAICompatExtractor(srv.URL+"/v1", "test-model", "", "none")
	if err != nil {
		t.Fatalf("NewOpenAICompatExtractor: %v", err)
	}
	adapter := &OpenAIVisionAdapter{Ex: inner}
	// ExtractDocument for vision adapter does not call the three-step Python path;
	// it delegates to ExtractPDFVision which renders PDF pages then calls vision LLM.
	// With invalid PDF bytes, RenderPDFPages will fail before reaching the LLM.
	// We only need to assert that the returned payload is nil regardless of the error.
	_, rawPayload, _ := adapter.ExtractDocument(context.Background(), []byte("not-a-pdf"), "application/pdf")
	if rawPayload != nil {
		t.Errorf("expected nil payload from OpenAIVisionAdapter, got: %s", string(rawPayload))
	}
}
