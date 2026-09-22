#!/usr/bin/env bash
set -euo pipefail

app_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
auth_dir="${AUTH_CENTER_SOURCE_DIR:-$(cd "${app_dir}/../iwut-auth-center-ddd" && pwd)}"
container_name="iwut-auth-app-integration-$$"
mongo_port="${MONGODB_INTEGRATION_PORT:-27029}"
auth_port="${AUTH_CENTER_INTEGRATION_PORT:-29001}"
temporary_dir="$(mktemp -d)"
auth_pid=""

cleanup() {
  if [[ -n "${auth_pid}" ]]; then
    kill "${auth_pid}" >/dev/null 2>&1 || true
    wait "${auth_pid}" >/dev/null 2>&1 || true
  fi
  docker rm -f "${container_name}" >/dev/null 2>&1 || true
  rm -rf "${temporary_dir}"
}
trap cleanup EXIT

openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "${temporary_dir}/app-center.pem" >/dev/null 2>&1
openssl pkey -in "${temporary_dir}/app-center.pem" -pubout -out "${temporary_dir}/app-center.pub.pem"
private_key_b64="$(base64 -w0 < "${temporary_dir}/app-center.pem")"
public_key_b64="$(base64 -w0 < "${temporary_dir}/app-center.pub.pem")"
caller_registry="$(printf '{"iwut-app-center":{"status":"ACTIVE","keys":{"app-center-e2e":{"publicKeyPemB64":"%s"}},"permissions":["auth.scope-catalog.read","auth.developer-status.read","auth.system-principal.resolve"],"systemPrincipalPurposes":["app-center.review-auto-rejection"]}}' "${public_key_b64}" | base64 -w0)"

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
export AUTH_CENTER_INTEGRATION_TARGET="127.0.0.1:${auth_port}"
export AUTH_CENTER_INTEGRATION_DATABASE="iwut_auth_center_dual_e2e"
export APP_CENTER_SERVICE_IDENTITY_ID="iwut-app-center"
export APP_CENTER_SERVICE_IDENTITY_KID="app-center-e2e"
export APP_CENTER_SERVICE_IDENTITY_PRIVATE_KEY_PEM_B64="${private_key_b64}"

docker exec "${container_name}" mongosh --quiet --port "${mongo_port}" "${AUTH_CENTER_INTEGRATION_DATABASE}" --eval \
  'db.auth_principals.insertOne({authId:"auth-dual-service-admin",principalType:"USER",developerStatus:"SUSPENDED",createdAt:new Date(),updatedAt:new Date()})' >/dev/null

(
  cd "${auth_dir}"
  AUTH_CENTER_GRPC_ADDR="${AUTH_CENTER_INTEGRATION_TARGET}" \
  AUTH_CENTER_MONGO_URI="${MONGODB_INTEGRATION_URI}" \
  AUTH_CENTER_MONGO_DATABASE="${AUTH_CENTER_INTEGRATION_DATABASE}" \
  AUTH_CENTER_SERVICE_CALLERS_B64="${caller_registry}" \
  go run ./cmd/auth-center >"${temporary_dir}/auth-center.log" 2>&1
) &
auth_pid=$!

cd "${app_dir}"
if ! go test -count=1 -run '^TestE2E_UCAPP005_RealAuthProcessServiceIdentity$' ./cmd/app-center; then
  printf '\nAuth Center output:\n' >&2
  tail -200 "${temporary_dir}/auth-center.log" >&2 || true
  exit 1
fi
