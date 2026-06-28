package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"fodmap/scraper"
	"fodmap/search"
	"fodmap/server"

	"github.com/google/uuid"
)

// ExtractMenu fetches the URL, runs the extraction cascade (JSON-LD → HTML/text → PDF/OCR → image),
// and returns the structured result and the raw response body.
// The raw body is nil for webagent JS paths. Callers may write it to the bronze layer.
func ExtractMenu(
	ctx context.Context,
	rawURL string,
	fetcher scraper.Fetcher,
	ex scraper.Extractor,
	enableVision bool,
	usePdftotext bool,
	webagentAdapter string,
) (*scraper.MenuExtractionResult, []byte, error) {
	slog.Info("scraping URL", "url", rawURL)

	fetchRes, err := fetcher.Fetch(ctx, rawURL)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch: %w", err)
	}

	bodyBytes, err := io.ReadAll(fetchRes.Body)
	_ = fetchRes.Body.Close()
	if err != nil {
		return nil, nil, fmt.Errorf("reading body: %w", err)
	}

	ct := fetchRes.ContentType
	var result scraper.MenuExtractionResult
	var jsonldMeta scraper.JSONLDMeta
	var usedJSONLD bool

	if !strings.Contains(ct, "pdf") {
		items, meta, ok := scraper.ExtractJSONLD(bytes.NewReader(bodyBytes))
		jsonldMeta = meta
		if ok {
			slog.Info("Tier 0: JSON-LD menu found", "items", len(items))
			result = scraper.MenuExtractionResult{
				RestaurantName: meta.RestaurantName,
				City:           meta.City,
				State:          meta.State,
				SourceURL:      rawURL,
				ScrapedAtUTC:   time.Now().UTC().Format(time.RFC3339),
				Items:          items,
			}
			usedJSONLD = true
		}
	}

	if !usedJSONLD {
		var pageText string
		var pdfResult *scraper.MenuExtractionResult
		var menuImgCandidates []string

		if strings.Contains(ct, "pdf") {
			text, structured, err := ExtractPDF(ctx, bodyBytes, usePdftotext, enableVision, ex)
			if err != nil {
				return nil, bodyBytes, fmt.Errorf("PDF extraction: %w", err)
			}
			if structured != nil {
				pdfResult = structured
			} else {
				pageText = text
			}
		} else {
			md, err := scraper.ConvertHTMLToMarkdown(bytes.NewReader(bodyBytes), ct)
			if err != nil {
				return nil, bodyBytes, fmt.Errorf("HTML conversion: %w", err)
			}
			noisy := scraper.IsTooNoisy(md)
			if noisy {
				slog.Warn("HTML→Markdown output is noisy, falling back to trafilatura", "url", rawURL)
				if fallback := scraper.TrafilaturaFallback(string(bodyBytes)); fallback != "" {
					md = fallback
				} else {
					slog.Warn("trafilatura fallback produced no output, using original conversion", "url", rawURL)
				}
			}

			tooShort := len([]rune(strings.TrimSpace(md))) < 200
			needsFallback := scraper.IsTooNoisy(md) || strings.TrimSpace(md) == "" || tooShort

			menuImgCandidates, _ = scraper.FindMenuImages(bodyBytes, ct, rawURL)

			if needsFallback {
				if len(menuImgCandidates) > 0 {
					if iex, ok := ex.(scraper.ImageExtractor); ok {
						if imgResult, ran, err := extractFromImageURL(ctx, fetcher, iex, menuImgCandidates); ran {
							if err != nil {
								return nil, bodyBytes, err
							}
							result = imgResult
							result.SourceURL = rawURL
							result.ScrapedAtUTC = time.Now().UTC().Format(time.RFC3339)
							if result.City == "" && jsonldMeta.City != "" {
								result.City = jsonldMeta.City
								result.State = jsonldMeta.State
							}
							if result.RestaurantName == "" && jsonldMeta.RestaurantName != "" {
								result.RestaurantName = jsonldMeta.RestaurantName
							}
							goto tier1Done
						}
					} else {
						slog.Warn("page appears to contain a menu image; set --enable-vision or --extractor-url to OCR it",
							"url", rawURL, "img", menuImgCandidates[0])
					}
				}

				if webagentAdapter != "" {
					if jsr, ok := ex.(scraper.JSRenderer); ok {
						slog.Info("HTML too noisy; routing to webagent", "url", rawURL, "adapter", webagentAdapter)
						jsResult, jsErr := jsr.ScrapeJS(ctx, webagentAdapter, map[string]any{
							"url": rawURL,
						})
						if jsErr != nil {
							return nil, bodyBytes, fmt.Errorf("webagent JS scrape: %w", jsErr)
						}
						result = jsResult
						result.SourceURL = rawURL
						result.ScrapedAtUTC = time.Now().UTC().Format(time.RFC3339)
						if result.City == "" && jsonldMeta.City != "" {
							result.City = jsonldMeta.City
							result.State = jsonldMeta.State
						}
						if result.RestaurantName == "" && jsonldMeta.RestaurantName != "" {
							result.RestaurantName = jsonldMeta.RestaurantName
						}
						goto tier1Done
					}
				}
			}
			pageText = md
		}

		if pdfResult != nil {
			result = *pdfResult
		} else {
			slog.Info("Tier 1: sending to LLM extractor", "chars", len([]rune(pageText)))
			var err error
			result, err = ex.Extract(ctx, pageText)
			if err != nil {
				return nil, bodyBytes, fmt.Errorf("LLM extraction: %w", err)
			}

			if len(result.Items) == 0 && len(menuImgCandidates) > 0 {
				if iex, ok := ex.(scraper.ImageExtractor); ok {
					if imgResult, ran, imgErr := extractFromImageURL(ctx, fetcher, iex, menuImgCandidates); ran {
						if imgErr != nil {
							return nil, bodyBytes, imgErr
						}
						if len(imgResult.Items) > 0 {
							slog.Info("text pass empty; routing to menu image OCR",
								"url", rawURL, "items", len(imgResult.Items))
							result = imgResult
						}
					}
				}
			}
		}
		result.SourceURL = rawURL
		result.ScrapedAtUTC = time.Now().UTC().Format(time.RFC3339)

		if result.City == "" && jsonldMeta.City != "" {
			result.City = jsonldMeta.City
			result.State = jsonldMeta.State
		}
		if result.RestaurantName == "" && jsonldMeta.RestaurantName != "" {
			result.RestaurantName = jsonldMeta.RestaurantName
		}
	}

