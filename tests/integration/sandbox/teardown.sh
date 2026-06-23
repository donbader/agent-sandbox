#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"
echo "--- Tearing down ---"
compose down -v 2>/dev/null || true
