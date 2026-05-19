#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_file() {
  local path="$1"
  [[ -f "${path}" ]] || fail "missing ${path#${ROOT_DIR}/}"
}

assert_contains() {
  local path="$1"
  local pattern="$2"
  grep -q -- "${pattern}" "${path}" || fail "${path#${ROOT_DIR}/} missing pattern: ${pattern}"
}

assert_not_contains() {
  local path="$1"
  local pattern="$2"
  if grep -q -- "${pattern}" "${path}"; then
    fail "${path#${ROOT_DIR}/} must not contain pattern: ${pattern}"
  fi
}

install_central="${ROOT_DIR}/scripts/install-central.sh"
install_agent="${ROOT_DIR}/scripts/install-agent.sh"
build_release="${ROOT_DIR}/scripts/build-release.sh"

assert_file "${install_central}"
assert_file "${install_agent}"
assert_file "${build_release}"

bash -n "${install_central}"
bash -n "${install_agent}"
bash -n "${build_release}"

assert_contains "${install_central}" "systemctl daemon-reload"
assert_contains "${install_central}" "systemctl enable --now serial-platform-central.service"
assert_contains "${install_central}" "central-server-linux-amd64"
assert_contains "${install_central}" "--listen"
assert_contains "${install_central}" "--rfc2217-bind"
assert_contains "${install_central}" "--data-dir"

assert_contains "${install_agent}" "systemctl daemon-reload"
assert_contains "${install_agent}" "systemctl enable --now serial-platform-agent.service"
assert_contains "${install_agent}" "command -v udevadm"
assert_contains "${install_agent}" "host-agent-linux-amd64"
assert_contains "${install_agent}" "host-agent-linux-arm64"
assert_contains "${install_agent}" "host-agent-linux-armv7"
assert_contains "${install_agent}" "--server"
assert_contains "${install_agent}" "--data-dir"
assert_not_contains "${install_agent}" "udevadm control --reload-rules"
assert_not_contains "${install_agent}" "armv6l"

assert_contains "${build_release}" "GOARCH=amd64"
assert_contains "${build_release}" "GOARCH=arm64"
assert_contains "${build_release}" "GOARCH=arm GOARM=7"
assert_contains "${build_release}" "central-server-linux-amd64"
assert_contains "${build_release}" "host-agent-linux-amd64"
assert_contains "${build_release}" "host-agent-linux-arm64"
assert_contains "${build_release}" "host-agent-linux-armv7"
assert_contains "${build_release}" "serialctl-linux-amd64"
assert_contains "${build_release}" ".release-build"
assert_contains "${build_release}" "serial-platform-linux.tar.gz"
assert_contains "${build_release}" "SOURCE_DATE_EPOCH"
assert_contains "${build_release}" "gzip -n"

echo "install script smoke tests passed"
