# Plan: Rules Compliance Remediation

**Status:** Proposed

## Context

A codebase-wide review against `.rules/` found 117 Go files with violations
spanning every rule category. This plan phases the remediation so each phase is
independently shippable, verifiable with `make check`, and reviewable in
isolation. Bugs with correctness impact ship first; mechanical style sweeps
ship last.

**Caveat on the "117" metric (treat as soft, not a target):** the count is
inflated by (a) confirmed false positives — see R7 (the `uuid.MustParse`
constants) and R4 — and (b) Phase 0, which resolves two whole categories by
**revising the rules** (CLI `fmt` output, terminal HTTP writes) rather than
changing code. Some "violations" disappear because the rubric changes, not the
codebase. Conversely, Phase 6's count is *under*stated (see R8). Do not treat
"reduced violation count" as the success criterion; per-phase `make check` +
review is the real bar.

## Decisions (settled during planning)

- **CLI logging**: revise `.rules/logging.md` to permit `fmt.Printf`/`fmt.Println`
  for direct user output in `cli` commands; restrict slog to diagnostic/log
  messages. CLI user-facing output is **not** a logging violation.
- **HTTP handler write errors**: add a rule to `.rules/errors.md` allowing
  `_ = json.NewEncoder(w).Encode(...)` (and similar terminal HTTP writes) with a
  justifying comment, since the connection is often already gone and the error
  cannot be meaningfully acted upon.
- **Plan storage**: `docs/plans/rules-compliance-remediation-plan.md`.

## Phases

### Phase 0 — Rule revisions

Two rule-file edits that reclassify currently-flagged patterns as compliant.
Must land first so subsequent phases measure against the updated rules.

1. `.rules/logging.md` — add a CLI exemption:
   - `fmt.Printf`/`fmt.Println` is permitted for **direct user output** in
     `cli` commands (the thing the user invoked the command to see).
   - slog remains required for all diagnostic/log messages (warnings, errors,
     progress that isn't the command's product output).
   - Server and library code: slog only, no change.

2. `.rules/errors.md` — add the terminal-HTTP-handler rule:
   - `_ = json.NewEncoder(w).Encode(...)` / `_ = w.Write(...)` /
     `_, _ = fmt.Fprintf(w, ...)` is permitted in HTTP handlers once the
     response has begun (status and headers written), with a one-line
     justifying comment. The error cannot be surfaced to the client after
     headers are flushed.
   - Before the first write, errors must be handled (e.g. via `respondError`).

### Phase 1 — Correctness bugs (highest priority)

Shippable independently. Each fix is small and has a clear test signal.

**Sequencing note:** items 1–3 (the `serve.go` context bugs, esp. the 30ns
timeout) are one-line, zero-risk fixes and should ship as their own tiny PR
**immediately** — do not gate them behind item 5, which is "the largest single
change in Phase 1" with query-plan risk. Bundling a critical bugfix with a risky
refactor defeats the "correctness ships first" intent. Item 5 should be its own
PR. Also reconsider whether Phase 9 (goroutine lifetime) belongs here — a
fire-and-forget goroutine with no shutdown signal is a correctness/leak bug, not
a cosmetic last-mile item (see Phase 9 note).

1. **`cli/serve.go:180,189`** — `context.WithTimeout(context.Background(), 30)`
   is 30 nanoseconds, not 30 seconds. Change to `30*time.Second`. Import `time`.
   - Verify: `make check`; manual `go run . serve` shutdown still drains within
     ~30s.
   - Risk: if any caller relied on the near-instant timeout (unlikely — the
     current 30ns effectively makes `Stop` non-blocking), they now block up to
     30s. Acceptable; that's the intent.

2. **`cli/serve.go:163`** — `context.Background()` → `cmd.Context()` so SIGTERM
   propagates to pipeline startup.

3. **`cli/serve.go:183`** — `_ = pipelineResult.Stop(stopCtx)` — add justifying
   comment (per `.rules/go-style.md`, `_ = x.Close()` is fine in error cleanup;
   make the intent explicit).

