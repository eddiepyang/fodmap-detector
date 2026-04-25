# Implementation Plan: Waiter Script Agent

## Objective
Empower users to safely communicate their dietary needs to restaurant staff by generating a clear, polite "Waiter Script." When a user asks about a specific dish and the chat agent flags high-risk ingredients, the user will be presented with a button to generate a script they can read directly to the waiter.

## Scope & Impact
- **Backend (`fodmap-detector`):**
  - Create a new API endpoint to handle the generation of the waiter script.
  - Create a new single-turn LLM generation function in the `chat` package.
- **Frontend (`fodmap-chat`):**
  - Update the chat UI to detect when a dish has been discussed and flagged.
  - Add a "Generate Waiter Script" button to the relevant chat messages.
  - Display the generated script in a clear, easy-to-read modal or inline card.

## Proposed Solution

### Phase 1: Backend Script Generator
1.  **Create Generation Logic:** In `chat/chat.go` (or a new file `chat/script.go`), create a function `GenerateWaiterScript(ctx context.Context, client *genai.Client, dishName string, flaggedIngredients []string, profile string) (string, error)`.
    - This function will use a targeted system prompt: "You are a communication expert helping a person with severe dietary restrictions order food at a restaurant. Generate a concise, polite, and firm script they can read to the waiter to ensure the '{{.DishName}}' is prepared without '{{.FlaggedIngredients}}'. Mention the risk of cross-contamination."
2.  **Create API Endpoint:** In `server/handlers.go`, add a new authenticated endpoint: `POST /api/v1/chat/{conversation_id}/script`.
    - The request payload should include the `dish_name` and an array of `flagged_ingredients` (extracted from the chat history or provided by the frontend).
    - The handler calls `GenerateWaiterScript` and returns the generated text.

### Phase 2: Frontend Integration
1.  **UI Updates:** In `fodmap-chat/src/components/ChatWindow.tsx`:
    - Implement logic to detect when the LLM's response has flagged a dish (this may require the backend to return structured data alongside the text, or simple heuristics on the frontend like looking for bolded dish names and "High FODMAP" warnings).
    - Render a "Generate Waiter Script" button below the relevant chat bubble.
2.  **API Connection:** Connect the button to the new `/api/v1/chat/{conversation_id}/script` endpoint.
3.  **Display Component:** Create a new UI component (e.g., `ScriptCard.tsx`) that displays the returned script in a large, legible font. Include a "Copy to Clipboard" button for convenience.

## Verification & Testing
1.  **Backend Unit Tests:**
    - Test `GenerateWaiterScript` to ensure it produces polite, appropriately toned output that includes all flagged ingredients.
    - Test the `/script` endpoint handler for proper request parsing and error handling.
2.  **Frontend Testing:**
    - Update `ChatWindow.test.tsx` to verify the "Generate Script" button appears under the correct conditions.
    - Test the API integration and ensure the `ScriptCard` renders correctly with the returned text.

## Migration & Rollback
-   This is an additive feature. If the endpoint fails or the LLM is unavailable, the frontend should gracefully hide the button or show a simple error toast, allowing the core chat functionality to continue uninterrupted.