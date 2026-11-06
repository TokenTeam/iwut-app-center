#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/integration-lib.sh"
integration_init
script_management=false
case "${1:-}" in
  "") ;;
  --script-management) script_management=true ;;
  *) echo "Unknown integration mode" >&2; exit 2 ;;
esac
if [[ "${script_management}" == true ]]; then
  : "${AUTH_CENTER_IMAGE:?set the locally built Auth image}"
  : "${GATEWAY_LOCAL_IMAGE:?set the locally built Gateway image}"
  : "${APP_CENTER_LOCAL_IMAGE:?set the locally built App image}"
fi
for tool in openssl base64 tar sha256sum; do command -v "${tool}" >/dev/null || { echo "Missing ${tool}" >&2; exit 1; }; done

gateway_source="${GATEWAY_SOURCE_DIR:-${app_dir}/../iwut-gateway}"
gateway_source="$(cd "${gateway_source}" && pwd)"
gateway_head="$(git -C "${gateway_source}" rev-parse HEAD)"
gateway_api_head="$(git -C "${gateway_source}" rev-parse "${gateway_head}:api")"
# Build immutable committed snapshots; never edit the neighboring checkout.
mkdir -p "${temporary_dir}/gateway-source/api"
git -C "${gateway_source}" archive "${gateway_head}" | tar -x -C "${temporary_dir}/gateway-source"
git -C "${gateway_source}/api" archive "${gateway_api_head}" | tar -x -C "${temporary_dir}/gateway-source/api"
if [[ "${script_management}" == false ]]; then
  (cd "${temporary_dir}/gateway-source" && go build -o "${temporary_dir}/gateway" ./cmd/gateway && go build -o "${temporary_dir}/gateway-config" ./cmd/gateway-config)
fi
echo "Gateway source: ${gateway_head}; API: ${gateway_api_head}"
printf '%s\n' "${gateway_head}" "${gateway_api_head}" >"${integration_log_dir}/gateway-inputs.txt"

auth_image="${AUTH_CENTER_IMAGE:-docker.cnb.cool/tokenteam/iwut-auth-center:latest}"
if [[ "${script_management}" == false ]]; then timeout 300s docker pull "${auth_image}"; fi
auth_image_id="$(docker image inspect --format '{{.Id}}' "${auth_image}")"
echo "Auth Center image: ${auth_image} (${auth_image_id})"
traefik_image="$(sed -n 's/^image=//p' "${temporary_dir}/gateway-source/deploy/traefik/image.lock")"
[[ "${traefik_image}" == *@sha256:* ]] || { echo "Traefik image must be digest pinned" >&2; exit 1; }
timeout 300s docker pull "${traefik_image}"
traefik_container_name="${container_name}-traefik"
gateway_pid=""
gateway_container_name="${container_name}-gateway"
app_container_name="${container_name}-app"
cleanup_uc002() {
  local code=$?
  set +e
  if [[ -n "${gateway_pid}" ]]; then kill "${gateway_pid}" 2>/dev/null; wait "${gateway_pid}" 2>/dev/null; fi
  if [[ "${script_management}" == true ]]; then
    timeout 10s docker logs "${app_container_name}" >"${integration_log_dir}/app.log" 2>&1
    timeout 10s docker logs "${gateway_container_name}" >"${integration_log_dir}/gateway.log" 2>&1
    timeout 15s docker rm -f "${app_container_name}" "${gateway_container_name}" >/dev/null 2>&1
  fi
  timeout 10s docker logs "${traefik_container_name}" >"${integration_log_dir}/traefik.log" 2>&1
  timeout 15s docker rm -f "${traefik_container_name}" >/dev/null 2>&1
  (exit "${code}")
  integration_cleanup
}
trap cleanup_uc002 EXIT

# Allocate the entire fixture's listener set at once, avoiding duplicate ports.
mapfile -t ports < <(python3 - <<'PY'
import socket
sockets=[socket.socket() for _ in range(10)]
for s in sockets:s.bind(('127.0.0.1',0))
for s in sockets:print(s.getsockname()[1])
for s in sockets:s.close()
PY
)
auth_http="${ports[0]}"; auth_grpc="${ports[1]}"
export UC002_APP_HTTP_ADDR="127.0.0.1:${ports[2]}" UC002_APP_GRPC_ADDR="127.0.0.1:${ports[3]}"
gateway_http="${ports[4]}"; gateway_grpc="${ports[5]}"
export UC002_EDGE_HTTP_ADDR="127.0.0.1:${ports[6]}" UC002_EDGE_GRPC_ADDR="127.0.0.1:${ports[7]}"
if [[ "${script_management}" == true ]]; then
  export UC041_ADMIN_FORWARD_AUTH_ADDR="127.0.0.1:${ports[9]}"
