package scraper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestServiceExtractor builds a ServiceExtractor pointing at srv.URL with
// short timeouts suitable for tests.
func newTestServiceExtractor(t *testing.T, baseURL string) *ServiceExtractor {
	t.Helper()
	text, err := NewOpenAICompatExtractor(baseURL+"/v1", "test", "", "none")
	if err != nil {
		t.Fatalf("NewOpenAICompatExtractor: %v", err)
	}
	return NewServiceExtractor(baseURL, text, 5*time.Second, 10*time.Second)
}

// serviceStub records the last request path/body so tests can assert the
// orchestration order (inspect → extract → structure).
type serviceStub struct {
	inspectCalls   int
	extractCalls   int
	structureCalls int
	pages          []int  // pages requested, in order
	structureBody  string // last merged_text sent to structure
}

// handler returns an http.HandlerFunc that serves canned responses for the
// three /v1 endpoints and records call order.
func (s *serviceStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/v1/documents:inspect"):
			s.inspectCalls++
			body, _ := io.ReadAll(r.Body)
			_ = body
			_ = json.NewEncoder(w).Encode(documentInspectResult{
				PageCount:   2,
				ContentType: "application/pdf",
				Pages: []pageRoute{
					{Page: 1, Route: "text", TextChars: 500},
					{Page: 2, Route: "ocr", TextChars: 0},
				},
			})
		case strings.Contains(r.URL.Path, ":extract"):
			s.extractCalls++
			var pg int
			if strings.Contains(r.URL.Path, "/pages/1") {
				pg = 1
			} else {
				pg = 2
			}
			s.pages = append(s.pages, pg)
			if pg == 1 {
				txt := "Appetizers\nBruschetta - tomato, basil"
				_ = json.NewEncoder(w).Encode(extractPageResult{
					Page: 1, Route: "text", Backend: "text-layer", Text: &txt,
				})
			} else {
				ocr := "Mains\nPizza Margherita - mozzarella, basil"
				layout := "[0.1,0.2,0.5,0.3]"
				_ = json.NewEncoder(w).Encode(extractPageResult{
					Page: 2, Route: "ocr", Backend: "vlm", OcrText: &ocr, OcrLayout: &layout,
				})
			}
		case strings.HasSuffix(r.URL.Path, "/v1/extractions:structure"):
			s.structureCalls++
			var req structureRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			s.structureBody = req.MergedText
			_ = json.NewEncoder(w).Encode(structureResult{
				SchemaRevision: "v1",
				Backend:        "test-backend",
				Menu: menuDocument{
					SchemaRevision: "v1",
					RestaurantName: "Test Pizzeria",
					City:           "Austin",
					State:          "TX",
					Sections: []menuSection{
						{
							Name: "Appetizers",
							Items: []menuItem{
								{Name: "Bruschetta", Description: "tomato, basil",
									StatedIngredients: []string{"tomato", "basil"}, HasFullIngredients: true},
							},
						},
						{
							Name: "Mains",
							Items: []menuItem{
								{Name: "Pizza Margherita", Description: "mozzarella, basil",
									StatedIngredients: []string{"mozzarella", "basil"}, HasFullIngredients: false},
							},
						},
					},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func TestServiceExtractor_ExtractPDF_Orchestration(t *testing.T) {
	stub := &serviceStub{}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	se := newTestServiceExtractor(t, srv.URL)
	res, err := se.ExtractPDF(context.Background(), []byte("fake-pdf-bytes"))
	if err != nil {
		t.Fatalf("ExtractPDF: %v", err)
	}

	// All three endpoints called exactly once.
	if stub.inspectCalls != 1 {
		t.Errorf("inspect calls = %d, want 1", stub.inspectCalls)
	}
	if stub.extractCalls != 2 {
		t.Errorf("extract calls = %d, want 2", stub.extractCalls)
	}
	if stub.structureCalls != 1 {
		t.Errorf("structure calls = %d, want 1", stub.structureCalls)
	}
	// Pages requested in order 1, 2.
	if len(stub.pages) != 2 || stub.pages[0] != 1 || stub.pages[1] != 2 {
		t.Errorf("page order = %v, want [1 2]", stub.pages)
	}

	// Schema mapping: two items flattened from two sections.
	if len(res.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(res.Items))
	}
	if res.Items[0].DishName != "Bruschetta" {
		t.Errorf("item[0].DishName = %q", res.Items[0].DishName)
	}
	if res.Items[1].DishName != "Pizza Margherita" {
		t.Errorf("item[1].DishName = %q", res.Items[1].DishName)
	}
	if !res.Items[0].HasFullIngredients || res.Items[1].HasFullIngredients {
		t.Errorf("has_full_ingredients mapping wrong: %v, %v",
			res.Items[0].HasFullIngredients, res.Items[1].HasFullIngredients)
	}
	if res.RestaurantName != "Test Pizzeria" || res.City != "Austin" || res.State != "TX" {
		t.Errorf("restaurant meta: %+v", res)
	}

	// Layout forwarded: structureBody should contain the layout marker.
	if !strings.Contains(stub.structureBody, "[layout]") {
		t.Errorf("structure body missing layout marker; got: %s", stub.structureBody)
	}
}

func TestServiceExtractor_ExtractPDF_EmptyMenuIsNotAnError(t *testing.T) {
	// The service returns an empty menu as a normal 200 with zero sections (the
	// MenuDocument contract allows empty). The client must surface that as an
	// empty result, not an error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/v1/documents:inspect"):
			_ = json.NewEncoder(w).Encode(documentInspectResult{
				PageCount: 1, ContentType: "application/pdf",
				Pages: []pageRoute{{Page: 1, Route: "text", TextChars: 500}},
			})
		case strings.Contains(r.URL.Path, ":extract"):
			txt := "nothing here"
			_ = json.NewEncoder(w).Encode(extractPageResult{Page: 1, Route: "text", Text: &txt})
		case strings.HasSuffix(r.URL.Path, "/v1/extractions:structure"):
			_ = json.NewEncoder(w).Encode(structureResult{
				SchemaRevision: "v1", Backend: "openai-compat",
				Menu: menuDocument{Sections: []menuSection{}},
			})
		}
	}))
	defer srv.Close()

	se := newTestServiceExtractor(t, srv.URL)
	res, err := se.ExtractPDF(context.Background(), []byte("fake"))
	if err != nil {
		t.Fatalf("empty menu should not be an error, got: %v", err)
	}
	if len(res.Items) != 0 {
		t.Errorf("expected zero items for empty menu, got %d", len(res.Items))
	}
}

