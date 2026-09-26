#!/usr/bin/env bash
# Shared lifecycle for disposable integration infrastructure. Source, do not run.
integration_init() {
  umask 077
  app_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  cd "${app_dir}"
  for tool in docker go timeout python3; do
    command -v "${tool}" >/dev/null || { echo "Missing integration prerequisite: ${tool}" >&2; return 1; }
  done
  timeout 15s docker info >/dev/null 2>&1 || { echo "Docker daemon is unavailable" >&2; return 1; }
  startup_timeout="${INTEGRATION_STARTUP_TIMEOUT_SECONDS:-60}"
  [[ "${startup_timeout}" =~ ^[1-9][0-9]*$ ]] || { echo "Invalid startup timeout" >&2; return 1; }
  if [[ -n "${MONGODB_INTEGRATION_PORT:-}" ]]; then validate_port "${MONGODB_INTEGRATION_PORT}"; fi
  temporary_dir="$(mktemp -d)"
  container_name="iwut-app-integration-${temporary_dir##*/}"
  auth_pid=""
  if [[ -n "${INTEGRATION_LOG_DIR:-}" ]]; then
    mkdir -p "${INTEGRATION_LOG_DIR}"
    integration_log_dir="$(cd "${INTEGRATION_LOG_DIR}" && pwd)"
  else
    mkdir -p "${app_dir}/.artifacts/integration"
    integration_log_dir="$(mktemp -d "${app_dir}/.artifacts/integration/run-XXXXXXXX")"
  fi
  trap integration_cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  echo "Integration diagnostics: ${integration_log_dir}"
}
validate_port() {
  [[ "$1" =~ ^[0-9]{1,5}$ ]] && (( 10#$1 >= 1 && 10#$1 <= 65535 )) || {
    echo "Port must be an integer from 1 to 65535" >&2; return 1;
  }
}
integration_cleanup() {
  local code=$?
  trap - EXIT INT TERM
  if [[ -n "${auth_pid:-}" ]]; then
    kill "${auth_pid}" 2>/dev/null || true
    for _ in {1..20}; do
      kill -0 "${auth_pid}" 2>/dev/null || break
      sleep 0.1
    done
    kill -KILL "${auth_pid}" 2>/dev/null || true
    wait "${auth_pid}" 2>/dev/null || true
  fi
  if [[ -n "${container_name:-}" ]]; then
    timeout 10s docker logs "${container_name}" >"${integration_log_dir}/mongo.log" 2>&1 || true
    timeout 15s docker rm -f "${container_name}" >/dev/null 2>&1 || true
  fi
  rm -rf "${temporary_dir}"
  if (( code != 0 )); then
    echo "Integration failed (exit ${code}); diagnostics: ${integration_log_dir}" >&2
  fi
  exit "${code}"
}
wait_for_mongo() {
  local phase="$1" expression="$2" deadline=$((SECONDS + startup_timeout))
  while (( SECONDS < deadline )); do
    if timeout 5s docker exec "${container_name}" mongosh --quiet --port 27017 \
      --eval "${expression}" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  echo "MongoDB ${phase} timed out after ${startup_timeout}s" >&2
  return 1
}
start_integration_mongo() {
  # Docker atomically allocates the host port. directConnection avoids discovery
  # of the replica-set address private to this container.
  docker run --detach --rm --name "${container_name}" --ulimit nofile=65536:65536 \
    --publish "127.0.0.1:${MONGODB_INTEGRATION_PORT:-}:27017" \
    mongo:8.2.12 mongod --replSet rs0 --bind_ip_all --port 27017 >/dev/null
  local mapping
  mapping="$(docker port "${container_name}" 27017/tcp)"
  mongo_port="${mapping##*:}"
  validate_port "${mongo_port}"
  wait_for_mongo ping 'if (db.runCommand({ping: 1}).ok !== 1) { quit(1) }'
  timeout 10s docker exec "${container_name}" mongosh --quiet --port 27017 --eval \
    "rs.initiate({_id: 'rs0', members: [{_id: 0, host: '127.0.0.1:27017'}]})" >/dev/null
  wait_for_mongo primary 'if (!db.hello().isWritablePrimary) { quit(1) }'
  export MONGODB_INTEGRATION_URI="mongodb://127.0.0.1:${mongo_port}/?replicaSet=rs0&directConnection=true"
  echo "Isolated MongoDB ready on localhost:${mongo_port}"
}