tier1Done:
	if len(result.Items) == 0 {
		slog.Warn("no menu items extracted", "url", rawURL)
		return &result, bodyBytes, nil
	}

	slog.Info("extracted menu items", "count", len(result.Items), "restaurant", result.RestaurantName)
	return &result, bodyBytes, nil
}

// StoreMenu embeds the extracted items and upserts them into the menu store (Weaviate).
// Returns the item count.
func StoreMenu(
	ctx context.Context,
	result *scraper.MenuExtractionResult,
	rawURL string,
	store server.MenuStore,
	embedder search.Embedder,
) (int, error) {
	if len(result.Items) == 0 {
		return 0, nil
	}

	items, err := ToMenuItems(ctx, *result, rawURL, embedder)
	if err != nil {
		return 0, fmt.Errorf("embedding menu items: %w", err)
	}

	if err := store.BatchUpsertMenu(ctx, items); err != nil {
		return 0, fmt.Errorf("upserting menu items: %w", err)
	}

	return len(items), nil
}

// ToMenuItems converts a MenuExtractionResult to []search.MenuItem, embedding each item's text vector.
func ToMenuItems(ctx context.Context, result scraper.MenuExtractionResult, rawURL string, embedder search.Embedder) ([]search.MenuItem, error) {
	businessID := scraper.BusinessID(rawURL)
	section := scraper.MenuSection(rawURL)
	now := result.ScrapedAtUTC

	texts := make([]string, len(result.Items))
	for i, item := range result.Items {
		parts := []string{"Menu item at " + result.RestaurantName + ": " + item.DishName}
		if item.Description != "" {
			parts = append(parts, item.Description)
		}
		if len(item.StatedIngredients) > 0 {
			parts = append(parts, "Stated ingredients: "+strings.Join(item.StatedIngredients, ", "))
		}
		texts[i] = strings.Join(parts, ". ")
	}

	vectors := make([][]float32, 0, len(texts))
	const batchSize = 50
	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batchVectors, err := embedder.EmbedBatch(ctx, texts[i:end])
		if err != nil {
			return nil, fmt.Errorf("embedding batch [%d:%d]: %w", i, end, err)
		}
		vectors = append(vectors, batchVectors...)
	}

	items := make([]search.MenuItem, len(result.Items))
	for i, entry := range result.Items {
		idKey := businessID + entry.DishName
		id := uuid.NewSHA1(menuCollectionNS, []byte(idKey)).String()
		items[i] = search.MenuItem{
			MenuItemID:         id,
			BusinessID:         businessID,
			MenuSection:        section,
			RestaurantName:     result.RestaurantName,
			City:               result.City,
			State:              result.State,
			DishName:           entry.DishName,
			Description:        entry.Description,
			StatedIngredients:  entry.StatedIngredients,
			HasFullIngredients: entry.HasFullIngredients,
			SourceURL:          rawURL,
			ScrapedAtUTC:       now,
			Vector:             vectors[i],
		}
	}
	return items, nil
}