func TestServiceExtractor_ExtractPDF_EnforcesOverallDeadline(t *testing.T) {
	// Each per-page extract sleeps; with many pages the cumulative time exceeds
	// the overall pdfTimeout even though no single request does. The overall
	// deadline must abort the loop (regression guard for the unbounded-loop bug).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/v1/documents:inspect"):
			pages := make([]pageRoute, 20)
			for i := range pages {
				pages[i] = pageRoute{Page: i + 1, Route: "ocr"}
			}
			_ = json.NewEncoder(w).Encode(documentInspectResult{
				PageCount: 20, ContentType: "application/pdf", Pages: pages,
			})
		case strings.Contains(r.URL.Path, ":extract"):
			time.Sleep(60 * time.Millisecond)
			txt := "page text"
			_ = json.NewEncoder(w).Encode(extractPageResult{Route: "text", Text: &txt})
		default:
			_ = json.NewEncoder(w).Encode(structureResult{Menu: menuDocument{}})
		}
	}))
	defer srv.Close()

	text, err := NewOpenAICompatExtractor(srv.URL+"/v1", "test", "", "none")
	if err != nil {
		t.Fatalf("NewOpenAICompatExtractor: %v", err)
	}
	// Per-page timeout is generous (no single call trips it); overall deadline is
	// short enough that 20 × 60ms pages must blow past it.
	se := NewServiceExtractor(srv.URL, text, time.Second, 200*time.Millisecond)

	_, err = se.ExtractPDF(context.Background(), []byte("fake"))
	if err == nil {
		t.Fatal("expected overall PDF deadline to abort the page loop")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got: %v", err)
	}
}

