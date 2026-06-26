#!/usr/bin/env bash
set -euo pipefail

version="${VERSION:-dev}"
commit="${COMMIT:-unknown}"
build_date="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
dist_dir="${DIST_DIR:-dist}"
if [ "${dist_dir}" = "/" ]; then
  echo "DIST_DIR must not be /" >&2
  exit 2
fi
buildinfo_pkg="github.com/BornChanger/sqlserver2tidb/internal/buildinfo"
ldflags="-X ${buildinfo_pkg}.Version=${version} -X ${buildinfo_pkg}.Commit=${commit} -X ${buildinfo_pkg}.BuildDate=${build_date}"

default_targets=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
  "windows/amd64"
)

targets=("${default_targets[@]}")
if [ -n "${DIST_TARGETS:-}" ]; then
  read -r -a targets <<<"${DIST_TARGETS}"
fi
if [ "${#targets[@]}" -eq 0 ]; then
  echo "DIST_TARGETS must include at least one GOOS/GOARCH target" >&2
  exit 2
fi

mkdir -p "${dist_dir}"

for target in "${targets[@]}"; do
  if [[ "${target}" != */* || "${target}" == /* || "${target}" == */ || "${target}" == */*/* ]]; then
    echo "invalid target ${target}; expected GOOS/GOARCH" >&2
    exit 2
  fi

  goos="${target%/*}"
  goarch="${target#*/}"
  suffix=""
  if [ "${goos}" = "windows" ]; then
    suffix=".exe"
  fi

  name="sqlserver2tidb_${version}_${goos}_${goarch}"
  outdir="${dist_dir}/${name}"
  rm -rf -- "${outdir}"
  mkdir -p "${outdir}"

  GOOS="${goos}" GOARCH="${goarch}" go build -ldflags "${ldflags}" -o "${outdir}/sqlserver2tidb${suffix}" ./cmd/sqlserver2tidb
  GOOS="${goos}" GOARCH="${goarch}" go build -ldflags "${ldflags}" -o "${outdir}/sqlserver2tidb-executor${suffix}" ./cmd/sqlserver2tidb-executor
  cp README.md LICENSE "${outdir}/"
  mkdir -p "${outdir}/docs"
  cp docs/design.md docs/user-manual.md "${outdir}/docs/"

  tar -C "${dist_dir}" -czf "${dist_dir}/${name}.tar.gz" "${name}"
  rm -rf -- "${outdir}"
done

if command -v sha256sum >/dev/null 2>&1; then
  sha256sum "${dist_dir}"/*.tar.gz > "${dist_dir}/checksums.txt"
else
  shasum -a 256 "${dist_dir}"/*.tar.gz > "${dist_dir}/checksums.txt"
fi
