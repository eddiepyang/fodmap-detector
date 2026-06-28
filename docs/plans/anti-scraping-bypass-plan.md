# Anti-Scraping Bypass & Status Fix Plan

> Reviewed against the current code (2026-06-28). The original three-part
> structure holds, but each part needed correcting against what the code
> actually does. Corrections are called out inline as **⚠ Correction**.

## 1. Python Service — generic rendered-fetch endpoint (`scraper` repo)

*   **Goal**: Expose an endpoint that renders a URL in a real browser (bypassing
    WAFs / Cloudflare / JS challenges) and returns the rendered HTML.
*   **⚠ Correction — where it lives**: The original plan said "add `POST /v1/fetch`
    to the FastAPI app." That doesn't work as written:
    *   The Playwright `BrowserPool` only exists when `SCRAPER_WEBAGENT_ENABLED=true`,
        and the `playwright` import is **deferred** to that branch in
        [app.py:76-86](../../../scraper/src/scraper/app.py#L76-L86) so the base
        service can boot without the dev dependency. A route in the always-mounted
        `v1.router` ([v1.py](../../../scraper/src/scraper/v1.py)) would have no pool
        to call and would break the import isolation.
    *   **Where to actually put it — read Finding A first.** The obvious "add it to
        `create_webagent_app`" is *unreachable today* because the `/v1/webagent`
        mount is shadowed by the `/v1` mount (Finding A). Until that mount bug is
        fixed, the only reachable surface is the `/v1` sub-app (`v1.router`). Two
        viable shapes:
        *   **(Recommended)** Fix the mount (Finding A) by mounting the webagent
            *inside* the v1 sub-app, then add the route to `create_webagent_app`.
            This gets reachability **and** bearer auth (Finding B) **and** the
            shared `WebAgentError` envelope in one move.
        *   **(Minimal)** Add `POST /fetch` to `v1.router` and have the handler
            lazily call `get_webagent_runtime()` for the pool (return 503 if it's
            `None`, i.e. webagent disabled). This keeps the deferred `playwright`
            import intact (the import only happens when the runtime was initialized
            at startup) and is reachable without touching the mount. Downside: you
            re-wire the error envelope by hand.
*   **⚠ Correction — can't reuse `Navigator.fetch` directly**: `Navigator.fetch`
    is adapter-coupled — it requires a `CompiledAdapter` (`records_selector`,
    `wait_for`, `anti_bot` config) and we have none for a generic URL. Add a thin
    generic render helper that calls `pool.submit(fn, stealth=...)` with a closure
    that:
    1.  `page.goto(url, wait_until="domcontentloaded", timeout=...)`,
    2.  optionally `page.wait_for_load_state("networkidle", ...)`,
    3.  reuses `AntiBotManager` (with a default `AntiBot` config — note the
        defaults: `stealth="playwright-stealth"`, `retry.on_status=[403, 500]`;
        **add `429`** to `on_status` for this use case) for the waiting-room /
        retry-on-block loop already implemented in
        [navigator.py:59-86](../../../scraper/src/scraper/webagent/fetch/navigator.py#L59-L86),
    4.  returns `page.content()`.
*   **Response**: `{ "html": "<rendered html>", "content_type": "text/html; charset=utf-8" }`.
    `page.content()` is always serialized HTML, so the content type is fixed.
*   **Request body**: small JSON `{ "url": "..." }` — well under
    `BodySizeLimitMiddleware`'s cap. Validate with a Pydantic model.
*   **Errors**: raise the existing `FetchFailed` / `WafBlocked` / `FetchTimeout`
    (`WebAgentError` subclasses) so they map to the shared error envelope and the
    Go side sees a structured non-2xx.
*   **Add a setting** if the generic fetch should be independently toggleable;
    otherwise it rides on `webagent_enabled`.

## 2. Go Pipeline — fall back to rendered fetch on hard blocks (`fodmap-detector` repo)

*   **Goal**: When the default HTTP fetch is blocked with `403`/`429`, fetch the
    page via the Python rendered-fetch endpoint and continue normal processing.
*   **⚠ Correction (critical) — there is no way to detect the status code today.**
    `HTTPFetcher.Fetch` collapses every non-200 into a plain string error:
    `fmt.Errorf("unexpected status %d for %s", ...)`
    ([scraper.go:131-134](../../scraper/scraper.go#L131-L134)). The pipeline
    cannot tell a 403 from a 404 without brittle string matching. **First introduce
    a typed error**, e.g.:
    ```go
    type HTTPStatusError struct{ StatusCode int; URL string }
    func (e *HTTPStatusError) Error() string { ... }
    ```
    Return it from `Fetch`, then detect with `errors.As` in the pipeline. Only
    `403` and `429` are eligible for the rendered-fetch fallback; `404`, dead
    domains, and TLS errors must still fail fast (they make up 20 of the 36
    failures — see Findings).
*   **⚠ Correction — wiring through `ExtractMenu`.** `ExtractMenu` already receives
    `fetcher scraper.Fetcher` and `ex scraper.Extractor` separately, and the
    `ServiceExtractor` is the `ex`. Follow the existing capability-interface
    pattern (`ex.(scraper.JSRenderer)`, `ex.(scraper.ImageExtractor)` —
    [pipeline.go:104,128](../../pipeline/pipeline.go#L104)):
    *   Add `FetchRenderedHTML(ctx, url) (FetchResult, error)` to
        `ServiceExtractor` ([service_extractor.go](../../scraper/service_extractor.go)),
        calling the new Python endpoint. (Note: a `RenderedFetcher` interface
        already exists in [scraper.go:78-81](../../scraper/scraper.go#L78-L81) but
        is unused and hangs off `Fetcher`; prefer a new small interface on the
        extractor so the capability lives with the thing that talks to the service.)
    *   Define an interface for it and type-assert `ex` to it.
*   **Integration point**: wrap the fetch+read at
    [pipeline.go:34-43](../../pipeline/pipeline.go#L34-L43) in a `fetchWithFallback`
    helper that returns `(bodyBytes, contentType, error)`:
    1.  call `fetcher.Fetch`;
    2.  on a `*HTTPStatusError` with 403/429, if `ex` implements the rendered-fetch
        interface, call `FetchRenderedHTML` and use its body/content-type;
    3.  otherwise return the original error.
    Everything downstream (JSON-LD → HTML→Markdown → LLM → image OCR) already keys
    off `bodyBytes`/`ct` and needs no change.
*   **Note (not a blocker)**: a `429` re-fetch from the same egress IP can still be
    throttled; the bet is that a real browser (different TLS/JS/headers) clears it.
    Some WAFs also return `200` with a challenge page — that case is already
    partially covered by the existing noisy-HTML → webagent path
    ([pipeline.go:127-148](../../pipeline/pipeline.go#L127-L148)); this plan only
    adds the hard-block (403/429) path.

## 3. Go Worker — stop failed jobs from clobbering a successful scrape (`fodmap-detector` repo)

*   **Goal**: A failed job for a restaurant must not downgrade a restaurant that
    another job already marked `scraped`.
*   **Root cause**: status is keyed by `CAMIS`, and a single restaurant gets
    multiple jobs (e.g. `/menus` and `/`). `UpdateScrapeResult` does an
    unconditional `UPDATE ... WHERE camis = $1`
    ([update_scrape_result.sql](../../menusearch/store/sql/update_scrape_result.sql)),
    so a later failing job overwrites an earlier success (the Favela Grill case).
*   **⚠ Correction — fix it at the SQL level, not with read-then-write.** The
    original plan ("fetch current status, if `StatusScraped` return") has a TOCTOU
    race: two concurrent jobs can both read non-scraped and both proceed. Instead
    make the downgrade conditional and atomic. Options:
    *   **Preferred**: guard inside `update_scrape_result.sql` —
        `... WHERE camis = $1 AND NOT (status = 'scraped' AND $2 = 'failed_scrape')`
        so a `failed_scrape` write is a no-op once the row is `scraped`. Single
        atomic statement, no race, and it covers **all three** failure call sites
        at once.
    *   This matters because `ScrapeMenuWorker.Work` writes `StatusFailedScrape`
        in **three** places — extract error
        ([scrape.go:60](../../menusearch/scrape.go#L60)), no-items
        ([scrape.go:89](../../menusearch/scrape.go#L89)), and store error
        ([scrape.go:122](../../menusearch/scrape.go#L122)). The original plan only
        mentioned "upon a job error"; a per-call-site check would have to be
        repeated three times. The SQL guard handles them uniformly.
*   Optionally still log a warning in the worker when the row was already
    `scraped`, for observability (compare rows-affected or re-read after update).

## Cross-cutting risks & gaps (found during review)

These are not in the original plan but materially affect whether it works.

*   **Finding A — `/v1/webagent` mount is shadowed (HIGH, pre-existing).**
    [app.py:67,86](../../../scraper/src/scraper/app.py#L67) mounts `/v1` *before*
    `/v1/webagent` on the root app. Starlette matches mounts in order and `/v1`
    prefix-matches `/v1/webagent/...`, so requests are routed into the v1 sub-app
    (which lacks those routes) and **404**. Verified with a minimal Starlette repro;
    reversing the mount order (or mounting webagent inside the v1 app) fixes it.
    Implication: the **existing** `ScrapeJS` path
    ([service_extractor.go:435-444](../../scraper/service_extractor.go#L435))
    calling `/v1/webagent/scrape/{site}/{target}` is currently dead in the
    assembled app, and `test_webagent_app.py` doesn't catch it because it tests
    `create_webagent_app` directly, bypassing the mount. **Fix this regardless of
    this feature**, and add a root-app (`create_app`) test that hits `/v1/webagent`.
*   **Finding B — webagent endpoints are not behind bearer auth.**
    `BearerAuthMiddleware` is added only inside `_create_v1_app`
    ([app.py:109-110](../../../scraper/src/scraper/app.py#L109)); the separate
    `/v1/webagent` root mount never gets it (the code comment claiming it "shares
    the bearer auth on the v1 sub-app" is wrong). So once Finding A is fixed, the
    render endpoint would be unauthenticated unless webagent is mounted *inside*
    the v1 app. Mounting inside v1 fixes A and B together — that's why it's the
    recommended shape in Part 1.
*   **Finding C — SSRF (HIGH).** The render endpoint takes an arbitrary URL and
    drives a headless browser to it from inside the service network. With no guard
    it can reach cloud metadata (`169.254.169.254`), RFC-1918 hosts, `localhost`,
    and non-`http(s)` schemes. The Go code already has an SSRF guard for Tier-2 API
    inference (`isPrivateHost` / `ValidateAPIURL`,
    [scraper.go:323-375](../../scraper/scraper.go#L323)) — mirror that on the Python
    side: reject non-http(s) schemes and hosts that resolve to private/loopback/
    link-local before calling `page.goto`. (DNS-rebinding-safe resolution is ideal;
    at minimum block the literal ranges.)
*   **Finding D — timeout budget mismatch (HIGH).** A render can take up to
    `FETCH_HARD_CAP_MS = 75s` ([schema.py:8](../../../scraper/src/scraper/webagent/schema.py#L8)).
    But `ServiceExtractor.pageClient` is built with a **30s** timeout
    ([serve.go:182](../../cli/serve.go#L182)) — if `FetchRenderedHTML` uses
    `pageClient` it will abort before the browser finishes. Use `pdfClient` (120s)
    or a dedicated client sized to `hard_cap + margin` (~90s), and keep it under the
    worker's 5-min `Timeout` ([scrape.go:44-46](../../menusearch/scrape.go#L44)).
    Specify the client explicitly in the method.
*   **Finding E — retryable vs permanent errors.** The pool raises `BrowserBusy`
    (HTTP 503) when all workers are saturated
    ([pool.py:106-114](../../../scraper/src/scraper/webagent/fetch/pool.py#L106));
    `FetchTimeout` → 504, `WafBlocked` → 503. Under a batch of blocked sites these
    are *transient*. The Go side should treat 503/504 as retryable (let River
    retry — `MaxAttempts` is 8 for scrape jobs, see
    [menutracking/workers.go:61](../../menutracking/workers.go#L61)) rather than a
    terminal `failed_scrape`. `decodeServiceError` already preserves `statusCode`,
    and `IsBackendUnavailable` already special-cases 503 — reuse that classification.
*   **Finding F — bronze capture.** The worker writes `rawBody` to the bronze layer
    ([scrape.go:78-86](../../menusearch/scrape.go#L78)); the webagent JS path returns
    `nil` body so nothing is captured ([pipeline.go:20-22](../../pipeline/pipeline.go#L20)).
    The new fallback **should** return the rendered HTML as `bodyBytes` so bronze
    capture and the normal HTML→Markdown path both work — don't repeat the JS-path
    nil-body pattern here.
*   **Finding G — robots.txt / ToS (POLICY).** `HTTPFetcher.Fetch` honors robots.txt
    before the GET ([scraper.go:115-119](../../scraper/scraper.go#L115)); the fallback
    only fires *after* that check has already passed, so robots is still respected.
    But the feature's intent — a stealth browser to defeat WAF/Cloudflare — is a
    deliberate anti-bot-evasion step with ToS/legal implications. Make that an
    explicit, opt-in decision (e.g. gated behind a flag), and keep `stealth` tiers
    configurable.
*   **Finding H — efficacy expectation (MEDIUM).** Plain headless Chromium (even with
    `playwright-stealth`) is routinely detected by Cloudflare/DataDome. The 4 blocked
    sites may not all be solvable without heavier tiers (`patchright`/`camoufox`,
    listed as future work in [pool.py:45](../../../scraper/src/scraper/webagent/fetch/pool.py#L45)).
    Treat this as best-effort and measure the actual unblock rate before assuming all
    4 are recovered.
*   **Finding I — non-HTML blocked resources (LOW).** `page.content()` returns
    serialized HTML; a 403 on a **PDF** URL would render Chromium's PDF viewer, not
    usable bytes. Scope the fallback to HTML and let PDF-behind-403 keep failing
    (rare in the dataset).

## Context & Findings
An analysis of 36 failed restaurant scrapes revealed the following distribution of errors:
*   **Genuine Website Issues (20)**: Dead domains (10), HTTP 404 Not Found (9), TLS Certificate Errors (1). These must keep failing fast — do **not** route them to the rendered fetcher.
*   **Anti-Scraping Blocks (4)**: HTTP 403 Forbidden (3), HTTP 429 Rate Limited (1). The initial `net/http` fetch aborted on these, never reaching the Python webagent. (Target of Parts 1 & 2.)
*   **Old Configuration Errors (10)**: Missing Ollama/Qwen models (now resolved by switching the scraper to `gemini-3-flash-preview`).
*   **Edge Cases (2)**: 1 failed due to empty text layer, and 1 (Favela Grill) failed because the scraper successfully extracted 54 items from `/menus`, but a secondary job failed on the homepage `/`, overwriting the success status to `failed_scrape`. (Target of Part 3.)

## Suggested build order
1.  **Part 3** (SQL guard) — smallest, highest-confidence, independently shippable; immediately stops the regression.
2.  **Findings A + B** (fix `/v1/webagent` mount shadowing + put it behind bearer auth) — pre-existing bug; the existing `ScrapeJS` path is dead until this is fixed, and Part 1 depends on it. Add a `create_app`-level test hitting `/v1/webagent`.
3.  **Part 1 + Finding C + Finding D-server** (Python rendered-fetch endpoint with SSRF guard) — build/test in the `scraper` repo on its own.
4.  **Part 2 + Findings D-client/E/F** (Go typed error, fallback wiring, correct timeout client, retryable-error classification, bronze capture) — depends on Part 1's contract.

Track Findings G (policy gate) and H (efficacy measurement) as decisions/acceptance criteria rather than code steps.
