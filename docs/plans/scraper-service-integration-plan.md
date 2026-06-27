# Plan: Integrate fodmap-detector with the Python `scraper` service (PDF/OCR now, JS/discovery later)

## Context

A claim circulated that the detector routes its extraction through the Python
`scraper` service and that "Phase 2 is done." Direct verification of both repos
shows that is **false**:

- The detector is **pure-Go and self-contained**. [cli/scrape.go](../../cli/scrape.go)
  builds `scraper.NewOpenAICompatExtractor(...)` (`cli/scrape.go:112`) and talks
  to an OpenAI-compatible LLM directly, including the PDF vision path
  (`cli/scrape.go:239` → `scraper.ExtractPDFVision`).
- **No code** references the service: zero hits for `8765`/`documents:inspect`/
  `extractions:structure` in any `.go` file (only in the archived
  [python-extractor-service-plan.md](python-extractor-service-plan.md)).
- The detector has **real capability gaps**:
  - Vector-outline / scanned / broken-CID-font PDFs fail — `RenderPDFPages`
    only pulls *embedded raster images* via `pdfcpu.ExtractImages`
    ([scraper/vision_pdf.go:89](../../scraper/vision_pdf.go)), and its own
    comment admits it fails on vector/text-only pages.
  - `--enable-js-render` is a **no-op stub**: `chromedp` is not a dependency,
    and the flag is never read.
- The "Phase 2 done" claim actually lives in the **`scraper` repo**
  (`../../../scraper/README.md:391`,
  `../../../scraper/docs/plans/menu-extraction-implementation-plan.md:43,123`)
  and is wrong.

The Python service already implements the hard paths: PyMuPDF rasterization, OCR
VLM, per-page routing, schema-validated structuring, and a *separate* Playwright
`webagent` with anti-bot + LLM-driven discovery.

**Goal:** Make the detector actually use the service for inputs Go can't handle,
in two phases, and correct the cross-repo docs to match reality.

## Decision

Build **both phases**: Phase A (PDF/OCR over the existing `/v1` HTTP API) now;
Phase B (JS-rendered pages via `webagent`) later, since that capability is not
yet exposed over HTTP in the Python repo.

---

## Phase A — Route the PDF/OCR path to the service

The service's `/v1` surface (mounted at `../../../scraper/src/scraper/app.py:65`)
is the only HTTP contract today: `documents:inspect`,
`documents/{doc}/pages/{n}:extract`, `extractions:structure`, `/models`. The
`webagent` is a *separate* app (`../../../scraper/src/scraper/webagent/app.py`),
not mounted — hence Phase B is separate.

### A1. New Go file `scraper/service_extractor.go`

**Key decision (from review):** HTML/text extraction stays in pure-Go
(`OpenAICompatExtractor`) — only PDFs route to the service. This honors the
"Go owns the easy paths" split and avoids a network round-trip on the common
HTML case. So `ServiceExtractor` is *composite*: it holds an
`OpenAICompatExtractor` for `Extract(ctx, pageText)` (HTML/text), and overrides
only the PDF path.

- `type ServiceExtractor struct { baseURL string; httpClient *http.Client; text *OpenAICompatExtractor }`
  with `NewServiceExtractor(baseURL string, text *OpenAICompatExtractor)`.
  Configure `httpClient` with **concrete, CLI-configurable timeouts**, not a
  vague "generous" value: per-page request timeout default **120s**
  (`--extractor-page-timeout`), overall PDF deadline default **10m**
  (`--extractor-pdf-timeout`), both overridable via CLI/env. Per-page vision
  OCR can exceed any fixed value, so the multi-page scan deadline must be
  separate from the single-request deadline. Surface per-page progress via
  `slog` (start/finish of each `pages:extract` call).
- `Extract(ctx, pageText)` → delegate to the embedded `text` extractor (pure-Go
  structuring of HTML/markdown). No service call.
- `ExtractPDF(ctx, pdfBytes)` → orchestrate the documented stateless flow:
  1. POST raw bytes to `/v1/documents:inspect` → `DocumentInspectResult`
     (`page_count`, per-page `route`).
  2. For each page POST raw bytes to `/v1/documents/{doc}/pages/{n}:extract`
     → `ExtractPageResult`. Build the merged blob from `text` (text route) or
     `ocr_text` + `ocr_layout` (ocr route). The `extractions:structure` contract
     *expects* the caller to fold text **and layout** into the single
     `merged_text` string (see the layout-serialization note in Risks) —
     `ocr_layout` is a `str`, so forwarding it is concatenation; pick the exact
     serialization here. Use a fixed opaque `{doc}`
     label (the service is stateless). Preserve page order when merging.
  3. POST the merged blob to `/v1/extractions:structure` → map to
     `MenuExtractionResult` (A3).
  - Parse the service error envelope `{"error":{code,message,request_id}}` and
    surface `X-Request-Id` in returned errors for cross-service debugging.
  - **Fallback policy:** on 503 (`BackendNotAvailableError` — OCR backend down),
    fall back to the pure-Go `ExtractPDFVision` cascade rather than hard-failing,
    so `--extractor-url` degrades gracefully. (Confirm this is desired vs. a hard
    error during impl.)

