# Plan: Add Places API discovery ahead of Gemini call

## Overview
The user requested using the Google Places API to discover a restaurant's website URL *before* making the Gemini web-search call. If the Places API successfully returns a website, we can extract the `menu_urls` heuristically from it and skip the slower/more expensive Gemini call.

## Changes

1. **Modify `menusearch/discover.go` (`Work` method)**:
   - Construct a text query from `DiscoverMenuURLArgs`: `fmt.Sprintf("%s, %s %s, %s %s", args.DBA, args.Building, args.Street, args.Boro, args.ZipCode)`.
   - Make a `POST` request to `https://places.googleapis.com/v1/places:searchText` using `w.HTTPClient`.
   - Add required headers: `X-Goog-Api-Key` (read from `os.Getenv("PLACES_API_KEY")` or we could pass it down) and `X-Goog-FieldMask` (`places.websiteUri,places.formattedAddress,places.nationalPhoneNumber`).
   - If the API returns a valid `websiteUri` (e.g. `https://joespizza.com`), we parse it.
   - Similar to the existing logic for Gemini, if we get a `websiteUri`:
     - Validate it using `reachableMenuURLs` or similar existing helpers to ensure it's not a dead domain or an aggregator (like grubhub).
     - We append common menu paths (`/menu`, `/menus`) and filter them.
     - Skip the Gemini call completely.

2. **Fallback to Gemini**:
   - If the Places API fails, times out, returns no results, or the result has no `websiteUri` (or it's a known non-menu host like `grubhub.com`), we seamlessly fall back to the existing `genai.GoogleSearch` prompt flow.
   - Log the outcome of the Places API call for observability (e.g. "places api hit", "places api miss").

3. **Modify `cli/menutracking_migrate.go`**:
   - The worker already has `HTTPClient` configured. 
   - We might need to ensure `PLACES_API_KEY` is plumbed if we add a dedicated `PlacesAPIKey` field to `DiscoverMenuURLWorker` (or simply read `os.Getenv`).

## Trade-offs
- **Pros**: Places API is typically faster and more deterministic for finding the canonical website of a business compared to Gemini. It reduces LLM costs.
- **Cons**: We still need heuristic URL guessing (e.g., adding `/menu` to the root URL) because Places API only returns the homepage, whereas Gemini attempts to find the actual menu deep-link. However, `discover.go` already does extensive link crawling and validation (`menuSignalFilter`), so giving it a high-quality root domain from Places API is usually sufficient.

## Verification
- Add a test or run the `discover` CLI command for a known restaurant (e.g. `fodmap restaurants discover 50165827`) and check the logs to verify it hits Places API, extracts the URL, and bypasses Gemini.
# Plan: Add Places API discovery ahead of Gemini call (with Risks and Gaps)

## Architecture & Responsibilities
- **Goal:** Try the Google Places API to look up a restaurant's website URL before making a Gemini grounding/web-search call. If Places returns a valid `websiteUri`, skip Gemini to save latency/cost.
- **Components:** `DiscoverMenuURLWorker.Work` in `menusearch/discover.go`.
- **Configuration:** Pass `PLACES_API_KEY` to the worker struct.

## Implementation Steps
1. Add `PlacesAPIKey string` to `menusearch.DiscoverMenuURLWorker`.
2. In `cli/menutracking_migrate.go` and wherever else the worker is instantiated (potentially `server/server.go` or tests), populate `PlacesAPIKey` from `os.Getenv("PLACES_API_KEY")`.
3. In `DiscoverMenuURLWorker.Work`, before formatting the Gemini prompt:
   - If `w.PlacesAPIKey != ""`, make a `POST https://places.googleapis.com/v1/places:searchText` call.
   - Body: `{"textQuery": "<DBA>, <Building> <Street>, <Boro> <ZipCode>"}`
   - Headers: `X-Goog-Api-Key`, `X-Goog-FieldMask: places.websiteUri`
   - Timeout: Use the existing `w.HTTPClient` (currently set to 10s).
4. **Result Parsing**:
   - If the API succeeds and `websiteUri` is present, treat it exactly as we would `result.WebsiteURL` from Gemini.
   - Specifically, we want to skip `genai.Client` call and jump to:
     ```go
     var rawURLs []string
     rawURLs = append(rawURLs, websiteUri)
     base := strings.TrimSuffix(websiteUri, "/")
     rawURLs = append(rawURLs, base+"/menu", base+"/menu/", base+"/menus")
     ```
   - Then let the rest of the existing `discover.go` pipeline run (`reachableMenuURLs`, `harvestOrderingLinks`, `menuSignalFilter`).
5. **Fallback:** If Places API fails (HTTP error, timeout) or returns no `websiteUri` in any of the returned places, catch the miss, log a warning/info, and seamlessly continue to the Gemini generation block.

## Risks and Gaps
1. **Places API `websiteUri` Quality:** The Places API often returns ordering platforms (DoorDash, GrubHub) or social media links (Instagram, Facebook) as the canonical `websiteUri` if the restaurant has no real site.
   - **Mitigation:** The existing `discover.go` pipeline already filters these! It calls `isOrderingPlatform()` and `isPrivateMenuHost()`, and later `menuSignalFilter()`. By feeding the Places API result into the *existing* filtering logic instead of skipping straight to enqueueing, we are protected from junk aggregator URLs.
2. **Places API Cost:** While cheaper than LLMs, Places Search Text API is ~$32 per 1000 requests. We need to ensure we aren't looping retries unnecessarily.
   - **Mitigation:** The pipeline already has `MaxNoURLAttempts` and exponential backoff via River. If Places API returns nothing, Gemini might return nothing, and it fails safely.
3. **Empty Result Handling:** `places:searchText` might return multiple places. We should probably just take `places[0].websiteUri` if it exists. If `places` is empty or `places[0]` has no `websiteUri`, we fall back to Gemini.
4. **Avro Schema Event ID:** The `DiscoverMenuURLWorker` writes a bronze-tier record (`gemini_discovery.avro`) via `w.writeAvroRecord`. This record includes the Gemini raw response text for observability.
   - **Gap:** If we skip Gemini, we have no "Gemini raw response text". We must either modify the Avro schema to handle "Places API" source, or just write empty text to the existing schema but populate the URLs so downstream processing (the `discovery_event_id` link) doesn't break.
   - **Resolution Plan:** In `gemini_discovery.avro` schema (`scraper/src/scraper/webagent/discovery/gemini_discovery.avsc` or similar), we should probably write a record with `prompt="PLACES_API"` and `raw_response="<places api json>"` so the provenance is intact without altering the bronze schema.
5. **Testing Constraints:** `menusearch/discover_test.go` stubs the HTTP client and Gemini client. We need to make sure the Places API HTTP call can also be stubbed or bypassed in unit tests.
   - **Resolution Plan:** The worker uses `w.HTTPClient`. The tests inject `mockTransport`. We'll need to update `mockTransport` in `discover_test.go` to handle or ignore `places.googleapis.com` requests gracefully.

