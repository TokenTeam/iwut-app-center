# iWUT App Center — new implementation

This repository is the App Center implementation from the authoritative design
in `../../docs/app-center/`. Read `AGENTS.md` and the active work package brief
before changing code.

## Prerequisites

- Go 1.24 or newer.
- MongoDB **replica set or mongos**. A standalone `mongod` cannot run the
  multi-document transactions the repositories require; startup and migrations
  fail closed instead of silently degrading.
- Proto toolchain for the API submodule: `protoc`, `protoc-gen-go`,
  `protoc-gen-go-grpc`, `protoc-gen-go-http`. Run `make -C api proto-tools` to
  print the pinned versions.

## Build order

```bash
git submodule update --init --recursive   # API code lives in api/ (ADR-006)
make generate                             # regenerate cmd/app-center/wire_gen.go
make build
make test
make migrate                              # schema changes: explicit deploy step
make run                                  # HTTP + gRPC; never migrates automatically
```

`migrate` loads only Mongo configuration and exits. `serve` (the default when no
subcommand is given) validates at startup, before serving, that the topology
supports transactions and that the latest migration is already recorded. It is
read-only and does not create collections or indexes. The required schema is
`0010_application_tester_membership`; apply it explicitly before running this
build. It adds membership episodes with at most one ACTIVE episode per
Application and user. Joins share the Application transaction write fence with
link rotation and enforce the fixed limit of 100 ACTIVE testers.

## Commands

| Command | Purpose |
| --- | --- |
| `make generate` | Regenerate `cmd/app-center/wire_gen.go` with `go run github.com/goforj/wire/cmd/wire@v1.2.0` |
| `make api-generate` | Regenerate the API submodule Go code (`make -C api proto-gen`) |
| `make fmt-check` | Fail if any Go file is not `gofmt`-ed |
| `make build` | Compile all packages |
| `make test` | Run the unit, transport and architecture test suite |
| `make test-mongo` | Run MongoDB integration and the UC-APP-001–012 end-to-end suites against an isolated replica set (Docker) |
| `make test-auth-app` | Start isolated Mongo plus a separate real Auth process and run the service-JWS UC-APP-005 double-service E2E |
| `make vet` | Run `go vet ./...` |
| `make wire-check` | Fail if `wire_gen.go` is stale |
| `make api-check` | Fail if the generated API code drifted from its Proto |
| `make doctor` | Check quick prerequisites only; no acceptance claim |
| `make check` | Quick tier: script tests, format/build/unit/transport/architecture, vet, Wire/Proto, docs/brief/registry, staged and working diff checks |
| `make check-full` | Quick tier + Go race + all MongoDB and HTTP/gRPC E2E tests with race |
| `make check-auth-app` | Full backend tier + the real Auth/App service-JWS E2E with race |
| `make test-race` / `make test-scripts` | Individual race or verification-tooling regression checks |
| `make migrate` | Apply pending MongoDB migrations and exit |
| `make run` | Start the HTTP and gRPC servers |

## Verification workflow

Use `make check` while implementing and `make check-full` before backend delivery.
Changes involving Auth consumers or shared Auth identity/contracts also require
`make check-auth-app`. Quick success does **not** mean Mongo, E2E or race passed.
The real Auth/App check currently covers the named UC005 service-JWS scenario;
it is not proof of every Auth UC, production Gateway or frontend flow.

The Python 3 standard-library runner can be called from any directory:

```bash
/path/to/iwut-app-center-ddd/scripts/verify.py backend --doctor
/path/to/iwut-app-center-ddd/scripts/verify.py backend
/path/to/iwut-app-center-ddd/scripts/verify.py cross-service --timeout 2400
```

Prerequisites are checked before gates: Go, Git, Make, Bash, gofmt, Python 3,
and the pinned Proto toolchain; integration tiers also need Docker and GNU
`timeout`, and the cross-service tier needs OpenSSL/base64 and the Auth source.
`make -C api proto-tools` prints/checks availability; it does not install tools.
Missing tools or inaccessible Docker fail rather than skip the tier.

