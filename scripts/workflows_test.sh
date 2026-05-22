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

ci="${ROOT_DIR}/.github/workflows/ci.yml"
release="${ROOT_DIR}/.github/workflows/release.yml"

assert_file "${ci}"
assert_file "${release}"

assert_contains "${ci}" "on:"
assert_contains "${ci}" "push:"
assert_contains "${ci}" "pull_request:"
assert_contains "${ci}" "branches:"
assert_contains "${ci}" "tags-ignore:"
assert_contains "${ci}" "make test-unit"
assert_contains "${ci}" "make build"
assert_contains "${ci}" "22.18.0"
assert_contains "${ci}" "bash scripts/install_scripts_test.sh"
assert_contains "${ci}" "bash scripts/workflows_test.sh"
assert_not_contains "${ci}" "tags:"

assert_contains "${release}" "on:"
assert_contains "${release}" "push:"
assert_contains "${release}" "tags:"
assert_contains "${release}" "v*"
assert_contains "${release}" "contents: write"
assert_contains "${release}" "bash scripts/build-release.sh"
assert_contains "${release}" "VERSION="
assert_contains "${release}" 'VERSION="${GITHUB_REF_NAME#v}"'
assert_contains "${release}" "22.18.0"
assert_contains "${release}" "serial-platform-linux.tar.gz"
assert_contains "${release}" "gh release create"

echo "workflow smoke tests passed"
