# Implementation Plan: Personal Dietary Profile Interviewer

## Objective
Enhance the personalization of the `fodmap-detector` by creating an onboarding agent workflow that captures a user's specific dietary sensitivities. This profile will be stored in PostgreSQL and injected into the main chat agent's system prompt, enabling the agent to provide highly individualized dish recommendations rather than relying on generic FODMAP rules.

## Scope & Impact
- **Backend (`fodmap-detector`):** 
  - Update the PostgreSQL schema to store a `DietaryProfile` JSON object.
  - Create a new API endpoint/handler to process the onboarding interview and generate the profile.
  - Modify `server/chat_handler.go` and `chat/chat.go` to inject the user's profile into the system prompt.
- **Frontend (`fodmap-chat`):**
  - Create a new onboarding UI workflow (e.g., a modal or dedicated page after registration) to ask the user about their dietary triggers.
  - Allow users to update their profile later.

## Proposed Solution

### Phase 1: Database & Backend Data Models
1.  **Update `auth.User` struct:** In `auth/user.go`, add a `DietaryProfile` field (type `json.RawMessage` or a defined struct) to the `User` model.
2.  **Update PostgreSQL Schema:** Modify `auth/postgres_store.go` to include a `dietary_profile` JSONB column in the `users` table. Provide migration queries if necessary.
3.  **Update Store Interfaces:** Ensure `auth.Store` methods (like `CreateUser`, `GetUser`, `UpdateUser`) handle the new `DietaryProfile` field.

### Phase 2: Onboarding Interview Handler
1.  **Create Profile Generator:** Create a new Go function (e.g., `chat.GenerateDietaryProfile`) that uses a single-turn Gemini prompt to extract a structured JSON profile from the user's natural language answers about their diet.
2.  **Create API Endpoint:** Add a new authenticated endpoint (e.g., `POST /api/v1/profile`) in `server/handlers.go` that:
    - Accepts the user's natural language input from the onboarding flow.
    - Calls `chat.GenerateDietaryProfile`.
    - Saves the resulting JSON profile to the database via `userStore.UpdateUser`.

### Phase 3: Prompt Injection
1.  **Update System Prompt Template:** Modify `chat/chat-instruction.txt` to include a conditional section for the user's dietary profile.
    - Example: `{{if .DietaryProfile}} The user has the following specific dietary profile: {{.DietaryProfile}}. Prioritize these specific triggers over general FODMAP rules.{{end}}`
2.  **Modify `chat_handler.go`:**
    - When loading a conversation, fetch the user's `DietaryProfile` from the database.
    - Pass this profile data into `chat.RenderChatSystemPrompt`.

### Phase 4: Frontend Implementation
1.  **Onboarding UI:** Create a new React component in `fodmap-chat` (e.g., `DietaryOnboarding.tsx`) that asks the user 3-4 simple questions (e.g., "What ingredients trigger your symptoms?", "Are you currently in the elimination phase?").
2.  **API Integration:** Connect the onboarding UI to the new `POST /api/v1/profile` endpoint.
3.  **Settings UI:** Add a section in the user settings to view and re-take the profile interview.

## Verification & Testing
1.  **Backend Unit Tests:**
    - Test `chat.GenerateDietaryProfile` with various natural language inputs to ensure valid JSON extraction.
    - Test PostgreSQL store methods for saving and retrieving the JSONB profile.
    - Test `chat.RenderChatSystemPrompt` to ensure the profile is correctly formatted into the instruction string.
2.  **Integration Testing:**
    - End-to-end test of the `POST /api/v1/profile` endpoint.
    - Start a chat session as a user with a specific profile (e.g., "severe lactose intolerance, tolerates fructans") and verify the LLM's responses adhere to that specific profile.

## Migration & Rollback
-   **Database:** The new JSONB column should be added with a `DEFAULT '{}'::jsonb` to ensure backwards compatibility with existing users.
-   **Logic:** The system prompt injection must be strictly conditional so that users without a profile receive the standard, general FODMAP guidance.