# Vision Menu Extraction — Findings & Design Notes

> **Scope:** How the vision (PDF/image) menu-extraction path behaves in practice,
> why the prompt and model defaults are what they are, and what an external VLM
> benchmark teaches us (and where it doesn't transfer). Companion to
> [llm-serving.md](llm-serving.md) (how to serve the model) and
> [cli-reference.md](cli-reference.md) (the `scrape` flags).

## TL;DR

- **Serve an 8-bit Qwen3-VL-8B, not 4-bit.** On a real dense menu, 4-bit
  fabricated ingredients and misread the restaurant name; 8-bit did not.
- **Structured output must be schema-enforced.** Use vLLM / vllm-metal
  (`response_format: json_schema` enforced via xgrammar), not Ollama's MLX
  engine (silently ignored). See [llm-serving.md](llm-serving.md).
- **The hard part is semantic, not OCR.** The model reads the pixels fine; the
  failure mode is mis-*structuring* — copying a neighboring item's name into a
  dish's `stated_ingredients`. That is a FODMAP-critical risk and is handled
  by prompt rules, not by a bigger model alone.
- **A non-menu image can hallucinate a whole menu.** Handed a branded *product
  photo* (no readable menu text), the model invented a plausible item list. The
  prompt now opens with an "IS THIS A MENU?" guard that returns zero items unless
  legible menu text is present. See [Real-world test](#real-world-test-9-nyc-restaurants).
- The vision prompt lives in
  [`scraper/scrape-prompt-vision.txt`](../../scraper/scrape-prompt-vision.txt)
  (used by `ExtractImage`); the text prompt in
  [`scraper/scrape-prompt.txt`](../../scraper/scrape-prompt.txt).

## Empirical finding: 8-bit vs 4-bit (Qwen3-VL-8B, MLX, on vllm-metal)

Tested against one dense full-page café menu (coffee/tea/bubble-tea/waffle/dessert
sections, mostly bare item names + prices, almost no per-item ingredient prose).
Same prompt and same `response_format: json_schema` both runs; temp 0.

| | 4-bit (`...-4bit`) | 8-bit (`...-8bit`) |
|---|---|---|
| Restaurant name | ❌ "Matcha & Ube" (grabbed a section header) | ✅ "Unknown Restaurant" (correct — the menu has no name) |
| Items captured | 78 | 84–85 |
| Fabricated `stated_ingredients` | several, ungrounded | **0** (with the prompt rules below) |
| Latency | ~100 s | ~170 s per full-page menu on an M2 |

Takeaways:
- 4-bit's failures were *structural* (fabricated content, header-as-name), not a
  small accuracy delta. 8-bit is worth the ~1.7× latency for one-off scrapes.
- `temp=0` is deterministic here — repeated runs were byte-identical.

## Prompt design rationale

The single biggest quality lever was **not** the model — it was three rules that
stop the model from inventing ingredients on option-style menus:

1. **Anti-cross-attribution.** "A nearby list of OTHER menu items, flavors,
   toppings, syrups, sizes, or a section header is NOT a list of ingredients."
   Without this, the model copied adjacent item names into `stated_ingredients`.
2. **Sibling-list rule.** "Items separated by dots, commas, bullets, or line
   breaks (e.g. `Earl Grey Black Tea · Jasmine Green Tea`) are SIBLING items …
   NOT ingredients of one another." This killed the last residual fabrication on
   dotted `A · B · C` lists.
3. **Empty-is-normal.** "If a dish is just a name (and maybe a price) … set
   `stated_ingredients` to `[]`. This is the common case." Biases the model
   toward omission over invention — the safe direction for a FODMAP app.

The same safety rules are folded into the text-path prompt. They were validated
to drive fabricated ingredients to **zero** on the test menu while keeping item
coverage high.

> **Safety stance:** for FODMAP, a *missed* ingredient is recoverable (the app
> shows "ingredients not listed"); a *fabricated* ingredient can produce a wrong
> safety verdict. The prompt deliberately trades recall on ingredients for zero
> fabrication. `json_schema` enforces output *shape*, never *truth* — it cannot
> catch a hallucinated ingredient.
>
> **Scope of the "zero fabrication" claim.** It holds for the `stated_ingredients`
> field *on a real menu image* — the model does not copy neighbors or invent
> ingredients for a transcribed dish. It does **not** automatically cover
> *whole-item* invention from a *non-menu* image: a fourth rule (the "IS THIS A
> MENU?" guard, below) is what prevents that, and it must be re-verified whenever
> the prompt changes.

## Real-world test (9 NYC restaurants)

Tested the shipping cascade against 9 live NYC restaurants (via a harness calling
the real `scraper` functions — `ConvertHTMLToMarkdown`, `FindMenuImage`,
`ExtractImage`, `Extract` — minus the Weaviate/embed step). Findings:

- **Coverage is fetch-bound, not extraction-bound.** 3/9 had no website; the other
  6 are JS-rendered (Wix/Squarespace/React) whose raw HTML is marketing boilerplate.
  Pure-Go text extraction correctly returned **0 items with 0 fabrication** — but
  also 0 real menus. The menu content lives behind JS or in an image.
- **Fallback-gate gap.** `needsFallback = noisy || empty || tooShort(<200 chars)`.
  JS homepages emit *enough* boilerplate to pass the gate, so the cascade never
  tries the image path — even when `FindMenuImage` found a real menu image
  (e.g. a café's `TRIFOLD_MENU…png`). Bypassing only the gate and routing that
  image through `ExtractImage` extracted a full menu. **Fix idea:** when
  `FindMenuImage` finds a candidate *and* text extraction yields 0 items, route to
  the image regardless of char count.
- **`FindMenuImage` precision is low (~1/5 here).** It surfaced hero food photos,
  a press badge, and a nav SVG alongside the one real menu. Needs a tighter signal
  (filename/aspect-ratio) or post-extraction validation.
- **Branded-photo fabrication (the safety failure).** Two of the surfaced images
  were non-menu *food photos*. A roast-chicken hero shot → **0 items** (safe). But
  a donut-shop marketing photo with the brand wordmark visible → **26 fabricated
  flavors** (Red Velvet, Crème Brûlée, Birthday Cake…), none readable in the image.
  The model filled in a "typical" menu from the brand + product category. The
  anti-cross-attribution / empty-is-normal rules did not catch this — they govern
  *ingredients within* a menu, not *whether the image is a menu at all*.

**Fix (in repo):** the vision prompt now opens with an **"IS THIS A MENU?"** rule —
transcribe only legible menu text; a photo/banner/logo with no readable item list
returns an empty `items` array; never invent items from branding or product type.
Re-verified: the donut photo went **26 → 0**, the chicken photo stayed **0**, and a
real dense trifold menu still extracted its full item list with no invented
ingredients.

### Phase 1: the detector now reaches image menus with no service dependency

The gaps above are closed in code (see
[../plans/vision-extraction-gaps-plan.md](../plans/vision-extraction-gaps-plan.md)):

- **The pure-Go extractor now satisfies `ImageExtractor`.** `ExtractImage` was
  widened to a 3-arg signature `(ctx, bytes, mime)` and normalizes any decodable
  image (PNG/JPEG/GIF/WEBP via `image.Decode` + `golang.org/x/image/webp`) to PNG
  before sending. So `--enable-vision` alone — no `--extractor-url` — drives the
  Phase C image path. AVIF is not yet supported by a pure-Go decoder (see the
  plan's Risks); request the `?format=png` CDN variant as the mitigation.
- **The routing gate no longer strands discovered menu images.** The cascade
  now runs a *post-text-empty* image pass: even when JS-homepage boilerplate
  passes the noisy/empty/tooShort gate, if `ex.Extract` returns 0 items and
  `FindMenuImages` found a candidate, the image is fetched and OCR'd. This is
  the Wix/Squarespace common case.
- **`FindMenuImage` precision tightened.** A `minMenuImageScore` threshold
  rejects size-only hero/banner photos; filename penalties (`logo`, `hero`,
  `banner`, `press`, `award`, `badge`) and a `.svg` reject drop non-menus even
  when alt contains "menu". A new `FindMenuImages` returns candidates in score
  order; the cascade tries up to 2, so a hero photo + a real menu image on the
  same page still reaches the menu.
- **Post-extraction validation is the safety net.** If the image path returns
  0 items (the "IS THIS A MENU?" guard fired), nothing is indexed — boilerplate
  text is never promoted to a menu.

**Re-test against the 6 reachable NYC sites (2026-06-27):**

| Restaurant | Text items | Image items | Notes |
|---|---|---|---|
| THRIFT N SIP | 0 | **105** | G1 fix routed the trifold image → full menu, no fabrication |
| CHICKEN AT LAST | 0 | 0 | hero photos → guard fired on both candidates |
| NICE DAY CHINESE | 0 | 0 | hero photos → guard fired on both candidates |
| JETBLUE LOUNGE | 0 | 0 | brand image → guard fired |
| DOUGHNUTTERY | 0 | 0 | the 26-fabrication failure → now **0** (safe) |
| WORLD SPA | 0 | 0 | no menu image candidate (JS-rendered; Phase B) |

Net: the donut-photo fabrication failure is closed (26 → 0), the one reachable
real menu (THRIFT) extracts end-to-end with `--enable-vision` and no service,
and the four non-menu image sites all return 0 and index nothing.

### Phase 3: generic JS render-and-re-cascade (no per-site adapter)

The Phase 1 measurement showed image-reachability is ~1/6 reachable sites —
more sites are JS-rendered than image-reachable. The original plan deferred JS
rendering as "per-site webagent adapters," but live testing reframed it: most
JS sites just hydrate the menu into the DOM, so a generic render-and-re-cascade
covers them without per-site authoring.

- **`ChromeRenderedFetcher`** (`scraper/jsrender.go`) implements
  `RenderedFetcher` via chromedp + a headless Chrome found in the standard
  locations. It navigates, waits for `body`, sleeps briefly for post-load
  XHRs, and returns the hydrated `outerHTML`.
- **`--enable-js-render` with no `--webagent-adapter`** uses this fetcher:
  when the text cascade finds nothing (`needsFallback`), `FetchRendered`
  runs, the rendered HTML re-converts to Markdown, menu-image candidates are
  re-scanned, and the text/image cascade re-runs on the hydrated DOM.
- **`--enable-js-render` with `--webagent-adapter`** keeps the per-site
  webagent path (preferred — selector-level guarantees). Reserved for
  interaction-heavy sites (click-to-reveal, infinite scroll, auth).

**Re-test (WORLD SPA, 2026-06-27):** raw HTML was 729 chars of boilerplate
(0 menu via the image path); the rendered DOM yielded **54 items** via the
text cascade, with real `stated_ingredients` populated and the restaurant name
read correctly. No per-site adapter, no service, no Weaviate.

| Restaurant | Raw text chars | Rendered items | Notes |
|---|---|---|---|
| WORLD SPA | 729 | **54** | generic render → text cascade on hydrated DOM |

Per-site webagent adapters remain available (the service's
`AgentDiscoveryLLM` drafts a validated `Adapter` from DOM + network log +
screenshot, then human review) for the minority of sites that need
interaction-level scripting.

## External benchmark: froggeric's VLM repeat-sampling report

Source: [froggeric/llm — benchmark/vlm/REPEAT-REPORT.md](https://github.com/froggeric/llm/blob/main/benchmark/vlm/REPEAT-REPORT.md)
(discussed on r/LocalLLaMA). The experiment re-queries the same image 5× in a
warm session and studies latency, multi-sample aggregation, and temperature.
Tasks were UI screenshots, OCR, table and code extraction — *ground-truthable,
structured* inputs.

**What it found (paraphrased):**
- **Prefer Q8 over Q4 on small models** — the Q4 variant "emits HTML garbage /
  malformed tables on some runs." Q8 is more reliable for extraction.
- **Temperature is the gate.** At temp 0.1 decoding is near-deterministic and
  multi-sample correlation gains are ≈0; benefits (+8 to +23 pts) only appear at
  temp 0.4–0.7 where outputs diverge.
- **Aggregator must match the task** — *union* of N samples for coverage
  (UI/charts), *majority* vote for precision-sensitive extraction (tables),
  *single* for deterministic IDs (code).
- **Bigger isn't uniformly better** — a 4B sometimes beat the 8B on raw token
  recall but lost on an LLM-judge holistic score due to syntax errors.
- **Warm calls** are 1.1–1.6× faster (cached prompt/image KV), saving ~25–29%
  vs. naïve N× querying.
- Recommendation: 3 repeats at temp 0.4–0.7 with task-appropriate aggregation;
  keep the server warm; prefer 8-bit on small models.

**How it maps to our pipeline:**
- ✅ **Q8 > Q4 corroborated.** His result and ours (above) are independent data
  points with the same conclusion. This is the finding we trust most.
- ✅ **Temp-0 determinism corroborated** — matches our byte-identical reruns.
- ✅ **Warm server** — we already keep a persistent vLLM/vllm-metal process.
- ⚠ **Multi-sample aggregation does not transfer cleanly to the safety field.**
  His own data shows union at temp 0.7 collapsing to F1 38% / 24% precision —
  union pulls in hallucinations along with coverage. For us, **union on
  `stated_ingredients` would be actively unsafe.** If adopted at all, use
  *majority* vote on safety-critical fields, never union, and treat raising temp
  as a hallucination-risk tradeoff that `json_schema` does not mitigate.
- ⚠ **His tasks are cleaner than menus.** None of his eight categories measure
  cross-attribution (sibling-vs-ingredient), which is exactly what bit us. A high
  OCR/table score would not have predicted our failure. The leaderboard answers
  "can it read the pixels," not "will it structure a chaotic menu safely."

**Net:** trust the quant and warm-server conclusions; treat multi-sampling as a
*coverage* pilot only (see below), and never union the ingredient field.

## Potential future work: multi-sample coverage (not yet implemented)

A bounded pilot worth running if single-pass item coverage proves too low on
real menus:

- Sample the image **3×** at **temp ≈ 0.4** in the warm session.
- **Majority-vote items** by normalized `dish` name to add coverage without
  inviting fabrication; keep `stated_ingredients` conservative (intersection /
  drop-on-disagreement), never union.
- Measure: does item coverage rise *without* any fabricated ingredient
  reappearing? Only adopt if both hold.

Until that is validated, the default stays **single pass at temp 0** + the prompt
rules above — the configuration that produced zero fabrications.

## Related

- [llm-serving.md](llm-serving.md) — serving the model (Mac MLX-8bit / 5080 FP8),
  the Ollama `json_schema` limitation, NVFP4 notes.
- [cli-reference.md](cli-reference.md) — `scrape` flags and defaults.
- [scraper-pipeline-plan.md](../plans/scraper-pipeline-plan.md) — pipeline + test
  strategy this feeds.
- `scripts/e2e_vision.sh` / `scripts/e2e_jsrender.sh` — end-to-end harnesses for
  the image and JS-render paths (slow, network + LLM dependent; not in `make check`).