- `APP_CENTER_DOCS_DIR`: override the docs repository (default `../../docs`).
- `AUTH_CENTER_SOURCE_DIR`: override the Auth worktree (default sibling
  `iwut-auth-center-ddd`). Prefer absolute paths for overrides.
- `MONGODB_INTEGRATION_PORT`: optional fixed host port. By default Docker assigns
  a separate port per run; parallel runs use separate containers and databases.
- `AUTH_CENTER_INTEGRATION_PORT`: optional Auth port; by default chosen from a
  free local port. A competing external process can still claim that port before
  bind; startup failure is diagnosed and must be retried, never treated as pass.
- `INTEGRATION_STARTUP_TIMEOUT_SECONDS`: Mongo/Auth readiness deadline (default
  60 seconds); timeout fails explicitly. `--timeout` bounds each verification gate.

Every run writes `.artifacts/verification/<run>/report.json` and per-gate logs.
The report records the tier, planned/executed gates, exit codes, durations, tool
versions, source HEADs, dirty flags and SHA-256 content fingerprints before/after.
Tracked and nonignored untracked files are included; ignored local files are not.
It stores hashes instead of source diffs or environment dumps. Logs are local
private diagnostics and may contain test fixture data; do not share them blindly.

A failure stops the pipeline; omitted gates are not claimed as successful.
`doctor-passed` only confirms prerequisites. `source-changed` invalidates an
otherwise passing run: stop concurrent edits and rerun against stable inputs.
On timeout/interruption, the runner terminates the process group and allows
cleanup time; a forced kill or host crash may still require manual Docker cleanup.
Integration logs are preserved under the report directory (or, when invoked
alone, `.artifacts/integration/`) while temporary keys and containers are removed.

For diagnosis, `scripts/test-mongo-integration.sh -race -run 'Revocation'` is
supported from any directory. Final delivery uses the unfiltered tier. Choose
one owner for final verification; freeze scripts and source inputs while it runs.
API commits precede the service gitlink commit. Hand off the report path and
commit IDs; report dirty-input fingerprints identify pre-commit verification.

## Environment variables

`migrate` needs only the Mongo variables.

| Variable | Required | Default | Meaning |
| --- | --- | --- | --- |
| `APP_CENTER_MONGO_URI` | `serve`, `migrate` | — | MongoDB connection string (replica set or mongos) |
| `APP_CENTER_MONGO_DATABASE` | no | `iwut_app_center` | Target database name |
| `APP_CENTER_HTTP_ADDR` | no | `:8080` | HTTP listen address |
| `APP_CENTER_GRPC_ADDR` | no | `:9090` | gRPC listen address |
| `APP_CENTER_INITIAL_APPLICATION_QUOTA` | no | `10` | Initial per-admin creation quota; only used to lazily create a missing quota record |
| `APP_CENTER_TESTER_JOIN_URL_PREFIX` | no | `https://app.example/tester/join` | Mock or deployed HTTP(S) join entrance, with optional path and no userinfo, query or fragment |
| `APP_CENTER_SCOPE_CATALOG_CACHE_TTL` | no | `5m` | Scope Catalog cache TTL |
| `APP_CENTER_AUTH_SCOPE_CATALOG_GRPC_TARGET` | `serve` | — | Auth Center native gRPC target shared by Scope Catalog, Developer Status and System Principal clients |
| `APP_CENTER_SERVICE_IDENTITY_ID` | `serve` | — | pre-registered caller service ID |
| `APP_CENTER_SERVICE_IDENTITY_KID` | `serve` | — | active private-key ID registered in Auth |
| `APP_CENTER_SERVICE_IDENTITY_AUDIENCE` | no | `iwut-auth-center` | audience for service-call JWS |
| `APP_CENTER_SERVICE_IDENTITY_PRIVATE_KEY_PEM_B64` | `serve` | — | strict standard Base64 of a PKCS#1/PKCS#8 RSA private-key PEM |
| `APP_CENTER_SERVICE_IDENTITY_TTL` | no | `1m` | lifetime of each service-call JWS |
| `APP_CENTER_SERVICE_CALLERS_B64` | `serve` | — | strict standard Base64 JSON registry of Auth service callers, public keys and the five `app.oauth.*` permissions and explicitly enabled `app.account-owner-exit.*` permissions |
| `APP_CENTER_SERVICE_IDENTITY_MAX_TTL` | no | `1m` | maximum lifetime accepted for Auth service-call JWS |
| `APP_CENTER_SERVICE_IDENTITY_CLOCK_SKEW` | no | `30s` | clock-skew allowance for Auth service-call JWS |
| `APP_CENTER_IDENTITY_ISSUER` | `serve` | — | Expected JWS `iss` |
| `APP_CENTER_IDENTITY_AUDIENCE` | no | `iwut-app-center` | Audience the JWS `aud` must contain |
| `APP_CENTER_IDENTITY_MAX_TTL` | no | `5m` | Maximum `exp - iat` accepted |
| `APP_CENTER_IDENTITY_CLOCK_SKEW` | no | `30s` | Clock-skew allowance for time claims |
| `APP_CENTER_IDENTITY_PUBLIC_KEYS` | `serve` | — | Comma-separated `kid=PEM-file` pairs, each RSA key at least 2048 bits |

