# FODMAP Detector — Feature Recommendations

Based on a thorough review of the codebase, documentation, and existing plans, below are feature recommendations organized by priority tier. Each recommendation includes the rationale, scope, and how it fits into the existing architecture.

---

## Current Feature Summary

| Area | What Exists |
|------|-------------|
| Data pipeline | Yelp dataset ingestion, Avro serialization, review/business models |
| Search | Semantic + hybrid search via Weaviate/Pinecone/Postgres, business & review endpoints, FODMAP ingredient vector lookup |
| Chat | Gemini-powered FODMAP/allergen agent with tool calling, SSE streaming, conversation persistence, dietary profile injection |
| Auth | JWT access/refresh tokens, register/login/delete, SQLite + PostgreSQL backends |
| Profile | Gemini-generated structured dietary profile, injected into system prompt |
| CLI | `index`, `serve`, `chat` commands |

*Note: Features 1 (Expanded FODMAP DB), 2 (Substitution Suggestions), and 9 (Rate Limit Headers) from the original roadmap have been successfully implemented. Feature 4 (Conversation Export API) is complete, pending CLI integration.*

---

## ⚠️ Important Note: Frontend & UI Considerations
This document primarily focuses on backend API changes in the `fodmap-detector` repository. However, almost all Tier 1 and 2 features will require corresponding UI implementation in the separate `fodmap-chat` frontend repository (React/Tailwind/Vite). When planning these features, consider the required state management, new components, and UX workflows required to expose the new endpoints to the user.

---

## Tier 1 — High Impact, Builds on Existing Strengths

### 1. <s>Expand the FODMAP Ingredient Database</s> (COMPLETED ✅)

**Status:** Completed. `FodmapDB` in `data/fodmap.go` now contains an extensive dataset of ingredients with serving-size thresholds and granular categorization.

---

### 2. <s>Low-FODMAP Ingredient Substitution Suggestions</s> (COMPLETED ✅)

**Status:** Completed. `FodmapEntry` now includes a `Substitutions` field, and the chat agent is instructed to proactively suggest these via `chat/chat-instruction.txt`.

---

### 3. Batch Ingredient List Analysis

**Problem:** Users must ask about ingredients one at a time in chat. If someone has a recipe or ingredient list, they need to send multiple messages. This is tedious for a common use case.

**Proposal:**
- Add a new endpoint: `POST /api/v1/analyze-ingredients`
- Accept a list of ingredients or a free-text ingredient list. **Security Gap:** Enforce a strict limit (e.g., max 50 items or 5000 chars) to prevent abuse and request timeouts.
- Return a structured FODMAP breakdown for each ingredient found in the database
- In chat, add a `analyze_ingredients` tool that the model can call when a user pastes a multi-ingredient list

**Example response:**
```json
{
  "ingredients": [
    {"name": "garlic", "level": "high", "groups": ["fructans"], "substitutions": ["garlic-infused oil"]},
    {"name": "rice", "level": "low", "groups": []},
    {"name": "unknown spice", "level": "unknown", "groups": []}
  ],
  "summary": {
    "high": 1, "moderate": 0, "low": 1, "unknown": 1,
    "total_fodmap_groups": ["fructans"]
  }
}
```

**Architecture fit:** New handler in `server/`, reuses `Searcher.SearchFodmap`. The chat tool extends `DispatchTool` with a new case.

---

### 4. Conversation Export (PARTIALLY COMPLETED ⏳)

**Status:** The backend API endpoint (`GET /api/v1/conversations/{id}/export?format=json|markdown`) is implemented with strict JWT validation. 

**Remaining Work:**
- Add `--output` flag to the CLI `chat` command to consume this endpoint.

---

### 5. Multi-Business Comparison

**Problem:** Already identified in `docs/chat.md`. Users researching FODMAP-friendly options want to compare restaurants side by side.

