#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
compose_file="${repo_root}/tests/integration/docker-compose.yaml"

if [ -z "${GOCACHE:-}" ]; then
  export GOCACHE="${TMPDIR:-/tmp}/sqlserver2tidb-gocache"
fi
mkdir -p "${GOCACHE}"

compose=(docker compose -f "${compose_file}" --project-name sqlserver2tidb-it)

"${compose[@]}" up -d
if [ "${SQLSERVER2TIDB_KEEP_INTEGRATION_ENV:-}" != "1" ]; then
  trap '"${compose[@]}" down -v' EXIT
fi

sqlserver_port="${SQLSERVER2TIDB_INTEGRATION_SQLSERVER_PORT:-14333}"
tidb_port="${SQLSERVER2TIDB_INTEGRATION_TIDB_PORT:-4000}"
sqlserver_password="${SQLSERVER2TIDB_INTEGRATION_SQLSERVER_PASSWORD:-Sqlserver2tidb!2026}"

export SQLSERVER2TIDB_INTEGRATION_SOURCE_DSN="${SQLSERVER2TIDB_INTEGRATION_SOURCE_DSN:-sqlserver://sa:${sqlserver_password}@127.0.0.1:${sqlserver_port}?database=tempdb&encrypt=disable&TrustServerCertificate=true}"
export SQLSERVER2TIDB_INTEGRATION_TARGET_DSN="${SQLSERVER2TIDB_INTEGRATION_TARGET_DSN:-root@tcp(127.0.0.1:${tidb_port})/}"

ready=0
for _ in $(seq 1 90); do
  if (cd "${repo_root}" && go test -tags=integration ./internal/executor -run TestIntegrationDependenciesAreReady -count=1); then
    ready=1
    break
  fi
  sleep 2
done

if [ "${ready}" != "1" ]; then
  "${compose[@]}" logs
  echo "integration dependencies did not become ready" >&2
  exit 1
fi

(cd "${repo_root}" && go test -tags=integration ./internal/executor -run TestSQLServerToTiDBFullLoadExecutorFlow -count=1 -v)