`serve` refuses to start on a missing or invalid value. Do not commit keys,
tokens or credentials. User-identity public keys remain file references;
service private-key PEM is Base64-wrapped for ENV transport and should be
injected by the deployment secret mechanism.

## Tests

- `go test ./...` includes the root `architecture_test.go` boundary guard.
- MongoDB integration tests (schema, indexes, transactions, concurrency) run
  only when `MONGODB_INTEGRATION_URI` is set; use
  `./scripts/test-mongo-integration.sh` or `make test-mongo`.
- The UC-APP-001–012 end-to-end tests in `cmd/app-center` run only when
  `MONGODB_INTEGRATION_URI` is set. It explicitly migrates an isolated database,
  starts the real `wireApp` composition root with real Kratos HTTP and native
  gRPC listeners, presents real RS256 compact JWS identities and asserts
  persisted application/version/review/publication/history rows. The consumer
  suites use a real test Auth gRPC listener implementing the shared generated
  Scope Catalog, Developer Status and System Principal interfaces. UC-APP-007
  covers test-slot creation, replacement and no-op through HTTP/gRPC and checks
  that publication leaves approved versions unchanged. UC-APP-008 covers real
  random secret generation, fragment URL round trips for default/custom prefixes,
  HTTP/gRPC no-store responses, atomic rotation and hash-only BSON persistence.
  No automatic migration happens during serve.

The Auth client signs every unary call with a fresh short-lived service JWS.
Auth-owned SYSTEM principal IDs are resolved lazily by purpose and successful
responses are cached for the process lifetime; App Center has no static System
Auth ID startup dependency.

UC-APP-007 consumer delivery is implemented. Auth's authoritative MongoDB Scope
Catalog and complete two-service production dependency verification remain
tracked in the external implementation coverage table; the test Auth server
does not close those obligations.

Tester join URLs use `{prefix}#joinLinkId={id}&secret={base64urlSecret}`. The
prefix is configurable without network or DNS access; the default is a mock
entrance with no hosted page. Leading/trailing whitespace, an explicit empty
value, userinfo, query or fragment fails startup. Paths and trailing slashes are
preserved. Only the entrance is mock: secrets use 32 bytes from the OS random
source and MongoDB stores SHA-256 of the raw bytes. A complete URL is returned
once after a successful create/rotation; never log or trace it. Frontend scanning and production Gateway identity issuance are delivered separately.
UC-APP-009 supplies authenticated membership creation.


UC-APP-012 resolves an ACTIVE Tester’s current test launch target through
`POST /v1/applications/{application_id}/test-launch:resolve` or native gRPC
`app_center.v1.catalog.Catalog/ResolveTestLaunchTarget`. The HTTP body contains
only `hostRpcApiMajor` and `hostCapabilities`; the authenticated caller comes
from the verified identity header. Duplicate host capabilities are normalized
as a set. Administrator or Developer status does not imply Tester membership.

