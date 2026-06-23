#!/usr/bin/env bash
# Shared test helpers for sandbox integration tests.
# Source this from each test_*.sh script.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLI="${CLI_PATH:-agent-sandbox}"

pass() { echo -e "  \033[32m✓\033[0m $1"; }
fail() { echo -e "  \033[31m✗\033[0m $1"; echo "    $2"; exit 1; }

compose() {
  "$CLI" -C "$SCRIPT_DIR" compose -f "$SCRIPT_DIR/compose-override.yml" "$@"
}

exec_in() {
  local service="$1"; shift
  compose exec "$service" "$@" 2>&1
}
