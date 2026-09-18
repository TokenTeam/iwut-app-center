# AGENTS.md — iWUT App Center

## Mission

This repository contains the App Center implementation. Build only from the current design contracts.

The authoritative design documents are intentionally outside this code repository:

```text
../../docs/app-center/
```

Do not copy those documents into this repository. If that path is unavailable, stop work that depends on business semantics and report the missing design source instead of guessing.

## Required reading

Before implementation work, read these files in order:

1. `../../docs/app-center/implements/README.md`
2. `../../docs/app-center/implements/implementation-conventions.md`
3. The UC named by the current task, including every referenced `BR-*`
4. The ADRs referenced by the implementation entry point or current UC

For the first work package, also read:

```text
../../docs/app-center/domain-model.md
../../docs/app-center/use-cases/UC-APP-001-create-application.md
../../docs/app-center/adr/ADR-003-go-package-and-dependency-boundaries.md
```

Business behavior is authoritative in `UC-*` and `BR-*`. Architecture decisions are authoritative in `ADR-*`. This file does not override them.

## Current work package

The first work package implements only the Domain and UseCase portions of UC-APP-001:

```text
Application
ApplicationName
DeveloperApplicationQuota
CreateApplication command handler
Identity, Clock, UUIDv7 and persistence ports required by that handler
Unit tests covering BR-APP-001 through BR-APP-007
```

Do not add MongoDB, Redis, Kratos, Proto, HTTP/gRPC, ApplicationVersion, Profile, Publication, Tester or Catalog in this work package.

## Code boundaries

Follow ADR-003 and the implementation conventions:

```text
cmd/app-center/
internal/shared/
internal/application/domain/
internal/application/usecase/
internal/application/port/
internal/adapter/
```

Name directories by current business responsibility. Do not create global `biz`, global `data`, catch-all `util`, or implementation-generation packages.

Domain code must not import Kratos, MongoDB drivers, generated Proto packages, HTTP packages or deployment configuration. Domain entities must not carry BSON, JSON or Proto tags.

UseCase code coordinates Domain and ports. Adapters perform external type conversion and technical error mapping. The composition root is the only place that knows concrete implementations.

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
- MongoDB transaction and index behavior belongs to later integration-test work and cannot be proven with an in-memory fake.

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
3. Run formatting, static checks available in the repository and all relevant tests.
4. Report which BRs are covered and any remaining test obligation.
5. Keep design changes in the external authoritative document tree; do not create a repository-local copy.
