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

**UC-APP-004 API Delivery and E2E.** Target UC: UC-APP-004
(SubmitApplicationVersionReview). Brief:
`../../docs/app-center/briefs/UC-APP-004.md`, generated from
`docs/tools/brief-specs/UC-APP-004.json`. The brief embeds the Auth Scope
Catalog v1 and App Center routing contracts; regenerate it when a selected
source changes.

Code scope:

- `api/` — add the resource-oriented UC-APP-004 POST method and response
  messages; the body contains only `expectedRevision` and no server-owned
  review, snapshot, state or audit fields.
- `internal/adapter/preflight/` — implement the DNS-only public HTTPS launch
  URL policy with an injectable resolver; it performs no HTTP request.
- `internal/adapter/transport/` — add UC-APP-004 HTTP + gRPC conversion,
  centralized ADR-005 error mapping and the created Review Location header.
- `cmd/app-center/` — assemble the existing UC-APP-004 core handler, real Auth
  Scope Catalog consumer and real preflight adapter with Wire.
- `cmd/app-center/e2e_integration_test.go` — cross real listeners, JWS, the
  generated Auth interface, the real policy with a deterministic resolver and
  a transaction-capable MongoDB.
- Contract/unit tests cover routing, request-field exclusion, public/special
  address policy and UC-APP-004 error mappings.

Explicit non-goals:

- No UC-APP-005 transport and no legacy API compatibility layer.
- No Auth database reads, production hardcoded Scope catalog or unsigned JSON
  identity header.
- No invented internal-service credential: until that platform contract is
  accepted the consumer connection is explicitly unauthenticated and this work
  remains non-production; tests use only the generated Auth interface.
- No HTTP fetcher, redirect follower or headless browser; DNS is the only
  launch URL inspection performed by this work package.
- No Gateway or Auth Center code change; the Gateway prefix strip is only
  recorded and asserted in the contract test.
- No gRPC-Web wrapper in this service (terminated at Traefik).
- No push in any repository; keep API, service and documentation commits local
  until the user chooses the publication batch.

Verification commands:

```bash
gofmt -l .
go test ./...
go vet ./...
go run github.com/goforj/wire/cmd/wire@v1.2.0 diff ./cmd/app-center
make -C api proto-check
./scripts/test-mongo-integration.sh
cd ../../docs && python3 -B -m unittest discover -s tools/tests
cd ../../docs && python3 -B tools/gen_brief.py --check --all
cd ../../docs && python3 -B tools/registry.py --check
git diff --check
```

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