func TestServiceExtractor_ErrorEnvelopeSurfacesRequestID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "rid-from-header")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(serviceErrorEnvelope{
			Error: serviceErrorDetail{
				Code: "service_unavailable", Message: "ocr backend down", RequestID: "rid-from-body",
			},
		})
	}))
	defer srv.Close()

	se := newTestServiceExtractor(t, srv.URL)
	_, err := se.ExtractPDF(context.Background(), []byte("fake"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsBackendUnavailable(err) {
		t.Errorf("expected IsBackendUnavailable, got: %v", err)
	}
	if !strings.Contains(err.Error(), "rid-from-body") {
		t.Errorf("error should surface request_id from body, got: %v", err)
	}
}

func TestServiceExtractor_503TriggersIsBackendUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// inspect returns 503 — OCR backend down.
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(serviceErrorEnvelope{
			Error: serviceErrorDetail{Code: "service_unavailable", Message: "down", RequestID: "r1"},
		})
	}))
	defer srv.Close()

	se := newTestServiceExtractor(t, srv.URL)
	_, err := se.ExtractPDF(context.Background(), []byte("fake"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsBackendUnavailable(err) {
		t.Errorf("expected IsBackendUnavailable, got: %v", err)
	}
}

func TestServiceExtractor_NonEnvelopeErrorUsesHeaderRequestID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 422 validation errors keep the default Pydantic shape, not the envelope.
		w.Header().Set("X-Request-Id", "rid-from-header-only")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":[{"loc":["body"],"msg":"bad"}]}`))
	}))
	defer srv.Close()

	se := newTestServiceExtractor(t, srv.URL)
	_, err := se.ExtractPDF(context.Background(), []byte("fake"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "rid-from-header-only") {
		t.Errorf("error should fall back to X-Request-Id header, got: %v", err)
	}
}

func TestServiceExtractor_Extract_DelegatesToText(t *testing.T) {
	// The text extractor is an OpenAICompatExtractor; verify Extract routes there.
	textSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(makeChoiceResp(
			`{"restaurant_name":"X","items":[{"dish":"soup","description":"","stated_ingredients":[],"has_full_ingredients":false}]}`,
			"", "", "stop"))
	}))
	defer textSrv.Close()

	text, err := NewOpenAICompatExtractor(textSrv.URL+"/v1", "m", "", "none")
	if err != nil {
		t.Fatalf("NewOpenAICompatExtractor: %v", err)
	}
	se := NewServiceExtractor("http://unused.example", text, time.Second, time.Second)

	res, err := se.Extract(context.Background(), "menu text")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].DishName != "soup" {
		t.Errorf("delegated result wrong: %+v", res)
	}
}

func TestServiceExtractor_ImplementsPDFExtractor(t *testing.T) {
	// Compile-time assertion that *ServiceExtractor implements PDFExtractor.
	var _ PDFExtractor = (*ServiceExtractor)(nil)
	var _ Extractor = (*ServiceExtractor)(nil)
}

func TestPageBlob_TextRoute(t *testing.T) {
	txt := "hello"
	got := pageBlob(extractPageResult{Page: 1, Route: "text", Text: &txt})
	if got != "hello" {
		t.Errorf("pageBlob text = %q", got)
	}
}

func TestPageBlob_OcrRouteIncludesLayout(t *testing.T) {
	ocr := "scanned text"
	layout := "<box>...</box>"
	got := pageBlob(extractPageResult{Page: 2, Route: "ocr", OcrText: &ocr, OcrLayout: &layout})
	if !strings.Contains(got, "scanned text") {
		t.Errorf("missing ocr_text: %q", got)
	}
	if !strings.Contains(got, "[layout]") {
		t.Errorf("missing layout marker: %q", got)
	}
	if !strings.Contains(got, "<box>") {
		t.Errorf("missing layout content: %q", got)
	}
}