4. **`menutracking/workers.go:288`** — inlined `INSERT INTO
   menutracking_dead_letter` SQL. Move to
   `menutracking/store/sql/insert_dead_letter.sql`, embed via `//go:embed`, read
   at package init into an exported `var InsertDeadLetterSQL string`.
   - Verify: `make check`; existing `menutracking` tests pass.

5. **`fodmap/store/postgres.go:35`** — `text/template` SQL rendering. Replace
   with static `.sql` files per `.rules/sql.md`. The current `sql/*.sql` files
   use `{{.Where}}`/`{{.LimitArg}}` template placeholders; refactor the
   `List`/`Count` query to use static SQL with a fixed `WHERE` clause shape and
   parameter positions, eliminating `buildListWhere` (`postgres.go:391,396,401,
   407`). This is the largest single change in Phase 1.
   - Approach: use a fixed parameter ordering (search, level, group, limit,
     offset) and always bind all five (pass `nil`/zero for unused filters).
     This makes positional `$N` parameters stable. Document the ordering in
     the `.sql` file header. See G10 in Risks and Gaps.
   - Risk: changing the query shape can change plans. Run `EXPLAIN` on the
     before/after for the three filter combinations.
   - Gaps: `fodmap/store/sql/` already has 12 files; this may add or merge
     some. Update `docs/guides/database-schema.md` if the file inventory
     changes. Also see R2 below.

6. **Library `panic`s** — replace with returned errors (but see G1/G2 in
   Risks and Gaps before acting):
   - `menutracking/schema.go:45,49` — `panic` on `json.Marshal` failure of a
     reflection-generated schema from a static type. Arguably infallible —
     decide whether to keep+document or return error.
   - `fodmap/store/postgres.go:368` — `mustRead` panics on embed-file read
     failure, which is unreachable (compile-time `//go:embed` guarantees).
     **Recommend**: replace `mustRead` with direct `//go:embed` string vars
     (`var getSQL string //go:embed sql/get.sql`), eliminating the function
     and the panic.
   - ~~`scraper/scraper.go:374`, `search/weaviate.go:1215` — `uuid.MustParse`~~
     **FALSE POSITIVE — drop from plan, do not change.** Verified: both are
     package-level **static literal** namespace UUIDs (`scraper.go:374`
     `businessNamespace = uuid.MustParse("f0d6c8a0-…")`; `weaviate.go:1215`
     `regUpdateNS = uuid.MustParse("a3c8e6d0-…")`; same for `cli/scrape.go:268`
     `menuCollectionNS`). This is the `regexp.MustCompile` idiom — the input is a
     compile-time constant, never externally derived. `MustParse` on a constant
     is correct; converting to `uuid.Parse` + returned error is wrong. See R7.

### Phase 2 — `errors.Is` / `errors.As` sweep

Mechanical, low-risk, high-value.

- `auth/postgres_store.go:94,119,134,225,343` — `err == sql.ErrNoRows` →
  `errors.Is(err, sql.ErrNoRows)`
- `cli/scrape.go:243` — `err != scraper.ErrNeedVision` →
  `errors.Is(err, scraper.ErrNeedVision)` (inverted)
- `cli/root.go:34` — `err.(viper.ConfigFileNotFoundError)` →
  `errors.As(err, &notFound)`
- `cli/menutracking_migrate_test.go:86` — `strings.Contains(err.Error(), ...)`
  → `errors.Is`/`errors.As` against a sentinel. **Requires introducing
  `ErrInvalidCron` in `cli/menutracking_migrate.go`** — a small new public API
  surface, not a pure mechanical rename. See G8 in Risks and Gaps.

Verify: `make check`.

### Phase 3 — `interface{}` → `any` sweep

Pure mechanical rename. Use `gofmt -r 'interface{} -> any'` or a per-file sed
pass. Repo-wide `grep -n 'interface{}' **/*.go` to catch any the review missed.

Files (non-exhaustive):
- `server/admin_handler.go:71,246,281,304`
- `server/chat_handler_test.go:100`
- `scraper/jsonld_extractor.go:38,106,137,154,191,217`
- `search/weaviate.go:1132,1162,1258,1272`
- `auth/jwt.go:65`
- `fodmap/store/postgres.go:124,416`

Verify: `make check`.

### Phase 4 — Logging: CLI diagnostic messages → slog

