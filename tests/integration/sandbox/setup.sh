#!/usr/bin/env bash
# Setup: generate, build, start containers.
set -euo pipefail
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"

export $(grep -v '^#' "$SCRIPT_DIR/test.env" | xargs)

echo "--- Generating build artifacts ---"
"$CLI" generate -C "$SCRIPT_DIR"

echo ""
echo "--- Building and starting containers ---"
if ! compose up -d --build --wait --wait-timeout 60; then
  echo ""
  echo "--- COMPOSE UP FAILED — dumping container logs ---"
  compose logs 2>&1 | tail -50
  echo ""
  echo "--- Container status ---"
  compose ps -a 2>&1
  exit 1
fi

sleep 3
echo "--- Setup complete ---"