func TestMapStructureToResult_NilIngredientsBecomesEmpty(t *testing.T) {
	res := mapStructureToResult(structureResult{
		Menu: menuDocument{
			Sections: []menuSection{{
				Name:  "S",
				Items: []menuItem{{Name: "D", StatedIngredients: nil}},
			}},
		},
	})
	if len(res.Items) != 1 {
		t.Fatalf("items = %d", len(res.Items))
	}
	if res.Items[0].StatedIngredients == nil {
		t.Error("nil ingredients should become non-nil empty slice")
	}
}

// ── Phase B: webagent JS-scrape path ─────────────────────────────────────────

func TestServiceExtractor_ImplementsJSRenderer(t *testing.T) {
	var _ JSRenderer = (*ServiceExtractor)(nil)
}

func TestSerializeRecords(t *testing.T) {
	records := []map[string]any{
		{"name": "Pizza", "price": 12.0},
		{"name": "Salad", "price": 8.5},
	}
	got := serializeRecords(records)
	if !strings.Contains(got, "name: Pizza") {
		t.Errorf("missing record 0: %q", got)
	}
	if !strings.Contains(got, "name: Salad") {
		t.Errorf("missing record 1: %q", got)
	}
	if !strings.Contains(got, "\n\n") {
		t.Errorf("records should be separated by blank line: %q", got)
	}
}

func TestServiceExtractor_ScrapeJS_Orchestration(t *testing.T) {
	stub := &webagentStub{}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	se := newTestServiceExtractor(t, srv.URL)
	res, err := se.ScrapeJS(context.Background(), "amc/seats", map[string]any{
		"showtime_id": 1, "movie_title": "X",
	})
	if err != nil {
		t.Fatalf("ScrapeJS: %v", err)
	}
	if stub.scrapeCalls != 1 {
		t.Errorf("scrape endpoint called %d times, want 1", stub.scrapeCalls)
	}
	if stub.structureCalls != 1 {
		t.Errorf("structure endpoint called %d times, want 1", stub.structureCalls)
	}
	if len(res.Items) != 1 || res.Items[0].DishName != "Pizza" {
		t.Errorf("structured result wrong: %+v", res)
	}
}