Resolution reads Application, Membership, the exact-major Publication, matching
History, Version and approved Review in one read-only MongoDB snapshot. It
performs no Auth, URL or DNS calls, acquires no write fence and does not fall
back to another major or slot. No new migration is required beyond the existing
`0010_application_tester_membership` baseline. Concurrent removal or replacement
is observed according to the snapshot, not a long-lived launch lease.

HTTP and gRPC responses use `Cache-Control: private, no-store`. Missing required
capabilities return HTTP 422 / gRPC FAILED_PRECONDITION, with only a sorted
`missingCapabilities` JSON-array string in error metadata. Inconsistent stored
publication/approval facts return HTTP 503 / gRPC UNAVAILABLE and produce a safe
internal alert. Neither failure returns a partial launch descriptor.

UC-APP-023 adds the unified single-Application resolver at
`POST /v1/applications/{application_id}/launch-target:resolve` and native gRPC
`app_center.v1.runtime_resolution.RuntimeResolutionService/ResolveLaunchTarget`.
Identity is optional: no identity considers only Stable, while a valid identity
enables server-side Tester and Grey cohort checks. A present invalid identity is
rejected rather than downgraded to anonymous.

The resolver reads only the exact RPC major and chooses `TEST > GREY > STABLE`.
Missing host capabilities may fall through to the next channel; dangling,
cross-Application, non-approved or snapshot-drifted state fails closed with
HTTP 500 / gRPC INTERNAL. Callers without an ACTIVE Tester episode do not load
the Test pointer or Test Version. The endpoint returns one channel-qualified
descriptor with `Cache-Control: private, no-store`; it does not return Profile,
Filter, OAuth client IDs, cohort material or alternative candidates.

UC-APP-024 exposes the ordinary public catalog through
`POST /v1/catalog/applications:search`,
`POST /v1/catalog/applications/{application_id}:get` and native gRPC
`app_center.v1.application_catalog.ApplicationCatalogService`. Ordinary catalog
eligibility always requires a current approved public Profile and a compatible
Stable version for the requested exact RPC major. After that eligibility check,
an optional valid identity selects the same `TEST > GREY > STABLE` launch target
as UC-APP-023. A present invalid identity is rejected.

Each item contains the reviewed public Profile, one launch target and the current
`profile-filter-v1` projection. The App Center does not receive user profile data
or evaluate the Filter. List order is by Application ID and uses a query-bound
opaque keyset token; the default page size is 20 and the maximum is 100. Every
page or detail is assembled in one read-only MongoDB snapshot using batched local
reads. Pointer, approval snapshot or Filter corruption fails the whole request
with HTTP 500 / gRPC INTERNAL. Responses use `Cache-Control: private, no-store`.
Migration `0019_application_catalog_indexes` adds the partial Stable-candidate
scan index required by the list query.

### Account owner exit coordination (UC-APP-025)

Migration `0020_account_owner_exit` adds permanent account fences and durable
operations. `APP_CENTER_ACCOUNT_OWNER_EXIT_ENABLED` defaults to `false`. Enable
only when Auth is ready to provide owner-exit decisions. The incoming
`iwut-auth-center` registration needs `app.account-owner-exit.prepare`,
`app.account-owner-exit.finish` and `app.account-owner-exit.read`; the outgoing
App identity needs Auth's `auth.account-owner-exit.read` permission.

These three provider methods are native gRPC only. All owned applications block
exit, including unpublished drafts. This package does not transfer or close
applications. Persistent fences apply to creation even when the provider is
disabled; account-closure fences also prevent old USER identities from creating
Tester memberships. Developer withdrawal alone preserves ordinary Tester use.

The process runs a restartable reconciliation worker with five-second RPC
deadlines, bounded exponential retry, and 500-membership cleanup batches. A
missing or unavailable Auth decision never unlocks a fence. Closure completion
removes this account's Tester episodes and empty creation quota, preserving
other users, application Filters, and historical review attribution.
