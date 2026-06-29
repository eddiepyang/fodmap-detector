# Plan: Sub-URL Extraction for Directory / Paginated Menus

## Background
Several restaurants fail with "no menu items extracted" because the provided URL is
not a menu — it is a **directory** that links out to the real menus:

- **Jing Li (`jinglinyc.com` / `onefork.nyc`)** — JS site-builder; main page links to
  sub-menus (`/appetizers`, `/dim-sum-soup`).
- **Queens Room (`queensroomnyc.com`)**, **Lighthouse Rooftop
  (`lighthouserooftop.com`)** — buttons linking to static **PDF** menus.
- **Kakes NYC (`kakes.nyc/menus`)**, **Stepping Stone Cafe (`steppingstone.cafe`)**,
  **Eatzy Thai (`eatzythai.com/menu`)**, **Nagai Ki Ramen (`nagaikiramen.com`)** —
  items live on sub-pages or PDFs, not the root URL.

The pipeline extracts items from the first page only, finds none, and writes
`failed_scrape` ([pipeline.go:249-252](../../pipeline/pipeline.go#L249-L252),
[scrape.go:95-98](../../menusearch/scrape.go#L95-L98)).

## Why the original LLM-from-text approach does NOT work
The earlier draft relied on the Tier-1 LLM returning `category_urls` it "sees" on the
page. It can't: the HTML→Markdown step strips every link before the LLM ever sees it.

- `walkHTML` emits anchor **text** but never the `href`
  ([scraper.go:244-284](../../scraper/scraper.go#L244-L284)).
- `button`, `img`, `svg` are in `skipTags` — so the PDF-button cases (Queens Room,
  Lighthouse) are doubly invisible.
- The trafilatura fallback uses `IncludeLinks: false`
  ([scraper.go:292](../../scraper/scraper.go#L292)).

So `pageText` contains zero URLs; the LLM could only hallucinate them. We therefore
extract links **deterministically from HTML in Go**, and validate them with the
menu-signal machinery we already built for discovery — no new LLM dependency.

## Architecture decisions (and why)

### 1. Aggregate in-loop inside the single `scrape_menu` job — do NOT fan out to River
When the directory's sub-URLs are found, fetch + extract them **in a loop within the
same job**, then do **one** `StoreMenu` + **one** `UpdateScrapeResult(scraped,
totalCount)`. We deliberately reject the "enqueue one `scrape_menu` job per sub-URL"
design:

- **`item_count` correctness.** N sibling jobs each call `UpdateScrapeResult` for the
  same CAMIS. The guard we added
  ([update_scrape_result.sql:12](../../menusearch/store/sql/update_scrape_result.sql#L12))
  allows `scraped → scraped`, so the last sibling to finish overwrites — the count
  reflects one sub-page, not the sum. In-loop aggregation writes the total once.
- **No status flapping / no sibling contention.** Fan-out re-opens the clobber/race
  class we just fixed; in-loop avoids it entirely.
- **Scrape ordering unchanged.** In-loop adds no new River jobs, so it does not push
  sub-pages to the tail of the queue. It is the same Go→Python→Go shape the webagent
  path already uses today ([pipeline.go:181-202](../../pipeline/pipeline.go#L181-L202)).
- Cost: one job runs longer. Bounded by the depth cap + the concurrency semaphore
  below; bump `ScrapeMenuWorker.Timeout` ([scrape.go:44](../../menusearch/scrape.go#L44))
  if needed.

### 2. Fetch each sub-URL through `ExtractMenu` so it inherits the full cascade
Each sub-URL is run through `ExtractMenu`, NOT a raw `fetcher.Fetch`. That is what makes
blocked / JS sub-pages work without new code:

- **403/429** → `fetchWithFallback` renders via the Python webagent
  ([pipeline.go:54-72](../../pipeline/pipeline.go#L54-L72)) — the same path that
  unblocked Applebee's in e2e.
- **JS-only / empty shell** → routes to `ScrapeJS` (rendered)
  ([pipeline.go:150-204](../../pipeline/pipeline.go#L150-L204)) — requires
  `--webagent-adapter` to be set.
- **PDF sub-URLs** (Queens Room, Lighthouse) → content-type routes to the PDF cascade
  (`ExtractPDF`). This is the main reason fetching stays in Go rather than recursing in
  Python.

### 3. Concurrency: Go-side semaphore, not async Python
The Python browser pool is **N persistent browser threads** (default 4,
`WEBAGENT_MAX_FETCH_CONCURRENCY`), with a bounded queue that returns `BrowserBusy`
(→ `IsRenderTransient` → River backoff) when full
([pool.py:55-124](../../../scraper/src/scraper/webagent/fetch/pool.py#L55-L124)). The
ceiling is real browser instances (RAM/CPU), which an async rewrite cannot raise. So:

- Cap the in-loop sub-URL fetches with an `errgroup` **semaphore ≤ pool size** (mirror
  the pattern in `reachableMenuURLs`) so we never spray `BrowserBusy` 503s.
- Scale throughput with `WEBAGENT_MAX_FETCH_CONCURRENCY` if hardware allows.
- **Camoufox** (heavier anti-bot tier) is auto-selected by the pool **if the `camoufox`
  package is importable** ([pool.py:83-91](../../../scraper/src/scraper/webagent/fetch/pool.py#L83-L91)).
  It is a dependency toggle (`uv add camoufox`), not a code change — keep as a follow-up
  escalation if playwright-stealth proves insufficient on hard sub-URLs.

### 4. Reuse the discovery validators — do not trust raw anchors
Page anchors include nav, footer, social, ordering platforms, and off-site junk. Filter
through the helpers already in [discover.go](../../menusearch/discover.go):
`isNonMenuURL`, `isOrderingPlatform`, `isPrivateMenuHost` (SSRF), and
`checkMenuSignal` / `hasMenuSignal`. Constrain candidates to the **same registrable
domain** as the directory page (allow same-domain PDFs), plus a path/text pre-filter
(`menu`, `lunch`, `dinner`, `food`, `.pdf`, …) to bound how many URLs we network-probe.

### 5. Depth cap + dedup
Add `Depth int` to `ScrapeMenuArgs` (default 0; existing River jobs deserialize to 0).
Only expand sub-URLs when `Depth == 0`; sub-jobs/iterations run at the leaf level and
never recurse further. Dedup candidates and drop the directory's own URL to avoid
self-loops and wasted work.

## Implementation steps

### Step 1 — `menusearch/jobs.go`
Add `Depth int json:"depth"` to `ScrapeMenuArgs`. No `Kind()` change.

### Step 2 — Link extraction helper (new, Go)
Add `extractMenuSubURLs(rendered []byte, baseURL string) []string` (likely in
`pipeline` or `menusearch`):
1. Parse anchors (and PDF `href`s) from the **rendered** HTML via `golang.org/x/net/html`.
2. Resolve relative links against `baseURL` (`url.Parse` / `ResolveReference`).
3. Same-registrable-domain filter (+ allow same-domain `.pdf`).
4. Path/text pre-filter for menu-ish links; drop `isNonMenuURL` / `isDeliveryURL` /
   self-URL; dedup.

Getting *rendered* HTML: for the directory page, reuse the body `ExtractMenu` already
fetched; if it was JS/empty (the `needsFallback` branch), obtain rendered HTML via
`FetchRenderedHTML` so anchors are present.

### Step 3 — Wire directory handling into the scrape flow
In `ScrapeMenuWorker.Work` (or a new `ExtractMenuWithExpansion` wrapper around
`ExtractMenu`):
1. Run `ExtractMenu` for `args.URL` as today.
2. If `len(result.Items) > 0` → unchanged path.
3. If `len(result.Items) == 0 && args.Depth == 0`:
   - `candidates := extractMenuSubURLs(rendered, finalURL)`
   - Validate with `menuSignalFilter` / `checkMenuSignal` (reuse the discovery
     `http.Client`).
   - If none survive → write `failed_scrape` as today.
   - Else: fetch each surviving URL via `ExtractMenu` (Depth=1) under an `errgroup`
     semaphore (≤ `WEBAGENT_MAX_FETCH_CONCURRENCY`), collecting items; tolerate
     per-URL failures (log + continue).
   - Aggregate items into one `MenuExtractionResult`, set tier
     (e.g. `html_llm`/`pdf` of children, or a new `directory_fanout` label), then the
     existing single `StoreMenu` + `UpdateScrapeResult(scraped, total)` runs once.
4. Bronze: write each sub-URL's raw body as today (per-URL provenance preserved).

### Step 4 — No required Python changes
Rendering already exists (`/v1/webagent/fetch`, `ScrapeJS`). The Pydantic schema /
prompt edits from the original draft are **dropped** — link selection is Go-side and
deterministic. (Optional future enhancement: an LLM classifier over rendered-DOM anchors
if the deterministic + menu-signal recall proves too low. Not in scope now — keeps the
dependency surface flat per AGENTS.md.)

## Testing
- Unit: `extractMenuSubURLs` against saved HTML for Jing Li, Queens Room (PDF buttons),
  Lighthouse — assert correct same-domain + PDF link extraction and junk rejection.
- Unit: depth cap (Depth=1 input never expands), dedup, self-URL drop.
- Integration: a fake directory page → assert one aggregated `scraped` write with summed
  `item_count` and no intermediate `failed_scrape`.
- e2e: re-run the known-failing CAMIS set; confirm tier mix + non-zero items and a single
  status write per restaurant.

## Open questions
- Tier label for an aggregated directory result — reuse child tier vs. new
  `directory_fanout` constant (affects tier-mix telemetry).
- Whether to bump `ScrapeMenuWorker.Timeout` now or only if observed sub-page counts are
  high.
