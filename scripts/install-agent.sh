#!/usr/bin/env bash
set -euo pipefail

SERVER=""
DATA_DIR="/var/lib/serial-agent"
RUN_USER="${SUDO_USER:-$(id -un)}"
SERVICE_PATH="/etc/systemd/system/serial-platform-agent.service"
INSTALL_PATH="/usr/local/bin/host-agent"

usage() {
  cat >&2 <<USAGE
usage: sudo ./install-agent.sh --server URL [--data-dir DIR] [--user USER]

Options:
  --server URL    central server URL, for example http://central:8080
  --data-dir DIR  host-agent data directory (default: /var/lib/serial-agent)
  --user USER     user to run host-agent as (default: SUDO_USER or current user)
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
    --server)
      require_value "$1" "${2:-}"
      SERVER="$2"
      shift 2
      ;;
    --data-dir)
      require_value "$1" "${2:-}"
      DATA_DIR="$2"
      shift 2
      ;;
    --user)
      require_value "$1" "${2:-}"
      RUN_USER="$2"
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

if [[ -z "${SERVER}" ]]; then
  echo "--server is required" >&2
  usage
  exit 2
fi

if [[ "${EUID}" -ne 0 ]]; then
  echo "install-agent.sh must run as root" >&2
  exit 1
fi

id "${RUN_USER}" >/dev/null 2>&1 || {
  echo "user not found: ${RUN_USER}" >&2
  exit 1
}

command -v systemctl >/dev/null || {
  echo "systemctl is required" >&2
  exit 1
}
command -v udevadm >/dev/null || {
  echo "udevadm is required" >&2
  exit 1
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
case "$(uname -m)" in
  x86_64|amd64)
    BIN="${SCRIPT_DIR}/host-agent-linux-amd64"
    ;;
  aarch64|arm64)
    BIN="${SCRIPT_DIR}/host-agent-linux-arm64"
    ;;
  armv7l|armv7*)
    BIN="${SCRIPT_DIR}/host-agent-linux-armv7"
    ;;
  *)
    echo "unsupported host-agent architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

if [[ ! -f "${BIN}" ]]; then
  echo "missing release binary: ${BIN}" >&2
  exit 1
fi

DIALOUT_UNIT_LINE=""
if getent group dialout >/dev/null 2>&1; then
  usermod -aG dialout "${RUN_USER}"
  DIALOUT_UNIT_LINE="SupplementaryGroups=dialout"
fi

install -m 0755 "${BIN}" "${INSTALL_PATH}"
install -d -m 0755 -o "${RUN_USER}" "${DATA_DIR}"

cat >"${SERVICE_PATH}" <<UNIT
[Unit]
Description=Serial Platform Host Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${RUN_USER}
${DIALOUT_UNIT_LINE}
ExecStart=${INSTALL_PATH} --server $(unit_arg "${SERVER}") --data-dir $(unit_arg "${DATA_DIR}")
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now serial-platform-agent.service
echo "If this is the first time the user was added to dialout, log out and log in again or restart the service after group membership is active."
