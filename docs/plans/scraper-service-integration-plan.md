# Plan: Integrate fodmap-detector with the Python `scraper` service (PDF/OCR, image-embedded menus, JS-rendered pages)

**Status:** Phases A, B, and C are **implemented** (branch `scraper-integration`).
See the [CLI reference](../guides/cli-reference.md) for usage and
[data-model.md](../guides/data-model.md) for the full cascade diagram.

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
in three phases, and correct the cross-repo docs to match reality.

## Decision

Build **all three phases**: Phase A (PDF/OCR over the existing `/v1` HTTP API),
Phase B (JS-rendered pages via `webagent`, now exposed under `/v1/webagent`),
and Phase C (image-embedded menus — discovered in the Phase A e2e test against
`thriftnsipcafe.com`). All three are implemented.

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
- Empty-menu handling (**RESOLVED** — implemented): `MenuDocument.sections`
  previously had `min_length=1`, which would have surfaced an empty menu as an
  error and pressured the model to fabricate a section. The Python schema was
  relaxed to allow `sections: []`, so an empty menu is now a normal **200** with
  zero sections. It maps to a `MenuExtractionResult` with zero `Items` and is
  handled by the existing `len(result.Items) == 0` check (`cli/scrape.go:214`) —
  no special error-detection heuristic on the Go side.

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

## Phase B — Route JS-rendered pages to `webagent` (implemented)

Phase B required prerequisite work in the Python repo to expose `webagent` over
HTTP; that is now done (`/v1/webagent/scrape/{site}/{target}`, gated by
`SCRAPER_WEBAGENT_ENABLED=true` and bearer auth).

### B1. Python repo: expose `webagent` over HTTP (done)

**Pipeline shape (the key point): JS pages are a two-call flow that converges on
the same structuring step as PDFs** — webagent is just another *acquisition*
front-end, alongside `documents:inspect`/`pages:extract`:

1. `POST /v1/webagent/scrape/{site}/{target}` (webagent) → navigates the JS page and
   returns `ScrapeResult{records: list[dict], meta}`
   (`../../../scraper/src/scraper/webagent/result.py`) — adapter-extracted
   content, **not** a `MenuDocument`.
2. The detector serializes those records/text into `merged_text` and calls the
   existing `POST /v1/extractions:structure` → validated `MenuDocument`.

So from the detector's side this is "acquire, then structure" — the same
converge-on-structuring pattern as PDF. **Server topology (one router via
`include_router` vs. a mounted sub-app vs. a separate process) is a Python-repo
decision, not something this plan should fix.** The only hard requirement the
detector cares about is that both endpoints are reachable on the configured base
URL. (Note: webagent does need a Playwright browser-pool lifecycle and its own
`WebAgentError` envelope, which is why it's currently a separate factory — that
informs the Python-side choice but doesn't change the two-call client contract.)

Work needed in the Python repo (done):
- Expose the `scrape` endpoint on the service the detector targets (mounted under
  `/v1/webagent` via `include_router`).
- **Discovery stays a separate, offline authoring step.** `scrape/{site}/{target}`
  requires a pre-compiled adapter in the `AdapterRegistry` produced by
  `webagent/discovery/cli.py`. The two-call runtime does *not* remove this; the
  detector calls `scrape` only for sites that already have an adapter. (Exposing
  discovery itself over HTTP is a larger, optional follow-on — see Risks.)
- Add bearer auth on `/v1` (including the webagent sub-app) via
  `BearerAuthMiddleware` when `SCRAPER_API_KEY` is set.

### B2. Go side: route JS pages (done)

- The `--enable-js-render` flag now means "send JS-only pages to the service's
  webagent endpoint" (gated by `--extractor-url` + `--webagent-adapter`).
- Detect JS-only pages (the `IsTooNoisy`/empty-content heuristics already in
  [scraper/scraper.go](../../scraper/scraper.go)), then run the two-call flow:
  call `scrape/{site}/{target}` → serialize the returned `records` into
  `merged_text` → reuse the **same** `extractions:structure` path the PDF flow
  already uses (`ServiceExtractor`'s structuring call). This keeps a single
  structuring code path on the Go side for both PDF and JS inputs.

---

## Phase C — Route image-embedded menus to the service (discovered in e2e)

### Context

The e2e test against `https://thriftnsipcafe.com/#MENU` exposed a gap neither
Phase A nor B covers: **the menu is a static HTML page whose only menu content
is an embedded `<img>` of a printed trifold menu** — no JSON-LD, no text items,
no JS rendering. The page is WordPress + LiteSpeed cached; the `#MENU` anchor
wraps a heading, one marketing sentence, and a single PNG
(`TRIFOLD_MENU_8x11_BC-2048x1596.png`, `alt=""`). The detector's current flow
converts HTML→Markdown→LLM and would send the LLM the text "Menu\n\n[marketing
sentence]\n\n" with no actual menu data — it cannot see the image.

The service already handles this: `documents:inspect` with
`Content-Type: image/png` returns `route=ocr` for a single "page", and
`pages:extract` OCRs it. The gap is on the Go side: `runScrapeWith` has no path
from "fetched an HTML page" → "found a menu image" → "sent the image bytes to
the service."

### C1. Detect menu-image candidates in fetched HTML

After the existing HTML→Markdown conversion, if the result is noisy/empty AND
JSON-LD found no items, scan the fetched HTML for the largest `<img>` likely to
be the menu. Heuristics (pick a subset, tune during impl):

- **Size:** `width`/`height` attributes ≥ 800px, or `srcset` containing a
  ≥1024w descriptor — menu trifolds are large images.
- **Filename:** `src` matching `/menu|trifold|menu-card|food|drink/i` — the
  test case is `TRIFOLD_MENU_8x11_BC-...png`.
- **Context:** the `<img>` is inside or near an element with `id` containing
  `menu` (the test case: `<div id="MENU">…<img …>`).
- **Alt text:** `alt` is empty or contains "menu" (empty alt is actually a
  positive signal here — text menus use descriptive alt).

Exclude: nav logos, icons (`width < 100`), social/share buttons, and images
inside `<header>`/`<footer>`/`<nav>`.

Implementation: a new `findMenuImage(htmlBytes, contentType) (imgURL string, ok bool)`
in `scraper/scraper.go` using `golang.org/x/net/html` (already a dependency).
Return the first candidate's absolute URL (resolve relative URLs against the
page URL). If multiple candidates, prefer the largest by `srcset` descriptor.