Per the revised rule (Phase 0), only **diagnostic** `fmt` calls move to slog.
Direct user output stays as `fmt.Printf`/`fmt.Println`.

Files (diagnostic only):
- `cli/root.go:35` — `fmt.Printf("Warning: ...")` → `slog.Warn`
- `cli/root.go:42` — `fmt.Println(err)` → `slog.Error`
- `cli/serve.go:192` — `"err"` key → `"error"` for codebase consistency
- `cli/chat.go:90` — `os.ReadFile` for instruction template → `//go:embed`
  (also a `.rules/static-assets.md` fix; not logging per se, groups naturally
  with this phase)

User-facing output (keep as `fmt.Printf`): `cli/chat.go:69,71,74,76,82,85`,
`cli/scrape.go:215,231`, `cli/db.go:168`.

Verify: `make check`; manual `go run . chat --help` and `go run . scrape ...`
to confirm user output is still readable.

### Phase 5 — Interface ownership refactor (`server/server.go`)

Architectural. Per `.rules/interfaces.md`. Must precede Phase 6 so we don't
rename methods on interfaces we're about to delete.

- `Searcher` (8 methods) — **has three real implementations**
  (`*weaviate.Client`, `*search.PostgresClient`, `*search.PineconeClient`;
  `*search.VectorizerClient` is an `Embedder`, not a `Searcher`); the interface
  is justified. The real issue is size and location: it's defined in `server/`
  (consumer) but should arguably live in `search/` (producer) as the "product"
  protocol, since multiple backends implement it. Recommend: relocate to
  `search/` and shrink to the subset `server` actually calls (audit
  `s.searcher.*` call sites first).
- `FodmapWriter` — **not a test-convenience interface**; it's a capability
  interface used via `s.searcher.(FodmapWriter)` type assertion in
  `admin_ingredients_handler.go:265,394`. This is a legitimate Go narrowing
  pattern. Recommend: keep, document as a capability interface, remove the
  "created for test convenience" framing.
- `CatalogStore` (16 methods) — too large. Split into `CatalogReader`,
  `CatalogWriter`, `CatalogAdmin` or similar; or accept it if the store
  genuinely needs all 16 (document the rationale in the doc comment).
- `MenuStore` — created before real need (comment says `DeleteStaleMenu` is
  YAGNI). Remove until the `--purge-stale` flag lands.

Risk: touches every server test stub. Do after Phase 3 (`any` sweep) and before
Phase 6 (Get rename).

### Phase 6 — Naming: `Get` prefix removal

API-breaking across the store/search layers. Highest churn. Sequence per
package:

1. Rename the method on the concrete type.
2. Rename the method on every interface that declares it.
3. Update all call sites.
4. `make check`.

**Scope warning:** the file list below is INCOMPLETE. `auth/postgres_store.go`
alone has **9** `Get`-prefixed exported methods (verified via grep), not the 3
listed. Re-run `grep -rn 'func ([^)]*) Get[A-Z]' --include='*.go' .` and rename
ALL of them, including each one's declaration on `auth/store.go` interfaces and
its stub in `server/mock_store.go`. Treat the lists here as a starting point,
not an inventory. See R8.

Files:
- `auth/store.go:9,10` — `GetUserByEmail`→`UserByEmail`,
  `GetUserByID`→`UserByID`
- `auth/admin.go:63` — `GetUserDetail`→`UserDetail`
- `auth/postgres_store.go` (also: `GetDietaryProfile`, `GetConversation`,
  `GetMessages`, `GetUserAnalytics`, `GetConversationActivity`,
  `GetConversationAnalytics`) — rename concrete methods + their interface decls
  + `server/mock_store.go` stubs
- `menutracking/rule.go:62,77` — `GetActiveRule`→`ActiveRule`,
  `GetProposedRule`→`ProposedRule`
- `menutracking/source.go:47` — `GetSourceByID`→`SourceByID`
- `search/pinecone.go:60,121`, `search/weaviate.go:309,388`,
  `search/postgres.go:315,380` — `GetBusinesses`→`Businesses`,
  `GetReviews`→`Reviews`
- `data/data.go:84,136` — `GetReviewsByBusiness`→`ReviewsByBusiness`,
  `GetBusinessMap`→`BusinessMap`
