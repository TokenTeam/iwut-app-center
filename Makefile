# App Center local gates. See README.md for the required environment and order.
GO ?= go
PYTHON ?= python3
VERIFY = $(PYTHON) -B scripts/verify.py
WIRE_PACKAGE := github.com/goforj/wire/cmd/wire@v1.2.0

.PHONY: help
help:
	@echo "App Center targets:"
	@echo "  make generate      regenerate cmd/app-center/wire_gen.go"
	@echo "  make api-generate  regenerate the API submodule Proto code"
	@echo "  make fmt-check     fail if any Go file is not gofmt-ed"
	@echo "  make build         compile all packages"
	@echo "  make test          run the unit/transport/architecture suite"
	@echo "  make test-mongo    run the MongoDB integration and all HTTP/gRPC E2E suites"
	@echo "  make test-auth-app run the production-identity Auth+App double-service E2E"
	@echo "  make vet           run go vet"
	@echo "  make wire-check    fail if wire_gen.go is stale"
	@echo "  make api-check     fail if generated API code drifted from Proto"
	@echo "  make check         quick gates (no Mongo, race or real Auth)"
	@echo "  make check-full    quick + race + isolated Mongo/HTTP/gRPC E2E"
	@echo "  make check-auth-app full backend + real Auth/App E2E"
	@echo "  make doctor        quick prerequisite check only"
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
	@unformatted=$$(gofmt -l .) || exit $$?; \
	if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi

.PHONY: build
build:
	$(GO) build ./...

.PHONY: test
test:
	$(GO) test ./...

# Starts and removes an isolated transaction-capable replica set, then runs the
# MongoDB adapter integration suite and the App Center end-to-end suites. Requires
# Docker; plain `make test` skips the MongoDB tests when MONGODB_INTEGRATION_URI
# is absent.
.PHONY: test-mongo
test-mongo:
	./scripts/test-mongo-integration.sh

.PHONY: test-auth-app
test-auth-app:
	./scripts/test-auth-app-integration.sh

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: wire-check
wire-check:
	$(GO) run $(WIRE_PACKAGE) diff ./cmd/app-center

.PHONY: api-check
api-check:
	$(MAKE) -C api proto-check

.PHONY: test-race test-scripts doctor check check-full check-auth-app
test-race:
	$(GO) test -race ./...

test-scripts:
	$(PYTHON) -B -m unittest discover -s scripts/tests

doctor:
	$(VERIFY) quick --doctor

check:
	$(VERIFY) quick

check-full:
	$(VERIFY) backend

check-auth-app:
	$(VERIFY) cross-service

.PHONY: migrate
migrate:
	$(GO) run ./cmd/app-center migrate

.PHONY: run
run:
	$(GO) run ./cmd/app-center serve