### C2. Route the image to the service

When `findMenuImage` returns a candidate AND `--extractor-url` is set:

1. **Download the image** via the existing `HTTPFetcher` (honors robots, body
   cap, UA). The image URL shares the page's host, so SSRF is not a new concern
   (the fetcher already validated the page URL).
2. **Call `ServiceExtractor.ExtractImage(ctx, imgBytes)`** — a new method that
   reuses the existing `inspectDocument` → `extractPage` → `structure` flow
   from A1, but sends the image with `Content-Type: image/png` (the service's
   image-input path skips PyMuPDF page logic per `v1.py:52-58`). The inspect
   call returns a single-page `route=ocr` decision; the extract call does the
   real OCR.
3. The result is a fully-structured `MenuExtractionResult` — skip `ex.Extract`
   (same control-flow branch as the PDF service path in A2).

Add `ExtractImage` to the `PDFExtractor` interface? No — rename the interface
to `ServiceExtractor`-level capability or add a sibling. Recommended: add a
new `ImageExtractor` interface so the type switch in `runScrapeWith` stays
readable:

```go
type ImageExtractor interface {
    ExtractImage(ctx context.Context, imgBytes []byte) (MenuExtractionResult, error)
}
```

`ServiceExtractor` implements both `PDFExtractor` and `ImageExtractor`.

### C3. Integration seam in `runScrapeWith`

In the HTML branch (the `else` clause at `scrape.go:213`), after the
trafilatura fallback still produces noisy/empty/too-short content:

```go
// Phase C: check for a menu image embedded in the HTML.
if (scraper.IsTooNoisy(md) || strings.TrimSpace(md) == "" || tooShort) && extractorURL != "" {
    if imgURL, ok := scraper.FindMenuImage(bodyBytes, ct, rawURL); ok {
        if iex, ok := ex.(scraper.ImageExtractor); ok {
            // Fetch the image and route to the service.
            imgFetch, err := fetcher.Fetch(ctx, imgURL)
            // … read bytes, call iex.ExtractImage, set result, goto tier1Done
        }
    }
}
```

This runs **before** the JS-render check (Phase B) — an image-embedded menu is
a more common case than a JS-rendered SPA, and doesn't require a pre-compiled
adapter. The two paths are mutually exclusive: if a menu image is found, skip
the webagent; if no image is found, fall through to the JS-render check.

### C4. Fallback policy

- `--extractor-url` unset → `FindMenuImage` result is ignored; the noisy HTML
  flows to the normal LLM path (which will likely produce no items — same as
  today). Log a warning: "page appears to contain a menu image
  (`<url>`); set --extractor-url to OCR it."
- Service returns 503 → same fallback as Phase A: log + hard error naming both
  failures (per the A1 503 policy). There is no pure-Go image-OCR fallback.
- `FindMenuImage` finds nothing → fall through to Phase B (JS-render) if
  configured, else the normal LLM path.

### C5. Tests