- `fodmap/store/postgres.go:122` — `Get`→`Ingredient` (or `FetchIngredient`)
- `server/server.go:75` — field `geminiApiKey`→`geminiAPIKey`
- `server/direct_fodmap_client.go:21` — `LookupFODMAP`→`LookupFodmap` (align
  with rest of codebase)
- `menutracking/admin.go:23` — `MenutrackingAdminHandler`→`AdminHandler`
  (drop package-name repetition)

Risk: large call-site surface. Do per-package and verify with `make check`
between each. See G9 in Risks and Gaps for rollback strategy and commit
granularity.

**Cross-phase churn collision (with Phase 8):** every `Get`-method rename here
rewrites `server/mock_store.go`, and Phase 8 separately renames `mock*`→`stub*`
in that same file. Two mass edits to the same stubs in different phases doubles
the merge-conflict surface. **Recommend**: do the `mock*`→`stub*` rename of
`mock_store.go` together with this phase's method renames on that file (one
pass), or land Phase 8's stub rename before starting Phase 6.

### Phase 7 — Doc comments on exported identifiers

Largest surface, lowest risk. Per `.rules/comments.md`, every exported
identifier needs a doc comment starting with its name.

Worst offenders:
- `chat/chat.go` — ~20 exported symbols without doc comments
- `chat/openai_backend.go:14,21,86`
- `scraper/api_inference.go:20`, `scraper/scraper.go:285` (comment says
  `isTooNoisy`, exported name is `IsTooNoisy`)
- `search/embedder_llama_stub.go:20,24,28`, `search/pinecone.go:272`
- `menutracking/workers.go:259` (typo "writeBronzenFile")
- `data/io/event.go:11,16,26,36,40`
- `cli/menutracking_migrate.go:113,120`
- `server/direct_fodmap_client.go:8,19` (inconsistent interface name in docs)

Approach: per-package, read each exported symbol, write a one-sentence doc
comment. If `golangci-lint` has `revive`'s `exported` rule enabled, it will
catch regressions automatically.

### Phase 8 — Remaining mechanical fixes

- **Context-as-first-arg**: `cli/scrape.go:272` (`toMenuItems`),
  `scraper/vision_pdf.go:23,90`, `search/postgres.go:47`
- **Unnecessary pointer**: `scraper/openai_extractor.go:117` (`*string`)
- **Capitalized error strings**: `scraper/openai_extractor.go:161,167,175,195,
  202`, `scraper/vision_pdf.go:42`
- **Discarded errors without comment** (add justifying comments or handle):
  `chat/chat.go`, `chat/openai_backend.go`, `server/chat_handler.go`,
  `server/conversation_handler.go`, `server/conversation_export_handler.go`,
  `menutracking/agent.go:109`. Note: HTTP handler terminal writes are now
  allowed per the Phase 0 rule revision — only fix the pre-header-write cases.
- **In-band errors**: `server/auth_handler.go:88` (empty tokens returned on
  failure), `server/admin_ingredients_handler.go:51-56` (HTTP 200 on error)
- **Static assets**: `chat/chat.go:452`, `chat/profile.go:12` — inline prompts
  → `//go:embed` text files
- **Import grouping**: `cli/event.go:6`, `server/profile_handler.go:4-7`,
  `server/create_conversation.go:12-15`
- **Blank imports in library**: `auth/postgres_store.go:11`,
  `search/postgres.go:15`, `fodmap/store/postgres.go:20` — see G6 in Risks
  and Gaps before moving. Tests in these packages depend on the driver being
  registered at import time. Safe approach: create `internal/db/driver.go`
  with the blank import, have both `main.go` and test files import it. May
  require a `.rules/imports.md` carve-out for driver registrations.
- **Test double naming**: `server/mock_store.go:13`,
  `server/direct_fodmap_client_test.go:12,13`, `chat/chat_test.go:156,662`,
  `chat/gemini_backend_test.go:14` — rename `mock*` → `stub*`
- **Dead code**: `cli/chat.go:18` (`const ( _ = iota )`), `search/pinecone.go:344`
  (duplicate `meta["substitutions"]` assignment), `temp/main.go`, `tmp/main.go`
  (scratch files — confirm not referenced by any build target before deleting)
