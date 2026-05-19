#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${ROOT_DIR}/dist"
BUILD_DIR="${ROOT_DIR}/.release-build"
ARCHIVE="${ROOT_DIR}/serial-platform-linux.tar.gz"
VERSION="${VERSION:-dev}"
COMMIT="${COMMIT:-$(git -C "${ROOT_DIR}" rev-parse --short HEAD 2>/dev/null || printf unknown)}"
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "${ROOT_DIR}" log -1 --format=%ct 2>/dev/null || date -u +%s)}"
DATE="${DATE:-$(date -u -d "@${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ)}"
LDFLAGS="-s -w -X serial-platform/internal/buildinfo.Version=${VERSION} -X serial-platform/internal/buildinfo.Commit=${COMMIT} -X serial-platform/internal/buildinfo.Date=${DATE}"

build_go() {
  local goarch="$1"
  local goarm="$2"
  local output="$3"
  local package="$4"

  if [[ -n "${goarm}" ]]; then
    CGO_ENABLED=0 GOOS=linux GOARCH="${goarch}" GOARM="${goarm}" go build -trimpath -ldflags "${LDFLAGS}" -o "${DIST_DIR}/${output}" "${package}"
  else
    CGO_ENABLED=0 GOOS=linux GOARCH="${goarch}" go build -trimpath -ldflags "${LDFLAGS}" -o "${DIST_DIR}/${output}" "${package}"
  fi
}

cd "${ROOT_DIR}"

rm -rf "${DIST_DIR}" "${BUILD_DIR}" "${ARCHIVE}"
mkdir -p "${DIST_DIR}"
trap 'rm -rf "${BUILD_DIR}"' EXIT

(cd web && npm ci && npm run lint && npm run build)
mkdir -p "${BUILD_DIR}"
tar \
  --exclude .git \
  --exclude bin \
  --exclude dist \
  --exclude .release-build \
  --exclude web/dist \
  --exclude web/node_modules \
  -C "${ROOT_DIR}" \
  -cf - . | tar -C "${BUILD_DIR}" -xf -
rm -rf "${BUILD_DIR}/internal/server/webdist"
mkdir -p "${BUILD_DIR}/internal/server/webdist"
cp -R web/dist/. "${BUILD_DIR}/internal/server/webdist/"

cd "${BUILD_DIR}"

GOARCH=amd64 build_go amd64 "" central-server-linux-amd64 ./cmd/central-server
GOARCH=amd64 build_go amd64 "" host-agent-linux-amd64 ./cmd/host-agent
GOARCH=arm64 build_go arm64 "" host-agent-linux-arm64 ./cmd/host-agent
GOARCH=arm GOARM=7 build_go arm 7 host-agent-linux-armv7 ./cmd/host-agent
GOARCH=amd64 build_go amd64 "" serialctl-linux-amd64 ./cmd/serialctl

cd "${ROOT_DIR}"

cp scripts/install-central.sh "${DIST_DIR}/"
cp scripts/install-agent.sh "${DIST_DIR}/"
chmod 0755 "${DIST_DIR}/install-central.sh" "${DIST_DIR}/install-agent.sh"

tar \
  --sort=name \
  --mtime="@${SOURCE_DATE_EPOCH}" \
  --owner=0 \
  --group=0 \
  --numeric-owner \
  -C "${DIST_DIR}" \
  -cf - . | gzip -n >"${ARCHIVE}"
echo "wrote ${ARCHIVE}"