### A2. Integration seam in the detector

`extractPDF` currently casts the `Extractor` to the concrete
`*scraper.OpenAICompatExtractor` for vision (`cli/scrape.go:252`). Replace that
type-switch with a small interface so either backend can own PDFs:

```go
type PDFExtractor interface { ExtractPDF(ctx context.Context, pdfBytes []byte) (MenuExtractionResult, error) }
```

In `extractPDF`, the service is the **`ErrNeedVision` fallback**, not a direct
replacement (see "Cascade ordering" below): try the local text-layer → pdftotext
cascade first, and only when that returns `ErrNeedVision` route to the service if
`ex` implements `PDFExtractor` (`ExtractPDF` then handles inspect→extract→structure
end-to-end). Keep `ExtractPDFVision` as the pure-Go fallback so the path still
works when `--extractor-url` is unset.

#### Control-flow change in `runScrapeWith` (not just `extractPDF`)

`extractPDF` returns `(string, error)` and its output is fed *unconditionally*
back into `ex.Extract(ctx, pageText)` at `cli/scrape.go:196` for a second LLM
structuring pass. The service path's `ExtractPDF` already returns a complete
`MenuExtractionResult`, so `runScrapeWith`'s Tier 1 block (`scrape.go:174-212`)
must branch, not just `extractPDF`:

- If `ex` implements `PDFExtractor`, take its result directly and **skip the
  `ex.Extract` call** — otherwise the service-extracted menu is re-structured by
  the local LLM, double-spending tokens and risking drift.
- Still apply the JSON-LD location/restaurant backfill (`scrape.go:203-210`)
  and the `SourceURL` / `ScrapedAtUTC` assignment to the service result, since
  the service has no notion of the fetch URL.

Recommended shape: have `extractPDF` return either `(text string, result
*MenuExtractionResult, err error)` — empty `text` + non-nil `result` signals
"already structured, skip the LLM pass" — or split into two helpers
(`extractPDFText` vs. `extractPDFStructured`) and let `runScrapeWith` pick.
Decide the exact signature during impl, but the *branch must live in
`runScrapeWith`*, not inside `extractPDF`.

#### Cascade ordering — service is a fallback, not a replacement

The current cascade is `ExtractPDFText → pdftotext → (vision)`. The service
should be tried **only after the text-layer/pdftotext cascade fails** (i.e.,
on `ErrNeedVision`), not in front of it. Rationale:

- Text-layer extraction is fast, free, and local; the service costs N+1
  uploads + N inspections per PDF (see Risks).
- Routing every text PDF through the service would regress the common case.

So when `--extractor-url` is set, the order becomes:
`ExtractPDFText → pdftotext → service.ExtractPDF → (on 503) pure-Go ExtractPDFVision`.
When unset, the order is unchanged (`… → ExtractPDFVision`).

#### `--enable-vision` interaction

Today `--enable-vision` gates the vision cascade (`scrape.go:247`): without it,
a text-less PDF hard-fails with a clear message. With `--extractor-url` set,
the service path **replaces** the vision path, so:

- `--extractor-url` set, `--enable-vision` unset → service path is used for
  text-less PDFs (the service *is* the vision path now).
- `--extractor-url` unset, `--enable-vision` unset → unchanged (hard fail on
  text-less PDF).
- `--enable-vision` set with `--extractor-url` unset → unchanged pure-Go vision.
- `--extractor-url` set **and** `--enable-vision` set → service path wins for
  text-less PDFs; pure-Go vision is only reached as the 503 fallback (see Risks).

In other words, `--extractor-url` is an alternative to `--enable-vision` for the
text-less branch, not an additional gate. Document this in `--help` and the
README so the two flags' relationship is explicit.

### A3. Schema mapping (Python `MenuDocument` → Go `MenuExtractionResult`)

Python is hierarchical and richer
(`../../../scraper/src/scraper/schema.py:82-89`); Go is flat
([scraper/scraper.go:41-55](../../scraper/scraper.go)):

