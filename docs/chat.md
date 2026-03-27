# Interactive FODMAP/Allergen Chat Agent

An interactive CLI agent that finds a restaurant matching a natural-language query, loads its reviews,
and starts a Gemini-powered chat session where users can ask questions about FODMAP content and
allergens in the dishes described. The agent grounds its answers in the actual review text and
verifies ingredient claims against a local FODMAP database and the Open Food Facts allergen API
via Gemini function calling.

---

## Architecture Overview

```
  fodmap chat "pad thai" --city "Las Vegas"
         │
         ▼
  ┌──────────────────────────────────────────────────┐
  │  cli/chat.go  (runChat)                          │
  │                                                  │
  │  1. GET /searchBusiness/pad%20thai?limit=1       │──► running HTTP server (port 8080)
  │     ← top business {id, name, city, state}       │        │
  │                                                  │        ▼
  │  2. GET /reviews?business_id={id}                │    Weaviate nearText
  │     ← []Review (capped at --limit)               │    + archive lookup
  │                                                  │
  │  3. Render chat-instruction.txt template              │
  │     with business name + formatted reviews       │
  │                                                  │
  │  4. Create Gemini chat session                   │
  │     system instruction = embedded instruction string│
  │     tools = [lookup_fodmap, lookup_allergens]     │
  │                                                  │
  │  5. REPL loop ─────────────────────────┐         │
  │     │  read stdin                      │         │
  │     │  code guardrails (len, inject)   │         │
  │     │  topic pre-screen (flash-lite)   │         │
  │     │  chat.SendMessage ───────────────┤         │
  │     │                                  │         │
  │     │  ◄── FunctionCall?               │         │
  │     │      ├─ lookup_fodmap ──► static map       │
  │     │      └─ lookup_allergens ──► OFF API       │
  │     │  send FunctionResponse ──►       │         │
  │     │                                  │         │
  │     │  ◄── text response               │         │
  │     │  print to stdout                 │         │
  │     └──────────────────────────────────┘         │
  └──────────────────────────────────────────────────┘
```

**Key property:** the agent talks to the running HTTP server rather than importing search/data
packages directly. This was a deliberate choice — the user's requirement was to exercise the
existing endpoints, keeping the agent decoupled from the data layer.

---

## Design Decisions

### 1. Database Grounding: Function Calling vs Context Pre-loading vs Post-processing

Three strategies were considered for grounding FODMAP/allergen claims in real data:

| Strategy | Mechanism | Token cost | Latency | Hallucination risk | Verdict |
|---|---|---|---|---|---|
| **Context pre-loading** | Dump entire FODMAP list into system prompt | High (every turn carries the full list) | None per-turn | Low — data always visible | Rejected: wastes tokens; ~60 entries is fine but doesn't scale |
| **Post-processing** | After LLM responds, extract claims and validate against DB | None | Added post-step | Medium — user sees unvalidated text first | Rejected: complex extraction; poor UX if corrections needed |
| **Function calling** *(chosen)* | Expose `lookup_fodmap` and `lookup_allergens` as Gemini tools; model calls them when needed | Low (only tool results added) | One extra round-trip per tool call | Low — model gets ground truth before responding | **Chosen** |

**Decision:** Function calling is the cleanest fit. The model decides when verification is needed
(not every turn mentions ingredients), tool results are compact, and the response already
incorporates the grounded data. The chat loop handles this transparently: send → check for
`FunctionCall` parts → dispatch → send `FunctionResponse` → repeat until pure text.

---

### 2. FODMAP Data Source: Static Map vs `fodmap-diet/go-sdk` vs Monash API

