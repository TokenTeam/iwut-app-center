#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/integration-lib.sh"
integration_init
start_integration_mongo
unset AUTH_CENTER_INTEGRATION_TARGET AUTH_CENTER_INTEGRATION_DATABASE
# No filter: all Mongo adapter + real HTTP/gRPC E2E tests. The migration chain
# is intentionally exercised against many isolated databases, so the complete
# race suite needs more than Go's default ten-minute package timeout. Local
# -run/-race/-v/-timeout flags are forwarded after the default and can override
# it; filtered runs do not replace backend verification.
go test -count=1 -timeout=30m "$@" ./internal/adapter/mongo ./cmd/app-center
