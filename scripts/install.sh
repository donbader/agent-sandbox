#!/bin/sh
# Install or upgrade the agent-sandbox shim.
set -e

AGENT_SANDBOX_HOME="${AGENT_SANDBOX_HOME:-$HOME/.agent-sandbox}"
BIN_DIR="$AGENT_SANDBOX_HOME/bin"
SHIM_URL="https://raw.githubusercontent.com/donbader/agent-sandbox/main/scripts/shim.sh"

printf 'Installing agent-sandbox...\n'

# Download shim
mkdir -p "$BIN_DIR"
curl -fsSL "$SHIM_URL" -o "$BIN_DIR/agent-sandbox" \
  || { printf 'Error: failed to download shim from %s\n' "$SHIM_URL" >&2; exit 1; }
chmod +x "$BIN_DIR/agent-sandbox"

# If an old binary exists elsewhere on PATH, replace it in-place
EXISTING=$(command -v agent-sandbox 2>/dev/null || true)
if [ -n "$EXISTING" ] && [ "$EXISTING" != "$BIN_DIR/agent-sandbox" ]; then
  printf 'Replacing %s\n' "$EXISTING"
  cp "$BIN_DIR/agent-sandbox" "$EXISTING" 2>/dev/null \
    || sudo cp "$BIN_DIR/agent-sandbox" "$EXISTING"
else
  # shellcheck disable=SC2016
  case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *) printf 'Add to your shell profile:\n  export PATH="%s:$PATH"\n' "$BIN_DIR" ;;
  esac
fi

_ver=$(grep '^SHIM_VERSION=' "$BIN_DIR/agent-sandbox" | cut -d'"' -f2)
printf 'Installed agent-sandbox shim v%s\n' "$_ver"
