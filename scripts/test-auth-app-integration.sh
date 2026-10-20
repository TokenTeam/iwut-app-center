#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/integration-lib.sh"
integration_init
auth_image="${AUTH_CENTER_IMAGE:-docker.cnb.cool/tokenteam/iwut-auth-center:latest}"
[[ -n "${auth_image}" ]] || { echo "AUTH_CENTER_IMAGE must not be empty" >&2; exit 1; }
for tool in openssl base64 sha256sum; do command -v "${tool}" >/dev/null || { echo "Missing ${tool}" >&2; exit 1; }; done
# Reserve separate Auth and controlled SMTP listeners as one set.
mapfile -t fixture_ports < <(python3 - <<'PORTS'
import socket
sockets = [socket.socket() for _ in range(2)]
for s in sockets: s.bind(('127.0.0.1', 0))
for s in sockets: print(s.getsockname()[1])
for s in sockets: s.close()
PORTS
)
auth_port="${AUTH_CENTER_INTEGRATION_PORT:-${fixture_ports[0]}}"
validate_port "${auth_port}"
[[ "${auth_port}" != "${fixture_ports[1]}" ]] || { echo "Auth and SMTP ports must differ" >&2; exit 1; }
export AUTH_FIXTURE_SMTP_ADDR="127.0.0.1:${fixture_ports[1]}"
export AUTH_FIXTURE_SMTP_CERT_FILE="${temporary_dir}/smtp-cert.pem" AUTH_FIXTURE_SMTP_KEY_FILE="${temporary_dir}/smtp-key.pem"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=localhost \
  -addext 'subjectAltName=IP:127.0.0.1' -keyout "${AUTH_FIXTURE_SMTP_KEY_FILE}" \
  -out "${AUTH_FIXTURE_SMTP_CERT_FILE}" >/dev/null 2>&1
printf '%s' 'known-compromised-fixture-password' | sha256sum | cut -d ' ' -f 1 >"${temporary_dir}/password-blocklist.txt"
password_blocklist_sha="$(sha256sum "${temporary_dir}/password-blocklist.txt" | cut -d ' ' -f 1)"

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
    {authId:"auth-dual-transfer-target",principalType:"USER",accountStatus:"ACTIVE",accountRevision:NumberLong(1),developerRevision:NumberLong(1),permissions:[],permissionRevision:NumberLong(1),developerStatus:"APPROVED",createdAt:new Date(),updatedAt:new Date()}
  ])' >/dev/null

docker run --detach --name "${auth_container_name}" \
  --network host --user "$(id -u):$(id -g)" \
  --mount "type=bind,src=${temporary_dir},dst=/run/iwut-integration,readonly" \
  --env "AUTH_CENTER_HTTP_ADDR=127.0.0.1:0" \
  --env "AUTH_CENTER_GRPC_ADDR=127.0.0.1:${auth_port}" \
  --env "AUTH_CENTER_MONGO_URI=${MONGODB_INTEGRATION_URI}" \
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
  --env AUTH_EMAIL_BINDING_ENABLED=true --env "AUTH_EMAIL_BINDING_HMAC_KEY=$(openssl rand 32 | base64 -w0)" \
  --env "AUTH_SMTP_ADDR=${AUTH_FIXTURE_SMTP_ADDR}" --env AUTH_SMTP_FROM=auth@example.test --env AUTH_SMTP_TLS_MODE=implicit_tls \
  --env AUTH_SMTP_CA_FILE=/run/iwut-integration/smtp-cert.pem --env AUTH_SMTP_TIMEOUT=10s \
  --env AUTH_PASSWORD_ENABLED=true --env "AUTH_PASSWORD_OPERATION_HMAC_KEY=$(openssl rand 32 | base64 -w0)" \
  --env AUTH_PASSWORD_BLOCKLIST_FILE=/run/iwut-integration/password-blocklist.txt \
  --env AUTH_PASSWORD_BLOCKLIST_VERSION=app-close-e2e-v1 --env AUTH_PASSWORD_BLOCKLIST_SOURCE=app-controlled-e2e \
  --env "AUTH_PASSWORD_BLOCKLIST_SHA256=${password_blocklist_sha}" --env AUTH_PASSWORD_READINESS_CONFIRMED=true \
  --env "AUTH_PROFILE_CATALOG_FILE=/run/iwut-integration/profile-catalog.json" \
  --env "AUTH_USER_IDENTITY_ISSUER=https://auth.e2e.test" \
  --env 'AUTH_USER_IDENTITY_PUBLIC_KEYS_JSON={"auth-close-e2e":"/run/iwut-integration/auth-identity.pub.pem"}' \
  --env "AUTH_USER_IDENTITY_SIGNING_KID=auth-close-e2e" \
  --env "AUTH_USER_IDENTITY_PRIVATE_KEY_PEM_B64=${auth_identity_private_key_b64}" \
  "${auth_image_id}" >/dev/null

# TCP is only an initial listener check. The Go fixture then requires an actual
# nonmutating v3 gRPC validation response before any business test proceeds.
deadline=$((SECONDS + startup_timeout))
while true; do
  running="$(docker inspect --format '{{.State.Running}}' "${auth_container_name}" 2>/dev/null || true)"
  [[ "${running}" == true ]] || { echo "Auth exited before readiness; see diagnostics" >&2; exit 1; }
  if python3 - "${auth_port}" <<'READY'
import socket, sys
try:
    with socket.create_connection(("127.0.0.1", int(sys.argv[1])), timeout=0.5): pass
except OSError: sys.exit(1)
READY
  then break; fi
  (( SECONDS < deadline )) || { echo "Auth readiness timed out" >&2; exit 1; }
  sleep 0.2
done
[[ "$(docker inspect --format '{{.State.Running}}' "${auth_container_name}")" == true ]] || { echo "Auth exited during readiness" >&2; exit 1; }
cd "${app_dir}"
if ! go test -v -count=1 -race -timeout=5m -run '^TestE2E_UCAPP005_026_027_RealAuthProcessServiceIdentity$' ./cmd/app-center; then
  printf '\nAuth Center output:\n' >&2
  echo "See ${integration_log_dir}/auth-center.log (local diagnostics)" >&2
  exit 1
fi
