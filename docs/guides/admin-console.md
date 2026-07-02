# Role-Based Access Control (RBAC) & Admin Console

## RBAC Model
Users are mapped to `'user'` or `'admin'` roles. The role is claim-based in JWT for client routing, but re-verified against the database on every admin API request for server security.

## Admin CLI Flag
The `--admin-email` (or `ADMIN_EMAIL` env var) flag promotes a specific registered user to the `admin` role on startup.

## Console Pages

The admin console (in the `fodmap-chat` frontend, under `/admin`) has five pages:

- **Dashboard** — user/conversation analytics and activity chart.
- **Users** — search, suspend/unban, delete, password reset.
- **Conversations** — browse and inspect all conversation transcripts.
- **Ingredient DB** — FODMAP catalog CRUD, search-test, reseed.
- **Scraper Pipeline** (`/admin/restaurants`) — live view of the restaurant
  menu pipeline: clickable status-rollup cards (double as filters), tier-mix /
  failure-reason / River-queue panels, a searchable restaurant table with
  per-row Discover / Scrape / Retry triggers, and expandable detail rows
  (menu URLs, `last_error`, extraction tier). Auto-refreshes every 5s. Backed
  by the restaurant admin endpoints, which require the server to run with
  `--enable-pipeline`.

## API Reference

For the full list of admin and ingredient administration API endpoints, along with request/response schemas and curl examples, see the [API Reference Guide](api-reference.md#admin-endpoints). The scraper pipeline endpoints (list/stats/discover/scrape/retry) are documented there under **Scraper Pipeline Administration**.