// ExtractPDF runs the PDF cascade: text-layer → pdftotext → (service | vision).
func ExtractPDF(ctx context.Context, pdfBytes []byte, usePdftotext, enableVision bool, ex scraper.Extractor) (string, *scraper.MenuExtractionResult, error) {
	text, err := scraper.ExtractPDFText(pdfBytes, usePdftotext)
	if err == nil {
		return text, nil, nil
	}
	if !errors.Is(err, scraper.ErrNeedVision) {
		return "", nil, err
	}

	if pex, ok := ex.(scraper.PDFExtractor); ok {
		result, sErr := pex.ExtractPDF(ctx, pdfBytes)
		if sErr == nil {
			return "", &result, nil
		}
		if !scraper.IsBackendUnavailable(sErr) {
			return "", nil, fmt.Errorf("service PDF extraction: %w", sErr)
		}
		slog.Warn("service OCR backend unavailable (503), falling back to pure-Go vision", "err", sErr)
		if !enableVision {
			return "", nil, fmt.Errorf("service OCR backend unavailable (503) and --enable-vision is not set: %w", sErr)
		}
	}

	if !enableVision {
		return "", nil, fmt.Errorf("PDF has no usable text layer; set --extractor-url or --enable-vision")
	}

	oaex, ok := ex.(*scraper.OpenAICompatExtractor)
	if !ok {
		if sex, ok2 := ex.(*scraper.ServiceExtractor); ok2 {
			oaex = sex.Text()
		}
	}
	if oaex == nil {
		return "", nil, fmt.Errorf("vision path requires --llm-backend openai-compat")
	}
	result, vErr := scraper.ExtractPDFVision(ctx, pdfBytes, oaex)
	if vErr != nil {
		return "", nil, vErr
	}
	var lines []string
	for _, item := range result.Items {
		lines = append(lines, item.DishName+": "+item.Description)
	}
	return strings.Join(lines, "\n"), nil, nil
}

// menuCollectionNS is the UUID namespace for deterministic menu item IDs.
var menuCollectionNS = uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

const maxMenuImageAttempts = 2

func extractFromImageURL(ctx context.Context, fetcher scraper.Fetcher, iex scraper.ImageExtractor, candidates []string) (scraper.MenuExtractionResult, bool, error) {
	attempts := len(candidates)
	if attempts > maxMenuImageAttempts {
		attempts = maxMenuImageAttempts
	}
	var last scraper.MenuExtractionResult
	ran := false
	for i := 0; i < attempts; i++ {
		imgURL := candidates[i]
		slog.Info("routing to menu image OCR", "url", imgURL, "candidate", i+1, "of", attempts)
		imgRes, err := fetcher.Fetch(ctx, imgURL)
		if err != nil {
			return scraper.MenuExtractionResult{}, false, fmt.Errorf("fetching menu image %s: %w", imgURL, err)
		}
		imgBytes, err := io.ReadAll(imgRes.Body)
		_ = imgRes.Body.Close()
		if err != nil {
			return scraper.MenuExtractionResult{}, false, fmt.Errorf("reading menu image %s: %w", imgURL, err)
		}
		result, err := iex.ExtractImage(ctx, imgBytes, imgRes.ContentType)
		if err != nil {
			return scraper.MenuExtractionResult{}, false, fmt.Errorf("image OCR %s: %w", imgURL, err)
		}
		ran = true
		last = result
		if len(result.Items) > 0 {
			return result, true, nil
		}
		slog.Info("menu image candidate yielded 0 items (not a menu)", "url", imgURL)
	}
	return last, ran, nil
}
