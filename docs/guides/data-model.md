# Data Model & Pipeline

For the relational/Postgres schema, see [database-schema.md](database-schema.md). For an overview of the Go/Python split service architecture, see [pipeline-architecture.md](pipeline-architecture.md).

## Core Data Model

Reviews reference businesses by ID only. The business name and location metadata live in a separate dataset file.

```go
// Review holds a single review record. BusinessID is a foreign key into the business dataset —
// the business name is NOT present here.
type Review struct {
    ReviewID   string  // Yelp review ID
    UserID     string  // Reviewer user ID
    BusinessID string  // Foreign key — look up name/location in Business
    Stars      float32 // Rating (1-5)
    Useful     int32   // Usefulness votes
    Funny      int32   // Funny votes
    Cool       int32   // Cool votes
    Text       string  // Full review text
}

// Business holds metadata from yelp_academic_dataset_business.json.
// Required to resolve a BusinessID to a human-readable name.
type Business struct {
    BusinessID string // Primary key, matches Review.BusinessID
    Name       string // Human-readable restaurant/business name
    City       string
    State      string
    Categories string // Comma-separated, e.g. "Italian, Pizza, Restaurants"
}
```

The FODMAP ingredient database (`data/fodmap.go`) contains 100+ entries with FODMAP level, group tags, notes, and substitution suggestions:

```go
type FodmapEntry struct {
    Level         string   `json:"level"`                    // "high", "moderate", or "low"
    Groups        []string `json:"groups"`                   // FODMAP groups, e.g. ["fructan", "mannitol"]
    Notes         string   `json:"notes,omitempty"`          // Additional context (serving thresholds, etc.)
    Substitutions []string `json:"substitutions,omitempty"`  // Low-FODMAP alternatives for high/moderate ingredients
}
```

When the chat agent looks up a high or moderate FODMAP ingredient, it automatically presents the substitution suggestions to the user as practical alternatives.

The Avro streaming schema (`EventSchema`) mirrors the `Review` struct and carries `business_id` but not the business name. During indexing, the name is joined from the business dataset and stored in Weaviate so search results include it directly.

---

## Menu Scraping Pipeline

The `scrape` command fetches a restaurant menu page and extracts structured menu items into Weaviate. The pipeline has multiple fallback tiers:

```
scrape <url>
    |
    v
Fetch (HTTPFetcher: robots.txt check, body cap, charset decode)
    |
    +-- Tier 0: JSON-LD fast-path
    |   ExtractJSONLD() parses schema.org Menu blocks — no LLM call.
    |   If found: done.
    |
    +-- Tier 1: HTML/PDF → LLM extraction
    |   HTML → ConvertHTMLToMarkdown → ex.Extract (OpenAI-compatible LLM)
    |   PDF  → ExtractPDFText (text-layer) → ex.Extract
    |   PDF  → pdftotext (poppler) → ex.Extract
    |
    +-- Tier 1.5: trafilatura fallback
    |   If Markdown is noisy (IsTooNoisy), try go-trafilatura boilerplate removal.
    |
    +-- JS-shell pre-render (--extractor-url required)
    |   If IsJSShell() flags the static HTML as an SPA shell AND the visible
    |   text is trivial (<200 runes), render the URL in the headless browser
    |   BEFORE the text pass and extract from the hydrated HTML instead. The
    |   LLM must never see a near-empty shell: it invents a plausible menu
    |   rather than returning zero items (hallucination guard).
    |
    +-- Phase C: Image-embedded menu (--extractor-url required)
    |   If content is still noisy/empty/too-short, FindMenuImage() scans the
    |   HTML for a large <img> likely to be a menu photo (size, filename,
    |   #MENU context heuristics). If found, fetch the image and OCR it via
    |   the service (inspect → pages:extract → extractions:structure).
    |
    +-- Phase B: JS-rendered page (--enable-js-render + --webagent-adapter)
    |   If no menu image found, route to the service's webagent endpoint
    |   (scrape/{site}/{target} → serialize records → extractions:structure).
    |
    +-- Refusal floor (minExtractRunes = 60)
    |   Page text under 60 runes is NEVER sent to the LLM — the job fails with
    |   "page text too short … refusing LLM call (hallucination risk)" instead
    |   of storing invented items. The Python service enforces the same floor
    |   on extractions:structure (422).
    |
    +-- JS-shell re-cascade (post-extract)
    |   If the text pass returns 0 items AND the static HTML was a JS shell,
    |   render via the webagent and re-run extraction on the hydrated HTML.
    |   Covers shells with real static text (header/hours) but a client-side
    |   menu; gated on 0 items so it never dilutes a working extraction.
    |
    +-- PDF service path (--extractor-url)
    |   PDFs without a text layer route to the service (inspect → per-page
    |   extract → structure). Pure-Go ExtractPDFVision is the 503 fallback.
    |
    v
MenuExtractionResult → EmbedBatch → BatchUpsertMenu → Weaviate
```

