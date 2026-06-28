# Role-Based Access Control (RBAC) & Admin Console

## RBAC Model
Users are mapped to `'user'` or `'admin'` roles. The role is claim-based in JWT for client routing, but re-verified against the database on every admin API request for server security.

## Admin CLI Flag
The `--admin-email` (or `ADMIN_EMAIL` env var) flag promotes a specific registered user to the `admin` role on startup.

## API Endpoints (`/api/v1/admin/*`)

- `GET /api/v1/admin/users` - Lists active/suspended users.
- `GET /api/v1/admin/users/{id}` - Returns user details, message counts, and saved dietary profile.
- `PUT /api/v1/admin/users/{id}/status` - Toggles user account status (`active` / `suspended`).
- `DELETE /api/v1/admin/users/{id}` - Performs permanent cascading delete of user's profile, conversations, and messages.
- `POST /api/v1/admin/users/{id}/reset-password` - Resets password to a secure temporary bcrypt hash.
- `GET /api/v1/admin/conversations` - Lists user chat sessions.
- `GET /api/v1/admin/conversations/{id}` - Reads complete transcript messages.
- `GET /api/v1/admin/analytics/overview` - Fetches total, active, and suspended user counts and signups.
- `GET /api/v1/admin/analytics/activity` - Fetches daily conversation volume.

## Ingredient Admin (`/api/v1/admin/ingredients/*`)

- `GET /api/v1/admin/ingredients` - Lists ingredients with optional filters and pagination.
- `GET /api/v1/admin/ingredients/stats` - Returns aggregate counts by FODMAP level and group.
- `GET /api/v1/admin/ingredients/search-test` - Runs a semantic search against the ingredient catalog.
- `GET /api/v1/admin/ingredients/{name}` - Returns a single ingredient by name.
- `POST /api/v1/admin/ingredients` - Creates a new ingredient (rejects duplicates).
- `PUT /api/v1/admin/ingredients/{name}` - Updates an existing ingredient.
- `DELETE /api/v1/admin/ingredients/{name}` - Deletes an ingredient from the catalog.
- `POST /api/v1/admin/ingredients/reseed` - Re-upserts the static FODMAP database into the store.