- **`data/data.go:23`** — `UnmarshalReview` takes unused `*regexp.Regexp`;
  remove the parameter
- **`data/io/event.go:1`** — package `io` shadows stdlib; rename to `eventio`
  or fold into `data`. **Note**: see G7 in Risks and Gaps — this is
  cross-cutting, not mechanical. Consider deferring.
- **`server/middleware.go:26`** — local `auth` shadows imported `fodmap/auth`;
  rename to `authHdr` or similar

### Phase 9 — Goroutine lifetime

**Priority note:** this is a *correctness* issue (unsupervised goroutine that
can leak / outlive shutdown), not a cosmetic cleanup. By the plan's own
"correctness ships first" philosophy it is mis-ranked at the end — consider
promoting it into Phase 1. Kept here only because it's architecturally
independent of the other phases.

- `server/create_conversation.go:112` — `go s.generateReviewSummary(...)` has
  no shutdown signal. Tie to a server-level `context.Context` that's cancelled
  in `Server.Stop()` (add one if it doesn't exist), or use a worker pool with
  explicit lifecycle.

## Risks and Gaps

### Critical gaps found in second-iteration review

**G1. Phase 1 item 6 (library panics) is underspecified — `StructuredUpdateSchema`
is a pure function, not a constructor.**
`menutracking/schema.go:38` `StructuredUpdateSchema()` is called from
`menutracking/agent.go:64` inside `(*ScrapeAgent).Work(ctx, job)` — a river
worker. Converting the panic to a returned error means changing the signature
to `(map[string]any, error)` and propagating the error up through `Work`. But
`json.Marshal` of a reflection-generated schema is effectively infallible (the
input is a static Go struct), so the panic is arguably correct per the guide's
"panic on API misuse / truly invariant" carve-out. **Recommend**: keep the
panic but document why it's safe (reflection on a static type can't fail at
marshal time), OR return error if we want to be strict. Decision needed.

**G2. Phase 1 item 6 — `fodmap/store/postgres.go:368` `mustRead` panics on
embed-file read failures, which are compile-time-invariant.**
The `mustRead` panics fire if an embedded `.sql` file is missing — but
`//go:embed sql/*.sql` fails at compile time if any glob match is absent, so
the runtime panic is unreachable. This is a "defensive panic for an impossible
state" pattern, not a library-panic-on-input violation. **Recommend**: keep
but document, or replace with `//go:embed` string vars directly (Go 1.16+
supports `var foo string //go:embed sql/foo.sql`), eliminating `mustRead`
entirely. The latter is cleaner and removes the issue.

**G3. Phase 5 (interface ownership) — `Searcher` has multiple real
implementations, not just one.**
The plan says "if `search.Client` is the only implementation, consider removing
the interface." Verified via grep: **three** concrete types satisfy `Searcher` —
`*weaviate.Client`, `*search.PostgresClient`, `*search.PineconeClient`.
(`*search.VectorizerClient` is an `Embedder`, not a `Searcher` — it has none of
the `GetBusinesses`/`SearchFodmap`/`EnsureSchema`/`BatchUpsert` methods.)
`cli/serve.go:63,132,139` picks one at startup based on config.
So the interface is genuinely needed for multiple implementations — this is
NOT a producer-owns-interface violation. **The real issue is size (8 methods)
and that it's defined in the producer-adjacent `server` package rather than
the consumer.** Recommend: keep the interface, but (a) split it if the server
only calls a subset, and (b) move it to the `search` package as the "product"
since multiple backends implement it. Update Phase 5 accordingly.

**G4. Phase 5 — `FodmapWriter` is used via type assertion on `Searcher`.**
`server/admin_ingredients_handler.go:265,394` does
`fw, ok := s.searcher.(FodmapWriter)` — it's a capability check, not a
standalone dependency. This is a legitimate Go pattern (narrowing an interface
to a capability), not a test-convenience interface. **Recommend**: keep
`FodmapWriter`, document it as a capability interface, and remove the
"created for test convenience" framing from Phase 5.

