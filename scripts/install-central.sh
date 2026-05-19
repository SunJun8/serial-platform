#!/usr/bin/env bash
set -euo pipefail

DATA_DIR="/data/serial-platform"
LISTEN=":8080"
RFC2217_BIND="0.0.0.0"
SERVICE_PATH="/etc/systemd/system/serial-platform-central.service"
INSTALL_PATH="/usr/local/bin/central-server"

usage() {
  cat >&2 <<USAGE
usage: sudo ./install-central.sh [--data-dir DIR] [--listen ADDR] [--rfc2217-bind HOST]

Options:
  --data-dir DIR      central data directory (default: /data/serial-platform)
  --listen ADDR       HTTP listen address (default: :8080)
  --rfc2217-bind HOST RFC2217 bind host (default: 0.0.0.0)
USAGE
}

require_value() {
  local opt="$1"
  local value="${2:-}"
  [[ -n "${value}" ]] || {
    echo "${opt} requires a value" >&2
    exit 2
  }
}

unit_arg() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  value="${value//%/%%}"
  printf '"%s"' "${value}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --data-dir)
      require_value "$1" "${2:-}"
      DATA_DIR="$2"
      shift 2
      ;;
    --listen)
      require_value "$1" "${2:-}"
      LISTEN="$2"
      shift 2
      ;;
    --rfc2217-bind)
      require_value "$1" "${2:-}"
      RFC2217_BIND="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage
      exit 2
      ;;
  esac
done

if [[ "${EUID}" -ne 0 ]]; then
  echo "install-central.sh must run as root" >&2
  exit 1
fi

command -v systemctl >/dev/null || {
  echo "systemctl is required" >&2
  exit 1
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
case "$(uname -m)" in
  x86_64|amd64)
    BIN="${SCRIPT_DIR}/central-server-linux-amd64"
    ;;
  *)
    echo "unsupported central-server architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

if [[ ! -f "${BIN}" ]]; then
  echo "missing release binary: ${BIN}" >&2
  exit 1
fi

install -m 0755 "${BIN}" "${INSTALL_PATH}"
install -d -m 0755 "${DATA_DIR}"
install -d -m 0755 "${DATA_DIR}/logs"

cat >"${SERVICE_PATH}" <<UNIT
[Unit]
Description=Serial Platform Central Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${INSTALL_PATH} --data-dir $(unit_arg "${DATA_DIR}") --listen $(unit_arg "${LISTEN}") --rfc2217-bind $(unit_arg "${RFC2217_BIND}")
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now serial-platform-central.service
