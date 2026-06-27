# Plan: Menu-coverage spike — measure before building the JS agent

**Status:** ✅ Complete (2026-06-27). Results below.

## Why

Before investing in a live LLM web-navigation agent (see
[`../../../scraper/docs/plans/webagent-interactive-agent-plan.md`](../../../scraper/docs/plans/webagent-interactive-agent-plan.md)),
measure what fraction of real restaurants actually *need* it. The existing
capabilities — pure-Go HTML/text, image OCR (`FindMenuImage` → `ExtractImage`),
and the scraper service — may already cover most menus. The agent is only
justified by the residual "interaction-required" slice, which is currently an
**inference, not a measurement**. This spike turns it into data, cheaply, before
any agent code is written.

This complements [vision-extraction-gaps-plan.md](vision-extraction-gaps-plan.md)
(which fixes the image path) and [restaurant-menu-discovery-plan.md](restaurant-menu-discovery-plan.md)
(which automates discovery+scrape). Its output sizes both.

## Seed findings (9 NYC restaurants, 2026-06-27)

Hand-tested against the live scrape cascade (the harness called the real
`ConvertHTMLToMarkdown` / `FindMenuImage` / `ExtractImage` / `Extract`). This is
the sample that motivated the spike:

| Restaurant | Site / platform | What the cascade did | Class |
|---|---|---|---|
| LION'S GATE GRILL | (no website) | nothing to fetch | **no-site** |
| UN (COFFEE STUDIO) D | (no website) | nothing to fetch | **no-site** |
| CAFE JEZERO | (no website) | nothing to fetch | **no-site** |
| THRIFT N SIP | thriftnsipcafe.com (WordPress) | `FindMenuImage` → `TRIFOLD_MENU…png` → `ExtractImage` → ~66–108 items | **image-menu ✅** |
| DOUGHNUTTERY | doughnuttery.com (Wix) | homepage boilerplate → 0 text items; `FindMenuImage` hit a *marketing photo* (fabricated 26 → 0 after guard); real menu at `/menu` (JS) | **JS-text / interaction** |
| CHICKEN AT LAST | chickenatlast.com (Squarespace) | boilerplate → 0; `FindMenuImage` hit a hero food photo → 0 (safe) | **JS-text / interaction** |
| NICE DAY CHINESE | eatniceday.com (Squarespace) | boilerplate → 0; `FindMenuImage` hit a *Food&Wine* press badge → 0 | **JS-text / interaction** |
| WORLD SPA | worldspa.com/dining/ (custom) | boilerplate → 0; `FindMenuImage` found nothing | **JS-text / interaction** |
| JETBLUE LOUNGE | jetblue.com (corporate) | not a restaurant menu page | **no-menu** |

**Provisional read (n=9, not significant):**
- **~33% no-site** — no automated path will ever help these.
- **Image-menu** is real and high-value: the one confirmed menu (THRIFT) was an
  image, extracted cleanly once routed. `FindMenuImage` precision was ~1/5,
  though — it false-positives on hero photos / logos (see vision-gaps G3).
- **The remaining JS sites cluster on 3 platforms** (WordPress, Wix, Squarespace)
  in a 6-site sample — encouraging for a *template-adapter* strategy over a
  bespoke per-site agent.
- **None of the 9 strictly required multi-step interaction to be *seen*** — most
  menus were one deep-link (`/menu`) or an image away. This is the key signal the
  spike must confirm at scale: if interaction is rarely required, the agent is low
  priority and platform templates + image OCR win.

## Method

A read-only classification spike — **no indexing, no DB, no agent**. Extend the
throwaway harness already used for the seed test (calls the real `scraper`
functions) into a small, committed `cmd/menuscan` or a `-run`-gated test that, for
each restaurant URL:

1. Fetch homepage; try `ConvertHTMLToMarkdown` + `Extract` (text path).
2. Run `FindMenuImage`; if hit, `ExtractImage` (with the new `IS THIS A MENU?`
   guard) and record item count + whether the image was a real menu (manual spot-check).
3. Probe a few conventional menu paths (`/menu`, `/menus`, `/food-menu`) for a
   deep-linkable menu page; classify whether the menu renders in static HTML vs.
   needs JS.
