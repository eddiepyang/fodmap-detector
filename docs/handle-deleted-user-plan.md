# Handle Deleted Users Plan

## Objective
Add backend support for user status updates and an API endpoint for users to delete their accounts.

## Implementation Steps
1. Add `UpdateUserStatus(ctx context.Context, userID string, status string) error` to the `Store` interface in `auth/store.go`.
2. Implement `UpdateUserStatus` in `auth/sqlite_store.go` and `auth/postgres_store.go`.
3. Update `server/mock_store.go` to include the new interface method.
4. Update `server/auth_handler.go`:
   - `loginHandler` and `refreshHandler` should reject users with `status == "deleted"`.
   - Add a `deleteUserHandler` to handle `DELETE /api/v1/auth/user`.
5. Update `server/server.go` to map the new endpoint.

## Verification & Testing
- Add tests to ensure deleted users cannot log in.