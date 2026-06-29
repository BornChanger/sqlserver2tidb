#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

export SQLSERVER2TIDB_RUN_CDC_SOAK=1
export SQLSERVER2TIDB_CDC_SOAK_ITERATIONS="${SQLSERVER2TIDB_CDC_SOAK_ITERATIONS:-3}"

bash "${repo_root}/scripts/run-integration-tests.sh"
