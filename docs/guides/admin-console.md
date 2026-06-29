# Role-Based Access Control (RBAC) & Admin Console

## RBAC Model
Users are mapped to `'user'` or `'admin'` roles. The role is claim-based in JWT for client routing, but re-verified against the database on every admin API request for server security.

## Admin CLI Flag
The `--admin-email` (or `ADMIN_EMAIL` env var) flag promotes a specific registered user to the `admin` role on startup.

## API Reference

For the full list of admin and ingredient administration API endpoints, along with request/response schemas and curl examples, see the [API Reference Guide](api-reference.md#admin-endpoints).
