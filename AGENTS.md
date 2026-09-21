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

No implementation work package is currently active. UC-APP-001 through
UC-APP-005 are `ACCEPTED / CORE_COMPLETE`. UC-APP-005 has working Domain,
UseCase, narrow external ports, MongoDB decision repository, explicit 0005
migration, permanent System rejection for suspended administrators/submitters,
and real replica-set transaction/concurrency coverage. Real Auth, ConfCenter,
suspension, URL-inspection and API transports remain separate future packages.

UC-APP-005 test obligations: BR-REV-010 through BR-REV-018 are covered by named
tests in `internal/review/domain`, `internal/review/usecase` and the decision
repository integration suite. BR-REV-019 (review/publication separation) has no
local test because no publication capability exists yet, so the invariant holds
vacuously; its obligation belongs to the UC-APP-007 work package, which must
assert that a decision never writes a test/grey/stable slot or an OAuth client.
BR-REV-020 is a limitation statement about remote content and needs no test.

Before the next implementation begins, replace this paragraph with a bounded
package that lists: target UC, the brief path for that UC, code scope, explicit
non-goals, and verification commands. Keep the design scope in the brief spec
(`docs/tools/brief-specs/UC-APP-XXX.json`) instead of restating section titles
here. Do not infer UC-APP-006 or any other next package from file order, recent
commits, or use-case numbering.

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

1. Re-read the work package brief's acceptance section (`## 测试与验收`) and its
   `BR-*` list, and confirm the brief still matches its sources:
   `cd ../../docs && python3 tools/gen_brief.py --check --all`.
2. Confirm no future capability entered the current package unintentionally.
3. Run formatting, static checks available in the repository and all relevant tests; `go test ./...` must include the root architecture guard.
4. Report which BRs are covered and any remaining test obligation.
5. Keep design changes in the external authoritative document tree; do not create a repository-local copy.
