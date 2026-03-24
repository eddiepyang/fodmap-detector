---
description: Run the local CI checks (linting and testing)
---
Run the full local CI check to match the GitHub Actions pipeline:

// turbo
1. Run `golangci-lint run ./...` — if golangci-lint is not installed, skip and note it

// turbo
2. Run `go test ./...`

Report a clear pass/fail summary for each step. If anything fails, show the relevant output and suggest a fix.