- Flatten `sections[].items[]` → `Items []MenuEntry`.
- Map `MenuItem.name`→`MenuEntry.DishName`, `description`→`Description`,
  `stated_ingredients`→`StatedIngredients`, `has_full_ingredients`→`HasFullIngredients`.
- Drop `price`/`modifiers`/`bounding_box` for now (no Go field). Optionally fold
  modifiers into description text — decide during impl.
- Handle the **behavior difference**: Python `MenuDocument.sections` is
  `min_length=1`, so an empty menu yields a 4xx, whereas Go currently just logs
  "no menu items" (`cli/scrape.go:214`). Treat that specific validation error as
  "no items found", not a hard failure.

### A4. CLI flag

Add to [cli/scrape.go](../../cli/scrape.go) init: `--extractor-url` (default
empty). When set, build `ServiceExtractor{text: <the OpenAICompatExtractor>,
baseURL: ...}` so HTML still uses the local LLM and only PDFs route to the
service; when empty, build the plain `OpenAICompatExtractor` (pure-Go default —
no behavior change). Mirror the existing flag/viper pattern (lines 54-57, 74-77).

**Config ownership note:** when `--extractor-url` is set, *PDF* structuring is
owned by the service's `SCRAPER_LLM_*` / OCR backend config — the detector's
`--llm-model`/`--llm-url` then only drive the HTML/text path (and embeddings
remain on `--ollama-*`). Document this split so the model knobs aren't
misleading.

### A5. Tests (per `.rules/testing.md`: stubs not mocks, external base-URL var)

- `scraper/service_extractor_test.go`: stub the service with `httptest.NewServer`
  returning canned `documents:inspect` / `pages:extract` / `extractions:structure`
  responses; assert the Go client orchestrates calls correctly and maps the
  schema. (The archived plan already specced this shape.)
- A `runScrapeWith` test that injects a `PDFExtractor` stub to confirm the PDF
  branch routes to the service path and that the `ex.Extract` second pass is
  **skipped** for the service result (see A2 control-flow change).
- An explicit **error-envelope contract test**: stub the service to return a
  `{"error":{"code","message","request_id"}}` body and assert the Go client
  surfaces `X-Request-Id` in the wrapped error. Without this, A1's
  request-id-surfacing claim is unverified.
- No Makefile change needed: `check: lint test build` and `test:` already runs
  `go test ./... -count=1 -v`, so `service_extractor_test.go` is picked up
  automatically.

---

## Phase B — Route JS-rendered pages to `webagent` (larger, spans both repos)

This is a **prerequisite project in the Python repo first**, because the
capability is not exposed by the service the detector calls.

### B1. Python repo: expose `webagent` over HTTP

`create_webagent_app` already defines `POST /v1/scrape/{site}/{target}`
(`../../../scraper/src/scraper/webagent/app.py:34`), but it requires a
**pre-compiled adapter** in the `AdapterRegistry` (produced by the discovery
flow). Work needed:
- Decide deployment: mount `webagent` into the main `app.py` (shared port 8765)
  or run it as a second service/port. A second mounted sub-app is cleanest.
- Provide a discovery entry point usable by the detector: either an HTTP
  `discover` endpoint taking a URL+goal returning/storing a compiled adapter, or
  keep discovery as the offline `webagent/discovery/cli.py` authoring step and
  have the detector only call the runtime `scrape` endpoint with a known
  `{site}/{target}`.
- Add auth (see Risks) before exposing anything callable.

### B2. Go side: route JS pages

- Repurpose the dead `--enable-js-render` flag to mean "send JS-only pages to the
  service's webagent endpoint" — or add `--webagent-url`.
- Detect JS-only pages (the `IsTooNoisy`/empty-content heuristics already in
  [scraper/scraper.go](../../scraper/scraper.go)) and route to the webagent
  `scrape` endpoint, mapping the returned snapshot/items into the extraction flow.

---

## Cross-repo documentation fixes (do in Phase A)

- `../../../scraper/README.md:391` and
  `../../../scraper/docs/plans/menu-extraction-implementation-plan.md:43,123`:
  change "Phase 2 … DONE" to reflect reality — Phase A wires PDF/OCR; JS/discovery
  (Phase B) is not yet exposed/wired.
- Detector [README.md](../../README.md) and the archived plan: note the new
  optional `--extractor-url` service path; keep the pure-Go default documented.
- Note that `--enable-js-render` is currently a no-op until Phase B.

## Deployment