The original plan (see [plan file](#plan-deviations)) proposed using `github.com/fodmap-diet/go-sdk`.
During implementation this was found to be unsuitable.

| Source | Coverage | Quality | Go integration | Verdict |
|---|---|---|---|---|
| **Monash University app** | 450+ foods, certified | Gold standard | No public API; mobile-only, paid | Not available programmatically |
| **`fodmap-diet/go-sdk`** | Unknown | Community | Requires `google.golang.org/appengine`; last updated 2019; uses deprecated `ioutil` | Rejected: broken dependency, unmaintained |
| **Embedded static map** *(chosen)* | ~60 common ingredients | Curated from public Monash/Stanford guides | Pure Go map, zero dependencies | **Chosen** |
| **External FODMAP API** | Varies | Varies | HTTP call | None found that are both free and reliable |

**Decision:** A hand-curated `map[string]fodmapEntry` in `cli/fodmap_data.go` with ~60 entries
covering the most common high/moderate/low FODMAP ingredients. Case-insensitive exact matching
plus substring fallback (e.g., "garlic powder" matches "garlic"). The model is instructed to call
`lookup_fodmap` and, when the ingredient is not found, explicitly tell the user to consult the
Monash app rather than guessing.

**Tradeoff:** limited coverage (60 ingredients vs Monash's 450+). Mitigated by clear "not found"
messaging and the prompt's uncertainty rule. A proper database can replace this without changing
the function-calling interface.

---

### 3. Allergen Data Source: Open Food Facts vs USDA FoodData Central vs Edamam

| Source | Cost | API key | Coverage | Data quality | Verdict |
|---|---|---|---|---|---|
| **Open Food Facts** *(chosen)* | Free | None required | Large product DB, global | Community-maintained; varies | **Chosen** |
| USDA FoodData Central | Free | Required (free) | 300K+ foods, US-focused | Government-backed, high | Good alternative; key requirement adds setup friction |
| Edamam | Freemium | Required | 900K+ foods | Commercial, high | Free tier has rate limits; overkill for this use case |

**Decision:** Open Food Facts is the simplest integration — no key, no signup, JSON response with
`allergens_tags`. The search endpoint is queried with the ingredient name and allergen tags are
deduplicated across the top 3 results.

**Tradeoff:** OFF is product-centric (barcoded items), not ingredient-centric. Searching for "garlic"
returns garlic-containing products, whose allergen tags may not reflect garlic itself. This is a
known limitation flagged in the `source` field of the tool response.

---

### 4. Model Selection: `gemini-3.1-flash` vs `gemini-3.1-pro`

The plan originally proposed `gemini-3.1-pro` for the main chat and `gemini-3.1-flash-lite` for the
topic pre-screen.

| Model | Reasoning quality | Speed | Cost | Verdict |
|---|---|---|---|---|
| `gemini-3.1-pro` | Best | Slower | Higher | Plan's original choice |
| **`gemini-3.1-flash`** *(chosen)* | Good | Fast | Lower | **Chosen** as default (configurable via `--model`) |
| `gemini-3.1-flash-lite` | Adequate for yes/no | Fastest | Lowest | Used for topic pre-screen only |

**Decision:** Default to `gemini-3.1-flash` — fast enough for interactive chat, good tool-calling
support, and significantly cheaper than Pro for a feature where users may send many turns. Users who
need stronger reasoning can pass `--model gemini-3.1-pro`. The plan's choice of `gemini-3.1-pro` was
adjusted because an interactive REPL benefits more from speed than peak reasoning quality.

The pre-screen model (`gemini-3.1-flash-lite`) is a hardcoded constant since it only answers
"yes"/"no" and doesn't need to be configurable.

---

### 5. Guardrail Architecture: Two-Layer Defense

```
User input
    │
    ▼
┌─────────────────────────────┐
│  Code guardrails (instant)  │   ← no API call, deterministic
│  • Length cap (2000 chars)   │
│  • Injection pattern match  │
│  • Topic pre-screen (LLM)   │
└──────────┬──────────────────┘
           │ passed
           ▼
┌─────────────────────────────┐
│  Prompt guardrails (LLM)    │   ← in system instruction
│  • Scope constraint         │
│  • Medical disclaimer       │
│  • Uncertainty requirement  │
│  • Grounding requirement    │
│  • No serving-size guesses  │
└──────────┬──────────────────┘
           │
           ▼
       Response
```

**Code layer** catches the obvious cases cheaply: oversized inputs, known injection patterns, and
clearly off-topic messages (via a lightweight Gemini call). These run before the main chat session
sees the message, saving tokens and preventing prompt pollution of the conversation history.

**Prompt layer** handles contextual cases the code layer cannot: nuanced off-topic pivots,
medical-advice-seeking, or FODMAP claims the model could make without calling the verification tool.
These are enforced by the system instruction in `chat-instruction.txt`.

**Tradeoff:** The topic pre-screen adds one extra API call per turn. It fails open (allows the
message through) if the screen call errors, avoiding false blocks at the cost of occasionally
letting off-topic messages through when the pre-screen model is unavailable.

---

### 6. Agent-Server Boundary: HTTP Calls vs Direct Package Import

| Approach | Pros | Cons | Verdict |
|---|---|---|---|
| **HTTP to running server** *(chosen)* | Exercises real endpoints; decoupled; tests server independently | Requires server to be running | **Chosen** |
| Direct `search.Client` + `data.GetReviewsByBusiness` | Simpler; no server needed | Bypasses HTTP layer; tighter coupling | Rejected per user requirement |

**Decision:** The user explicitly wanted the agent to use the existing HTTP endpoints. This means
the `chat` command requires `fodmap serve` to be running in a separate terminal — documented in the
README and in the `--server` flag's default value (`http://localhost:8080`).

---

## Plan Deviations

The implementation plan is preserved at `.claude/plans/toasty-sniffing-prism.md`. Key deviations
from that plan and the reasoning behind each:

| Plan | Implementation | Reason |
|---|---|---|
| Use `fodmap-diet/go-sdk` for FODMAP data | Embedded static `map[string]fodmapEntry` | SDK requires App Engine, deprecated `ioutil`, unmaintained since 2019 |
| Default model `gemini-3.1-pro` | Default model `gemini-3.1-flash` | Interactive REPL benefits from speed over peak reasoning; configurable via `--model` |
| Add `github.com/fodmap-diet/go-sdk` to `go.mod` | No new dependencies | Static map eliminated the external dependency entirely |
| Separate `--screen-model` flag | Hardcoded `gemini-3.1-flash-lite` constant | Pre-screen only answers yes/no; not worth a flag |

---

## Future Improvements

## Completed Improvements (March 2026)

1. **Weaviate-backed FODMAP lookup** — Migrated static map lookups to Weaviate vector similarity matching, allowing `lookup_fodmap` to resolve gracefully via `/searchFodmap/{ingredient}`.
2. **Business ID filter on review search** — Implemented semantic fallback `/searchReview/{query}?business_id={id}` so the agent fetches context-relevant reviews exclusively scoped to the active restaurant, avoiding hallucination.
3. **Streaming responses** — Eliminated latency bottlenecks by replacing batch `.SendMessage` loops with native Go 1.23 `iter.Seq2` iterators wrapping `chat.SendMessageStream`.
4. **Allergen response caching** — Placed a `sync.Map` cache layer in front of Open Food Facts HTTP requests inside `cli/chat.go` `lookupAllergens`, reducing redundant round-trips for common ingredients.

### Medium-term

5. **Monash FODMAP integration** — If Monash releases a public API or data license, replace the
   static map with certified serving-size-aware FODMAP data. The tool interface is designed for this
   upgrade: just swap the `lookupFODMAP` body.

6. **Multi-business comparison** — Let the user query multiple restaurants in one session. Fetch
   reviews for each and let the model compare FODMAP-friendliness across venues.

7. **Conversation export** — Add an `--output` flag that writes the full conversation transcript
   (user turns + model responses + tool calls) to a JSON file for later reference.

8. **USDA FoodData Central fallback** — When Open Food Facts returns no allergen data, fall back to
   the USDA API (requires a free API key from data.gov). Add as a second tool or chain within
   `lookup_allergens`.

### Long-term

9. **Persistent FODMAP database** — Migrate from static map to SQLite with a schema migration path.
   Allow users (or a periodic job) to import updated FODMAP datasets without rebuilding the binary.

10. **Review summarization pre-pass** — Before starting the chat, run a one-shot Gemini call to
    summarize the fetched reviews into a structured menu-like format (dish → ingredients mentioned).
    This gives the chat session a denser, more actionable context than raw review text.

---

## Files

| File | Role | Coverage |
|---|---|---|
| `chat/chat.go` | Chat session logic, tool dispatch, HTTP clients | 87.6% |
| `cli/chat.go` | Command entry point, REPL loop, guardrails | — |
| `server/chat_handler.go` | SSE streaming handler, model factory | 68.7% |
| `cli/fodmap_data.go` | Static FODMAP ingredient database (~60 entries) | 100% |
| `cli/chat_test.go` | Tests for guardrails, template rendering, tool dispatch | — |
| `server/conversation_handler_test.go` | Tests for conversation CRUD and error paths | — |
```
