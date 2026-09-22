#!/usr/bin/env bash
set -euo pipefail

container_name="iwut-app-center-mongo-integration-$$"
mongo_port="${MONGODB_INTEGRATION_PORT:-27028}"

cleanup() {
  docker rm -f "${container_name}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run --detach --rm \
  --name "${container_name}" \
  --ulimit nofile=65536:65536 \
  --publish "127.0.0.1:${mongo_port}:${mongo_port}" \
  mongo:8.2.12 \
  mongod --replSet rs0 --bind_ip_all --port "${mongo_port}" >/dev/null

for _ in $(seq 1 60); do
  if docker exec "${container_name}" mongosh --quiet --port "${mongo_port}" --eval 'db.runCommand({ping: 1}).ok' >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

docker exec "${container_name}" mongosh --quiet --port "${mongo_port}" --eval \
  "rs.initiate({_id: 'rs0', members: [{_id: 0, host: '127.0.0.1:${mongo_port}'}]})" >/dev/null

for _ in $(seq 1 60); do
  if docker exec "${container_name}" mongosh --quiet --port "${mongo_port}" --eval 'if (!db.hello().isWritablePrimary) { quit(1) }' >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

export MONGODB_INTEGRATION_URI="mongodb://127.0.0.1:${mongo_port}/?replicaSet=rs0&directConnection=true"

# One command runs the MongoDB adapter integration suite and the UC-APP-001–009
# end-to-end suites (real Kratos HTTP + gRPC, real RS256 JWS, generated Auth
# client/server contract, explicit migrations through 0010_application_tester_membership)
# against the same isolated replica set.
go test -count=1 "$@" ./internal/adapter/mongo ./cmd/app-center
