---
name: debug-scrape
description: Diagnose why the menu scrape pipeline fails or produces junk for a restaurant/URL. Use when a site "can't be scraped", a restaurant shows failed_scrape, item_count looks wrong (1-3 items), or a known menu (Grubhub, dine.online, ordering SPA) isn't being captured. Walks DB triage → URL probing → webagent render testing → known failure patterns → fix + rescrape + anti-hallucination verification.
---

# Debug a Failing Menu Scrape

Systematic diagnosis for "the pipeline can't scrape X". Work the layers in
order — most failures are in **discovery** (wrong/dead URLs stored), not in
rendering. Postgres runs in docker (`fodmap-detector-postgres-1`, user/db
`fodmap`); the Python scraper service is at `localhost:8765` (repo `../scraper`).

## 1. Pull the DB row first

```bash
docker exec fodmap-detector-postgres-1 psql -U fodmap -d fodmap -Atc \
  "SELECT camis, dba, status, website_url, array_to_string(menu_urls, E'\n  '),
          url_source, extraction_tier, item_count, last_error, scraped_at
   FROM restaurants WHERE dba ILIKE '%<name>%' OR camis = '<camis>'"
```

Junk-result smells (a "scraped" status can still be wrong):
- `extraction_tier = image_ocr` with `item_count` 1–3 → OCR'd a decorative
  homepage photo, not a menu. The real menu is elsewhere.
- `menu_urls` holds only the homepage + guessed `/menu`, `/menus` paths →
  discovery never found the real menu (often an external ordering SPA).
- `last_error = "no menu items found"` on every attempt → probe whether the
  stored URL is even alive (step 2).
- `last_error` containing `refusing LLM call (hallucination risk)` → JS shell
  that could not be rendered; check the service is up with the webagent
  enabled and the server runs with `--extractor-url`.

## 2. Probe each stored URL

```bash
curl -sL --max-time 20 -A "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/126.0.0.0 Safari/537.36" \
  -o /tmp/page.html -w "status=%{http_code} size=%{size_download} final=%{url_effective}\n" "<url>"
python3 -c "
import re,html
raw=open('/tmp/page.html').read()
txt=re.sub(r'<script.*?</script>|<style.*?</style>|<noscript.*?</noscript>','',raw,flags=re.S)
txt=html.unescape(re.sub(r'<[^>]+>',' ',txt)); txt=re.sub(r'\s+',' ',txt).strip()
print('visible:',len(txt),'| prices:',len(re.findall(r'\\\$\d+',txt)),
      '| scripts:',raw.count('<script'),'| ld+json:',raw.count('application/ld+json'))
print(txt[:300])"
```

Classify:
- **404/410** → dead URL. Discovery now drops these (`checkMenuSignal`), but
  old rows may still hold them. A dead "direct" URL blocks the
  delivery-platform fallback — replace it (step 5).
- **200, tiny visible text (<60), scripts present** → JS shell. `IsJSShell`
  flags this (trivial rule) and the pipeline pre-renders. If items are still
  missing, test the render directly (step 4).
- **200 with real text + prices** → fetch is fine; the problem is extraction
  (check cascade logs / trafilatura noise / `<button>`-wrapped content).
- **403/429** → bot wall; the rendered-fetch fallback should engage.

Note: strip `noscript` when measuring — a "please enable JavaScript" block
inflates the visible count; the Go converter never sees it.

## 3. Find where the menu actually lives

The homepage's static HTML usually links the real menu even when discovery
missed it:

```bash
curl -sL "<website_url>" | grep -oE 'href="https?://[^"]+"' | sort -u \
  | grep -viE 'facebook|instagram|parastorage|static\.' | head -20
```

Look for ordering platforms: `dine.online`, `order.store`, `toasttab.com`,
`square.site`, `popmenu.com`, `chownow.com`, plus delivery (`grubhub.com`,
`seamless.com`). Discovery harvests allowlisted hosts automatically
(`harvestOrderingLinks` / `orderingPlatformHosts` in `menusearch/discover.go`)
— if the platform you found is missing from the allowlist, **add it there**
(with an `isOrderingPlatform` test), or its URL gets dropped as
"no menu signal on 2xx GET".

## 4. Test the webagent render directly

```bash
curl -s --max-time 3 http://localhost:8765/healthz   # service up?
curl -s --max-time 150 -X POST http://localhost:8765/v1/webagent/fetch \
  -H "Content-Type: application/json" \
  -d '{"url":"<url>","network_idle":true,"scroll":true}' -o /tmp/render.json \
  -w "status=%{http_code} time=%{time_total}s\n"
python3 -c "
import json,re,html as H
d=json.load(open('/tmp/render.json')); raw=d.get('html','')
if not raw: print('error:', d); raise SystemExit
txt=re.sub(r'<script.*?</script>|<style.*?</style>|<noscript.*?</noscript>','',raw,flags=re.S)
txt=H.unescape(re.sub(r'<[^>]+>',' ',txt)); txt=re.sub(r'\s+',' ',txt).strip()
print('bytes:',len(raw),'| visible:',len(txt),'| prices:',len(re.findall(r'\\\$\d+\.\d{2}',txt)))
print('captcha:', bool(re.search('perimeterx|px-captcha|press & hold',raw,re.I)))"
```