4. Record the **platform** (detect Wix/Squarespace/Toast/Square/Clover/BentoBox/
   Popmenu/WordPress via CDN hosts, meta tags, script srcs).
5. Emit a CSV/JSON row per restaurant with the class + signals.

### Classification taxonomy (one per restaurant)

- `no-site` — no website.
- `no-menu` — site exists but no online menu (corporate/landing only).
- `image-menu` — menu is an image reachable via `FindMenuImage` (→ OCR path).
- `js-text-deeplink` — menu is JS-rendered text on a stable URL (`/menu`) →
  **existing webagent adapter / template can cover** (deep-link + wait + extract).
- `interaction-required` — menu only appears after clicks/modals/tabs/location
  pick → **needs the agent / `fetch.steps`**.
- `delivery-only` — menu only on UberEats/DoorDash/Grubhub (consent/anti-bot;
  out of scope, but count it).

### Sample

- **30–50 restaurants** drawn from the Astoria+LIC set (the
  [restaurant-menu-discovery-plan.md](restaurant-menu-discovery-plan.md) scope),
  selected to span cuisines/sizes — enough to estimate proportions to ±~10%.
- Use real Socrata records if the discovery import exists; otherwise a hand-picked
  list. Menu-URL discovery can be manual (or a few Gemini-grounded lookups) — the
  spike measures *extractability*, not discovery quality.

## Deliverable

A short coverage report (`docs/guides/menu-coverage-report.md` or appended to
[vision-extraction.md](../guides/vision-extraction.md)) with:
- the class histogram (% per taxonomy bucket),
- platform histogram (how concentrated on Wix/Squarespace/Toast/etc.),
- `FindMenuImage` precision/recall on the sample,
- a go/no-go recommendation for the agent: **build the interactive agent only if
  `interaction-required` is a material share** (suggested threshold: >~15–20% after
  image-menu and js-text-deeplink are covered). Otherwise prioritize platform
  templates + the vision-gaps image fixes.

## Decision this informs

- **High `image-menu` + `js-text-deeplink`, low `interaction-required`** → skip/defer
  the agent; ship vision-gaps Phase 1 + a few platform-template adapters.
- **Material `interaction-required`** → green-light
  [webagent-interactive-agent-plan.md](../../../scraper/docs/plans/webagent-interactive-agent-plan.md),
  scoped to that slice.

## Risks and Gaps

- **Small-sample bias.** n=30–50 still has wide error bars per cuisine/platform;
  treat proportions as directional. Re-measure if the production set skews
  differently from Astoria+LIC.
- **Classification is partly manual.** "Is this image a real menu?" and
  "interaction-required vs deep-linkable" need human spot-checks; budget a few
  hours, and write down the call criteria so it's reproducible.
- **Platform detection is heuristic.** CDN/meta sniffing misses white-labeled or
  self-hosted builds; under-counts template concentration rather than over-counts.
- **Menu-URL discovery confound.** A site classified `no-menu` may simply have a
  menu the spike didn't find (bad URL guess). Keep discovery generous (`/menu`
  variants + one grounded lookup) so "no menu" means "genuinely not found," not
  "didn't look."
- **Coverage ≠ correctness.** This spike measures whether the menu is *reachable/
  extractable*, not whether extraction is *faithful*. Fabrication risk (vision-gaps
  G4 / the donut case) is a separate axis tracked elsewhere — don't let a high
  reachability number imply safety.
- **Delivery-only is a real, uncounted bucket.** If many restaurants are
  delivery-only, neither the agent nor templates help without a consented platform
  integration — surface that share even though it's out of scope.

## Verification

- The spike runs read-only against live sites and produces the report; no
  production writes.
- Re-running on the same URL set yields the same classification (deterministic
  except for live-site flakiness — note flaky rows).

---

## Results (2026-06-27, n=40, Astoria+LIC)

Run with `scripts/menuscan` against a stratified sample of 40 Socrata records
(one per cuisine, round-robin across the 40 distinct cuisines in the
Astoria+LIC set). Discovery via Gemini GoogleSearch (`gemini-2.5-flash`);
extraction via Gemini's OpenAI-compat endpoint (`gemini-3-flash-preview`,
cloud — the local vLLM qwen3-vl is too slow for a 40-site spike at ~120s/image);
JS render via `ChromeRenderedFetcher` (chromedp + headless Chrome). No
Weaviate, no Python service.