**Proposal:**
- Add `POST /api/v1/compare` endpoint accepting 2-5 business IDs or search queries
- Return a side-by-side FODMAP-friendliness summary based on review analysis
- In chat, support queries like "Compare Italian restaurants in Las Vegas for FODMAP options"
- Requires extending the conversation model to support multiple business contexts
- **Performance Gap:** Pulling full review sets for 5 businesses will blow out the Gemini token context window and cause massive latency. This feature requires either pre-summarizing reviews per-business before merging, or strictly capping the number of retrieved reviews to 2-3 per business.

**Architecture fit:** Extends `Conversation` with a `BusinessIDs []string` field (currently single `BusinessID`). The chat handler already fetches reviews per business — loop over multiple IDs and merge context.

---

## Tier 2 — Significant Value Add, Moderate Complexity

### 6. FODMAP Reintroduction Challenge Tracker

**Problem:** The dietary profile supports `diet_phase: "elimination | reintroduction | personalized"` but there is no actual tracking mechanism. The reintroduction phase is the most complex part of the FODMAP diet — users systematically test each FODMAP group and record reactions.

**Proposal:**
- Add a `ReintroductionLog` model to the `auth/` package:
  ```go
  type ReintroductionLog struct {
      ID         string    `json:"id"`
      UserID     string    `json:"user_id"`
      FodmapGroup string   `json:"fodmap_group"` // fructans, GOS, lactose, etc.
      TestFood   string    `json:"test_food"`     // e.g., "wheat bread"
      Result     string    `json:"result"`       // "tolerated", "mild", "severe"
      Date       time.Time `json:"date"`
      Notes      string    `json:"notes"`
  }
  ```
- Add CRUD endpoints: `POST /api/v1/reintroduction`, `GET /api/v1/reintroduction`
- Inject the log into the chat system prompt so the agent knows which groups have been tested
- Generate a "FODMAP tolerance map" summarizing which groups the user tolerates

**Architecture fit:** Follows the same pattern as `DietaryProfile`. New table, new store methods, prompt injection via `PromptData`.

---

### 7. Restaurant Favorites / Bookmarks

**Problem:** Users who find FODMAP-friendly restaurants have no way to save them for later. Every session requires re-searching.

**Proposal:**
- Add a `Favorite` model: `UserID`, `BusinessID`, `BusinessName`, `Notes`, `CreatedAt`
- Endpoints: `POST /api/v1/favorites`, `GET /api/v1/favorites`, `DELETE /api/v1/favorites/{id}`
- Display favorites in the frontend sidebar for quick access
- Allow starting a new chat directly from a favorited business

**Architecture fit:** Simple CRUD following the `Store` interface pattern. New table, new handler file.

---

### 8. OpenAPI / Swagger Specification

**Problem:** The API is documented in prose in `README.md` but has no machine-readable spec. This makes frontend development harder and prevents auto-generated client SDKs.

**Proposal:**
- Add an OpenAPI 3.1 spec file at `docs/api/openapi.yaml`
- Auto-serve it at `GET /api/v1/docs` (or use Swagger UI)
- Generate TypeScript types for the frontend from the spec
- Consider using `swaggo/swag` to generate docs from Go annotations

**Architecture fit:** Static file, no code changes to handlers. Can be added incrementally.

---

### 9. <s>Rate Limit Response Headers</s> (COMPLETED ✅)

**Status:** Completed. Standard rate limit headers (`X-RateLimit-Limit`, `X-RateLimit-Remaining`, and `Retry-After`) have been integrated into the `rateLimitMiddleware` in `server/middleware.go`.

---

### 10. Usage Metrics and Observability

**Problem:** There is no visibility into API usage, chat quality, search latency, or error rates. Production issues would be invisible.

**Proposal:**
- Add a `metrics` endpoint (`GET /metrics`) exposing Prometheus-format metrics:
  - `fodmap_chat_requests_total` (by status, model)
  - `fodmap_search_requests_total` (by backend, status)
  - `fodmap_tool_calls_total` (by tool name, found/not-found)
  - `fodmap_search_latency_seconds` (histogram)