Interpretation:
- **Few items but section titles present** → lazy-load and/or a virtualized
  list (react-window unmounts off-screen rows — scrolling can *reduce* DOM
  items). `scroll:true` counters both via a tall viewport (15000px, everything
  "in view") + a wheel pass. The pipeline's shell pre-render/re-cascade always
  sets it.
- **`browser job did not complete within Ns`** → pool result-timeout killed
  the job; historically caused by a networkidle wait consuming the whole
  budget on chatty SPAs (fixed: margin reserved, pool timeout > render cap).
- **Uncaught `Timeout Nms exceeded` (no `goto timeout:` prefix)** → an engine
  exception-class mismatch: patchright raises its own exception types, which
  vanilla `playwright.sync_api` except-clauses don't match. Catch
  `PW_ERRORS` / `PW_TIMEOUTS` from `scraper/webagent/fetch/pwerrors.py`.
- **Stealth check**: the pool prefers patchright > camoufox > chromium but the
  extras are opt-in — `uv run python -c "import patchright"`; install with
  `uv sync --extra patchright && uv run patchright install chromium`.

## 5. Fix the data and rescrape

```bash
# Point the row at the real menu URL (strip tracking params: utm_*, rwg_token…)
docker exec fodmap-detector-postgres-1 psql -U fodmap -d fodmap -Atc \
  "UPDATE restaurants SET menu_urls = ARRAY['<canonical-url>'], url_source='manual',
   last_error=NULL WHERE camis='<camis>'"

# Inline end-to-end scrape (no River worker needed); services: postgres,
# scraper on 8765, vllm on 8000, ollama embedder on 11434
go run . scrape "<url>" --camis <camis> --menu-store postgres \
  --postgres-dsn "postgres://fodmap:fodmap@localhost:5432/fodmap" \
  --extractor-url http://localhost:8765
```

Watch the log for the expected path: `JS shell with trivial static text` →
`rendered-fetch: done` → item count. `Tier 1: sending to LLM extractor
chars=1` (trivial input reaching the LLM) must never happen — that's the
hallucination hole; the refusal floor should error instead.

## 6. Verify — always run the anti-hallucination check

The LLM can invent a plausible menu (observed: 1 rune in → 34 fake items for
the wrong restaurant). After any rescrape, spot-check stored dishes against
the *rendered* page:

```bash
docker exec fodmap-detector-postgres-1 psql -U fodmap -d fodmap -Atc \
  "SELECT dish_name FROM menu_items m JOIN restaurants r ON m.business_id=r.id
   WHERE r.camis='<camis>' ORDER BY random() LIMIT 8"
python3 -c "
import json
raw=json.load(open('/tmp/render.json'))['html']
for n in [<names>]: print('FOUND ' if n in raw else 'MISSING', n)"
```

All names must appear verbatim (whitespace may be normalized — check the raw
HTML before declaring a miss). Also sanity-check the extracted
`restaurant_name` matches, and delete junk rows before the row's
`item_count`/`extraction_tier`/`scraped_at` are updated.

## Known failure patterns (fastest lookup)

| Symptom | Root cause | Fix location |
|---|---|---|
| 1–3 items via `image_ocr` | Real menu on external ordering SPA; discovery stored homepage | Allowlist + harvest in `menusearch/discover.go`; repoint `menu_urls` |
| Ordering URL never stored | Host missing from `orderingPlatformHosts` (JS shell fails menu-signal GET) | Add host + test |
| "no menu items found" forever | Stored URL is 404-dead | 404/410 drop in `checkMenuSignal`; repoint row |
| Fake menu, wrong restaurant | Trivial text sent to LLM | Refusal floor (`minExtractRunes`), shell pre-render in `pipeline/pipeline.go` |
| Menu present but items missing after render | Lazy-load / virtualized list | `scroll:true` (tall viewport) in webagent fetch |
| Items in DOM but not in markdown | Content inside `<button>` (skipped historically) | `ConvertHTMLToMarkdown` keeps button text as list items |
| Render 502 at exactly the pool timeout | networkidle never settles / timeout mismatch | Margin + `_RESULT_TIMEOUT_S` in `../scraper` fetch |
| goto timeout crashes render (patchright) | Engine exception classes differ from playwright's | `pwerrors.py` tuples |

See also: `docs/guides/scrape-diagnostics.md`, `docs/guides/troubleshooting.md`
§8 (JS shells) and §11 (ordering-platform SPAs).