fi
for name in app-service gateway-service auth-identity auth-service oidc delegation; do
 openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "${temporary_dir}/${name}.pem" >/dev/null 2>&1
 openssl pkey -in "${temporary_dir}/${name}.pem" -pubout -out "${temporary_dir}/${name}.pub.pem" >/dev/null 2>&1
done
# Disposable SMTP trust and password readiness inputs for formal v3 registration.
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=localhost \
 -addext 'subjectAltName=IP:127.0.0.1' -keyout "${temporary_dir}/smtp-key.pem" \
 -out "${temporary_dir}/smtp-cert.pem" >/dev/null 2>&1
# Auth accepts pinned whole-password SHA-256 digests, one per line.
printf '%s' 'known-compromised-fixture-password' | sha256sum | cut -d ' ' -f 1 >"${temporary_dir}/password-blocklist.txt"
password_blocklist_sha="$(sha256sum "${temporary_dir}/password-blocklist.txt" | cut -d ' ' -f 1)"
export AUTH_FIXTURE_SMTP_ADDR="127.0.0.1:${ports[8]}" AUTH_FIXTURE_SMTP_CERT_FILE="${temporary_dir}/smtp-cert.pem" AUTH_FIXTURE_SMTP_KEY_FILE="${temporary_dir}/smtp-key.pem"
export APP_CENTER_SERVICE_IDENTITY_ID=iwut-app-center APP_CENTER_SERVICE_IDENTITY_KID=uc002-app
export APP_CENTER_SERVICE_IDENTITY_PRIVATE_KEY_PEM_B64="$(base64 -w0 < "${temporary_dir}/app-service.pem")"
export AUTH_CENTER_IDENTITY_PUBLIC_KEY_PATH="${temporary_dir}/auth-identity.pub.pem"
export UC002_AUTH_CONTAINER_NAME="${auth_container_name}" UC002_BOOTSTRAP_LOG="${integration_log_dir}/bootstrap.log"
export UC002_GATEWAY_KEY_FILE="${temporary_dir}/gateway-service.pem"
export AUTH_CENTER_INTEGRATION_TARGET="127.0.0.1:${auth_grpc}" AUTH_CENTER_INTEGRATION_DATABASE=iwut_auth_uc002
app_public="$(base64 -w0 < "${temporary_dir}/app-service.pub.pem")"
gateway_public="$(base64 -w0 < "${temporary_dir}/gateway-service.pub.pem")"
callers="$(printf '{"iwut-app-center":{"status":"ACTIVE","keys":{"uc002-app":{"publicKeyPemB64":"%s"}},"permissions":["auth.scope-catalog.read","auth.developer-status.read","auth.system-principal.resolve"],"systemPrincipalPurposes":["app-center.review-auto-rejection"]},"iwut-gateway":{"status":"ACTIVE","keys":{"uc002-gateway":{"publicKeyPemB64":"%s"}},"permissions":["auth.identity.issue"],"identityAudiences":["iwut-app-center","iwut-auth-center"]}}' "${app_public}" "${gateway_public}" | base64 -w0)"