**G5. Phase 5 — `MenutrackingAdminHandler` rename (Phase 6) will collide with
`menutracking.AdminHandler` if we naively drop the package prefix.**
The plan proposes `MenutrackingAdminHandler`→`AdminHandler`. But
`server/server.go` has a `SetMenutrackingAdmin(http.Handler)` method and a
`menutrackingAdmin http.Handler` field — the call site
(`cli/serve.go:174`) is `&menutracking.MenutrackingAdminHandler{...}`. After
rename it becomes `&menutracking.AdminHandler{...}`, which is clear. But if
any other package defines `AdminHandler`, there's a collision risk. Verified:
no other `AdminHandler` exists today. Safe to proceed.

**G6. Phase 8 — moving the pgx blank import to `main.go` breaks test isolation.**
`auth/postgres_store.go:11`, `search/postgres.go:15`,
`fodmap/store/postgres.go:20` all have `_ "github.com/jackc/pgx/v5/stdlib"`.
Verified additional dependents that would break: `internal/db/migrate_test.go:11`,
`internal/db/migrate_integration_test.go:12`, `internal/db/migrate_unit_test.go:7`,
and `menutracking/admin_test.go:19` each carry their own blank import — proof
that the driver registration is needed package-locally, not centralizable.
Tests in those packages (e.g. `auth/postgres_store_test.go`) rely on the
driver being registered when they import the package. Moving the blank import
to `main.go` means tests that don't import `main` will lose the driver and
fail at `sql.Open("pgx", ...)` time. The `internal/db/migrate_test.go:11`
already has its own blank import, confirming tests need it locally. **Recommend
revision**: keep the blank imports in the library packages, OR create
`internal/db/driver.go` with the blank import and have both `main.go` AND
test files import it. The rule "blank imports only in main/tests" may need a
carve-out for driver registrations that tests depend on. Update `.rules/imports.md`
to permit blank-imported drivers in library packages when tests require them.

**G7. Phase 8 — `data/io` package rename is a large, cross-cutting change that
doesn't belong in "remaining mechanical fixes."**
Renaming package `io` to `eventio` touches every import of `fodmap/data/io`
(currently `cli/event.go:7` uses `dataio "fodmap/data/io"`). It also changes
the import path, which affects any external consumers. This should be its own
phase or deferred — it's not mechanical. **Recommend**: pull out of Phase 8,
make it Phase 8b or defer entirely. The shadowing is currently worked around
with the `dataio` rename, which is functional.

**G8. Phase 2 — `cli/menutracking_migrate_test.go:86` string-matches an error
that may not have a sentinel.**
The plan says "may require introducing one in `cli/menutracking_migrate.go`."
Introducing a sentinel is a new public API surface, not a mechanical fix.
**Recommend**: scope this as "introduce `ErrInvalidCron` sentinel in
`cli/menutracking_migrate.go`, return it from the validation function, then
use `errors.Is` in the test." Call it out as a small API addition, not a
pure sweep.

**G9. No rollback strategy for Phase 6 (Get-prefix rename).**
Phase 6 is the highest-churn, API-breaking phase. The plan says "do per-package
and verify with `make check`" but doesn't address: (a) commit granularity
(one commit per package vs. one big PR), (b) backward compatibility (do we
keep deprecated aliases?), (c) external consumers (is this module imported
by anything outside the repo?). **Recommend**: one PR per package, no
deprecation aliases (internal codebase). **External consumers: verified none** —
the module is named `fodmap` (go.mod) and the sibling `../fodmap-chat` repo does
not import it (grep for `fodmap` imports returned nothing). So the rename is
internal-only and (b)/(c) above are low-risk; no deprecation shims needed.

**G10. Phase 1 item 5 (fodmap/store SQL refactor) may break the
`{{.LimitArg}}`/`{{.OffsetArg}}` placeholder scheme.**
The current templates use `{{.LimitArg}}` to emit `LIMIT $N` with the correct
positional parameter index, which varies based on how many WHERE parameters
precede it. Static SQL fixes the positions, but if the filter combination
changes the parameter count, the LIMIT/OFFSET positions shift. **Recommend**:
pick a fixed parameter ordering (search, level, group, limit, offset) and
always bind all five (pass `nil`/zero for unused filters). This makes positions
stable. Document this in the `.sql` file header.

### Previously identified risks (retained)

