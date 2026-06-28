#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="${repo_root}/examples/quickstart/inventory.json"

if [ -z "${GOCACHE:-}" ]; then
  export GOCACHE="${TMPDIR:-/tmp}/sqlserver2tidb-gocache"
fi
mkdir -p "${GOCACHE}"

if [ ! -s "${fixture}" ]; then
  echo "quickstart inventory fixture not found: ${fixture}" >&2
  exit 1
fi

if [ -n "${SQLSERVER2TIDB_QUICKSTART_ROOT:-}" ]; then
  work_root="${SQLSERVER2TIDB_QUICKSTART_ROOT}"
  mkdir -p "${work_root}"
  entry_count="$(find "${work_root}" -mindepth 1 -maxdepth 1 -print | wc -l | tr -d ' ')"
  if [ "${entry_count}" != "0" ]; then
    echo "SQLSERVER2TIDB_QUICKSTART_ROOT must be empty: ${work_root}" >&2
    exit 2
  fi
else
  work_root="$(mktemp -d)"
fi

if [ -d "${repo_root}/cmd/sqlserver2tidb" ]; then
  cli=(go run ./cmd/sqlserver2tidb)
elif [ -x "${repo_root}/sqlserver2tidb" ]; then
  cli=("${repo_root}/sqlserver2tidb")
elif [ -x "${repo_root}/bin/sqlserver2tidb" ]; then
  cli=("${repo_root}/bin/sqlserver2tidb")
else
  echo "sqlserver2tidb binary not found and source tree is incomplete" >&2
  exit 1
fi

run_cli() {
  (cd "${repo_root}" && "${cli[@]}" "$@")
}

run_cli init-repo --root "${work_root}"
run_cli create-cluster \
  --root "${work_root}" \
  --cluster-id prod-sqlserver-a \
  --display-name "prod SQL Server A" \
  --listener sqlserver-a.internal \
  --port 1433 \
  --secret-ref vault://migration/prod-sqlserver-a/readonly \
  --owner dba-team,sre-team

run_cli create-project \
  --root "${work_root}" \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --display-name "sales DB to TiDB prod A" \
  --source-database sales \
  --source-schema dbo \
  --target-name tidb-prod-a \
  --target-database app \
  --target-secret-ref vault://migration/tidb-prod-a/migrate-user \
  --owner dba-team,app-team

cp "${fixture}" "${work_root}/clusters/prod-sqlserver-a/inventory/inventory.json"

run_cli analyze-compatibility \
  --root "${work_root}" \
  --source-cluster-id prod-sqlserver-a

run_cli generate-schema-draft \
  --root "${work_root}" \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a

run_cli generate-data-plans \
  --root "${work_root}" \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --object-uri-prefix "file://${work_root}/object-store/full"

run_cli generate-cdc-plan \
  --root "${work_root}" \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --mode sqlserver-cdc \
  --retention-hours 168 \
  --apply-batch-size 1000

run_cli prepare-cdc-range \
  --root "${work_root}" \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --from-lsn 0x00000027000001f40000 \
  --to-lsn 0x00000027000001f40003

run_cli generate-validation-plan \
  --root "${work_root}" \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --include-checksum \
  --include-sampled-hash \
  --sample-modulo 100

run_cli generate-pr-draft \
  --root "${work_root}" \
  --source-cluster-id prod-sqlserver-a \
  --project-id sales-db-to-tidb-prod-a \
  --stage plan

run_cli worker-reconcile --root "${work_root}" --dry-run >"${work_root}/worker-reconcile.txt"
grep -q "worker reconcile dry run" "${work_root}/worker-reconcile.txt"

run_cli worker-reconcile --root "${work_root}" --dry-run --json >"${work_root}/worker-reconcile.json"
grep -q '"projects": 1' "${work_root}/worker-reconcile.json"

run_cli worker-reconcile --root "${work_root}" --loop --holder quickstart-agent --max-iterations 1 --interval 1ms >"${work_root}/worker-reconcile-loop.txt"
grep -q "worker reconcile loop" "${work_root}/worker-reconcile-loop.txt"

run_cli worker-agent --root "${work_root}" --holder quickstart-agent --max-iterations 1 --interval 1ms --poll --idle-iterations 1 >"${work_root}/worker-agent.txt"
grep -q "worker agent" "${work_root}/worker-agent.txt"
grep -q "worker agent poll" "${work_root}/worker-agent.txt"

run_cli doctor --root "${work_root}" >"${work_root}/doctor.txt"
grep -q "repository: valid" "${work_root}/doctor.txt"

run_cli validate-repo --root "${work_root}"

echo "quickstart example generated and validated at ${work_root}"
