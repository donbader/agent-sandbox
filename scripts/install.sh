#!/bin/sh
set -e

AGENT_SANDBOX_HOME="${AGENT_SANDBOX_HOME:-$HOME/.agent-sandbox}"
BIN_DIR="$AGENT_SANDBOX_HOME/bin"
SHIM_URL="https://raw.githubusercontent.com/donbader/agent-sandbox/main/scripts/shim.sh"

echo "Installing agent-sandbox shim..."

mkdir -p "$BIN_DIR"
curl -fsSL "$SHIM_URL" -o "$BIN_DIR/agent-sandbox"
chmod +x "$BIN_DIR/agent-sandbox"

echo "Installed to $BIN_DIR/agent-sandbox"

case ":$PATH:" in
  *":$BIN_DIR:"*)
    echo "Already on PATH"
    ;;
  *)
    echo "Add to your shell profile:"
    echo "  export PATH=\"$BIN_DIR:\$PATH\""
    ;;
esac

echo ""
echo "Run: agent-sandbox init"