func TestServiceExtractor_ScrapeJS_EmptyRecordsReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/webagent/scrape/") {
			_ = json.NewEncoder(w).Encode(scrapeResult{
				Records: []map[string]any{},
				Meta:    scrapeMeta{Site: "amc", Target: "seats", Empty: true},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	se := newTestServiceExtractor(t, srv.URL)
	res, err := se.ScrapeJS(context.Background(), "amc/seats", map[string]any{})
	if err != nil {
		t.Fatalf("ScrapeJS empty: %v", err)
	}
	if len(res.Items) != 0 {
		t.Errorf("expected empty result, got %d items", len(res.Items))
	}
}

// webagentStub serves both the webagent scrape endpoint and the
// extractions:structure endpoint, recording call counts.
type webagentStub struct {
	scrapeCalls    int
	structureCalls int
}

func (s *webagentStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/webagent/scrape/"):
			s.scrapeCalls++
			_ = json.NewEncoder(w).Encode(scrapeResult{
				Records: []map[string]any{
					{"name": "Pizza", "description": "cheese"},
				},
				Meta: scrapeMeta{Site: "amc", Target: "seats"},
			})
		case strings.HasSuffix(r.URL.Path, "/v1/extractions:structure"):
			s.structureCalls++
			_ = json.NewEncoder(w).Encode(structureResult{
				SchemaRevision: "v1",
				Backend:        "test",
				Menu: menuDocument{
					SchemaRevision: "v1",
					Sections: []menuSection{{
						Name:  "Mains",
						Items: []menuItem{{Name: "Pizza", Description: "cheese"}},
					}},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// ── Phase C: image-embedded menu path ────────────────────────────────────────

func TestServiceExtractor_ImplementsImageExtractor(t *testing.T) {
	var _ ImageExtractor = (*ServiceExtractor)(nil)
}

func TestServiceExtractor_ExtractImage_Orchestration(t *testing.T) {
	var inspectCalls, extractCalls, structureCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		ct := r.Header.Get("Content-Type")
		switch {
		case strings.HasSuffix(r.URL.Path, "/v1/documents:inspect"):
			inspectCalls++
			if !strings.HasPrefix(ct, "image/") {
				t.Errorf("inspect: expected image/* content-type, got %q", ct)
			}
			_ = json.NewEncoder(w).Encode(documentInspectResult{
				PageCount:   1,
				ContentType: ct,
				Pages:       []pageRoute{{Page: 1, Route: "ocr", TextChars: 0}},
			})
		case strings.Contains(r.URL.Path, ":extract"):
			extractCalls++
			if !strings.HasPrefix(ct, "image/") {
				t.Errorf("extract: expected image/* content-type, got %q", ct)
			}
			ocr := "Pizza Margherita $4.95\nLatte $4.50"
			_ = json.NewEncoder(w).Encode(extractPageResult{
				Page: 1, Route: "ocr", Backend: "vlm", OcrText: &ocr,
			})
		case strings.HasSuffix(r.URL.Path, "/v1/extractions:structure"):
			structureCalls++
			_ = json.NewEncoder(w).Encode(structureResult{
				SchemaRevision: "v1",
				Backend:        "test",
				Menu: menuDocument{
					SchemaRevision: "v1",
					RestaurantName: "Test Cafe",
					Sections: []menuSection{{
						Name: "Drinks",
						Items: []menuItem{
							{Name: "Pizza Margherita", Description: "$4.95",
								StatedIngredients: []string{}},
							{Name: "Latte", Description: "$4.50",
								StatedIngredients: []string{}},
						},
					}},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	se := newTestServiceExtractor(t, srv.URL)
	res, err := se.ExtractImage(context.Background(), []byte("fake-png-bytes"), "image/png")
	if err != nil {
		t.Fatalf("ExtractImage: %v", err)
	}
	if inspectCalls != 1 {
		t.Errorf("inspect calls = %d, want 1", inspectCalls)
	}
	if extractCalls != 1 {
		t.Errorf("extract calls = %d, want 1", extractCalls)
	}
	if structureCalls != 1 {
		t.Errorf("structure calls = %d, want 1", structureCalls)
	}
	if len(res.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(res.Items))
	}
	if res.Items[0].DishName != "Pizza Margherita" {
		t.Errorf("item[0] = %q", res.Items[0].DishName)
	}
	if res.RestaurantName != "Test Cafe" {
		t.Errorf("restaurant = %q", res.RestaurantName)
	}
}

func TestServiceExtractor_ExtractImage_EmptyMimeDefaultsToPng(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		ct := r.Header.Get("Content-Type")
		if strings.HasSuffix(r.URL.Path, "/v1/documents:inspect") {
			if ct != "image/png" {
				t.Errorf("expected image/png default, got %q", ct)
			}
			_ = json.NewEncoder(w).Encode(documentInspectResult{
				PageCount: 1, ContentType: ct,
				Pages: []pageRoute{{Page: 1, Route: "ocr"}},
			})
		}
		if strings.Contains(r.URL.Path, ":extract") {
			ocr := "empty"
			_ = json.NewEncoder(w).Encode(extractPageResult{Page: 1, Route: "ocr", OcrText: &ocr})
		}
		if strings.HasSuffix(r.URL.Path, "/v1/extractions:structure") {
			_ = json.NewEncoder(w).Encode(structureResult{
				Backend: "t",
				Menu:    menuDocument{Sections: []menuSection{{Name: "S", Items: []menuItem{{Name: "x"}}}}},
			})
		}
	}))
	defer srv.Close()

	se := newTestServiceExtractor(t, srv.URL)
	_, err := se.ExtractImage(context.Background(), []byte("fake"), "")
	if err != nil {
		t.Fatalf("ExtractImage with empty mime: %v", err)
	}
}

func TestServiceExtractor_ExtractImage_503ReturnsServiceError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(serviceErrorEnvelope{
			Error: serviceErrorDetail{Code: "service_unavailable", Message: "ocr down", RequestID: "r1"},
		})
	}))
	defer srv.Close()

	se := newTestServiceExtractor(t, srv.URL)
	_, err := se.ExtractImage(context.Background(), []byte("fake"), "image/png")
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsBackendUnavailable(err) {
		t.Errorf("expected IsBackendUnavailable, got: %v", err)
	}
}
