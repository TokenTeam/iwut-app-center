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
read-only and does not create collections or indexes.

## Commands

| Command | Purpose |
| --- | --- |
| `make generate` | Regenerate `cmd/app-center/wire_gen.go` with `go run github.com/goforj/wire/cmd/wire@v1.2.0` |
| `make api-generate` | Regenerate the API submodule Go code (`make -C api proto-gen`) |
| `make fmt-check` | Fail if any Go file is not `gofmt`-ed |
| `make build` | Compile all packages |
| `make test` | Run the unit, transport and architecture test suite |
| `make test-mongo` | Run MongoDB integration and the UC-APP-001/002 end-to-end suites against an isolated replica set (Docker) |
| `make vet` | Run `go vet ./...` |
| `make wire-check` | Fail if `wire_gen.go` is stale |
| `make api-check` | Fail if the generated API code drifted from its Proto |
| `make check` | Run all local gates |
| `make migrate` | Apply pending MongoDB migrations and exit |
| `make run` | Start the HTTP and gRPC servers |

## Environment variables

`migrate` needs only the Mongo variables.

| Variable | Required | Default | Meaning |
| --- | --- | --- | --- |
| `APP_CENTER_MONGO_URI` | `serve`, `migrate` | — | MongoDB connection string (replica set or mongos) |
| `APP_CENTER_MONGO_DATABASE` | no | `iwut_app_center` | Target database name |
| `APP_CENTER_HTTP_ADDR` | no | `:8080` | HTTP listen address |
| `APP_CENTER_GRPC_ADDR` | no | `:9090` | gRPC listen address |
| `APP_CENTER_INITIAL_APPLICATION_QUOTA` | no | `10` | Initial per-admin creation quota; only used to lazily create a missing quota record |
| `APP_CENTER_SCOPE_CATALOG_CACHE_TTL` | no | `5m` | Scope Catalog cache TTL |
| `APP_CENTER_AUTH_SCOPE_CATALOG_GRPC_TARGET` | `serve` | — | Auth Center native gRPC target for Scope Catalog snapshots |
| `APP_CENTER_IDENTITY_ISSUER` | `serve` | — | Expected JWS `iss` |
| `APP_CENTER_IDENTITY_AUDIENCE` | no | `iwut-app-center` | Audience the JWS `aud` must contain |
| `APP_CENTER_IDENTITY_MAX_TTL` | no | `5m` | Maximum `exp - iat` accepted |
| `APP_CENTER_IDENTITY_CLOCK_SKEW` | no | `30s` | Clock-skew allowance for time claims |
| `APP_CENTER_IDENTITY_PUBLIC_KEYS` | `serve` | — | Comma-separated `kid=PEM-file` pairs, each RSA key at least 2048 bits |

`serve` refuses to start on a missing or invalid value. Do not commit keys,
tokens or credentials; `APP_CENTER_IDENTITY_PUBLIC_KEYS` points at files outside
version control.

## Tests

- `go test ./...` includes the root `architecture_test.go` boundary guard.
- MongoDB integration tests (schema, indexes, transactions, concurrency) run
  only when `MONGODB_INTEGRATION_URI` is set; use
  `./scripts/test-mongo-integration.sh` or `make test-mongo`.
- The UC-APP-001/002 end-to-end tests in `cmd/app-center` run only when
  `MONGODB_INTEGRATION_URI` is set. It explicitly migrates an isolated database,
  starts the real `wireApp` composition root with real Kratos HTTP and native
  gRPC listeners, presents real RS256 compact JWS identities and asserts
  persisted application/version rows. UC-APP-002 additionally uses a real test
  Auth gRPC listener implementing the shared generated Scope Catalog interface.
  No automatic migration happens during serve.

The Auth client currently uses an unauthenticated internal gRPC channel because
the platform service-identity credential is still an explicit open decision.
Do not treat this bootstrap connection as production-ready or expose the Auth
method through Gateway/Traefik.
