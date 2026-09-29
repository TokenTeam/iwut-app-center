# AGENTS.md — iWUT App Center

## Mission

This repository contains the App Center implementation. Build only from the current design contracts.

The authoritative design documents are intentionally outside this code repository:

```text
../../docs/app-center/
```

Do not copy those documents into this repository. If that path is unavailable, stop work that depends on business semantics and report the missing design source instead of guessing.

## Required reading

Default design input for an implementation work package is the generated brief,
not the UC/ADR sources. Before implementation work, read only:

1. `../../docs/app-center/implements/README.md` — the local-reading protocol.
2. `../../docs/app-center/briefs/_engineering-baseline.md` — cross-cutting
   architecture decisions; read once per session and reuse.
3. `../../docs/app-center/briefs/UC-APP-XXX.md` — the brief for the active work
   package named below.

If that brief does not exist yet, create or refresh
`docs/tools/brief-specs/UC-APP-XXX.json` in the design docs repository and
regenerate the brief before starting. Do not begin implementation without it,
and do not fall back to reading whole UC or ADR files.

The brief is a derived, non-authoritative extract. Business behavior stays
authoritative in `UC-*` and `BR-*`; architecture decisions stay authoritative in
`ADR-*`. On any conflict the source file wins. This file does not override them.

When the brief's `未纳入本 brief 的源小节` index names a section the task really
needs, read that single section by its anchor — never the whole file. When the
brief does not cover something, or two authoritative rules conflict, file a
structured gap (`authority` / `conflict` / `options` / `suggested`) and route it
to the design task instead of deciding in code.

## Current work package

**UC-APP-002 → UC-APP-003 → UC-APP-004 → UC-APP-005 → UC-APP-007 — Version
OAuth redirect delivery.** Design inputs are the corresponding generated briefs
under `../../docs/app-center/briefs/`, the engineering baseline, the readiness
note and implements README. Deliver the accepted extensions serially: attach the
PKCE and confidential redirect URI arrays to each ApplicationVersion through a
dependent `ApplicationVersionOAuthConfig`; create and replace it atomically with
the Version under the Version revision; deep-copy it into immutable review
snapshots; require the immutable `app-version-review-v2` redirect checks for new
decisions; and recheck the TEST registration/credential required by non-empty
approved arrays before publication commits. Add the committed backward-compatible
Proto surface, migration/backfill, validators, HTTP/gRPC/Wire mappings and real
MongoDB concurrency/E2E coverage. Keep the existing Scope Catalog `requestable`
consumer contract: Auth now derives that compatibility projection from its single
authoritative `enabled` state. Do not implement UC-APP-019 provider resolution,
Auth secret verification, grants, tokens, sector/sub, frontend or future channels
in this work package. Required final tier: `make check-auth-app`. Commit API inputs
and generated outputs before the service gitlink, keep each accepted UC boundary
reviewable, and keep all commits local.

## Verification entry points

Run commands from the repository root; `scripts/verify.py` also works from any
working directory. See README for overrides, reports and troubleshooting.

```bash
make doctor          # quick prerequisites only; not acceptance
make check           # fast checks + scripts + generated code + external docs
make check-full      # above + race + all real Mongo/HTTP/gRPC E2E tests
make check-auth-app  # above + real Auth process E2E (cross-service changes)
```

- Use `check` during development. Use `check-full` for backend behavior,
  transactions, schemas, concurrency, wiring, transport or integration tooling.
  Auth client/identity/shared Auth-contract changes also require `check-auth-app`.
- `go test ./...` alone skips Mongo tests without its URI; it is not full delivery
  evidence. Filtered `-run` checks are diagnosis, not full-tier acceptance.
- Reports live under ignored `.artifacts/verification/`; `status=passed` applies
  only to the recorded tier and source fingerprint. Missing tools, a timeout or
  changed inputs fail verification. `doctor-passed` is not an acceptance result.
- Never dump environment variables or commit generated test keys/logs/reports.
- Keep all changes local. No push unless the user explicitly authorizes it.

## Subagent coordination and handoff

- The owner assigns concrete file/package ownership and interface contracts
  before parallel work. Shared-file edits and API generation have one owner.
- Child agents run targeted checks; one owner integrates changes and runs the
  final unfiltered tier. Coordinate shared API worktrees rather than resetting,
  checking out or committing another agent's work.
- Freeze service, API and selected external sources during final verification.
  Do not edit a running shell script. Any source change invalidates that report;
  resolve the cause and rerun the relevant tier on the final inputs.
- Commit API generator inputs and outputs together, then update the service
  gitlink. Preserve unrelated edits and shared API history. A clean worktree is
  not required to test: the report hashes dirty tracked and untracked inputs.
