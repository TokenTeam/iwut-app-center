#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/integration-lib.sh"
integration_init
auth_image="${AUTH_CENTER_IMAGE:-docker.cnb.cool/tokenteam/iwut-auth-center:latest}"
[[ -n "${auth_image}" ]] || { echo "AUTH_CENTER_IMAGE must not be empty" >&2; exit 1; }
command -v openssl >/dev/null || { echo "Missing openssl" >&2; exit 1; }
auth_port="${AUTH_CENTER_INTEGRATION_PORT:-$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')}"
validate_port "${auth_port}"

# Always resolve the remote tag at the start of the gate. The immutable image ID
# is printed into the gate log so a report can identify what "latest" meant.
timeout 300s docker pull "${auth_image}"
auth_image_id="$(docker image inspect --format '{{.Id}}' "${auth_image}")"
echo "Auth Center image: ${auth_image} (${auth_image_id})"

openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "${temporary_dir}/app-center.pem" >/dev/null 2>&1
openssl pkey -in "${temporary_dir}/app-center.pem" -pubout -out "${temporary_dir}/app-center.pub.pem"
private_key_b64="$(base64 -w0 < "${temporary_dir}/app-center.pem")"
public_key_b64="$(base64 -w0 < "${temporary_dir}/app-center.pub.pem")"
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "${temporary_dir}/auth-identity.pem" >/dev/null 2>&1
openssl pkey -in "${temporary_dir}/auth-identity.pem" -pubout -out "${temporary_dir}/auth-identity.pub.pem"
auth_identity_private_key_b64="$(base64 -w0 < "${temporary_dir}/auth-identity.pem")"
association_encrypt_key="$(printf 'auth-app-association-encrypt-key' | base64 -w0)"
association_lookup_key="$(printf 'auth-app-association-lookup-key-' | base64 -w0)"
printf '{"definitions":[]}' >"${temporary_dir}/profile-catalog.json"
chmod 0444 "${temporary_dir}/auth-identity.pub.pem" "${temporary_dir}/profile-catalog.json"
caller_registry="$(printf '{"iwut-app-center":{"status":"ACTIVE","keys":{"app-center-e2e":{"publicKeyPemB64":"%s"}},"permissions":["auth.scope-catalog.read","auth.developer-status.read","auth.system-principal.resolve","auth.application-closure.apply","auth.application-closure.read"],"systemPrincipalPurposes":["app-center.review-auto-rejection"]}}' "${public_key_b64}" | base64 -w0)"

start_integration_mongo
export AUTH_CENTER_INTEGRATION_TARGET="127.0.0.1:${auth_port}"
export AUTH_CENTER_INTEGRATION_DATABASE="iwut_auth_center_dual_e2e"
export APP_CENTER_SERVICE_IDENTITY_ID="iwut-app-center"
export APP_CENTER_SERVICE_IDENTITY_KID="app-center-e2e"
export APP_CENTER_SERVICE_IDENTITY_PRIVATE_KEY_PEM_B64="${private_key_b64}"
export AUTH_CENTER_IDENTITY_PUBLIC_KEY_PATH="${temporary_dir}/auth-identity.pub.pem"

docker exec "${container_name}" mongosh --quiet --port 27017 "${AUTH_CENTER_INTEGRATION_DATABASE}" --eval \
  'db.auth_principals.insertMany([
    {authId:"auth-dual-service-admin",principalType:"USER",accountStatus:"ACTIVE",accountRevision:NumberLong(1),developerRevision:NumberLong(1),permissions:[],permissionRevision:NumberLong(1),developerStatus:"SUSPENDED",createdAt:new Date(),updatedAt:new Date()},
    {authId:"auth-dual-transfer-source",principalType:"USER",accountStatus:"ACTIVE",accountRevision:NumberLong(1),developerRevision:NumberLong(1),permissions:[],permissionRevision:NumberLong(1),developerStatus:"APPROVED",createdAt:new Date(),updatedAt:new Date()},
    {authId:"auth-dual-transfer-target",principalType:"USER",accountStatus:"ACTIVE",accountRevision:NumberLong(1),developerRevision:NumberLong(1),permissions:[],permissionRevision:NumberLong(1),developerStatus:"APPROVED",createdAt:new Date(),updatedAt:new Date()}
  ])' >/dev/null

docker run --detach --name "${auth_container_name}" \
  --network "${network_name}" \
  --publish "127.0.0.1:${auth_port}:9000" \
  --mount "type=bind,src=${temporary_dir}/profile-catalog.json,dst=/run/iwut-integration/profile-catalog.json,readonly" \
  --mount "type=bind,src=${temporary_dir}/auth-identity.pub.pem,dst=/run/iwut-integration/auth-identity.pub.pem,readonly" \
  --env "AUTH_CENTER_HTTP_ADDR=:0" \
  --env "AUTH_CENTER_GRPC_ADDR=:9000" \
  --env "AUTH_CENTER_MONGO_URI=mongodb://mongo:27017/?replicaSet=rs0&directConnection=true" \
  --env "AUTH_CENTER_MONGO_DATABASE=${AUTH_CENTER_INTEGRATION_DATABASE}" \
  --env "AUTH_CENTER_SERVICE_CALLERS_B64=${caller_registry}" \
  --env "AUTH_APPLICATION_CLOSURE_ENABLED=true" \
  --env "AUTH_APPLICATION_CLOSE_REAUTH_ENABLED=true" \
  --env "AUTH_USER_ENDPOINTS_ENABLED=true" \
  --env "AUTH_IDENTITY_ISSUANCE_ENABLED=true" \
  --env "AUTH_AUTHENTICATION_SERVICE_ID=iwut-auth-center:test" \
  --env "AUTH_SESSION_TTL=24h" \
  --env "AUTH_ASSOC_KEY_VERSION=v1" \
  --env "AUTH_ASSOC_ENCRYPT_KEY=${association_encrypt_key}" \
  --env "AUTH_ASSOC_LOOKUP_KEY=${association_lookup_key}" \
  --env "AUTH_PROFILE_CATALOG_FILE=/run/iwut-integration/profile-catalog.json" \
  --env "AUTH_USER_IDENTITY_ISSUER=https://auth.e2e.test" \
  --env 'AUTH_USER_IDENTITY_PUBLIC_KEYS_JSON={"auth-close-e2e":"/run/iwut-integration/auth-identity.pub.pem"}' \
  --env "AUTH_USER_IDENTITY_SIGNING_KID=auth-close-e2e" \
  --env "AUTH_USER_IDENTITY_PRIVATE_KEY_PEM_B64=${auth_identity_private_key_b64}" \
  "${auth_image}" >/dev/null

# Bound startup and distinguish a dead server from a slow connection.
deadline=$((SECONDS + startup_timeout))
until python3 - "${auth_port}" <<'READY'
import socket, sys
try:
    with socket.create_connection(("127.0.0.1", int(sys.argv[1])), timeout=0.5):
        pass
except OSError:
    sys.exit(1)
READY
do
  docker inspect "${auth_container_name}" >/dev/null 2>&1 || { echo "Auth exited before readiness; see diagnostics" >&2; exit 1; }
  (( SECONDS < deadline )) || { echo "Auth readiness timed out" >&2; exit 1; }
  sleep 0.2
done
cd "${app_dir}"
if ! go test -count=1 -race -run '^TestE2E_UCAPP005_026_027_RealAuthProcessServiceIdentity$' ./cmd/app-center; then
  printf '\nAuth Center output:\n' >&2
  echo "See ${integration_log_dir}/auth-center.log (local diagnostics)" >&2
  exit 1
fi