- `scraper/scraper_test.go`: `FindMenuImage` with a fixture HTML containing
  `<div id="MENU"><img src="menu.png" width="1024" height="798"></div>` →
  returns the absolute URL. Negative cases: nav logo, small icon, no image.
- `scraper/service_extractor_test.go`: `ExtractImage` orchestration via
  `httptest` stub (inspect with `image/png` → extract → structure), asserting
  the image content-type is sent and the single-page ocr route is taken.
- `cli/scrape_service_test.go`: an `ImageExtractor` stub + a fetcher that
  serves HTML-with-menu-image then the image bytes, asserting the image path
  is taken and `ex.Extract` is skipped.

### C6. What this does NOT cover

- **Galleries / multiple menu images.** A page with 5 menu photos needs a
  multi-image flow (loop → merge). Scope to single-image for now; the test
  case and likely majority are single-trifold menus.
- **Image menus without `--extractor-url`.** Pure-Go cannot OCR images. The
  warning in C4 is the best we can do without a local vision model that
  actually works (the e2e test showed `qwen3.6:35b-mlx` reports vision
  capability but refuses images).
- **SVG menus.** `FindMenuImage` looks for raster `<img>`/`<picture>`. SVG
  menus are text-extractable and don't need this path.

---

## Cross-repo documentation fixes (done)

- `../../../scraper/README.md` and
  `../../../scraper/docs/plans/menu-extraction-implementation-plan.md`:
  "Phase 2" status now reads "wired" — Phase A wires PDF/OCR, Phase B wires
  JS/webagent, Phase C wires image-embedded menus.
- Detector [README.md](../../README.md) lists this plan under "In Progress";
  the archived plan notes it's extended by the service integration.
- `--enable-js-render` is no longer a no-op — it routes to webagent when
  `--extractor-url` + `--webagent-adapter` are set (see
  [cli-reference.md](../guides/cli-reference.md)).

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
- **Phase B unit/contract:** `make check` in the detector; `cli/scrape_service_test.go`
  covers the webagent routing path with a `jsRendererStub`. Phase B e2e is pending
  a real adapter in the Python repo's `AdapterRegistry` — the endpoint is live
  but requires a pre-compiled adapter for the target site.
- **Phase C e2e (validated manually, to automate):** the
  `thriftnsipcafe.com/#MENU` case — a static HTML page whose menu is a single
  embedded PNG. With `--extractor-url` set, `FindMenuImage` detects the
  trifold PNG, fetches it, and routes it through the service's image-OCR path,
  producing the same 12-section / 21-item structured menu the manual e2e
  produced (OCR via `minicpm-v:8b`, structuring via `qwen3.6:35b-mlx`).
  Without `--extractor-url`, the scrape logs "page appears to contain a menu
  image" and produces no items (same as today).

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
- **Slow OCR / timeouts (RESOLVED — implemented).** Per-page vision OCR can take
  many seconds each. `ServiceExtractor` uses a per-page request timeout
  (`--extractor-page-timeout`, default 2m) *and* enforces an overall wall-clock
  deadline across the whole page loop via `context.WithTimeout`
  (`--extractor-pdf-timeout`, default 10m) — the per-request timeout alone does
  not bound an N-page loop. Per-page progress is logged via `slog`.
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
- **Image-menu detection heuristics are fragile.** `FindMenuImage` (Phase C)
  relies on filename/context/size heuristics. A page that embeds its menu as a
  background-image, a CSS `::after`, or an unlabelled `<img>` in a non-`#MENU`
  container will be missed. The heuristics need tuning against real-world
  fixtures; the e2e test case is one data point. Consider a fallback: if the
  LLM extraction yields zero items AND the page has any large image, prompt the
  LLM with "is there a menu image on this page?" (costs one extra call).
- **Vision model availability is a hard dependency for Phase C.** The e2e test
  showed `qwen3.6:35b-mlx` reports `vision` capability but refuses images
  (text-only quantization), and the MLX DeepSeek-OCR model hit a
  `transformers`/`mlx-vlm` version incompatibility. Phase C only works when the
  service has a genuinely functional OCR backend (MLX DeepSeek-OCR with a
  compatible `transformers` version, or a vLLM/Ollama vision model like
  `minicpm-v:8b`). Document the known-good backend matrix in the service README.
- **Structuring quality on OCR'd images is lower than on text.** The e2e OCR
  (minicpm-v) produced 1,969 chars but the structuring LLM (qwen3.6) took ~5 min
  and produced 21 items across 12 sections — some sections are coarse ("Coffee:
  3 items" with no descriptions/prices). The service's structuring prompt may
  need tuning for image-OCR input (which is noisier and less structured than
  HTML→Markdown text). This is a prompt-quality issue, not an architecture
  issue, but it affects Phase C's real-world value.
