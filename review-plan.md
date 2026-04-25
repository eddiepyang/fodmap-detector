 1 # FODMAP Domain Expert Agent Implementation Plan
    2
    3 ## Objective
    4 Create a specialized agent logic integrated into the backend to act as a dietary logic validator. When a user
      queries about food reviews, the backend will process the review text to estimate the underlying ingredients
      and then flag any high-FODMAP components based on general guidelines (e.g., Monash University standards).
    5
    6 ## Key Files & Context
    7 - **Target Repository:** `~/projects/fodmap-detector` (The backend Go application).
    8 - **Core File:** `server/server.go` (and related LLM routing/generation files).
    9 - This feature involves updating the backend AI generation logic, not the frontend UI repository.
   10
   11 ## Proposed Solution: Backend Service Update
   12
   13 This requirement (parsing food reviews to estimate ingredients and validating them against FODMAP rules) must
      be implemented within the backend application (`fodmap-detector`). The frontend (`fodmap-chat`) relies on the
      backend to stream AI responses based on the chosen restaurant/review.
   14
   15 We will create a plan to update the backend service to incorporate this extraction and validation logic.
   16
   17 ### 1. Identify Backend Entry Points
   18 Investigate the `~/projects/fodmap-detector` repository to find the service or handler responsible for
      processing chat requests and interacting with the LLM.
   19
   20 ### 2. Update AI Prompt/Logic
   21 Modify the backend logic to execute a two-step (or chained) process when handling user queries about food
      reviews:
   22     *   **Step 1: Ingredient Extraction:** Instruct the LLM to analyze the food review text and estimate the
      hidden/likely ingredients (e.g., sauces, broths, seasonings).
   23     *   **Step 2: FODMAP Validation:** Cross-reference the extracted ingredients against a known set of FODMAP
      guidelines (e.g., flagging garlic, onion, wheat).
   24
   25 ### 3. Format the Response
   26 Ensure the backend API returns this information in a structured format (e.g., JSON stream) that the frontend
      `fodmap-chat` application can parse and display.
   27
   28 ## Implementation Steps
   29
   30 1.  **Switch Context:** We need to transition our focus to the `~/projects/fodmap-detector` repository to
      implement this feature.
   31 2.  **Analyze Backend:** Analyze the Go backend (specifically files like `server/server.go`) to locate the
      chat generation logic.
   32 3.  **Implement Logic:** Update the LLM prompt or agent chain in the backend to perform the extraction and
      validation.
   33
   34 ## Verification & Testing
   35
   36 1.  **Backend Unit/Integration Tests:** Write or update tests in the `fodmap-detector` backend repository to
      verify the prompt logic correctly identifies ingredients and flags FODMAPs given sample review text.
   37 2.  **API Verification:** Start the backend server and send a curl request (or use a tool like Postman) to the
      chat generation endpoint with a sample review to ensure the structured JSON response contains the correct
      ingredient and FODMAP data.
   38 3.  **Frontend Integration Test:** Once the backend is returning the correct data, verify that the
      `fodmap-chat` frontend application correctly parses and displays the FODMAP warnings in the UI.