- Update `start.sh` and `docker-compose.yaml` to optionally bring up / depend on
  the `scraper` service (CLAUDE.md mandate to keep `start.sh` working).
- **VRAM constraint:** a 16 GB-VRAM GPU cannot host the service's OCR VLM +
  structuring LLM alongside the detector's models. Document that the routed setup
  targets a high-memory host (e.g. Apple-silicon unified memory) or a split-host
  layout; pure-Go remains the single-box default.

## Verification

- **Phase A unit/contract:** `make check` in the detector (per `.rules/testing.md`);
  the `httptest`-stubbed `service_extractor_test.go` passes.
- **Phase A e2e:** run the service locally
  (`cd ../scraper && uv run uvicorn scraper.app:app --port 8765`), then run a
  vector-outline/scanned PDF that pure-Go fails on:
  `go run . scrape <pdf-url> --extractor-url http://localhost:8765` and confirm
  menu items are extracted (where the no-flag run produces "no embedded images
  found"). Confirm `--extractor-url` unset still uses pure-Go unchanged.
- **Phase B:** deferred; verify once `webagent` is exposed.

## Risks and Gaps

- **JS/discovery is not a simple sync call.** Discovery produces a *reusable
  adapter* stored in a registry (an authoring step), then the runner fetches with
  it. This is an impedance mismatch with the detector's one-shot `scrape <url>`
  CLI — Phase B may need an adapter-authoring workflow + cache, not just an
  endpoint call. Scope Phase B carefully before committing.
- **No auth on the service.** `app.py`/`v1.py` have no auth/bearer/api-key. The
  detector's `/chat` is bearer-protected. The service must stay on a private
  network in prod, or add auth before exposure (mandatory for B1).
- **Stateless re-uploads AND re-inspection — O(N), not O(1).** `pages:extract`
  re-takes the full PDF bytes per page (the `{doc}` segment is a label only)
  *and* re-runs `pdf.inspect()` internally on every page call
  (`../../../scraper/src/scraper/v1.py:128`). An N-page PDF = N+1 uploads + N
  inspections. Acceptable for typical 1-3 page menus; for large scanned PDFs
  consider asking the Python side for a batch/multi-page extract endpoint.
- **Layout serialization — an impl decision, not a blocker.** Earlier framing
  ("layout cannot be forwarded") was a misread: `StructureRequest.merged_text`
  is *designed* to carry merged text **and** layout (see `schema.py:132` docstring
  and `v1.py:10,172` "merged text+layout blob"), and `ocr_layout` is a `str`. So
  the Go client folds `text`/`ocr_text` + `ocr_layout` into the one `merged_text`
  string — no cross-repo change required. The open question is the *serialization
  format* and whether structuring quality measurably improves with layout
  included; decide during A1 and validate in the e2e check. (Do **not** scope
  Phase A to text-route-only PDFs — those already work via pure-Go `pdftotext`,
  so that would strip Phase A of its entire value.)
- **Slow OCR / timeouts.** Per-page vision OCR can take many seconds each. The Go
  HTTP client needs long, configurable timeouts; a multi-page scan can run for
  minutes. Surface progress via `slog`.
- **503 fallback policy is a real decision.** If the service is up but its OCR
  backend is unavailable (503), the plan falls back to pure-Go vision — which on
  vector PDFs will *also* fail, leaving the user with a silent failure on both
  paths. Recommended UX: log the 503 + `X-Request-Id`, attempt the pure-Go
  fallback, and if that also fails return a **clear hard error** naming both
  failures (not a silent empty result). Confirm during impl.
- **Schema-revision pinning.** Python `MenuDocument` carries a `SchemaRevision`
  enum. The Go client should pin/assert the expected revision so a service-side
  schema bump fails loudly (the contract test in A5 is the guard).
- **Schema drift across two repos** with no shared schema package. The `/v1`
  contract is the only coupling — the Go contract test (A5) is the guard.
- **Lost fields.** Dropping `price`/`modifiers`/`bounding_box` loses data the
  service extracts. If FODMAP analysis later wants ingredients-per-modifier, the
  Go schema + embedding text must be extended.
- **robots vs. stealth posture.** Go honors robots.txt (`checkRobots`,
  [scraper/scraper.go:140](../../scraper/scraper.go)); the webagent's anti-bot
  stealth evades detection. Routing JS through it shifts the project's
  scraping-consent posture — confirm this is intended in Phase B.
- **Dual prompt/schema maintenance.** Keeping the pure-Go extractor as a fallback
  means two extraction prompts/schemas to maintain. Decide whether pure-Go stays
  a supported fallback or becomes deprecated once the service path is proven.