python3 - "${temporary_dir}" "${UC002_APP_GRPC_ADDR}" "${ports[6]}" "${ports[7]}" <<'PY'
import json,pathlib,sys
root=pathlib.Path(sys.argv[1]);base='/run/iwut-uc002/'
def write(name,value):(root/name).write_text(json.dumps(value,separators=(',',':')))
write('profile-catalog.json',{'definitions':[]})
write('scopes.json',[{'Name':name,'Label':name,'Description':'UC002 integration fixture','Kind':'RESOURCE','Audience':'resource-service','Enabled':enabled} for name,enabled in [('items.disabled',False),('items.read',True),('items.write',True)]])
write('routes.json',{'version':1,'routes':[],'callers':{}})
write('oauth.json',{'resourceScopesReady':True,'issuer':'https://auth.e2e.test/oidc','portalOrigin':'https://auth.e2e.test','portalLoginURL':'https://auth.e2e.test/login','sectorSuffix':'sectors.example.test','catalogFile':base+'scopes.json','appTarget':sys.argv[2],'appPlaintextLoopback':True,'serviceKeyFile':base+'auth-service.pem','serviceKeyId':'uc002-auth-service','oidcKeyFile':base+'oidc.pem','oidcKeyId':'uc002-oidc','delegationKeyFile':base+'delegation.pem','delegationKeyId':'uc002-delegation','routePolicyFile':base+'routes.json'})
static=(root/'gateway-source/deploy/traefik/static.yaml').read_text().replace('address: :8080','address: 127.0.0.1:'+sys.argv[3]).replace('address: :8081','address: 127.0.0.1:'+sys.argv[4])
# Keep the generator's private entrypoint bound only to an ephemeral loopback port.
static=static.replace('address: :8082','address: 127.0.0.1:0')
static=static.replace('/etc/traefik/dynamic.yaml','/run/iwut-uc002/dynamic.yaml')
(root/'traefik.yaml').write_text(static)
PY
start_integration_mongo

docker run --detach --name "${auth_container_name}" --network host --user "$(id -u):$(id -g)" \
 --mount "type=bind,src=${temporary_dir},dst=/run/iwut-uc002,readonly" \
 --env "AUTH_CENTER_HTTP_ADDR=127.0.0.1:${auth_http}" --env "AUTH_CENTER_GRPC_ADDR=127.0.0.1:${auth_grpc}" \
 --env "AUTH_CENTER_MONGO_URI=${MONGODB_INTEGRATION_URI}" --env "AUTH_CENTER_MONGO_DATABASE=${AUTH_CENTER_INTEGRATION_DATABASE}" \
 --env "AUTH_CENTER_SERVICE_CALLERS_B64=${callers}" --env AUTH_USER_ENDPOINTS_ENABLED=true --env AUTH_IDENTITY_ISSUANCE_ENABLED=true \
 --env AUTH_AUTHENTICATION_SERVICE_ID=iwut-auth-center:uc002 --env AUTH_SESSION_TTL=24h \
 --env AUTH_ASSOC_KEY_VERSION=v1 --env "AUTH_ASSOC_ENCRYPT_KEY=$(openssl rand 32 | base64 -w0)" --env "AUTH_ASSOC_LOOKUP_KEY=$(openssl rand 32 | base64 -w0)" \
 --env AUTH_EMAIL_BINDING_ENABLED=true --env "AUTH_EMAIL_BINDING_HMAC_KEY=$(openssl rand 32 | base64 -w0)" \
 --env AUTH_EMAIL_RECOVERY_READY=true --env AUTH_EMAIL_LOGIN_ENABLED=true --env "AUTH_EMAIL_LOGIN_CODE_KEY=$(openssl rand 32 | base64 -w0)" \
 --env "AUTH_SMTP_ADDR=${AUTH_FIXTURE_SMTP_ADDR}" --env AUTH_SMTP_FROM=auth@example.test --env AUTH_SMTP_TLS_MODE=implicit_tls \
 --env AUTH_SMTP_CA_FILE=/run/iwut-uc002/smtp-cert.pem --env AUTH_SMTP_TIMEOUT=10s \
 --env AUTH_PASSWORD_ENABLED=true --env "AUTH_PASSWORD_OPERATION_HMAC_KEY=$(openssl rand 32 | base64 -w0)" \
 --env AUTH_PASSWORD_BLOCKLIST_FILE=/run/iwut-uc002/password-blocklist.txt \
 --env AUTH_PASSWORD_BLOCKLIST_VERSION=app-uc005-e2e-v1 --env AUTH_PASSWORD_BLOCKLIST_SOURCE=app-controlled-e2e \
 --env "AUTH_PASSWORD_BLOCKLIST_SHA256=${password_blocklist_sha}" --env AUTH_PASSWORD_READINESS_CONFIRMED=true \
 --env AUTH_PROFILE_CATALOG_FILE=/run/iwut-uc002/profile-catalog.json --env AUTH_USER_IDENTITY_ISSUER=https://auth.e2e.test \
 --env 'AUTH_USER_IDENTITY_PUBLIC_KEYS_JSON={"uc002-auth":"/run/iwut-uc002/auth-identity.pub.pem"}' \
 --env AUTH_USER_IDENTITY_SIGNING_KID=uc002-auth --env "AUTH_USER_IDENTITY_PRIVATE_KEY_PEM_B64=$(base64 -w0 < "${temporary_dir}/auth-identity.pem")" \
 --env "AUTH_SCRIPT_MAINTAINER_PERMISSION_ENDPOINTS_ENABLED=${script_management}" \
 --env AUTH_CENTER_OAUTH_CONFIG_FILE=/run/iwut-uc002/oauth.json "${auth_image_id}" >/dev/null

