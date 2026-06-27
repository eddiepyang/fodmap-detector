# Menu Coverage Report — Astoria+LIC Spike (n=40, 2026-06-27)

> Companion to [`../plans/menu-coverage-spike-plan.md`](../plans/menu-coverage-spike-plan.md).
> The spike is **complete**; this is the measured result that sizes the
> discovery pipeline and the interactive-agent decision.

## TL;DR

- **72.5% of reachable sites need JS** (55% deeplink-render + 17.5% interaction).
- The detector's **generic render-and-re-cascade (Phase 3, shipped)** already
  covers the 55% deeplink slice — no adapter, no agent.
- The interactive agent is **green-lit for the 17.5% interaction-required
  tail** (just over the 15% threshold) — menus behind clicks/tabs/location
  that a passive render can't reach.
- **Platform templates** are the highest coverage-per-effort for the deeplink
  slice (WordPress 15% of the sample), but platform detection needs
  refinement (19 "other/unknown") before templates can target by platform.

## Sample

40 restaurants drawn from the NYC DOHMH Socrata dataset (`43nn-pn8j`), filtered
to Astoria+LIC NTAs (QN70/71/72/68 + QN31 restricted to 11101/11109),
stratified round-robin across the 40 distinct cuisines present. Discovery via
Gemini GoogleSearch (`gemini-2.5-flash`); extraction via
`gemini-3-flash-preview` (cloud, for speed — the local vLLM qwen3-vl is
~120s/image and too slow for a 40-site spike); JS render via
`ChromeRenderedFetcher` (chromedp + headless Chrome). Read-only — no Weaviate,
no Python service, no indexing.

## Classification histogram

| Class | Count | % | Meaning |
|---|---|---|---|
| `js-text-deeplink` | 22 | 55.0% | menu is JS-rendered text on a reachable URL → generic render covers it |
| `no-site` | 6 | 15.0% | no website found (delivery/social only, or discovery miss) |
| `interaction-required` | 7 | 17.5% | menu behind clicks/tabs/location → needs the agent / `fetch.steps` |
| `no-menu` | 3 | 7.5% | site exists, no online menu (corporate/landing) |
| `image-menu` | 2 | 5.0% | menu is an image → `FindMenuImage` → `ExtractImage` |
| `delivery-only` | 0 | 0.0% | (not separately detected this run) |

## Platform histogram

| Platform | Count |
|---|---|
| other/unknown | 19 |
| WordPress | 6 |
| *(blank — unfetchable)* | 9 |
| Next.js | 2 |
| Shopify | 2 |
| Squarespace | 1 |
| Wix | 1 |

WordPress is the only platform with meaningful concentration (15% of the
sample). The 19 "other/unknown" + 9 blank show platform detection undercounts
— a second pass with CDN-host sniffing on the rendered DOM would tighten this.

## `FindMenuImage` precision

| Signal | Count |
|---|---|
| Candidates surfaced | 57 |
| Guard fired (0 items — "IS THIS A MENU?" rejected the candidate) | 6 |
| Real image menus (image path yielded items) | 2 |
| **Precision** | 2/57 ≈ **3.5%** |

The low heuristic precision is **by design**: `FindMenuImage` is permissive
(surfaces anything plausibly menu-shaped), and the prompt guard is the safety
net (6 fired, leaving 2 true positives). The vision-gaps plan's
"post-extraction validation is the real safety net" holds — the guard, not the
heuristic, filters. Recall is 2/2 on this sample (both real image menus were
found), but n=2 is too small to trust.

## Decision

`interaction-required` is **17.5%** (just over the 15% threshold) → **GO** on
the interactive agent, scoped to that slice. But the priority order, given
that the generic render path already ships:

1. **Generic JS render (shipped, Phase 3)** — covers the 55% deeplink slice.
   Highest coverage, zero new build cost.
2. **Interactive agent** ([`webagent-interactive-agent-plan.md`](../../../scraper/docs/plans/webagent-interactive-agent-plan.md))
   — covers the 17.5% interaction tail. Build Phase 1 (step executor) + Phase 2
   (agent) scoped to interaction-required sites.
3. **Platform templates** — author a handful of adapters for the dominant
   builders (WordPress first) for the deeplink slice; highest
   coverage-per-effort *if* platform detection improves. Exercise the step
   executor.

## Caveats

- **n=40 is small.** Per-class error bars are ±~15%. Re-run on 100+ before
  committing build budget to the agent.
- **Discovery non-determinism.** Gemini returned different URLs across runs on
  the same restaurants; some `no-site`/`no-menu` rows are discovery misses.
- **Extraction backend swap.** Cloud `gemini-3-flash-preview` was used for
  speed, not the production local `qwen3-vl` (which has the tuned
  anti-fabrication prompt). Item counts are reachability signals, not
  faithfulness verdicts.
- **Platform detection undercounts.** 19 "other/unknown" + 9 blank mean the
  platform histogram understates template concentration.

## Harness

`scripts/menuscan/main.go` — run with:

```sh
go run ./scripts/menuscan --sample /tmp/spike_sample.json --v --timeout 60m
```

Emits `scripts/menuscan_results.json` (per-restaurant rows) + the histogram
above. Read-only. See the plan for the taxonomy definitions.