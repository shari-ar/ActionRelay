#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERSION="${1:-}"
DIST_DIR="${ROOT_DIR}/dist"

if [[ -z "${VERSION}" ]]; then
  echo "usage: $(basename "$0") <version-tag>" >&2
  exit 1
fi

rm -rf "${DIST_DIR}"
mkdir -p "${DIST_DIR}"

targets=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
  "windows amd64"
  "windows arm64"
)

for target in "${targets[@]}"; do
  read -r GOOS GOARCH <<<"${target}"

  binary_name="actionrelay"
  archive_ext="tar.gz"
  if [[ "${GOOS}" == "windows" ]]; then
    binary_name="actionrelay.exe"
    archive_ext="zip"
  fi

  staging_dir="${DIST_DIR}/actionrelay_${VERSION}_${GOOS}_${GOARCH}"
  mkdir -p "${staging_dir}"

  (
    cd "${ROOT_DIR}/client"
    CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" \
      go build -trimpath -ldflags="-s -w" -o "${staging_dir}/${binary_name}" ./cmd/actionrelay
  )

  cp "${ROOT_DIR}/README.md" "${staging_dir}/README.md"

  if [[ "${archive_ext}" == "zip" ]]; then
    (
      cd "${staging_dir}"
      zip -q -9 "${DIST_DIR}/actionrelay_${VERSION}_${GOOS}_${GOARCH}.zip" "${binary_name}" "README.md"
    )
  else
    tar -C "${staging_dir}" -czf "${DIST_DIR}/actionrelay_${VERSION}_${GOOS}_${GOARCH}.tar.gz" "${binary_name}" "README.md"
  fi

  rm -rf "${staging_dir}"
done

(
  cd "${DIST_DIR}"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum actionrelay_* > SHA256SUMS.txt
  else
    shasum -a 256 actionrelay_* > SHA256SUMS.txt
  fi
)

echo "release assets written to ${DIST_DIR}"