export GATEWAY_HTTP_ADDR="127.0.0.1:${gateway_http}" GATEWAY_GRPC_ADDR="127.0.0.1:${gateway_grpc}"
export GATEWAY_ROUTES_FILE="${temporary_dir}/gateway-source/config/routes.v3.yaml"
export GATEWAY_SERVICE_IDENTITY_ID=iwut-gateway GATEWAY_SERVICE_IDENTITY_KID=uc002-gateway
export GATEWAY_SERVICE_IDENTITY_PRIVATE_KEY_PEM_B64="$(base64 -w0 < "${temporary_dir}/gateway-service.pem")"
export GATEWAY_AUTH_CENTER_HTTP_URL="http://127.0.0.1:${auth_http}" GATEWAY_AUTH_CENTER_GRPC_TARGET="${AUTH_CENTER_INTEGRATION_TARGET}"
export GATEWAY_APP_CENTER_HTTP_URL="http://${UC002_APP_HTTP_ADDR}" GATEWAY_APP_CENTER_GRPC_TARGET="${UC002_APP_GRPC_ADDR}"
export GATEWAY_INTERNAL_HTTP_URL="http://${GATEWAY_HTTP_ADDR}" GATEWAY_INTERNAL_GRPC_URL="h2c://${GATEWAY_GRPC_ADDR}"
export GATEWAY_PUBLIC_API_HOST=api.uc002.test GATEWAY_DEVELOPER_CONSOLE_HOST=developer.uc002.test GATEWAY_ADMIN_CONSOLE_HOST=admin.uc002.test
export GATEWAY_DEVELOPER_BFF_FORWARD_AUTH_URL=http://127.0.0.1:1/internal/forward-auth GATEWAY_ADMIN_BFF_FORWARD_AUTH_URL=http://127.0.0.1:1/internal/forward-auth
if [[ "${script_management}" == true ]]; then
  export GATEWAY_ADMIN_BFF_FORWARD_AUTH_URL="http://${UC041_ADMIN_FORWARD_AUTH_ADDR}/internal/forward-auth"
  gateway_image_id="$(docker image inspect --format '{{.Id}}' "${GATEWAY_LOCAL_IMAGE}")"
  app_image_id="$(docker image inspect --format '{{.Id}}' "${APP_CENTER_LOCAL_IMAGE}")"
  printf 'App %s\nGateway %s\nAuth %s\n' "${app_image_id}" "${gateway_image_id}" "${auth_image_id}" \
    >"${integration_log_dir}/script-management-image-ids.txt"
  gateway_env=(
    --env GATEWAY_HTTP_ADDR --env GATEWAY_GRPC_ADDR
    --env GATEWAY_SERVICE_IDENTITY_ID --env GATEWAY_SERVICE_IDENTITY_KID --env GATEWAY_SERVICE_IDENTITY_PRIVATE_KEY_PEM_B64
    --env GATEWAY_AUTH_CENTER_HTTP_URL --env GATEWAY_AUTH_CENTER_GRPC_TARGET
    --env GATEWAY_APP_CENTER_HTTP_URL --env GATEWAY_APP_CENTER_GRPC_TARGET
    --env GATEWAY_INTERNAL_HTTP_URL --env GATEWAY_INTERNAL_GRPC_URL
    --env GATEWAY_PUBLIC_API_HOST --env GATEWAY_DEVELOPER_CONSOLE_HOST --env GATEWAY_ADMIN_CONSOLE_HOST
    --env GATEWAY_DEVELOPER_BFF_FORWARD_AUTH_URL --env GATEWAY_ADMIN_BFF_FORWARD_AUTH_URL
  )
  docker run --rm --user "$(id -u):$(id -g)" --entrypoint /gateway-config \
    --mount "type=bind,src=${temporary_dir},dst=/run/iwut-uc002" "${gateway_env[@]}" "${gateway_image_id}" \
    --render --routes /config/routes.v3.yaml --traefik /run/iwut-uc002/dynamic.yaml
  docker run --detach --name "${gateway_container_name}" --network host "${gateway_env[@]}" "${gateway_image_id}" >/dev/null

  export UC041_APP_DATABASE=iwut_app_uc041
  auth_public="$(base64 -w0 < "${temporary_dir}/auth-service.pub.pem")"
  app_callers="$(printf '{"iwut-auth-center":{"status":"ACTIVE","keys":{"uc002-auth-service":{"publicKeyPemB64":"%s"}},"permissions":["app.oauth.client.read"]}}' "${auth_public}" | base64 -w0)"
  app_mongo=(--env "APP_CENTER_MONGO_URI=${MONGODB_INTEGRATION_URI}" --env "APP_CENTER_MONGO_DATABASE=${UC041_APP_DATABASE}")
  docker run --rm --network host "${app_mongo[@]}" "${app_image_id}" migrate >"${integration_log_dir}/app-migrate.log" 2>&1
  docker run --detach --name "${app_container_name}" --network host --user "$(id -u):$(id -g)" \
    --mount "type=bind,src=${AUTH_CENTER_IDENTITY_PUBLIC_KEY_PATH},dst=/run/auth-identity.pem,readonly" "${app_mongo[@]}" \
    --env "APP_CENTER_HTTP_ADDR=${UC002_APP_HTTP_ADDR}" --env "APP_CENTER_GRPC_ADDR=${UC002_APP_GRPC_ADDR}" \
    --env APP_CENTER_IDENTITY_ISSUER=https://auth.e2e.test --env APP_CENTER_IDENTITY_AUDIENCE=iwut-app-center \
    --env APP_CENTER_IDENTITY_PUBLIC_KEYS=uc002-auth=/run/auth-identity.pem \
    --env "APP_CENTER_AUTH_SCOPE_CATALOG_GRPC_TARGET=${AUTH_CENTER_INTEGRATION_TARGET}" \
    --env APP_CENTER_SERVICE_IDENTITY_ID --env APP_CENTER_SERVICE_IDENTITY_KID --env APP_CENTER_SERVICE_IDENTITY_PRIVATE_KEY_PEM_B64 \
    --env "APP_CENTER_SERVICE_CALLERS_B64=${app_callers}" "${app_image_id}" >/dev/null
