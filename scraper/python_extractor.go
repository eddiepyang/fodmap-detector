package scraper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// VisionExtractor turns a raw document (PDF or image bytes) into a structured
// MenuExtractionResult plus the raw schema-v1 payload (nil for backends with no
// structured source). The raw payload is stored as jsonb.
type VisionExtractor interface {
	ExtractDocument(ctx context.Context, docBytes []byte, contentType string) (MenuExtractionResult, json.RawMessage, error)
}

// PythonVisionExtractor calls the Python FastAPI microservice to inspect,
// extract, and structure a document (PDF or image) into a MenuExtractionResult.
// It orchestrates three HTTP calls:
//  1. POST /v1/documents:inspect — page routing
//  2. POST /v1/documents/{doc}/pages/{n}:extract — per-page text/OCR
//  3. POST /v1/extractions:structure — LLM structuring to schema-v1
type PythonVisionExtractor struct {
	BaseURL string       // e.g. "http://localhost:8765"
	Client  *http.Client // if nil, a client with 120s timeout is used
}

func (e *PythonVisionExtractor) httpClient() *http.Client {
	if e.Client != nil {
		return e.Client
	}
	return &http.Client{Timeout: 120 * time.Second}
}

// doPost issues a POST to url with the given body and content type. It returns
// the raw response bytes on 2xx, or an error including the status and body for
// non-2xx responses.
func (e *PythonVisionExtractor) doPost(ctx context.Context, url string, body []byte, contentType string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := e.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status %d from %s: %s", resp.StatusCode, url, strings.TrimSpace(string(respBody)))
	}
	return respBody, nil
}

// inspectResponse is the JSON shape returned by POST /v1/documents:inspect.
type inspectResponse struct {
	PageCount   int    `json:"page_count"`
	ContentType string `json:"content_type"`
	Pages       []struct {
		Page          int      `json:"page"`
		Route         string   `json:"route"`
		TextChars     int      `json:"text_chars"`
		ImageCoverage *float64 `json:"image_coverage"`
	} `json:"pages"`
}

// pageExtractResponse is the JSON shape returned by
// POST /v1/documents/{doc}/pages/{n}:extract.
type pageExtractResponse struct {
	Page      int     `json:"page"`
	Route     string  `json:"route"`
	Backend   string  `json:"backend"`
	Text      *string `json:"text"`
	OCRText   *string `json:"ocr_text"`
	OCRLayout *string `json:"ocr_layout"`
}

// structureRequest is the request body for POST /v1/extractions:structure.
type structureRequest struct {
	MergedText     string `json:"merged_text"`
	SchemaRevision string `json:"schema_revision"`
}

// schemaV1MenuItem maps one item from the schema-v1 structuring response.
type schemaV1MenuItem struct {
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	StatedIngredients  []string `json:"stated_ingredients"`
	HasFullIngredients bool     `json:"has_full_ingredients"`
}

// schemaV1Section is one section within a schema-v1 menu.
type schemaV1Section struct {
	Name  string             `json:"name"`
	Items []schemaV1MenuItem `json:"items"`
}

// schemaV1Menu is the inner "menu" object in the structuring response.
type schemaV1Menu struct {
	SchemaRevision string            `json:"schema_revision"`
	RestaurantName string            `json:"restaurant_name"`
	City           string            `json:"city"`
	State          string            `json:"state"`
	Sections       []schemaV1Section `json:"sections"`
}

// structureResponse is the full JSON body from POST /v1/extractions:structure.
type structureResponse struct {
	SchemaRevision string       `json:"schema_revision"`
	Backend        string       `json:"backend"`
	Menu           schemaV1Menu `json:"menu"`
}

