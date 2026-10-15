#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/integration-lib.sh"
integration_init
for tool in openssl base64 tar; do command -v "${tool}" >/dev/null || { echo "Missing ${tool}" >&2; exit 1; }; done

gateway_source="${GATEWAY_SOURCE_DIR:-${app_dir}/../iwut-gateway}"
gateway_source="$(cd "${gateway_source}" && pwd)"
gateway_head="$(git -C "${gateway_source}" rev-parse HEAD)"
gateway_api_head="$(git -C "${gateway_source}/api" rev-parse HEAD)"
# Build immutable committed snapshots; never edit the neighboring checkout.
mkdir -p "${temporary_dir}/gateway-source/api"
git -C "${gateway_source}" archive "${gateway_head}" | tar -x -C "${temporary_dir}/gateway-source"
git -C "${gateway_source}/api" archive "${gateway_api_head}" | tar -x -C "${temporary_dir}/gateway-source/api"
(cd "${temporary_dir}/gateway-source" && go build -o "${temporary_dir}/gateway" ./cmd/gateway && go build -o "${temporary_dir}/gateway-config" ./cmd/gateway-config)
echo "Gateway source: ${gateway_head}; API: ${gateway_api_head}"
printf '%s\n' "${gateway_head}" "${gateway_api_head}" >"${integration_log_dir}/gateway-inputs.txt"

auth_image="${AUTH_CENTER_IMAGE:-docker.cnb.cool/tokenteam/iwut-auth-center:latest}"
timeout 300s docker pull "${auth_image}"
auth_image_id="$(docker image inspect --format '{{.Id}}' "${auth_image}")"
echo "Auth Center image: ${auth_image} (${auth_image_id})"
traefik_image="$(sed -n 's/^image=//p' "${temporary_dir}/gateway-source/deploy/traefik/image.lock")"
[[ "${traefik_image}" == *@sha256:* ]] || { echo "Traefik image must be digest pinned" >&2; exit 1; }
timeout 300s docker pull "${traefik_image}"
traefik_container_name="${container_name}-traefik"
gateway_pid=""
cleanup_uc002() {
  local code=$?
  set +e
  if [[ -n "${gateway_pid}" ]]; then kill "${gateway_pid}" 2>/dev/null; wait "${gateway_pid}" 2>/dev/null; fi
  timeout 10s docker logs "${traefik_container_name}" >"${integration_log_dir}/traefik.log" 2>&1
  timeout 15s docker rm -f "${traefik_container_name}" >/dev/null 2>&1
  (exit "${code}")
  integration_cleanup
}
trap cleanup_uc002 EXIT

# Allocate the entire fixture's listener set at once, avoiding duplicate ports.
mapfile -t ports < <(python3 - <<'PY'
import socket
sockets=[socket.socket() for _ in range(8)]
for s in sockets:s.bind(('127.0.0.1',0))
for s in sockets:print(s.getsockname()[1])
for s in sockets:s.close()
PY
)
auth_http="${ports[0]}"; auth_grpc="${ports[1]}"
export UC002_APP_HTTP_ADDR="127.0.0.1:${ports[2]}" UC002_APP_GRPC_ADDR="127.0.0.1:${ports[3]}"
gateway_http="${ports[4]}"; gateway_grpc="${ports[5]}"
export UC002_EDGE_HTTP_ADDR="127.0.0.1:${ports[6]}" UC002_EDGE_GRPC_ADDR="127.0.0.1:${ports[7]}"
for name in app-service gateway-service auth-identity auth-service oidc delegation; do
 openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "${temporary_dir}/${name}.pem" >/dev/null 2>&1
 openssl pkey -in "${temporary_dir}/${name}.pem" -pubout -out "${temporary_dir}/${name}.pub.pem" >/dev/null 2>&1
done
export APP_CENTER_SERVICE_IDENTITY_ID=iwut-app-center APP_CENTER_SERVICE_IDENTITY_KID=uc002-app
export APP_CENTER_SERVICE_IDENTITY_PRIVATE_KEY_PEM_B64="$(base64 -w0 < "${temporary_dir}/app-service.pem")"
export AUTH_CENTER_IDENTITY_PUBLIC_KEY_PATH="${temporary_dir}/auth-identity.pub.pem"
export UC002_GATEWAY_KEY_FILE="${temporary_dir}/gateway-service.pem"
export AUTH_CENTER_INTEGRATION_TARGET="127.0.0.1:${auth_grpc}" AUTH_CENTER_INTEGRATION_DATABASE=iwut_auth_uc002
app_public="$(base64 -w0 < "${temporary_dir}/app-service.pub.pem")"
gateway_public="$(base64 -w0 < "${temporary_dir}/gateway-service.pub.pem")"
callers="$(printf '{"iwut-app-center":{"status":"ACTIVE","keys":{"uc002-app":{"publicKeyPemB64":"%s"}},"permissions":["auth.scope-catalog.read"]},"iwut-gateway":{"status":"ACTIVE","keys":{"uc002-gateway":{"publicKeyPemB64":"%s"}},"permissions":["auth.identity.issue"],"identityAudiences":["iwut-app-center"]}}' "${app_public}" "${gateway_public}" | base64 -w0)"

python3 - "${temporary_dir}" "${UC002_APP_GRPC_ADDR}" "${ports[6]}" "${ports[7]}" <<'PY'
import json,pathlib,sys
root=pathlib.Path(sys.argv[1]);base='/run/iwut-uc002/'
def write(name,value):(root/name).write_text(json.dumps(value,separators=(',',':')))
write('profile-catalog.json',{'definitions':[]})
write('scopes.json',[{'Name':name,'Label':name,'Description':'UC002 integration fixture','Kind':'RESOURCE','Audience':'resource-service','Enabled':enabled} for name,enabled in [('items.disabled',False),('items.read',True)]])
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
 --env AUTH_PROFILE_CATALOG_FILE=/run/iwut-uc002/profile-catalog.json --env AUTH_USER_IDENTITY_ISSUER=https://auth.e2e.test \
 --env 'AUTH_USER_IDENTITY_PUBLIC_KEYS_JSON={"uc002-auth":"/run/iwut-uc002/auth-identity.pub.pem"}' \
 --env AUTH_USER_IDENTITY_SIGNING_KID=uc002-auth --env "AUTH_USER_IDENTITY_PRIVATE_KEY_PEM_B64=$(base64 -w0 < "${temporary_dir}/auth-identity.pem")" \
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
"${temporary_dir}/gateway-config" --render --routes "${GATEWAY_ROUTES_FILE}" --traefik "${temporary_dir}/dynamic.yaml"
"${temporary_dir}/gateway" >"${integration_log_dir}/gateway.log" 2>&1 &
gateway_pid=$!
docker run --detach --name "${traefik_container_name}" --network host --user "$(id -u):$(id -g)" \
 --mount "type=bind,src=${temporary_dir},dst=/run/iwut-uc002,readonly" "${traefik_image}" --configFile=/run/iwut-uc002/traefik.yaml >/dev/null
python3 - "${auth_grpc}" "${gateway_http}" "${ports[6]}" "${startup_timeout}" <<'PY'
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
go test -count=1 -race -timeout=5m -run '^TestE2E_UCAPP002GatewayAuthIntegration$' ./cmd/app-center