**R1. `CatalogStore` 16-method interface (Phase 5)** — splitting may cascade
into every caller. If the store genuinely needs all 16 methods, the rule's
"keep interfaces small" guidance conflicts with the practical need. Gap:
the rule doesn't define "small" numerically. **Recommend**: split into
reader/writer/admin triples and document the rationale in the doc comment.

**R2. `fodmap/store/postgres.go` SQL refactor (Phase 1, item 5)** — eliminating
`text/template` may require either multiple near-duplicate `.sql` files or
a query shape change. Gap: the existing
`docs/plans/sql-file-management-plan.md` doesn't cover this package's
template approach. **Recommend**: extend that plan or add a sibling doc
for the `fodmap/store` refactor specifically.

**R3. Phase ordering** — Phases 0-4 are safe in order. Phase 5 (interface
ownership) must precede Phase 6 (Get rename) to avoid renaming methods on
interfaces we're about to delete. Phases 7-9 are independent and can be
parallelized across PRs. Caveat: G3/G4 above change Phase 5's scope — it's
now "shrink + relocate" not "delete."

**R4. Subagent false positives** — the review was delegated to explore agents;
some reported "violations" were self-corrected by the subagents. Spot-check
before fixing, particularly the `_ = json.NewEncoder(w).Encode(...)` cases
(now covered by the Phase 0 rule revision) and any "no violation" lines in
the raw report.

**R5. No automated rule enforcement — the highest-ROI gap, should NOT be
out of scope.** Verified: `.golangci.yml` has **no** `revive`, `exported`,
`godot`, or `stylecheck` config today, so none of the doc-comment, `Get`-prefix,
naming, or interface rules are machine-enforced. Consequence: after a 117-file
manual sweep (Phases 6–8 especially), nothing prevents same-day re-violation —
the cleanup decays. For a plan whose value is mostly compliance, the enforcement
ratchet is arguably the single highest-leverage deliverable. **Recommend
promoting this into Phase 0**: enable `revive` (`exported`, `unexported-return`,
`context-as-argument`) and `godot`/`stylecheck` *first* (accept a baseline
allowlist), so each subsequent phase removes lint exceptions rather than fixing
invisible debt. Without this, treat the cosmetic phases as one-time and expect
regression.

**R6. `temp/main.go` and `tmp/main.go`** — scratch files. `temp/main.go` has
`//go:build ignore` so it's excluded from builds. `tmp/main.go` does NOT have
a build tag — it's a `package main` in `tmp/` that will compile as a separate
binary if `go build ./tmp/...` is run. Confirm `tmp/` is not in any build
target (Makefile, CI, Bazel) before deleting.

### New findings from code verification (third-iteration review)

**R7. Confirmed false positive — `uuid.MustParse` items in Phase 1 item 6.**
The two cited `uuid.MustParse` calls (`scraper/scraper.go:374`,
`search/weaviate.go:1215`, plus `cli/scrape.go:268`) operate on **compile-time
string literals** defining package-level namespace UUIDs — the
`regexp.MustCompile` idiom, not external input. They are correct as-is.
**Action**: removed from Phase 1 item 6; do not convert to `uuid.Parse`. This is
a concrete instance of the R4 false-positive class — re-audit other
`Must*`/`panic` items the same way (check whether the argument is a constant).

**R8. Phase 6 file inventory is incomplete (understated scope).**
Verified via `grep -rn 'func ([^)]*) Get[A-Z]' --include='*.go'`: the `auth`
store alone exposes **9** `Get`-prefixed methods, but Phase 6 lists only 3.
Missing: `GetDietaryProfile`, `GetConversation`, `GetMessages`,
`GetUserAnalytics`, `GetConversationActivity`, `GetConversationAnalytics`. Each
also has an interface declaration and a `server/mock_store.go` stub. **Action**:
re-grep and rename exhaustively per package; do not trust the plan's file list
as an inventory. This roughly triples the `auth` portion of Phase 6's churn.

## Verification

Every phase ends with `make check` (lint + test + build). Phases that change
SQL or queries (1.5) additionally run `EXPLAIN` on affected queries. Phase 4
(CLI logging) requires manual UX verification. Phase 5 (interfaces) requires
verifying no test stub is left orphaned.