// ExtractDocument implements VisionExtractor for the Python microservice path.
func (e *PythonVisionExtractor) ExtractDocument(ctx context.Context, docBytes []byte, contentType string) (MenuExtractionResult, json.RawMessage, error) {
	// Step 1: inspect — determine page count and per-page routing.
	inspectURL := e.BaseURL + "/v1/documents:inspect"
	inspectRaw, err := e.doPost(ctx, inspectURL, docBytes, contentType)
	if err != nil {
		return MenuExtractionResult{}, nil, fmt.Errorf("inspecting document: %w", err)
	}
	var inspection inspectResponse
	if err := json.Unmarshal(inspectRaw, &inspection); err != nil {
		return MenuExtractionResult{}, nil, fmt.Errorf("decoding inspect response: %w", err)
	}

	// Step 2: extract each page's text.
	const docID = "doc"
	var sb strings.Builder
	for _, page := range inspection.Pages {
		extractURL := fmt.Sprintf("%s/v1/documents/%s/pages/%d:extract", e.BaseURL, docID, page.Page)
		extractRaw, err := e.doPost(ctx, extractURL, docBytes, contentType)
		if err != nil {
			return MenuExtractionResult{}, nil, fmt.Errorf("extracting page %d: %w", page.Page, err)
		}
		var pageResult pageExtractResponse
		if err := json.Unmarshal(extractRaw, &pageResult); err != nil {
			return MenuExtractionResult{}, nil, fmt.Errorf("decoding page %d extract response: %w", page.Page, err)
		}
		// Prefer text; fall back to ocr_text (with ocr_layout appended).
		if pageResult.Text != nil {
			sb.WriteString(*pageResult.Text)
		} else if pageResult.OCRText != nil {
			sb.WriteString(*pageResult.OCRText)
			if pageResult.OCRLayout != nil {
				sb.WriteByte('\n')
				sb.WriteString(*pageResult.OCRLayout)
			}
		}
		sb.WriteByte('\n')
	}

	// Step 3: structure the merged text into schema-v1.
	structBody, err := json.Marshal(structureRequest{
		MergedText:     sb.String(),
		SchemaRevision: "v1",
	})
	if err != nil {
		return MenuExtractionResult{}, nil, fmt.Errorf("marshalling structure request: %w", err)
	}
	structureURL := e.BaseURL + "/v1/extractions:structure"
	structureRaw, err := e.doPost(ctx, structureURL, structBody, "application/json")
	if err != nil {
		return MenuExtractionResult{}, nil, fmt.Errorf("structuring document: %w", err)
	}
	var structured structureResponse
	if err := json.Unmarshal(structureRaw, &structured); err != nil {
		return MenuExtractionResult{}, nil, fmt.Errorf("decoding structure response: %w", err)
	}

	// Map schema-v1 → MenuExtractionResult: flatten all sections' items.
	menu := structured.Menu
	var items []MenuEntry
	for _, section := range menu.Sections {
		for _, item := range section.Items {
			items = append(items, MenuEntry{
				DishName:           item.Name,
				Description:        item.Description,
				StatedIngredients:  item.StatedIngredients,
				HasFullIngredients: item.HasFullIngredients,
			})
		}
	}

	result := MenuExtractionResult{
		RestaurantName: menu.RestaurantName,
		City:           menu.City,
		State:          menu.State,
		Items:          items,
		// SourceURL and ScrapedAtUTC are stamped by the CLI caller.
	}
	return result, json.RawMessage(structureRaw), nil
}

// OpenAIVisionAdapter wraps an OpenAICompatExtractor so it satisfies
// VisionExtractor. The raw JSON payload is nil because the vision LLM path
// has no structured schema-v1 source.
type OpenAIVisionAdapter struct {
	Ex *OpenAICompatExtractor
}

// ExtractDocument implements VisionExtractor by delegating to ExtractPDFVision.
// The second return value (raw payload) is always nil for this backend.
func (a *OpenAIVisionAdapter) ExtractDocument(ctx context.Context, docBytes []byte, contentType string) (MenuExtractionResult, json.RawMessage, error) {
	result, err := ExtractPDFVision(ctx, docBytes, a.Ex)
	return result, nil, err
}