- Handoff includes UC/BR coverage (or tooling acceptance cases), limitations,
  service/API commit IDs, dirty status, verification tier and report path. Link
  the report to the pre-commit content fingerprint; committing alone does not
  require repeating tests when the committed content is identical. Code changes
  after verification do require new evidence.

## Code boundaries

Follow ADR-003 and the implementation conventions:

```text
cmd/app-center/
internal/shared/
internal/application/domain/
internal/application/usecase/
internal/application/port/
internal/version/domain/
internal/version/usecase/
internal/version/port/
internal/review/domain/
internal/review/usecase/
internal/review/port/
internal/profile/domain/
internal/profile/usecase/
internal/profile/port/
internal/publication/domain/
internal/publication/usecase/
internal/publication/port/
internal/tester/domain/
internal/tester/usecase/
internal/tester/port/
internal/catalog/domain/
internal/catalog/usecase/
internal/catalog/port/
internal/adapter/
```

Name directories by current business responsibility. Do not create global `biz`, global `data`, catch-all `util`, or implementation-generation packages.

Domain code must not import Kratos, MongoDB drivers, generated Proto packages, HTTP packages or deployment configuration. Domain entities must not carry BSON, JSON or Proto tags.

UseCase code coordinates Domain and ports. Adapters perform external type conversion and technical error mapping. The composition root is the only place that knows concrete implementations.

Environment variables are read only by `internal/config` or a future
composition root. UseCases and adapters receive validated scalar values through
constructors. Explicit invalid configuration must fail loading rather than fall
back silently.

The root `architecture_test.go` enforces these package and import boundaries as
part of `go test ./...`. A boundary change requires an accepted ADR update and
the corresponding architecture-test change in the same commit. Do not skip,
weaken or relocate the test to make a dependency pass.

## Design discipline

- Implement only the active work package; do not prebuild abstractions for future UCs.
- Do not invent business behavior. If an ambiguity changes externally visible behavior or an invariant, report it and update the authoritative design before coding it.
- Keep implementation choices local when they do not change business semantics or cross-module architecture.
- Every relevant BR must map to an automated test or an explicit higher-level test obligation.
- Use deterministic fakes for time, IDs and external dependencies.
- Do not weaken an atomicity or concurrency rule to simplify an adapter.

## Go conventions

- Use the Go version declared by this repository's `go.mod` once it exists.
- Run `gofmt` on all changed Go files.
- Use typed IDs and explicit value objects at domain boundaries.
- Obtain time and UUIDv7 values through ports; do not call global time/random functions inside Domain or UseCase code.
- Prefer small interfaces owned by the consuming capability.
- Repository methods express business atomic operations rather than generic CRUD.
- Expected business errors must support stable type/code matching; clients must never branch on message text.
- Never use `panic` for expected validation, authorization, conflict or dependency failures.

## Testing

- Use table-driven tests where they improve boundary coverage.
- Include the relevant BR ID in the test name or case name.
- Cover success, invalid input, authorization, conflict, dependency failure and no-partial-result behavior as applicable.
- Do not use sleeps to coordinate concurrency tests.
- `go test ./...` is the minimum verification once a Go module exists.
- Use `go test -race ./...` for code that introduces meaningful in-process concurrency.
- MongoDB transaction and index behavior cannot be proven with an in-memory fake.
- Run `./scripts/test-mongo-integration.sh` whenever a work package changes MongoDB transactions, migrations, validators, indexes or repository concurrency. The script starts and removes an isolated transaction-capable replica set; plain `go test ./...` skips these tests when `MONGODB_INTEGRATION_URI` is absent.

## Generated code and secrets

- Never hand-edit generated files.
- Commit a generator input and its generated output together.
- Do not commit tokens, credentials, private keys, local environment files, database dumps, editor state or build artifacts.
- Do not log JWTs, tester secrets, authorization headers, arbitrary user KV values or complete request bodies.

## Commit messages

Every commit title must use:

```text
:emoji: type: commit title
```

Allowed types:

```text
feat fix docs test refactor perf build ci chore revert
```

Examples:

```text
:sparkles: feat: implement application name value object
:white_check_mark: test: cover application quota limits
:wrench: chore: initialize go module
```

Use an English imperative title without a trailing period. Keep each commit focused on one reviewable purpose.

## Verification before handoff

Before declaring work complete:

1. Re-read the active business brief acceptance section and BR list when doing
   UC work; for engineering maintenance, check the stated tooling scope. The
   verification runner checks external brief freshness without changing cwd.
2. Confirm no future capability entered the current package unintentionally.
3. Run the required verification tier and inspect its report; do not describe
   omitted tiers as passed. The quick tier includes the root architecture guard.
4. Report which BRs are covered and any remaining test obligation.
5. Keep design changes in the external authoritative document tree; do not create a repository-local copy.