**JS-shell detection** (`scraper.IsJSShell`) uses two rules, either flags a shell:
- *Ratio rule*: raw HTML ≥ 50KB but < 500 visible runes and a byte-to-rune ratio
  > 500× — the classic Wix shape with inlined JS bundles.
- *Trivial rule*: < 60 visible runes and any `<script>` tag — external-bundle
  SPAs (e.g. `dine.online` serves a ~20KB shell whose bundles load via
  `<script src>`, invisible to the ratio rule).

**`<button>` text is kept** by `ConvertHTMLToMarkdown` (emitted as list items):
ordering SPAs render each menu item card — name, description, price — as a
`<button>`, so skipping them erased whole menus. Stray UI labels ("Add to
cart") are cheap noise the extractor prompt ignores.

**Key types** (`scraper/scraper.go`):

```go
type MenuEntry struct {
    DishName            string   `json:"dish"`
    Description         string   `json:"description"`
    StatedIngredients   []string `json:"stated_ingredients"`
    HasFullIngredients  bool     `json:"has_full_ingredients"`
}

type MenuExtractionResult struct {
    RestaurantName string      `json:"restaurant_name"`
    City           string      `json:"city,omitempty"`
    State          string      `json:"state,omitempty"`
    SourceURL      string      `json:"source_url"`
    Address        string      `json:"address"`
    PhoneNumber    string      `json:"phone_number"`
    ScrapedAtUTC   string      `json:"scraped_at_utc"`
    Items          []MenuEntry `json:"items"`
}
```

The `ServiceExtractor` (`scraper/service_extractor.go`) implements three interfaces for the service-backed paths:
- `PDFExtractor` — `ExtractPDF(ctx, pdfBytes)` for PDF/OCR
- `ImageExtractor` — `ExtractImage(ctx, imgBytes, mime)` for image-embedded menus
- `JSRenderer` — `ScrapeJS(ctx, adapterID, params)` for JS-rendered pages

All three converge on the same `extractions:structure` endpoint to produce a `MenuExtractionResult`. See the [Scraper Service Integration Plan](../plans/scraper-service-integration-plan.md) for details.

---

## Data Pipeline

```
data/archive.tar.gz  (Yelp JSON lines, gzip-compressed)
        |
        v
   GetArchive(path, "review")  ->  *bufio.Scanner
        |
   |
Avro path (event cmd)
   |
EventWriter.Write()
   |
*.avro
```

---

## Input Data

Place the Yelp dataset archive at:

```
./data/archive.tar.gz
```

The archive must contain files whose names include `"review"` and `"business"`:
- `yelp_academic_dataset_review.json` — review text and ratings (required for all features)
- `yelp_academic_dataset_business.json` — business name, city, state, categories (required for search filters)

Both files must be formatted as newline-delimited JSON (JSONL).

