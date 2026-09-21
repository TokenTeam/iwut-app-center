# App Center local gates. See README.md for the required environment and order.
GO ?= go
WIRE_PACKAGE := github.com/goforj/wire/cmd/wire@v1.2.0

.PHONY: help
help:
	@echo "App Center targets:"
	@echo "  make generate      regenerate cmd/app-center/wire_gen.go"
	@echo "  make api-generate  regenerate the API submodule Proto code"
	@echo "  make fmt-check     fail if any Go file is not gofmt-ed"
	@echo "  make build         compile all packages"
	@echo "  make test          run the unit/transport/architecture suite"
	@echo "  make test-mongo    run the MongoDB integration and UC-APP-001 E2E suites"
	@echo "  make vet           run go vet"
	@echo "  make wire-check    fail if wire_gen.go is stale"
	@echo "  make api-check     fail if generated API code drifted from Proto"
	@echo "  make check         run every local gate"
	@echo "  make migrate       apply MongoDB migrations (Mongo settings only)"
	@echo "  make run           start the HTTP + gRPC server"
	@echo ""
	@echo "Order: make generate && make migrate && make run"

.PHONY: generate
generate:
	$(GO) run $(WIRE_PACKAGE) gen ./cmd/app-center

.PHONY: api-generate
api-generate:
	$(MAKE) -C api proto-gen

.PHONY: fmt-check
fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi

.PHONY: build
build:
	$(GO) build ./...

.PHONY: test
test:
	$(GO) test ./...

# Starts and removes an isolated transaction-capable replica set, then runs the
# MongoDB adapter integration suite and the UC-APP-001 end-to-end suite. Requires
# Docker; plain `make test` skips the MongoDB tests when MONGODB_INTEGRATION_URI
# is absent.
.PHONY: test-mongo
test-mongo:
	./scripts/test-mongo-integration.sh

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: wire-check
wire-check:
	$(GO) run $(WIRE_PACKAGE) diff ./cmd/app-center

.PHONY: api-check
api-check:
	$(MAKE) -C api proto-check

.PHONY: check
check: fmt-check build test vet wire-check api-check

.PHONY: migrate
migrate:
	$(GO) run ./cmd/app-center migrate

.PHONY: run
run:
	$(GO) run ./cmd/app-center serve
