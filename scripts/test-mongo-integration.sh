#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/integration-lib.sh"
integration_init
start_integration_mongo
unset AUTH_CENTER_INTEGRATION_TARGET AUTH_CENTER_INTEGRATION_DATABASE
# No filter: all Mongo adapter + real HTTP/gRPC E2E tests. Local -run/-race/-v
# flags are forwarded; filtered runs do not replace backend verification.
go test -count=1 "$@" ./internal/adapter/mongo ./cmd/app-center
