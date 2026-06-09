#!/bin/sh
set -eu

SHIM_VERSION="1.0.0"
GITHUB_REPO="donbader/agent-sandbox"
SANDBOX_HOME="${AGENT_SANDBOX_HOME:-$HOME/.agent-sandbox}"
CACHE_DIR="$SANDBOX_HOME/core"

platform_detect() {
  OS=$(uname -s | tr '[:upper:]' '[:lower:]')
  ARCH=$(uname -m)
  case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
  esac
  PLATFORM="${OS}-${ARCH}"
}

resolve_latest() {
  curl -fsSL "https://api.github.com/repos/$GITHUB_REPO/releases?per_page=20" \
    | grep '"tag_name":' \
    | grep 'core-v' \
    | head -1 \
    | sed 's/.*"core-v\([^"]*\)".*/\1/'
}

ensure_cached() {
  _ver="$1"
  _dir="$CACHE_DIR/$_ver"
  if [ -f "$_dir/.complete" ]; then
    return
  fi
  mkdir -p "$_dir"
  _url="https://github.com/$GITHUB_REPO/releases/download/core-v${_ver}/agent-sandbox-core-v${_ver}-${PLATFORM}.tar.gz"
  echo "Downloading agent-sandbox-core v${_ver}..." >&2
  curl -fsSL "$_url" | tar -xz -C "$_dir"
  touch "$_dir/.complete"
}

platform_detect

case "${1:-}" in
  upgrade)
    curl -fsSL "https://raw.githubusercontent.com/$GITHUB_REPO/main/scripts/install.sh" | sh
    exit $?
    ;;
  version)
    echo "shim: $SHIM_VERSION"
    if [ -f agent.yaml ]; then
      _cv=$(grep '^core_version:' agent.yaml | awk '{print $2}')
      [ -n "$_cv" ] && echo "core: $_cv"
    fi
    exit 0
    ;;
esac

# Resolve core version
if [ -f agent.yaml ]; then
  VER=$(grep '^core_version:' agent.yaml | awk '{print $2}')
  if [ -z "$VER" ]; then
    VER=$(resolve_latest)
    echo "Warning: core_version not set in agent.yaml. Defaulting to latest ($VER)." >&2
    echo "Set 'core_version: $VER' in agent.yaml to pin." >&2
  elif [ "$VER" = "latest" ]; then
    VER=$(resolve_latest)
  fi
else
  if [ "${1:-}" = "init" ]; then
    VER=$(resolve_latest)
  else
    echo "Error: No agent.yaml found. Run 'agent-sandbox init' first." >&2
    exit 1
  fi
fi

ensure_cached "$VER"
exec "$CACHE_DIR/$VER/agent-sandbox-core" "$@"
