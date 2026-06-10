#!/bin/sh
set -eu

SHIM_VERSION="1.8.0"
GITHUB_REPO="donbader/agent-sandbox"
SANDBOX_HOME="${AGENT_SANDBOX_HOME:-$HOME/.agent-sandbox}"
CACHE_DIR="$SANDBOX_HOME/core"
SHIM_URL="https://api.github.com/repos/$GITHUB_REPO/contents/scripts/shim.sh"

die() { printf 'Error: %s\n' "$1" >&2; exit 1; }

# --- Helpers ---

CURL_CONNECT_TIMEOUT=10
CURL_MAX_TIME=60

find_local_latest() {
  # Find the newest cached version with a .complete sentinel.
  # Returns the version string (e.g. "1.30.0") or empty if none found.
  _found=""
  if [ -d "$CACHE_DIR" ]; then
    _found=$(
      for _d in "$CACHE_DIR"/*/; do
        [ -f "${_d}.complete" ] && basename "$_d"
      done | sort -t. -k1,1n -k2,2n -k3,3n | tail -1
    )
  fi
  printf '%s' "$_found"
}

resolve_latest() {
  _ver=$(curl -fsSL --connect-timeout "$CURL_CONNECT_TIMEOUT" --max-time "$CURL_MAX_TIME" \
    "https://api.github.com/repos/$GITHUB_REPO/releases?per_page=20" 2>/dev/null \
    | grep '"tag_name":' | grep '"v' | head -1 \
    | sed 's/.*"v\([^"]*\)".*/\1/')
  if [ -n "$_ver" ]; then
    printf '%s' "$_ver"
    return
  fi
  # Network failed — try local fallback
  _local=$(find_local_latest)
  if [ -n "$_local" ]; then
    printf 'Warning: Failed to resolve latest version (network). Falling back to local %s.\n' "$_local" >&2
    printf '%s' "$_local"
  fi
}

ensure_cached() {
  _dir="$CACHE_DIR/$1"
  [ -f "$_dir/.complete" ] && return
  _tmp="$_dir.tmp.$$"
  rm -rf "$_tmp"; mkdir -p "$_tmp"
  printf 'Downloading core %s...\n' "$1" >&2
  if curl -fsSL --connect-timeout "$CURL_CONNECT_TIMEOUT" --max-time "$CURL_MAX_TIME" \
    "https://github.com/$GITHUB_REPO/releases/download/v${1}/agent-sandbox-core-v${1}-${PLATFORM}.tar.gz" \
    | tar -xz -C "$_tmp"; then
    rm -rf "$_dir"; mv "$_tmp" "$_dir"; touch "$_dir/.complete"
  else
    rm -rf "$_tmp"
    # Download failed — try local fallback
    _local=$(find_local_latest)
    if [ -n "$_local" ]; then
      printf 'Warning: Failed to download core %s (timeout or network error). Falling back to local %s.\n' "$1" "$_local" >&2
      VER="$_local"
    else
      die "Failed to download core $1 and no local version available"
    fi
  fi
}

self_replace() {
  # Download latest shim, install to SANDBOX_HOME, replace existing on PATH
  mkdir -p "$SANDBOX_HOME/bin"
  _dest="$SANDBOX_HOME/bin/agent-sandbox"
  if command -v gh >/dev/null 2>&1; then
    gh api "repos/$GITHUB_REPO/contents/scripts/shim.sh" -H "Accept: application/vnd.github.raw" > "$_dest" \
      || die "Failed to download shim via gh"
  else
    curl -fsSL -H "Accept: application/vnd.github.raw" "$SHIM_URL" -o "$_dest" \
      || die "Failed to download shim"
  fi
  chmod +x "$_dest"
  _existing=$(command -v agent-sandbox 2>/dev/null || true)
  if [ -n "$_existing" ] && [ "$_existing" != "$_dest" ]; then
    cp "$_dest" "$_existing" 2>/dev/null \
      || sudo cp "$_dest" "$_existing"
  fi
}

# --- Platform detection ---

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in x86_64) ARCH="amd64" ;; aarch64) ARCH="arm64" ;; esac
PLATFORM="${OS}-${ARCH}"

# --- Parse args: extract -C value, --dev flag, and subcommand in one pass ---

PROJECT_DIR="."
CMD=""
DEV_MODE=""
_grab_next=""
for _arg in "$@"; do
  if [ -n "$_grab_next" ]; then
    PROJECT_DIR="$_arg"; _grab_next=""; continue
  fi
  case "$_arg" in
    -C|--dir) _grab_next=1 ;;
    --dev) DEV_MODE=1 ;;
    -*) ;;
    *) [ -z "$CMD" ] && CMD="$_arg" ;;
  esac
done

# --- Shim-owned commands ---

