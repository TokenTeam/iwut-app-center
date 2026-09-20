# AGENTS.md — iWUT App Center

## Mission

This repository contains the App Center implementation. Build only from the current design contracts.

The authoritative design documents are intentionally outside this code repository:

```text
../../docs/app-center/
```

Do not copy those documents into this repository. If that path is unavailable, stop work that depends on business semantics and report the missing design source instead of guessing.

## Required reading

Use the local-reading protocol in `../../docs/app-center/implements/README.md`.
Before implementation work, read only:

1. `../../docs/app-center/implements/README.md`
2. The exact UC/BR/ADR sections listed under the active work package below.
3. Only the implementation-convention sections relevant to that package.

Do not read the complete design registry, every UC, whole domain/lifecycle
documents, or every referenced ADR by default. Expand the working set only
when the active package contains a precise reference or implementation exposes
a concrete conflict or missing definition.

Business behavior is authoritative in `UC-*` and `BR-*`. Architecture decisions are authoritative in `ADR-*`. This file does not override them.

## Current work package

Integrate and repair UC-APP-003 and UC-APP-004 as one bounded package.

Required design sections:

- `UC-APP-003`: Goal and scope; Editable and immutable fields; Input and
  identity; Main flow; Exceptional flows; BR-VER-010 through BR-VER-017; Use
  case ports; Data model changes; Tests and acceptance.
- `UC-APP-004`: Goal and scope; Submission result; Input and identity; Main
  flow; Exceptional flows; BR-REV-001 through BR-REV-009; Minimal domain model;
  Use case ports; Data model; Tests and acceptance.
- `UC-APP-002`: BR-VER-003 through BR-VER-009 for reused candidate and snapshot
  field validation.
- `ADR-001`: Decision and consistency/failure behavior for Scope Catalog.
- `ADR-003`: Decision, Port ownership and automated constraints.
- `ADR-004`: Transaction boundary, retry and schema migration rules.
- `ADR-005`: Domain error classification and adapter mapping rules.

Code scope: UC-APP-003 draft replacement, UC-APP-004 review submission,
MongoDB atomic authorization fences, the explicit immutable migration chain up
to 0004, and Domain/UseCase plus real replica-set integration tests.

Non-goals: API/Proto/HTTP transport, real Auth or URL-check transports,
UC-APP-005 or later lifecycle transitions, generic CRUD repositories, and
unrelated refactors.

Verification: `gofmt`, `go vet ./...`, `go test ./...`, `go test -race ./...`,
and `./scripts/test-mongo-integration.sh -race -count=1`.

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

1. Re-read the target UC acceptance section and referenced BRs.
2. Confirm no future capability entered the current package unintentionally.
3. Run formatting, static checks available in the repository and all relevant tests; `go test ./...` must include the root architecture guard.
4. Report which BRs are covered and any remaining test obligation.
5. Keep design changes in the external authoritative document tree; do not create a repository-local copy.
