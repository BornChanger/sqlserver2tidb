#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"

image="${CONTAINER_SMOKE_IMAGE:-sqlserver2tidb:container-smoke}"
version="${VERSION:-dev}"
commit="${COMMIT:-$(git -C "${repo_root}" rev-parse --short HEAD 2>/dev/null || echo unknown)}"
build_date="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required for container smoke" >&2
  exit 1
fi

docker version >/dev/null

DOCKER_BUILDKIT="${DOCKER_BUILDKIT:-1}" docker build \
  --build-arg "VERSION=${version}" \
  --build-arg "COMMIT=${commit}" \
  --build-arg "BUILD_DATE=${build_date}" \
  -t "${image}" \
  "${repo_root}"

docker run --rm --entrypoint /bin/sh "${image}" -lc '
set -eu
if [ "$(id -u)" = "0" ]; then
  echo "expected container to run as a non-root user" >&2
  exit 1
fi
git --version
gh --version | head -n 1
sqlserver2tidb version
sqlserver2tidb-executor version
'

docker run --rm \
  -v "${repo_root}:/workspace:ro" \
  "${image}" doctor --root /workspace --require-tools

tmpdir="$(mktemp -d "${repo_root}/.tmp-container-smoke.XXXXXX")"
chmod 0777 "${tmpdir}"
cleanup() {
  rm -rf "${tmpdir}"
}
trap cleanup EXIT

run_metadata_command() {
  docker run --rm \
    -v "${tmpdir}:/workspace" \
    "${image}" "$@"
}

run_metadata_command init-repo --root /workspace
run_metadata_command create-cluster \
  --root /workspace \
  --cluster-id smoke-sqlserver \
  --display-name smoke-sqlserver \
  --listener sqlserver.example.internal \
  --secret-ref vault://smoke/sqlserver \
  --owner dba-team
run_metadata_command create-project \
  --root /workspace \
  --source-cluster-id smoke-sqlserver \
  --project-id smoke-project \
  --display-name smoke-project \
  --source-database app \
  --source-schema dbo \
  --target-name tidb-smoke \
  --target-database app \
  --target-secret-ref vault://smoke/tidb \
  --owner dba-team
run_metadata_command generate-pr-draft \
  --root /workspace \
  --source-cluster-id smoke-sqlserver \
  --project-id smoke-project \
  --stage plan

pr_dry_run="$(
  run_metadata_command create-pr \
    --root /workspace \
    --source-cluster-id smoke-sqlserver \
    --project-id smoke-project \
    --stage plan
)"
printf "%s\n" "${pr_dry_run}"
printf "%s" "${pr_dry_run}" | grep -q "gh pr create --base main --head agent/smoke-project/plan"

if [ -n "${GH_TOKEN:-}" ] && [ -n "${GITHUB_REPOSITORY:-}" ]; then
  pr_number=""
  case "${GITHUB_REF:-}" in
    refs/pull/*/merge|refs/pull/*/head)
      pr_number="${GITHUB_REF#refs/pull/}"
      pr_number="${pr_number%%/*}"
      ;;
  esac

  if [ -n "${pr_number}" ]; then
    docker run --rm \
      -e GH_TOKEN \
      --entrypoint gh \
      "${image}" pr view "${pr_number}" --repo "${GITHUB_REPOSITORY}" --json state,url
  else
    docker run --rm \
      -e GH_TOKEN \
      --entrypoint gh \
      "${image}" repo view "${GITHUB_REPOSITORY}" --json nameWithOwner,url
  fi
else
  echo "skipping GitHub API smoke because GH_TOKEN or GITHUB_REPOSITORY is not set"
fi
