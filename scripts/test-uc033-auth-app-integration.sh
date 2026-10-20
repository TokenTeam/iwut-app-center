#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/integration-lib.sh"
integration_init

auth_image="${AUTH_CENTER_IMAGE:-docker.cnb.cool/tokenteam/iwut-auth-center:latest}"
[[ -n "${auth_image}" ]] || { echo "AUTH_CENTER_IMAGE must not be empty" >&2; exit 1; }
for tool in openssl base64 sha256sum; do command -v "${tool}" >/dev/null || { echo "Missing ${tool}" >&2; exit 1; }; done

# Reserve all five default listeners together, including controlled SMTP.
mapfile -t fixture_ports < <(python3 - <<'PORTS'
import socket
sockets = [socket.socket() for _ in range(5)]
for s in sockets: s.bind(('127.0.0.1', 0))
for s in sockets: print(s.getsockname()[1])
for s in sockets: s.close()
PORTS
)
auth_http_port="${UC033_AUTH_HTTP_PORT:-${fixture_ports[0]}}"
auth_grpc_port="${UC033_AUTH_GRPC_PORT:-${fixture_ports[1]}}"
app_http_port="${UC033_APP_HTTP_PORT:-${fixture_ports[2]}}"
app_grpc_port="${UC033_APP_GRPC_PORT:-${fixture_ports[3]}}"
for port in "${auth_http_port}" "${auth_grpc_port}" "${app_http_port}" "${app_grpc_port}" "${fixture_ports[4]}"; do
  validate_port "${port}"
done
python3 - "${auth_http_port}" "${auth_grpc_port}" "${app_http_port}" "${app_grpc_port}" "${fixture_ports[4]}" <<'PORTCHECK'
import sys
ports = list(map(int, sys.argv[1:]))
if len(set(ports)) != len(ports): raise SystemExit('Fixture ports must be distinct')
PORTCHECK
export AUTH_FIXTURE_SMTP_ADDR="127.0.0.1:${fixture_ports[4]}"
export AUTH_FIXTURE_SMTP_CERT_FILE="${temporary_dir}/smtp-cert.pem" AUTH_FIXTURE_SMTP_KEY_FILE="${temporary_dir}/smtp-key.pem"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=localhost \
  -addext 'subjectAltName=IP:127.0.0.1' -keyout "${AUTH_FIXTURE_SMTP_KEY_FILE}" \
  -out "${AUTH_FIXTURE_SMTP_CERT_FILE}" >/dev/null 2>&1
printf '%s' 'known-compromised-fixture-password' | sha256sum | cut -d ' ' -f 1 >"${temporary_dir}/password-blocklist.txt"
password_blocklist_sha="$(sha256sum "${temporary_dir}/password-blocklist.txt" | cut -d ' ' -f 1)"

timeout 300s docker pull "${auth_image}"
auth_image_id="$(docker image inspect --format '{{.Id}}' "${auth_image}")"
echo "Auth Center image: ${auth_image} (${auth_image_id})"

generate_key() {
  local name="$1"
  openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "${temporary_dir}/${name}.pem" >/dev/null 2>&1
  openssl pkey -in "${temporary_dir}/${name}.pem" -pubout -out "${temporary_dir}/${name}.pub.pem"
}

generate_key app-service
generate_key auth-service
generate_key auth-identity
generate_key oidc
generate_key delegation

app_private_key_b64="$(base64 -w0 < "${temporary_dir}/app-service.pem")"
app_public_key_b64="$(base64 -w0 < "${temporary_dir}/app-service.pub.pem")"
auth_public_key_b64="$(base64 -w0 < "${temporary_dir}/auth-service.pub.pem")"
auth_identity_private_key_b64="$(base64 -w0 < "${temporary_dir}/auth-identity.pem")"
association_encrypt_key="$(openssl rand -base64 32 | tr -d '\n')"
association_lookup_key="$(openssl rand -base64 32 | tr -d '\n')"

auth_callers_json="$(printf '{"iwut-app-center":{"status":"ACTIVE","keys":{"app-center-uc033":{"publicKeyPemB64":"%s"}},"permissions":["auth.scope-catalog.read","auth.developer-status.read","auth.system-principal.resolve"],"systemPrincipalPurposes":["app-center.review-auto-rejection"]},"gateway":{"status":"ACTIVE","keys":{"app-center-uc033":{"publicKeyPemB64":"%s"}},"permissions":["auth.oauth.delegation.issue"]}}' "${app_public_key_b64}" "${app_public_key_b64}")"
auth_callers_b64="$(printf '%s' "${auth_callers_json}" | base64 -w0)"
app_callers_json="$(printf '{"iwut-auth-center":{"status":"ACTIVE","keys":{"auth-service-uc033":{"publicKeyPemB64":"%s"}},"permissions":["app.oauth.client.read","app.oauth.client.verify","app.oauth.runtime.resolve","app.oauth.context.resolve","app.oauth.redirects.read"]}}' "${auth_public_key_b64}")"
app_callers_b64="$(printf '%s' "${app_callers_json}" | base64 -w0)"

