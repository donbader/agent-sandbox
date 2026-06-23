#!/usr/bin/env bash
# Integration test runner: setup → run all test_*.sh → teardown.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Ensure teardown always runs — even if setup fails.
trap '"$SCRIPT_DIR/teardown.sh"' EXIT

echo "=== Sandbox Integration Tests ==="
echo ""

# Setup
"$SCRIPT_DIR/setup.sh"

# Run each test script (continue on failure to get full results)
FAILED=0
for test_script in "$SCRIPT_DIR"/test_*.sh; do
  echo ""
  if ! "$test_script"; then
    FAILED=1
  fi
done

echo ""

if [ "$FAILED" -ne 0 ]; then
  echo "=== SOME TESTS FAILED ==="
  exit 1
fi

echo "=== All checks passed ==="
