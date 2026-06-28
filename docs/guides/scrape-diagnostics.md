# Scrape Diagnostics Guide

How to follow a menu scrape from outcome back to root cause, using the database,
the bronze/silver data layers, the cascade logs, and a couple of replay scripts.
This is the exact trail used to triage the 36-failure batch behind
[anti-scraping-bypass-plan.md](../plans/anti-scraping-bypass-plan.md) and to
measure the extraction-tier mix.

The scrape cascade itself is described in
[pipeline-architecture.md](pipeline-architecture.md); this guide is the
operational counterpart — *how to inspect what actually happened*.

> `psql` is generally **not installed on the host**. Query Postgres through the
> running container instead (note the `-e PGPASSWORD` and `-tA` for clean,
> tab-separated, unaligned output):
>
> ```bash
> PSQL="docker exec -e PGPASSWORD=fodmap fodmap-detector-postgres-1 psql -U fodmap -d fodmap -tA"
> ```

---

## 1. Outcome overview (Postgres)

Start with the status distribution across all restaurants. `status` is one of
`pending_discovery | url_found | scraping | scraped | failed_scrape | no_url_found`
(see [menusearch/restaurant.go](../../menusearch/restaurant.go)).

```bash
$PSQL -c "SELECT status, count(*), COALESCE(sum(item_count),0) AS items
          FROM restaurants GROUP BY status ORDER BY count(*) DESC;"
```

How many items successful scrapes actually yielded (a long tail of `1-9` often
signals partial extraction, not a healthy menu):

```bash
$PSQL -c "SELECT CASE WHEN item_count=0 THEN '0'
                      WHEN item_count<10 THEN '1-9'
                      WHEN item_count<30 THEN '10-29'
                      ELSE '30+' END AS bucket, count(*)
          FROM restaurants WHERE status='scraped' GROUP BY 1 ORDER BY 1;"
```

## 2. Drill into failures (`last_error`)

Each failed scrape stores the wrapped error string. Grouping it bucketizes the
failure modes (dead domain, HTTP 404/403/429, TLS, "no menu items found", …) —
this is what produced the failure taxonomy in the plan's Findings.

```bash
# Top failure messages
$PSQL -c "SELECT last_error, count(*) FROM restaurants
          WHERE status='failed_scrape' GROUP BY last_error ORDER BY count(*) DESC;"

# Everything about one restaurant
$PSQL -c "SELECT camis, dba, status, item_count, extraction_tier, website_url, last_error
          FROM restaurants WHERE camis='50044186';"
```

Anti-scraping blocks specifically (the target of the bypass plan):

```bash
$PSQL -c "SELECT camis, dba, last_error FROM restaurants
          WHERE status='failed_scrape'
            AND (last_error LIKE '%status 403%' OR last_error LIKE '%status 429%');"
```

## 3. Tier mix (`extraction_tier`)

Which cascade tier produced each success is persisted per scrape (see
[pipeline.Tier* constants](../../pipeline/pipeline.go)):
`jsonld` (pure-Go, no LLM) · `html_llm` · `pdf` · `image_ocr` · `webagent`.

```bash
$PSQL -c "SELECT COALESCE(extraction_tier,'(none)') AS tier,
                 count(*), COALESCE(sum(item_count),0) AS items
          FROM restaurants WHERE status='scraped' GROUP BY 1 ORDER BY 2 DESC;"
```

A low `jsonld` share means the Go-only path carries little unique load and most
extractions already route through the Python LLM/OCR paths — the signal for
whether to consolidate the cascade into Python.

## 4. Bronze layer — the raw fetched bytes

The worker writes the raw response body (HTML, or PDF bytes with an `.html`
extension) to the bronze layer, best-effort, keyed by date and attempt
([menusearch/scrape.go](../../menusearch/scrape.go)):

```
data/bronze/restaurants/<YYYY-MM-DD>/<CAMIS>-<attempt>.html
```

Inspect what was actually fetched (vs. what the LLM saw):

```bash
find data/bronze/restaurants -name '*.html' -printf '%s\t%p\n' | sort -n

# Does a page carry schema.org menu markup at all? (rough proxy)
for f in $(find data/bronze/restaurants -name '*.html'); do
  echo "$f ldjson=$(grep -c 'application/ld+json' "$f") \
menuType=$(grep -ciE '"@type"\s*:\s*"(Menu|MenuItem|MenuSection)"' "$f")"
done
```

Multiple `<CAMIS>-N.html` files for one CAMIS = multiple scrape attempts/URLs for
the same restaurant — the exact condition behind the "a later failed job
overwrote a successful one" bug (plan Part 3).

## 5. Replay the Tier-0 detector (`jsonld_probe`)

`grep` only tells you markup is *present*. To know what the production detector
would actually extract, replay `scraper.ExtractJSONLD` over the bronze HTML with
[scripts/jsonld_probe](../../scripts/jsonld_probe):

```bash
go run ./scripts/jsonld_probe                       # defaults to data/bronze/restaurants
go run ./scripts/jsonld_probe --dir /some/other/dir
```

It reports a HIT/miss per distinct restaurant and an overall hit rate — the
precise, no-LLM Tier-0 coverage number.

## 6. Silver layer — structured Avro records

Post-extraction results are written to silver as Avro
([menusearch/avro.go](../../menusearch/avro.go),
[data/schemas/schemas.go](../../data/schemas/schemas.go)):

```
data/silver/menus/<CAMIS>-<attempt>.avro
```

These now carry `extraction_tier`. Verify the contract round-trips and that old
files (written before the field existed) still decode, with
[scripts/avro_tier_check](../../scripts/avro_tier_check):

```bash
go run ./scripts/avro_tier_check
```

## 7. Cascade logs

The pipeline emits a structured `slog` line at each tier decision
([pipeline/pipeline.go](../../pipeline/pipeline.go)), so a single job's path is
readable end-to-end. Key markers:

| Log message | Tier reached |
| --- | --- |
| `scraping URL` | fetch start (every job) |
| `Tier 0: JSON-LD menu found` | `jsonld` — served in Go, no LLM |
| `Tier 1: sending to LLM extractor` | `html_llm` (or PDF text → LLM) |
| `HTML→Markdown output is noisy, falling back to trafilatura` | boilerplate-heavy page |
| `HTML too noisy; routing to webagent` | `webagent` (JS render) |
| `routing to menu image OCR` | `image_ocr` |
| `scrape successful` (now includes `tier=`) | terminal success |

Filter a run to one restaurant and watch the cascade:

```bash
# if logs are captured to a file
grep '"camis":"50044186"' scrape.log | jq -r '"\(.time) \(.msg) \(.tier // "")"'
```

## 8. Cross-service correlation (Go ↔ Python)

When the Go side calls the Python scraper service, a non-2xx response is wrapped
as a `serviceError` carrying the Python `request_id`
([scraper/service_extractor.go](../../scraper/service_extractor.go)). The error
string looks like:

```
service 502 structuring_failed: ... (request_id=abc123)
```

Grep the Python service logs for that `request_id` to join the two sides of a
single extraction across the service boundary.

---

## Quick reference

| Question | Where to look |
| --- | --- |
| How many scrapes succeeded/failed? | §1 status query |
| Why did *this* one fail? | §2 `last_error` |
| Which tier handled the successes? | §3 `extraction_tier` |
| What HTML did we actually fetch? | §4 bronze |
| Would JSON-LD alone have worked? | §5 `jsonld_probe` |
| What structured data was stored? | §6 silver Avro |
| What path did the job take? | §7 cascade logs |
| Where did it break across services? | §8 `request_id` |