case "$CMD" in
  upgrade)
    _old_ver="$SHIM_VERSION"
    self_replace
    _new_ver=$(grep '^SHIM_VERSION=' "$SANDBOX_HOME/bin/agent-sandbox" | cut -d'"' -f2)
    if [ "$_old_ver" = "$_new_ver" ]; then
      printf 'Already up to date (shim v%s)\n' "$_old_ver"
    else
      printf 'Upgraded shim v%s → v%s\n' "$_old_ver" "$_new_ver"
    fi
    exit 0 ;;
  version)
    printf 'shim: %s\n' "$SHIM_VERSION"
    if [ -f "$PROJECT_DIR/agent.yaml" ]; then
      _cv=$(grep '^core_version:' "$PROJECT_DIR/agent.yaml" | awk '{print $2}' | tr -d '"'"'")
      [ -n "$_cv" ] && printf 'core: %s\n' "$_cv"
    elif [ -f "$PROJECT_DIR/fleet.yaml" ]; then
      _first=$(grep -A1 '^agents:' "$PROJECT_DIR/fleet.yaml" | tail -1 | sed 's/^[[:space:]]*-[[:space:]]*//')
      if [ -n "$_first" ] && [ -f "$PROJECT_DIR/$_first/agent.yaml" ]; then
        _cv=$(grep '^core_version:' "$PROJECT_DIR/$_first/agent.yaml" | awk '{print $2}' | tr -d '"'"'")
        [ -n "$_cv" ] && printf 'core: %s (from %s)\n' "$_cv" "$_first"
      fi
      printf 'mode: fleet\n'
    fi
    exit 0 ;;
esac

# --- Dev mode: build from source when --dev flag is passed ---

if [ -n "$DEV_MODE" ]; then
  [ -f "cmd/agent-sandbox-core/main.go" ] || die "--dev requires running from the agent-sandbox source repo"
  DEV_BIN="./core/agent-sandbox-core"
  printf '[dev] Building from source...\n' >&2
  if command -v go >/dev/null 2>&1; then
    go build -o "$DEV_BIN" ./cmd/agent-sandbox-core/ || die "Dev build failed"
  elif command -v flox >/dev/null 2>&1; then
    flox activate -- go build -o "$DEV_BIN" ./cmd/agent-sandbox-core/ || die "Dev build failed"
  else
    die "Dev mode requires 'go' or 'flox' on PATH"
  fi
  # Strip --dev from args before exec
  _i=0; _total=$#
  while [ "$_i" -lt "$_total" ]; do
    _i=$((_i + 1))
    _arg="$1"; shift
    [ "$_arg" = "--dev" ] && continue
    set -- "$@" "$_arg"
  done
  exec "$DEV_BIN" "$@"
fi

# --- Resolve core version ---

# resolve_version_from_yaml <path> extracts core_version from a YAML file.
resolve_version_from_yaml() {
  grep '^core_version:' "$1" | awk '{print $2}' | tr -d '"'"'"
}

# resolve_fleet_version extracts core_version from the first agent in fleet.yaml.
resolve_fleet_version() {
  _first_agent=$(grep -A1 '^agents:' "$PROJECT_DIR/fleet.yaml" | tail -1 | sed 's/^[[:space:]]*-[[:space:]]*//')
  [ -n "$_first_agent" ] || return 1
  _agent_yaml="$PROJECT_DIR/$_first_agent/agent.yaml"
  [ -f "$_agent_yaml" ] || die "Fleet agent '$_first_agent' missing agent.yaml at $_agent_yaml"
  resolve_version_from_yaml "$_agent_yaml"
}

# require_latest resolves latest version or dies trying.
require_latest() {
  _v=$(resolve_latest)
  [ -n "$_v" ] || die "Could not resolve latest core version"
  printf '%s' "$_v"
}

if [ -f "$PROJECT_DIR/agent.yaml" ]; then
  VER=$(resolve_version_from_yaml "$PROJECT_DIR/agent.yaml")
  if [ -z "$VER" ]; then
    VER=$(require_latest)
    printf 'Warning: core_version not set. Using latest (%s).\n' "$VER" >&2
    printf 'Pin it: add core_version: %s to agent.yaml\n' "$VER" >&2
  elif [ "$VER" = "latest" ]; then
    VER=$(require_latest)
  fi
elif [ -f "$PROJECT_DIR/fleet.yaml" ]; then
  VER=$(resolve_fleet_version)
  if [ -z "$VER" ]; then
    VER=$(require_latest)
    printf 'Warning: core_version not set in fleet agents. Using latest (%s).\n' "$VER" >&2
  elif [ "$VER" = "latest" ]; then
    VER=$(require_latest)
  fi
elif [ "$CMD" = "init" ]; then
  VER=$(require_latest)
else
  die "No agent.yaml or fleet.yaml found in $PROJECT_DIR. Run 'agent-sandbox init' first."
fi

# Strip leading 'v' prefix if present (e.g. v1.2.3 → 1.2.3)
VER="${VER#v}"

case "$VER" in [0-9]*.[0-9]*.[0-9]*) ;; *) die "Invalid core_version: '$VER'" ;; esac

ensure_cached "$VER"
exec "$CACHE_DIR/$VER/agent-sandbox-core" "$@"