else
  "${temporary_dir}/gateway-config" --render --routes "${GATEWAY_ROUTES_FILE}" --traefik "${temporary_dir}/dynamic.yaml"
  "${temporary_dir}/gateway" >"${integration_log_dir}/gateway.log" 2>&1 &
  gateway_pid=$!
fi
docker run --detach --name "${traefik_container_name}" --network host --user "$(id -u):$(id -g)" \
 --mount "type=bind,src=${temporary_dir},dst=/run/iwut-uc002,readonly" "${traefik_image}" --configFile=/run/iwut-uc002/traefik.yaml >/dev/null
listener_ports=("${auth_grpc}" "${gateway_http}" "${ports[6]}")
if [[ "${script_management}" == true ]]; then listener_ports+=("${ports[2]}" "${ports[3]}" "${ports[7]}"); fi
python3 - "${listener_ports[@]}" "${startup_timeout}" <<'PY'
import socket,sys,time
end=time.monotonic()+int(sys.argv[-1])
for port in map(int,sys.argv[1:-1]):
 while True:
  try:
   with socket.create_connection(('127.0.0.1',port),timeout=.2):break
  except OSError:
   if time.monotonic()>=end:raise SystemExit('UC002 fixture listener did not start')
   time.sleep(.1)
PY
# This gate has its own disposable Auth/Mongo/Gateway/Traefik inputs; the normal
# backend tier still runs the unfiltered Mongo/HTTP/gRPC suite first.
if [[ "${script_management}" == true ]]; then
  go test -v -count=1 -race -timeout=5m -run '^TestE2E_UCAPP041_042_ScriptManagementLocalImages$' ./cmd/app-center
else
  go test -v -count=1 -race -timeout=5m -run '^TestE2E_UCAPP00(2GatewayAuthIntegration|3_004_005_BR_VER_010_017_BR_REV_001_020_GatewayAuthIntegration)$' ./cmd/app-center
fi
