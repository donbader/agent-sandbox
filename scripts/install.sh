#!/bin/sh
set -e

AGENT_SANDBOX_HOME="${AGENT_SANDBOX_HOME:-$HOME/.agent-sandbox}"
BIN_DIR="$AGENT_SANDBOX_HOME/bin"
SHIM_URL="https://raw.githubusercontent.com/donbader/agent-sandbox/main/scripts/shim.sh"

printf 'Installing agent-sandbox shim...\n'

mkdir -p "$BIN_DIR"
curl -fsSL "$SHIM_URL" -o "$BIN_DIR/agent-sandbox" || { printf 'Error: failed to download shim from %s\n' "$SHIM_URL" >&2; exit 1; }
chmod +x "$BIN_DIR/agent-sandbox"

# If an existing agent-sandbox binary is on PATH (and it's not our shim),
# replace it in-place so the user doesn't need to change PATH.
EXISTING=$(command -v agent-sandbox 2>/dev/null || true)
if [ -n "$EXISTING" ] && [ "$EXISTING" != "$BIN_DIR/agent-sandbox" ]; then
  # Check it's not the shim already (shim has SHIM_VERSION near the top)
  if ! grep -q 'SHIM_VERSION=' "$EXISTING" 2>/dev/null; then
    printf 'Replacing existing binary at %s\n' "$EXISTING"
    if cp "$BIN_DIR/agent-sandbox" "$EXISTING" 2>/dev/null; then
      printf 'Done. agent-sandbox is now the shim.\n'
    else
      printf 'Permission denied. Trying with sudo...\n'
      sudo cp "$BIN_DIR/agent-sandbox" "$EXISTING"
      printf 'Done. agent-sandbox is now the shim.\n'
    fi
  fi
else
  case ":$PATH:" in
    *":$BIN_DIR:"*)
      printf 'Already on PATH.\n'
      ;;
    *)
      printf 'Add to your shell profile:\n'
      printf '  export PATH="%s:$PATH"\n' "$BIN_DIR"
      ;;
  esac
fi

printf '\nRun: agent-sandbox version\n'
