# Admin Page: Account Management & User Analytics

Add a full admin system with role-based access control, account management endpoints,
analytics queries, and a React frontend admin UI with data visualizations.

## Design Decisions

| Decision | Choice |
|---|---|
| Admin **access** model | `role` field on `User` (`"user"` / `"admin"`) — used **only** to gate admin access, not as a managed surface |
| Account **management** model | Status-based (`active` / `suspended`), reusing the **existing** `User.Status` field and `UpdateUserStatus` store method. The mock has no role-management UI. |
| Admin bootstrap | Seed via `ADMIN_EMAIL` env var at startup, applied with `AdminStore.SetUserRole`. Additionally, `registerHandler` checks whether the new user's email matches `ADMIN_EMAIL` and auto-promotes them — eliminating the race condition where a user registers *after* startup. Startup promotion is still performed for users who registered before the server started. |
| Database scope | **SQLite removed entirely.** PostgreSQL is the only store backend. The existing `Store` is renamed to **`ChatStore`** (user auth, conversations, messages, profiles). A new **`AdminStore`** interface (embeds `ChatStore`) adds admin-specific methods. `PostgresStore` implements both. The in-memory mock implements both for tests. |
| JWT changes | Add `role` claim to the JWT for client-side routing only. The server **never trusts the claim** — `adminRequired` re-looks-up the user's role in the store on every request (a demoted admin loses access immediately server-side; the stale claim only affects client UI until token refresh). |
| Token signature | **Do not** change `GenerateTokens(userID, secret)` (≈15 call sites across 5 test files). Add a sibling `GenerateTokensWithRole(userID, role, secret)`; the existing function delegates with `role="user"`. Non-breaking. |
| Suspended enforcement | `loginHandler`/`refreshHandler` currently block only `status=="deleted"`. Also block `"suspended"`. Add `GET /api/v1/auth/me` returning `{id,email,role,status}` so a logged-in app can discover suspension/role changes (the mock fakes this via localStorage; the real app has no such channel). |
| Suspension enforcement **scope** | Enforced at **token issuance only** (`loginHandler`/`refreshHandler`) plus the client-side `/me` overlay. `jwtAuth` validates only the signature and access tokens live **2h** ([jwt.go:11](../../auth/jwt.go#L11)) — so a user suspended mid-session **keeps a working access token for up to 2h** on data-plane endpoints (`/chat`, `/conversations`), and the overlay is client-side (bypassable). Accepted **≤2h window** for v1 (avoids a per-request DB status check in `jwtAuth`). |
| Token revocation | There is **none**. Consequence: admin **password reset does not invalidate** the user's existing JWTs (same root cause as the suspension window). Acceptable for v1; revisit with suspension if either must be immediate. |
| New admin SQL location | New admin queries go in **`auth/sql/*.sql`** embedded via `//go:embed` per CLAUDE.md (params `$1`/`$2` only, never `fmt.Sprintf`). NOTE: the existing `auth` package inlines all its SQL as Go string literals (it predates the rule), so the package will be **mixed style** — new admin queries follow the rule, old ones are left as-is. |
| Frontend layout | Shared admin sidebar over three pages: `/admin` (Dashboard), `/admin/users`, `/admin/conversations` |
| Charts | The only chart the mock draws is a **conversation-activity** bar chart (7/14/30-day toggles). A small inline SVG bar chart is sufficient; Recharts is **not** installed — do not add it. |
| User-facing role management | Out of scope — not present in the mock |
| Delete model | Admin delete = **hard** delete (cascade via existing FKs). Note this differs from user self-delete (`deleteUserHandler`), which is a **soft** delete (`status="deleted"`). Both are intentional. No `deleted` analytics bucket. **CASCADE confirmed present** — `user_profiles`, `conversations`, and `messages`→conversations all declare `ON DELETE CASCADE` in `internal/db/migrations/`, so `DELETE FROM users` cascades cleanly; no migration change needed. |
| Admin handler tests | The **in-memory mock store implements both `ChatStore` and `AdminStore`** so handler success paths are testable without a live Postgres. |
| Password reset | No email infrastructure — the handler **generates a random temporary password** server-side, bcrypt-hashes it, stores the hash via `ResetUserPassword`, and returns the plaintext to the admin in the JSON response. The admin communicates it to the user out-of-band. UI toast copy updated to reflect this (not "link sent"). |
| Top FODMAP Triggers / Recent Sign-ups | Recent Sign-ups: reuse `ListUsers` ordered by `created_at DESC`. Top Triggers needs message-content analysis — **deferred** (see Open Questions); dashboard ships without it. |
| Search query tracking | Deferred to future iteration |

> **Reconciled with existing code:** `User.Status` already exists (currently `active`/`deleted`)
> and `UpdateUserStatus` is already implemented in `auth/postgres_store.go`. This plan **adds the
> `suspended` value** and reuses that method rather than introducing new status plumbing. Only the
> `Role` field is genuinely new.

## Proposed Changes

### Backend: User Model & Auth (`fodmap-detector`)

---

#### [MODIFY] `auth/user.go`

Add `Role` field to `User` struct:
```go
Role string `json:"role"` // "user" or "admin"
```

---

#### [MODIFY] `auth/jwt.go`

Add `Role` field to `UserClaims`:
```go
type UserClaims struct {
    UserID string `json:"user_id"`
    Role   string `json:"role"`
    jwt.RegisteredClaims
}
```

Add a role-aware token generator **without breaking the existing signature** (≈15 call sites,
mostly tests, use the 2-arg form):
```go
// GenerateTokens keeps its signature and delegates with the default role.
func GenerateTokens(userID, secret string) (string, string, error) {
    return GenerateTokensWithRole(userID, "user", secret)
}

// GenerateTokensWithRole embeds the role claim.
func GenerateTokensWithRole(userID, role, secret string) (string, string, error)
```
Only `loginHandler`/`refreshHandler` (which know the user's role) call the new function; every
other caller stays untouched.

---

#### [MODIFY] `auth/store.go` → rename interface to `ChatStore`

Rename the existing `Store` interface to **`ChatStore`**. It keeps all current methods unchanged:
user auth (Create/Get/UpdateStatus), dietary profiles, conversations, and messages. All existing
callers (`postgres_store.go`, `server.go`, `chat_handler.go`, handlers, tests) update the type name —
this is a mechanical rename with no behavioral change.

```go
// ChatStore defines the interface for user auth, conversations, and messages.
type ChatStore interface {
    // ... all existing methods unchanged ...
}
```

---

#### [NEW] `auth/admin.go`

New file defining the **`AdminStore`** interface and its supporting types (`UserFilter`,
`UserDetail`, `ConversationSummary`, `UserAnalytics`, `ConversationAnalytics`, `DailyCount`).
`AdminStore` embeds `ChatStore` so a Postgres implementation satisfies both.

```go
// AdminStore extends ChatStore with admin-specific operations.
// Implemented by PostgresStore and the in-memory mock.
type AdminStore interface {
    ChatStore

    // Role management
    SetUserRole(ctx context.Context, userID string, role string) error

    // User admin
    ListUsers(ctx context.Context, offset, limit int, filter UserFilter) ([]*User, int, error)
    GetUserDetail(ctx context.Context, userID string) (*UserDetail, error)
    DeleteUserPermanently(ctx context.Context, userID string) error
    ResetUserPassword(ctx context.Context, userID string, hashedPassword string) error

    // Conversation admin
    ListAllConversations(ctx context.Context, offset, limit int, search string) ([]*ConversationSummary, int, error)

    // Analytics aggregates
    GetUserAnalytics(ctx context.Context) (*UserAnalytics, error)
    GetConversationActivity(ctx context.Context, days int) ([]DailyCount, error)
    GetConversationAnalytics(ctx context.Context) (*ConversationAnalytics, error)
}
```

Notes:
- `GetConversationThread`/`GetRecentSignups` are **not** new methods — the admin handlers reuse
  the existing `ChatStore.GetConversation` + `GetMessages` for a thread, and `ListUsers(0, N, {})` for recent signups.
- Removed vs. the original draft: `UpdateUserRole` (replaced by the simpler `SetUserRole`, used only by
  bootstrap), `GetUserGrowth`/`GetTopUsers`/`GetRetentionData` (not in the mock dashboard).

---

#### [MODIFY] `auth/postgres_store.go` — implements both `ChatStore` and `AdminStore`

- Add DB migration: `ALTER TABLE users ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'user'`
  (the `status` column migration already exists in `internal/db/migrations/`)
- Update `CreateUser` to persist role; update `GetUserByEmail` / `GetUserByID` to scan role
- `UpdateUserStatus` already exists — no change needed for suspend/unban
- Implement the `AdminStore` methods with PostgreSQL queries (all parameterized — never interpolate filter values):
  - `SetUserRole`: `UPDATE users SET role = $1 WHERE id = $2` (error if 0 rows)
  - `ListUsers`: `WHERE status != 'deleted' AND email ILIKE $search AND ($status = '' OR status = $status)`, **`ORDER BY created_at DESC`** (deterministic — also backs Recent Sign-ups), `LIMIT/OFFSET`; returns the page + total count. **Soft-deleted users are excluded** — self-delete leaves `status='deleted'` tombstone rows the admin UI (active/suspended only) can't act on. **No role filter** (not in the mock)
  - `GetUserDetail`: user row + `COUNT(conversations)` + `COUNT(messages)` + dietary-profile JSON (keys `diet_phase`/`intolerances`/`triggers`, as consumed by `DietaryProfileModal.tsx`). **If the user has no dietary profile**, return the profile key as `null` (not omitted) — the frontend renders an empty state when `profile === null`.
  - `DeleteUserPermanently`: `DELETE FROM users WHERE id = $1` (FKs cascade to conversations/messages/profile)
  - `ResetUserPassword`: `UPDATE users SET password = $1 WHERE id = $2`
  - `ListAllConversations`: JOIN users for email + `COUNT(messages)`, search across **conversation title and user email** (`ILIKE` across both), `ORDER BY updated_at DESC`, paginated
  - `GetUserAnalytics`: aggregate counts — total, active, suspended, all **excluding `status='deleted'`** (no `deleted` bucket). "Total Users" = non-deleted users; otherwise self-delete tombstones inflate the count
  - `GetConversationActivity`: `GROUP BY DATE(created_at) AT TIME ZONE 'UTC'` on conversations for last N days
  - `ListUsers` filter: the pattern `($status = '' OR status = $status)` uses empty string as "no filter." This works but is worth noting — consider whether `NULL` or a separate boolean flag would be clearer in a future iteration.
  - `GetConversationAnalytics`: total conversations, avg per user

---

#### [DELETE] `auth/sqlite_store.go`

Remove entirely. PostgreSQL is the only store backend.

---

#### [DELETE] `auth/sqlite_store_test.go`

Remove entirely.

---

#### [DELETE] `auth/sqlite_conversation_test.go`

Remove entirely.

---

#### [MODIFY] `go.mod`

Run `go mod tidy` after removing the SQLite files to drop the `modernc.org/sqlite` dependency
and its transitive deps.

---

#### [MODIFY] `server/mock_store.go` — implements both `ChatStore` and `AdminStore`

Implement all `AdminStore` methods **in-memory** (not stubs) so `admin_handler_test.go` can exercise
real success paths: filter/paginate the `users` map, count conversations/messages, set role, hard-delete
(also drop the user's conversations/messages), and compute the analytics aggregates from the maps.
The mock satisfies `auth.AdminStore` (which embeds `auth.ChatStore`), so it works for all handler tests.

---

### Backend: Admin API Endpoints (`fodmap-detector`)

---

#### [NEW] `server/admin_handler.go`

New handler file. **Handler names are prefixed `admin*` to avoid collisions with the existing
`listConversationsHandler`/`getConversationHandler` in `server/conversation_handler.go`.**

- `adminRequired`: middleware with signature **`func(next http.Handler) http.Handler`** (matching
  the `chain()` convention). Reads `userID` from context (set by `jwtAuth`), looks the user up
  via `s.userStore` (now typed `auth.AdminStore` — see server.go changes; **no per-request type
  assertion**), and 403s unless `role == "admin"`. Does **not** trust the JWT claim.
- `adminListUsersHandler`: `GET /api/v1/admin/users?search=&status=&page=&limit=` (no `role` param)
- `adminGetUserHandler`: `GET /api/v1/admin/users/{id}` — counts **and dietary profile** (drives the detail modal)
- `adminUpdateUserStatusHandler`: `PUT /api/v1/admin/users/{id}/status` (suspend/unban; reuses `UpdateUserStatus` from `ChatStore`). **Reject suspending the calling admin** (self-protection).
- `adminDeleteUserHandler`: `DELETE /api/v1/admin/users/{id}`. **Reject deleting the calling admin.**
  The frontend must fetch user detail first and display counts in the confirmation modal before sending DELETE.
- `adminResetPasswordHandler`: `POST /api/v1/admin/users/{id}/reset-password` — **no request body needed**.
  The handler generates a `crypto/rand` 16-char temporary password, bcrypt-hashes it, calls
  `AdminStore.ResetUserPassword(ctx, id, hashedPassword)`, and returns `{"temporary_password": "..."}` to the admin.
  The admin communicates it to the user out-of-band.
- `adminListConversationsHandler`: `GET /api/v1/admin/conversations?search=&page=&limit=`
- `adminGetConversationHandler`: `GET /api/v1/admin/conversations/{id}` — read-only thread via existing
  `ChatStore.GetConversation` + `GetMessages`, **without** the per-user ownership check that the user-facing handler enforces
- `adminAnalyticsOverviewHandler`: `GET /api/v1/admin/analytics/overview` (total/active/suspended + conversation count + recent signups via `ListUsers`)
- `adminConversationActivityHandler`: `GET /api/v1/admin/analytics/activity?days=30`

All admin handlers call `AdminStore` methods directly on `s.userStore` (typed `auth.AdminStore`) —
no per-handler type assertion, no cached store in the request context. Transcript export reuses the
existing `conversation_export_handler.go`. Removed vs. the original draft: `updateUserRoleHandler`,
`userGrowthHandler`, `topUsersHandler`, `retentionHandler`.

**Pagination & search input handling** (apply consistently across `adminListUsersHandler` /
`adminListConversationsHandler` / `adminConversationActivityHandler`):
- Convert `page`→`offset` as `offset = (page-1)*limit`; default and clamp `limit` (reject 0, cap at a max
  e.g. 100) and treat `page < 1` as page 1.
- For `ILIKE` search, wrap the term with `%…%` before binding (in Go, or `'%'||$1||'%'` in the `.sql`),
  otherwise it matches exact strings only.
- Validate/clamp `days` in `adminConversationActivityHandler` (reject ≤0, cap at e.g. 90) to avoid a
  pathological range scan.

---

#### [NEW] `server/admin_handler_test.go`

Tests for all admin endpoints covering:
- Non-admin users get 403
- Valid admin requests succeed
- Pagination and status filtering work
- Conversation list/thread endpoints return data
- Edge cases (delete self, suspend self)

---

#### [MODIFY] `server/server.go`

- Update `Server.userStore` **and `Config.UserStore`** field types from `auth.Store` to `auth.AdminStore`
  (since `PostgresStore` always implements `AdminStore`, and `AdminStore` embeds `ChatStore`, this is the
  only store type now)
- Remove the separate `adminStore` field — `userStore` itself is the `AdminStore`
- **Compile impact:** widening `Config.UserStore` means **every Server-constructing test must supply an
  `AdminStore`**, not merely a `ChatStore`. The in-memory mock already will (it implements `AdminStore`),
  but `mockErrorStore` in `server/conversation_handler_test.go` embeds `auth.Store` — see the
  `[MODIFY]` below.
- Register the `GET /api/v1/auth/me` route (JWT-protected) and the admin routes in `Handler()`:
```go
mux.Handle("GET /api/v1/auth/me", jwtAuth(s.jwtSecret)(http.HandlerFunc(s.meHandler)))

// Admin endpoints (JWT → adminRequired middleware → handler)
// adminRequired is func(http.Handler) http.Handler — compatible with chain().
adminMid := func(h http.HandlerFunc) http.Handler {
    return chain(http.HandlerFunc(h), jwtAuth(s.jwtSecret), s.adminRequired)
}
mux.Handle("GET /api/v1/admin/users", adminMid(s.adminListUsersHandler))
mux.Handle("GET /api/v1/admin/users/{id}", adminMid(s.adminGetUserHandler))
mux.Handle("PUT /api/v1/admin/users/{id}/status", adminMid(s.adminUpdateUserStatusHandler))
mux.Handle("DELETE /api/v1/admin/users/{id}", adminMid(s.adminDeleteUserHandler))
mux.Handle("POST /api/v1/admin/users/{id}/reset-password", adminMid(s.adminResetPasswordHandler))
mux.Handle("GET /api/v1/admin/conversations", adminMid(s.adminListConversationsHandler))
mux.Handle("GET /api/v1/admin/conversations/{id}", adminMid(s.adminGetConversationHandler))
mux.Handle("GET /api/v1/admin/analytics/overview", adminMid(s.adminAnalyticsOverviewHandler))
mux.Handle("GET /api/v1/admin/analytics/activity", adminMid(s.adminConversationActivityHandler))
```

---

#### [MODIFY] `server/conversation_handler_test.go`

`mockErrorStore` (line ~216) embeds `auth.ChatStore` for interface satisfaction. After widening
`Config.UserStore` to `auth.AdminStore`, change the embedded interface to **`auth.AdminStore`** so the
stub still satisfies the field type. It's a one-word change — the embedded interface stays nil at
runtime, and these tests never call admin methods through it, so no further stubbing is needed.

---

#### [MODIFY] `server/auth_handler.go`

- Update `registerHandler` to set `user.Role = "user"` on creation; **also check** if the new user's email matches `ADMIN_EMAIL` (from config) and if so, immediately call `SetUserRole(ctx, user.ID, "admin")` — this eliminates the bootstrap race condition where a user registers after startup and isn't promoted until restart
- Update `loginHandler`/`refreshHandler` to call `GenerateTokensWithRole(user.ID, user.Role, secret)`
  (existing `GenerateTokens` callers — all tests — stay unchanged)
- Add `"suspended"` to the rejection check in `loginHandler` **and** `refreshHandler` (they currently
  block only `"deleted"`), returning 401/403 so a suspended user cannot obtain or refresh tokens
- Include `role` (and `status`) in `authUserResponse`
- Add `meHandler` (`GET /api/v1/auth/me`): look up the user from the context `userID`, return
  `{id, email, role, status}`. This is the channel the running app uses to detect suspension / role change.
  **Error behavior:** if the user no longer exists (hard-deleted), return **401 Unauthorized** with a
  response body `{ "error": "user not found" }` — this causes the frontend to clear tokens and redirect
  to login, which is the correct UX for a deleted account. A suspended user still exists, so `/me`
  returns 200 with `status: "suspended"`; the frontend then shows the suspended overlay.
  **File location:** place `meHandler` in `auth_handler.go` for proximity to other JWT-protected routes.

---

#### [MODIFY] `cli/serve.go`

- **Remove** `--store-type` flag and `--db` flag — Postgres is the only backend now.
  `postgres-dsn` becomes **required** (error if empty).
- Add `admin-email` CLI flag: `serveCmd.Flags().String("admin-email", "", "Email of the user to promote to admin on startup")`
- Bind it explicitly: `_ = viper.BindEnv("admin-email", "ADMIN_EMAIL")` (viper's `BindPFlags` handles
  the flag; `BindEnv` maps the underscore env var `ADMIN_EMAIL` to the hyphenated key `admin-email`)
- After store init, if `admin-email` is set: `GetUserByEmail` → `store.SetUserRole(ctx, user.ID, "admin")`.
  Log (don't fail) if the email isn't registered yet — the user can register and be auto-promoted
  via `registerHandler` (see auth_handler.go changes). Startup promotion handles the case where the
  user registered before the server started.

---

#### [MODIFY] `.env.example`

Add `ADMIN_EMAIL=` entry.

---

#### [MODIFY] `start.sh`

CLAUDE.md requires `start.sh` stays working after flag/service changes. Concretely:
- **Remove `STORE_TYPE=postgres`** from the server launch ([start.sh:93](../../start.sh#L93)) — the
  `--store-type` flag no longer exists.
- Optionally export `ADMIN_EMAIL` before the `go run . serve` line so the local stack bootstraps an
  admin. With the `registerHandler` auto-promotion, the user can register either before or after startup
  and will be promoted automatically.

---

#### [MODIFY] `README.md` (+ relevant `docs/`)

- Remove `sqlite_store.go` from the source-tree listing ([README.md:82](../../README.md#L82)) and any
  prose describing SQLite/`--store-type`/`--db` as a backend option (Postgres is now required).
- Document the admin UI: `ADMIN_EMAIL` bootstrap, the Postgres-only requirement, and the
  `/api/v1/admin/*` + `/api/v1/auth/me` endpoints.

---

### Frontend: Admin UI (`fodmap-chat`)

---

#### [NEW] `src/api/admin.ts`

API client functions for all admin endpoints:
- `fetchUsers(params)`, `fetchUser(id)`, `updateUserStatus(id, status)`, `deleteUser(id)`, `resetPassword(id)`
- `fetchConversations(params)`, `fetchConversation(id)`
- `fetchAnalyticsOverview()`, `fetchConversationActivity(days)`

---

#### [NEW] `src/types/admin.ts`

TypeScript types for admin API responses:
- `AdminUser`, `AdminUserDetail` (counts + dietary profile), `UserFilter` (search + status), `PaginatedUsers`
- `AdminConversationSummary`, `AdminConversationThread`, `PaginatedConversations`
- `AnalyticsOverview` (total/active/suspended + conversation count + recent signups), `DailyActivity`

---

#### [MODIFY] `src/hooks/useAuth.ts`

- Add `role` (and `status`) to the user state (`{ id, email, role, status }`)
- Add `isAdmin` computed getter
- Derive `role` by **decoding the JWT** rather than reading `response.user.role` — `refreshHandler`
  returns tokens only (no `user`), so role must survive a refresh
- Expose a `refreshMe()` that calls `GET /api/v1/auth/me` and updates `{role, status}`

---

#### [NEW] `src/components/AdminLayout.tsx`

Shared admin layout component with:
- Sidebar navigation under an "Overview" group: Dashboard, Users, Conversations
- A "System" group with disabled placeholders (Ingredient DB, API Health) marked "Soon", per the mock
- Back-to-app link + admin identity footer
- Admin branding/header
- `<Outlet />` for page content

---

#### [NEW] `src/pages/admin/users.tsx`

User management page with:
- Search bar (email filter)
- Status filter chips: All / Active / Suspended (no role filter)
- Paginated user table (user, status, conversation count, joined, actions)
- Row actions: View, Suspend/Unban, and a menu (View profile, Reset password, Export data, Delete account)
- User-detail modal: conversation/message counts + **dietary profile** (diet phase, intolerances, triggers), plus View-conversations / Export / Suspend-Reactivate actions
- Confirmation modal for delete (copy: permanent, cannot be undone)

---

#### [NEW] `src/pages/admin/dashboard.tsx`

Dashboard page (route `/admin`) matching the mock:
- Summary cards: Total Users, Active Users, Conversations, **Suspended**
- **Conversation Activity** chart: conversations/day with 7D / 14D / 30D toggles (inline SVG bar chart; Recharts optional)
- **Recent Sign-ups** list (latest users, from the overview payload)
- **Top FODMAP Triggers** — **deferred** (needs message-content analysis). Omit from v1; the card slot
  shows a **greyed-out card with "Coming soon" text** and a subtle lock icon to preserve the mock's layout.

(Removed from the original draft: signup-growth area chart, status pie chart, top-users table,
retention table — none appear in the mock.)

---

#### [NEW] `src/pages/admin/conversations.tsx`

Conversations browser matching the mock:
- Search bar (user email / conversation title)
- Paginated table (user, conversation, message count, updated)
- Row click opens a read-only thread modal; "Export transcript" reuses existing export

---

#### [MODIFY] `src/App.tsx`

Add admin routes:
```tsx
<Route element={<AdminRoute><AdminLayout /></AdminRoute>}>
  <Route path="/admin" element={<AdminDashboard />} />
  <Route path="/admin/users" element={<AdminUsers />} />
  <Route path="/admin/conversations" element={<AdminConversations />} />
</Route>
```

`AdminRoute` checks `isAdmin` from auth state and **redirects non-admins to `/`** (the main chat page). A non-admin who manually navigates to `/admin` sees a brief redirect with no error message — they simply land on the chat interface.

---

#### [MODIFY] `src/components/AppLayout.tsx`

- Add an "Admin" navigation button in the sidebar (only visible when `isAdmin` is true), linking to `/admin`.
- On mount **and periodically** (every 5 minutes), call `refreshMe()`; if `status === "suspended"`,
  render the **suspended overlay** (the mock's `suspWrap`/`suspCard`) and block the app. This makes
  "suspend a user → app shows overlay" real instead of the mock's localStorage fake. Additionally,
  add an **axios response interceptor** that calls `/me` on any 401/403 response — this catches
  mid-session suspensions between the periodic polls. (A 401 from `/me` after a hard delete sends
  the user back to login.)

---

## Open Questions

- **Top FODMAP Triggers widget** — *deferred*. Needs message-content analysis; v1 ships a
  placeholder, matching the deferred search-query tracking.

## Resolved Questions

- **Password reset flow** — *resolved*: no email infrastructure. The handler generates a random
  temporary password server-side (`crypto/rand`, 16 chars), bcrypt-hashes it, stores the hash,
  and returns the plaintext in the response. The admin communicates it out-of-band. Frontend
  toast copy updated from "link sent" to "Temporary password generated".

- **Admin self-protection** — *resolved*: the status/delete handlers reject the calling admin acting on
  their own account (prevents accidental lockout). Revisit only if multi-admin workflows need it relaxed.

- **Store interface split** — *resolved*: existing `Store` renamed to `ChatStore`; new `AdminStore`
  interface (embeds `ChatStore`) implemented by `PostgresStore` and mock. SQLite removed entirely.
  Rename is cosmetic but adopted for naming clarity.

- **SQLite removal** — *resolved*: SQLite was a second-class citizen (no admin, no pgvector search,
  no menutracking pipeline). Removed `sqlite_store.go`, `sqlite_store_test.go`,
  `sqlite_conversation_test.go`, and the `modernc.org/sqlite` dependency. `--store-type` and `--db`
  flags removed; `--postgres-dsn` is now required. **Blast radius confirmed clean** (`findReferences`
  on `NewSQLiteStore`): the only callers are the three deleted files plus `cli/serve.go:81` (rewritten
  here) — no stragglers in `server/`, `menutracking/`, or `search/`.

- **Token signature** — *resolved*: avoided via `GenerateTokensWithRole`; suspended users are
  enforced at login/refresh + surfaced through `GET /api/v1/auth/me`.

- **`ADMIN_EMAIL` race condition** — *resolved*: eliminated via `registerHandler` auto-promotion.
  When a user registers with the `ADMIN_EMAIL` address, `registerHandler` immediately calls `SetUserRole`
  to promote them. Startup promotion (`cli/serve.go`) handles users who registered before the server
  started. No restart is needed either way.

- **Suspension/reset immediacy** — *resolved (accepted tradeoff)*: with no token-revocation list,
  suspension is enforced only at token issuance + a client overlay, and password reset doesn't kill
  live sessions. A suspended user's existing access token works on data-plane endpoints for up to its
  2h TTL. The client-side `/me` overlay is reinforced with **periodic polling (every 5 min)** and an
  **axios 401/403 interceptor** to reduce the detection window. Accepted for v1; revisit (status check
  in `jwtAuth`, shorter access-token TTL, or a revocation list) only if immediacy becomes a requirement.

- **`/me` error behavior** — *resolved*: if the user no longer exists (hard-deleted), `meHandler`
  returns **401** with `{ "error": "user not found" }`, causing the frontend to clear tokens and
  redirect to login. A suspended user gets 200 with `status: "suspended"`, triggering the overlay.

- **Password reset rate limiting** — *noted*: no rate limit or cooldown on `adminResetPasswordHandler`
  for v1. An admin could repeatedly reset a user's password, each time invalidating the previous one.
  Acceptable for v1 since only admins can call it; revisit if multi-admin audit logging is added.

- **Pagination limits** — *resolved*: `adminListUsersHandler` and `adminListConversationsHandler`
  cap `limit` at **100** (reject 0, default to 20). `adminConversationActivityHandler` clamps `days`
  to 1–90 (reject ≤0). Prevents unbounded queries.

- **Admin SQL style** — *resolved*: new admin queries live in `auth/sql/*.sql` (`//go:embed`, params
  only) per CLAUDE.md, even though the existing `auth` package inlines its SQL. The package will be
  mixed-style; old inline queries are left untouched. Add `//nolint:embedsql` or a brief comment
  on the old inline queries so future contributors don't emulate the legacy pattern by accident.

- **`adminRequired` middleware signature** — *resolved*: `func(next http.Handler) http.Handler`,
  compatible with `chain()`. No type-assertion needed since `userStore` is always `AdminStore`.

## Verification Plan

### Automated Tests
```bash
# Backend
cd fodmap-detector
go mod tidy   # after removing SQLite files
go test ./auth/... ./server/... -v -count=1

# Frontend
cd fodmap-chat
npm run check
```

### Manual Verification
1. Start the backend with `POSTGRES_DSN` and `ADMIN_EMAIL` set
2. Register a user, verify they get `role: "user"` in JWT
3. Register a user **with the `ADMIN_EMAIL` address** — verify they are **auto-promoted to admin** without restart
4. Set `ADMIN_EMAIL` to a pre-registered user's email, restart server, verify promotion (startup path)
5. Login as admin; verify the JWT decodes to `role: "admin"` and `GET /api/v1/auth/me` returns `role:"admin", status:"active"`
6. Navigate to `/admin` in the frontend, verify sidebar (Dashboard / Users, Conversations) and pages load; verify non-admins are redirected to `/`
7. Test user management actions: suspend/unban (status), reset password (verify temporary password returned),
   delete (verify detail modal shows counts before confirmation); confirm delete/suspend on your own admin account is rejected
8. Open the Conversations tab, search, and open a thread (read-only) + export transcript
9. Verify the dashboard cards (incl. Suspended) and the conversation-activity chart render; verify the Top FODMAP Triggers card shows "Coming soon"
10. Suspend a user in another session; within 5 minutes (or on next API 401/403), the app shows the suspended overlay; verify login/refresh are rejected for that user
11. Hard-delete a user via admin; in that user's session, verify `GET /api/v1/auth/me` returns 401 and the frontend redirects to login
12. Verify non-admin users cannot access `/admin` routes or API endpoints (403), and that a demoted admin loses access immediately (server re-checks the store, ignoring the stale JWT claim)
13. Verify server fails to start if `POSTGRES_DSN` is not provided