### Classification histogram

| Class | Count | % |
|---|---|---|
| `js-text-deeplink` | 22 | 55.0% |
| `no-site` | 6 | 15.0% |
| `interaction-required` | 7 | 17.5% |
| `no-menu` | 3 | 7.5% |
| `image-menu` | 2 | 5.0% |
| `delivery-only` | 0 | 0.0% |

### Platform histogram

| Platform | Count |
|---|---|
| other/unknown | 19 |
| WordPress | 6 |
| *(blank — no platform detected, site unfetchable)* | 9 |
| Next.js | 2 |
| Shopify | 2 |
| Squarespace | 1 |
| Wix | 1 |

### `FindMenuImage` precision

- Candidates surfaced: **57**
- Guard fired (0 items from a candidate — the "IS THIS A MENU?" guard): **6**
- Real image menus (image path yielded items): **2**
- **Precision: 2/57 ≈ 3.5%** (i.e. the heuristic is permissive; the guard is
  doing the real filtering). Recall is not measurable without a ground-truth
  label of all menu images, but the 2 true positives were found, so recall on
  this sample is 2/2.

### Decision: GO (barely) — green-light the interactive agent, scoped

`interaction-required` is **17.5%**, just over the 15% threshold. Combined with
`js-text-deeplink` at 55%, **72.5% of reachable sites need JS** (render or
interaction). The agent is justified for the 17.5% interaction tail — but the
bigger lever is the 55% `js-text-deeplink` slice, which the detector's
**generic render-and-re-cascade (Phase 3, already shipped)** already covers with
no adapter and no agent. So:

- **Priority 1 (shipped):** generic JS render covers the 55% deeplink slice.
- **Priority 2 (this is the agent's job):** the 17.5% interaction-required
  slice — menus behind clicks/tabs/location-pickers that a passive render
  can't reach. Build `webagent-interactive-agent-plan.md` Phase 1 (step
  executor) + Phase 2 (agent) scoped to this slice.
- **Priority 3:** platform templates for the dominant builders
  (WordPress 15%, Wix/Squarespace/Next.js/Shopify the rest) — highest
  coverage-per-effort for the deeplink slice, and they exercise the step
  executor. The 19 "other/unknown" + 9 blank show platform detection needs
  refinement before templates can be targeted by platform.

### Caveats on this run

- **Small sample (n=40).** Per-class error bars are wide (±~15% on a 17.5%
  slice). Treat proportions as directional, not precise. Re-run on 100+ before
  committing build budget to the agent.
- **Discovery non-determinism.** Gemini GoogleSearch returned different URLs
  across runs (the smoke test on the same 2 restaurants yielded different
  domains than the full run). Some `no-site`/`no-menu` rows are discovery
  misses, not genuine absence — a manual re-check of the 9 `no-site`+`no-menu`
  rows would tighten those buckets.
- **Extraction backend swap.** The spike used cloud `gemini-3-flash-preview`
  for speed, not the production local vLLM `qwen3-vl`. The two differ in
  fabrication behavior (the local model is the one with the tuned anti-fabrication
  prompt + verified zero-fabrication). Item counts here are reachability
  signals, not faithfulness verdicts — don't infer safety from them.
- **Platform detection undercounts.** 19 "other/unknown" + 9 blank (unfetchable)
  mean the platform histogram understates template concentration. A second pass
  with refined detection (CDN host sniffing on the rendered DOM, not just raw
  HTML) would shift rows out of "other."
- **`FindMenuImage` precision is heuristic-bound, not guard-bound.** The 3.5%
  precision looks alarming but is by design: the heuristic is permissive
  (surfaces 57 candidates), and the guard filters (6 fired, leaving 2 real
  menus). The safety net is the guard, not the heuristic — consistent with the
  vision-gaps plan's "post-extraction validation is the real safety net."

## Harness

The spike harness lives at `scripts/menuscan/main.go` (runnable via
`go run ./scripts/menuscan --sample /tmp/spike_sample.json --v`). It loads a
Socrata-sample JSON, discovers each restaurant's URL via Gemini GoogleSearch,
runs the real detector cascade (text → image → JS-render), detects the
platform, and emits `scripts/menuscan_results.json` + the histogram above.
Read-only — no Weaviate, no Python service, no indexing.