- Add request logging middleware with trace IDs
- Consider OpenTelemetry integration (dependencies already in `go.mod`)

**Architecture fit:** Middleware-based, no handler changes. The project already has `go.opentelemetry.io` indirect dependencies in `go.mod`.

---

## Tier 3 — Longer-Term, Higher Complexity

### 11. Menu / Photo Analysis (Multimodal)

**Problem:** Users often have photos of menus but cannot analyze them through the app. They must manually type out dish descriptions.

**Proposal:**
- Extend the chat endpoint to accept image uploads (Gemini supports multimodal input)
- Add `POST /api/v1/analyze-menu` accepting an image file
- Use Gemini Vision to extract dish names and ingredients from menu photos
- Run the extracted ingredients through the FODMAP database automatically

**Architecture fit:** The `genai.Client` already supports multimodal parts (`InlineData`, `FileData` in `SendWithToolCalls`). The main work is accepting file uploads and constructing the appropriate `genai.Part`.

---

### 12. USDA FoodData Central Allergen Fallback

**Problem:** Already identified in `docs/chat.md`. Open Food Facts is product-centric, not ingredient-centric, leading to noisy allergen results.

**Proposal:**
- When OFF returns no results or low-confidence results, fall back to USDA FoodData Central
- Requires a free API key from `data.gov`
- Chain within `lookup_allergens` or add as a separate tool `lookup_allergens_usda`
- Merge and deduplicate results from both sources

**Architecture fit:** New client struct following the `AllergenClient` interface. The `DispatchTool` function can try OFF first, then USDA on miss.

---

### 13. Chat Quality Feedback Loop

**Problem:** There is no mechanism for users to indicate whether the chat agent's FODMAP advice was accurate or helpful. This makes it impossible to improve the system over time.

**Proposal:**
- Add thumbs-up/thumbs-down on chat messages: `POST /api/v1/conversations/{id}/messages/{msg_id}/feedback`
- Store feedback in a `message_feedback` table
- Aggregate feedback to identify systematic issues (e.g., specific ingredients the model consistently misclassifies)
- Use feedback to fine-tune prompt instructions or flag FODMAP database gaps

**Architecture fit:** New `Feedback` model, new store method, new endpoint. Lightweight addition.

---

### 14. Persistent FODMAP Database with Admin CRUD

**Problem:** Already identified in `docs/chat.md` as a long-term goal. The static Go map requires rebuilding the binary to update FODMAP data.

**Proposal:**
- Migrate `FodmapDB` from the Go map to a database table (already exists in Weaviate/Postgres for vector search, but the source-of-truth is the Go map)
- Add admin endpoints: `POST /api/v1/admin/fodmap`, `PUT /api/v1/admin/fodmap/{ingredient}`, `DELETE /api/v1/admin/fodmap/{ingredient}`
- Add an import endpoint that accepts a JSON or CSV file of FODMAP entries
- Keep the Go map as a seed/fallback for fresh installations

**Architecture fit:** The `BatchUpsertFodmap` method already exists. Adding CRUD endpoints is straightforward. The admin routes need a separate auth middleware (admin role).

---

### 15. Proactive FODMAP Alerts in Chat

**Problem:** The chat agent only reacts to user questions. It does not proactively flag high-FODMAP patterns in the review context.

**Proposal:**
- After loading review context, run a pre-analysis pass that identifies commonly mentioned high-FODMAP ingredients
- Inject a "proactive alerts" section into the system prompt: "Common high-FODMAP ingredients mentioned in reviews for this restaurant: garlic (mentioned in 8 reviews), onion (6 reviews), wheat/dairy (5 reviews)"
- This gives the model a head start and ensures critical warnings are surfaced even if the user does not ask about specific ingredients

**Architecture fit:** Extends `generateReviewSummary` or adds a second background pass. No new endpoints needed.

---

## Summary Priority Matrix

