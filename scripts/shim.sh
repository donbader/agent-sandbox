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

die() { echo "Error: $1" >&2; exit 1; }

resolve_latest() {
  curl -fsSL "https://api.github.com/repos/$GITHUB_REPO/releases?per_page=20" \
    | grep '"tag_name":' \
    | grep 'core-v' \
    | head -1 \
    | sed 's/.*"core-v\([^"]*\)".*/\1/'
}

validate_version() {
  case "$1" in
    [0-9]*.[0-9]*.[0-9]*) ;;
    *) die "Invalid core_version: '$1'" ;;
  esac
}

ensure_cached() {
  _ver="$1"
  _dir="$CACHE_DIR/$_ver"
  if [ -f "$_dir/.complete" ]; then
    return
  fi
  _tmp="$_dir.tmp.$$"
  rm -rf "$_tmp"
  mkdir -p "$_tmp"
  _url="https://github.com/$GITHUB_REPO/releases/download/core-v${_ver}/agent-sandbox-core-v${_ver}-${PLATFORM}.tar.gz"
  echo "Downloading agent-sandbox-core v${_ver}..." >&2
  curl -fsSL "$_url" | tar -xz -C "$_tmp" || { rm -rf "$_tmp"; die "Failed to download core $_ver"; }
  rm -rf "$_dir"
  mv "$_tmp" "$_dir"
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
      _cv=$(grep '^core_version:' agent.yaml | awk '{print $2}' | tr -d '"'"'")
      [ -n "$_cv" ] && echo "core: $_cv"
    fi
    exit 0
    ;;
esac

# Resolve core version
if [ -f agent.yaml ]; then
  VER=$(grep '^core_version:' agent.yaml | awk '{print $2}' | tr -d '"'"'")
  if [ -z "$VER" ]; then
    VER=$(resolve_latest)
    [ -n "$VER" ] || die "Could not resolve latest core version (GitHub API rate limit?)"
    echo "Warning: core_version not set in agent.yaml. Defaulting to latest ($VER)." >&2
    echo "Set 'core_version: $VER' in agent.yaml to pin." >&2
  elif [ "$VER" = "latest" ]; then
    VER=$(resolve_latest)
    [ -n "$VER" ] || die "Could not resolve latest core version (GitHub API rate limit?)"
  fi
else
  if [ "${1:-}" = "init" ]; then
    VER=$(resolve_latest)
    [ -n "$VER" ] || die "Could not resolve latest core version (GitHub API rate limit?)"
  else
    echo "Error: No agent.yaml found. Run 'agent-sandbox init' first." >&2
    exit 1
  fi
fi

validate_version "$VER"

ensure_cached "$VER"
exec "$CACHE_DIR/$VER/agent-sandbox-core" "$@"