python3 - "${temporary_dir}" "${app_grpc_port}" <<'PY'
import datetime
import hashlib
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
app_grpc_port = sys.argv[2]

def write(name, value):
    (root / name).write_text(json.dumps(value, separators=(",", ":")), encoding="utf-8")

write("profile-catalog.json", {"definitions": []})
write("oauth-scopes.json", [
    {"Name": "openid", "Label": "Login", "Description": "Identify account", "Kind": "PROTOCOL", "Audience": "", "Enabled": True},
    {"Name": "offline_access", "Label": "Background", "Description": "Refresh access", "Kind": "PROTOCOL", "Audience": "", "Enabled": True},
    {"Name": "items.read", "Label": "Items", "Description": "Read items", "Kind": "RESOURCE", "Audience": "resource-service", "Enabled": True},
])
route = {
    "routeId": "resource-read",
    "audience": "resource-service",
    "protocol": "HTTP",
    "method": "GET",
    "path": "/items/{id}",
    "requiredScopes": ["items.read"],
    "allowedChannels": ["STABLE"],
}
write("oauth-routes.json", {
    "version": 1,
    "routes": [route],
    "callers": {"gateway": {"routes": ["resource-read"], "audiences": ["resource-service"]}},
})
canonical = json.dumps({"version": 1, "routes": [route]}, separators=(",", ":")).encode()
(root / "route-policy-digest").write_text(hashlib.sha256(canonical).hexdigest(), encoding="ascii")
write("clock.json", {"observedAt": datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z"), "offsetMillis": 0})
oauth_config = {
    "clockObservationFile": "/run/iwut-uc033/clock.json",
    "resourceScopesReady": True,
    "issuer": "https://auth.example.test/oidc",
    "portalOrigin": "https://auth.example.test",
    "portalLoginURL": "https://auth.example.test/login",
    "sectorSuffix": "sectors.example.test",
    "catalogFile": "/run/iwut-uc033/oauth-scopes.json",
    "appTarget": "127.0.0.1:" + app_grpc_port,
    "appPlaintextLoopback": True,
    "serviceKeyFile": "/run/iwut-uc033/auth-service.pem",
    "serviceKeyId": "auth-service-uc033",
    "oidcKeyFile": "/run/iwut-uc033/oidc.pem",
    "oidcKeyId": "oidc-uc033",
    "delegationKeyFile": "/run/iwut-uc033/delegation.pem",
    "delegationKeyId": "delegation-uc033",
    "routePolicyFile": "/run/iwut-uc033/oauth-routes.json",
    "publicEnabled": True,
    "portalReady": True,
    "gatewayReady": True,
    "refreshEnabled": True,
}
write("oauth.json", oauth_config)
PY

# Keep fixture keys/private material under integration_init's umask 077;
# the Auth container runs as this user and only reads the mounted directory.
start_integration_mongo

export AUTH_CENTER_INTEGRATION_TARGET="127.0.0.1:${auth_grpc_port}"
export AUTH_CENTER_INTEGRATION_DATABASE="iwut_auth_center_uc033_e2e"
export APP_CENTER_SERVICE_IDENTITY_ID="iwut-app-center"
export APP_CENTER_SERVICE_IDENTITY_KID="app-center-uc033"
export APP_CENTER_SERVICE_IDENTITY_PRIVATE_KEY_PEM_B64="${app_private_key_b64}"
export UC033_APP_SERVICE_CALLERS_B64="${app_callers_b64}"
export UC033_AUTH_HTTP_ADDR="127.0.0.1:${auth_http_port}"
export UC033_APP_HTTP_ADDR="127.0.0.1:${app_http_port}"
export UC033_APP_GRPC_ADDR="127.0.0.1:${app_grpc_port}"
export UC033_ROUTE_POLICY_DIGEST="$(<"${temporary_dir}/route-policy-digest")"
export UC033_AUTH_CLOCK_FILE="${temporary_dir}/clock.json"

docker run --detach --name "${auth_container_name}" \
  --network host --user "$(id -u):$(id -g)" \
  --mount "type=bind,src=${temporary_dir},dst=/run/iwut-uc033,readonly" \
  --env "AUTH_CENTER_HTTP_ADDR=127.0.0.1:${auth_http_port}" \
  --env "AUTH_CENTER_GRPC_ADDR=127.0.0.1:${auth_grpc_port}" \
  --env "AUTH_CENTER_MONGO_URI=${MONGODB_INTEGRATION_URI}" \
  --env "AUTH_CENTER_MONGO_DATABASE=${AUTH_CENTER_INTEGRATION_DATABASE}" \
  --env "AUTH_CENTER_SERVICE_CALLERS_B64=${auth_callers_b64}" \
  --env "AUTH_USER_ENDPOINTS_ENABLED=true" \
  --env "AUTH_IDENTITY_ISSUANCE_ENABLED=true" \
  --env "AUTH_AUTHENTICATION_SERVICE_ID=iwut-auth-center:test" \
  --env "AUTH_SESSION_TTL=24h" \
  --env "AUTH_ASSOC_KEY_VERSION=v1" \
  --env "AUTH_ASSOC_ENCRYPT_KEY=${association_encrypt_key}" \
  --env "AUTH_ASSOC_LOOKUP_KEY=${association_lookup_key}" \
  --env AUTH_EMAIL_BINDING_ENABLED=true --env "AUTH_EMAIL_BINDING_HMAC_KEY=$(openssl rand 32 | base64 -w0)" \
  --env "AUTH_SMTP_ADDR=${AUTH_FIXTURE_SMTP_ADDR}" --env AUTH_SMTP_FROM=auth@example.test --env AUTH_SMTP_TLS_MODE=implicit_tls \
  --env AUTH_SMTP_CA_FILE=/run/iwut-uc033/smtp-cert.pem --env AUTH_SMTP_TIMEOUT=10s \
  --env AUTH_PASSWORD_ENABLED=true --env "AUTH_PASSWORD_OPERATION_HMAC_KEY=$(openssl rand 32 | base64 -w0)" \
  --env AUTH_PASSWORD_BLOCKLIST_FILE=/run/iwut-uc033/password-blocklist.txt \
  --env AUTH_PASSWORD_BLOCKLIST_VERSION=app-uc033-e2e-v1 --env AUTH_PASSWORD_BLOCKLIST_SOURCE=app-controlled-e2e \
  --env "AUTH_PASSWORD_BLOCKLIST_SHA256=${password_blocklist_sha}" --env AUTH_PASSWORD_READINESS_CONFIRMED=true \
  --env "AUTH_PROFILE_CATALOG_FILE=/run/iwut-uc033/profile-catalog.json" \
  --env "AUTH_USER_IDENTITY_ISSUER=https://auth.e2e.test" \
  --env 'AUTH_USER_IDENTITY_PUBLIC_KEYS_JSON={"auth-identity-uc033":"/run/iwut-uc033/auth-identity.pub.pem"}' \
  --env "AUTH_USER_IDENTITY_SIGNING_KID=auth-identity-uc033" \
  --env "AUTH_USER_IDENTITY_PRIVATE_KEY_PEM_B64=${auth_identity_private_key_b64}" \
  --env "AUTH_CENTER_OAUTH_CONFIG_FILE=/run/iwut-uc033/oauth.json" \
  "${auth_image_id}" >/dev/null

deadline=$((SECONDS + startup_timeout))
# Listener readiness is followed by a nonmutating real gRPC readiness probe
# in registerAuthFixture, before the App test starts its business requests.
while true; do
  running="$(docker inspect --format '{{.State.Running}}' "${auth_container_name}" 2>/dev/null || true)"
  [[ "${running}" == "true" ]] || { echo "Auth exited before readiness; see diagnostics" >&2; exit 1; }
  if python3 - "${auth_http_port}" "${auth_grpc_port}" <<'READY'
import socket
import sys

for raw in sys.argv[1:]:
    try:
        with socket.create_connection(("127.0.0.1", int(raw)), timeout=0.5):
            pass
    except OSError:
        sys.exit(1)
READY
  then break; fi
  (( SECONDS < deadline )) || { echo "Auth readiness timed out" >&2; exit 1; }
  sleep 0.2
done
[[ "$(docker inspect --format '{{.State.Running}}' "${auth_container_name}")" == true ]] || { echo "Auth exited during readiness" >&2; exit 1; }

cd "${app_dir}"
if ! go test -v -count=1 -race -timeout=5m -run '^TestE2E_UCAPP033_RealAuthOAuthArchival$' ./cmd/app-center; then
  echo "See ${integration_log_dir}/auth-center.log (local diagnostics)" >&2
  exit 1
fi