| # | Feature | Impact | Effort | Tier |
|---|---------|--------|--------|------|
| 3 | Batch ingredient analysis | High | Medium | 1 |
| 5 | Multi-business comparison | High | Medium | 1 |
| 6 | Reintroduction tracker | High | Medium | 2 |
| 7 | Restaurant favorites | Medium | Low | 2 |
| 8 | OpenAPI spec | Medium | Low | 2 |
| 10 | Usage metrics | Medium | Medium | 2 |
| 11 | Menu/photo analysis | High | High | 3 |
| 12 | USDA allergen fallback | Medium | Medium | 3 |
| 13 | Chat quality feedback | Medium | Low | 3 |
| 14 | Persistent FODMAP DB + admin | High | High | 3 |
| 15 | Proactive FODMAP alerts | Medium | Medium | 3 |

---

## Recommended Implementation Order

```
Phase A (Quick Wins):  CLI flag for 4
Phase B (Core Features): 3 → 5 → 7 → 8
Phase C (Engagement):   6 → 13 → 15 → 10
Phase D (Advanced):     11 → 12 → 14
```

Phase A items are low-effort, high-impact improvements that build directly on existing patterns. Phase B extends the core product surface. Phase C adds engagement and quality loops. Phase D requires significant new capabilities.

---

## Frontend Implementation Plan (Phase 1)

The `fodmap-chat` frontend (React/Tailwind/Vite) needs updates to expose backend features that already have API endpoints but no UI. Phase 1 covers only features with working backends.

### 1. Navigation — Expand Sidebar

**Files to modify:**
- `fodmap-chat/src/components/ConversationList.tsx` — Add a nav section above the conversation list with icon+text links for "Chat" and "Ingredients"
- `fodmap-chat/src/App.tsx` — Add `/ingredients` route, import IngredientsPage

**Details:**
- Sidebar gets a top section with icon+text nav links (using lucide-react icons: `MessageSquare` for Chat, `Leaf` for Ingredients)
- "Chat" link navigates to `/`, "Ingredients" link navigates to `/ingredients`
- Below the nav, the existing conversation list continues as-is
- Active route is highlighted

### 2. FODMAP Ingredient Lookup Page

**New files:**
- `fodmap-chat/src/pages/ingredients.tsx` — Page wrapper with layout
- `fodmap-chat/src/components/IngredientSearch.tsx` — Search form + results display

**Files to modify:**
- `fodmap-chat/src/types/api.ts` — Add `FodmapResult` type: `{ ingredient, level, groups, notes, substitutions }`

**Behavior:**
- Search input → calls `GET /api/v1/search/fodmap/{ingredient}`
- Result displayed as a styled card with: ingredient name, FODMAP level badge (color-coded: red=high, yellow=moderate, green=low), groups as tags, substitutions as chips, and notes
- Consistent dark glass-morphism styling (bg-card/50, backdrop-blur, ring-white/10)
- Handles "not found" gracefully with a message directing users to the Monash app

### 3. Conversation Export Button

**New files:**
- `fodmap-chat/src/lib/export.ts` — Utility for triggering browser file downloads

**Files to modify:**
- `fodmap-chat/src/components/ChatWindow.tsx` — Add export dropdown/icon button in the conversation header area

**Behavior:**
- Export icon button appears in the ChatWindow header when viewing a conversation
- Click opens a small dropdown: "Export as JSON" / "Export as Markdown"
- Calls `GET /api/v1/conversations/{id}/export?format=json|markdown`
- Triggers browser download of the response

### 4. Dietary Profile Polish

**Files to modify:**
- `fodmap-chat/src/components/DietaryProfileModal.tsx` — Replace raw JSON `<pre>` display with structured field rendering

**Behavior:**
- Parse the profile object and render structured fields:
  - `diet_phase` → colored badge (elimination/reintroduction/personalized)
  - `intolerances` → list of colored tags
  - `triggers` → list of warning tags
  - Other fields → key-value display
- Keep the textarea for updating
- Maintain existing update functionality

### Implementation Order

```
1 → 2 → 3 → 4
Nav    FODMAP   Export   Profile
```
