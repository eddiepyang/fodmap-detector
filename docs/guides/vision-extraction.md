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
  dish's `stated_ingredients`. That is the FODMAP-critical risk and is handled
  by prompt rules, not by a bigger model alone.
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
