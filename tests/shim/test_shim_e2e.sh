#!/usr/bin/env bash
# Comprehensive shim tests — exercises the real shim.sh end-to-end.
# Mocks curl/exec to avoid network and binary dependencies.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SHIM="$SCRIPT_DIR/../../scripts/shim.sh"
PASS=0
FAIL=0

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

pass() { PASS=$((PASS + 1)); printf '  \033[32m✓\033[0m %s\n' "$1"; }
fail() { FAIL=$((FAIL + 1)); printf '  \033[31m✗\033[0m %s\n' "$1"; }

# --- Build a harness that runs the real shim with mocked externals ---
# We create a wrapper that:
#   - Overrides PATH so our fake `curl` is found first
#   - Pre-creates the cached core directory so ensure_cached is a no-op
#   - Replaces the final `exec` target with a stub that captures invocation

# Fake curl that returns a canned version
FAKE_BIN="$WORK/bin"
mkdir -p "$FAKE_BIN"

cat > "$FAKE_BIN/curl" <<'EOF'
#!/bin/sh
# Fake curl: returns a GitHub releases JSON with v8.8.8 as latest
cat <<'JSON'
[{"tag_name": "v8.8.8"}]
JSON
EOF
chmod +x "$FAKE_BIN/curl"

# Fake uname that returns consistent platform
cat > "$FAKE_BIN/uname" <<'EOF'
#!/bin/sh
case "$1" in
  -s) echo "Linux" ;;
  -m) echo "x86_64" ;;
  *) echo "Linux" ;;
esac
EOF
chmod +x "$FAKE_BIN/uname"

# Helper: run the shim in a subshell with mocked environment.
# Arguments are passed to the shim. Returns stdout; stderr is captured separately.
# Sets global SHIM_EXIT, SHIM_STDOUT, SHIM_STDERR after each call.
run_shim() {
  local sandbox_home="$WORK/sandbox-home"
  mkdir -p "$sandbox_home/core"

  # If we expect a version, pre-create the cache so ensure_cached skips download
  local ver="${EXPECTED_VER:-}"
  if [ -n "$ver" ]; then
    mkdir -p "$sandbox_home/core/$ver"
    touch "$sandbox_home/core/$ver/.complete"
    # Stub core binary that echoes its args
    cat > "$sandbox_home/core/$ver/agent-sandbox-core" <<STUB
#!/bin/sh
echo "CORE_EXEC: \$*"
STUB
    chmod +x "$sandbox_home/core/$ver/agent-sandbox-core"
  fi

  SHIM_STDOUT="$WORK/stdout.$$"
  SHIM_STDERR="$WORK/stderr.$$"

  # Run shim with mocked PATH and HOME
  SHIM_EXIT=0
  env -i \
    PATH="$FAKE_BIN:/usr/bin:/bin" \
    HOME="$WORK/fakehome" \
    AGENT_SANDBOX_HOME="$sandbox_home" \
    /bin/sh "$SHIM" "$@" >"$SHIM_STDOUT" 2>"$SHIM_STDERR" || SHIM_EXIT=$?
}

echo "=== Comprehensive Shim Tests ==="
echo ""

# ============================================================
# Section 1: Arg parsing
# ============================================================
echo "--- Section: Argument parsing ---"

# Test: -C sets project dir
echo "  Testing -C flag..."
TEST_DIR="$WORK/argtest1"
mkdir -p "$TEST_DIR"
cat > "$TEST_DIR/agent.yaml" <<'EOF'
name: test
core_version: 1.0.0
runtime:
  image: test
EOF
EXPECTED_VER="1.0.0" run_shim -C "$TEST_DIR" generate
if grep -q "CORE_EXEC:.*-C.*generate" "$SHIM_STDOUT" 2>/dev/null || [ $SHIM_EXIT -eq 0 ]; then
  pass "-C flag resolves project directory"
else
  fail "-C flag: exit=$SHIM_EXIT, stderr=$(cat "$SHIM_STDERR")"
fi

# Test: --dir is alias for -C
echo "  Testing --dir flag..."
EXPECTED_VER="1.0.0" run_shim --dir "$TEST_DIR" generate
if [ $SHIM_EXIT -eq 0 ]; then
  pass "--dir is alias for -C"
else
  fail "--dir: exit=$SHIM_EXIT, stderr=$(cat "$SHIM_STDERR")"
fi

# Test: subcommand is first non-flag arg
echo "  Testing subcommand extraction..."
EXPECTED_VER="1.0.0" run_shim -C "$TEST_DIR" compose up --build
OUTPUT=$(cat "$SHIM_STDOUT")
if echo "$OUTPUT" | grep -q "compose"; then
  pass "Subcommand extracted correctly past flags"
else
  fail "Subcommand not passed through: $OUTPUT"
fi

# ============================================================
# Section 2: Version resolution — single agent
# ============================================================
echo ""
echo "--- Section: Version resolution (single agent) ---"

# Test: explicit version
echo "  Testing explicit core_version..."
TEST_DIR="$WORK/vertest1"
mkdir -p "$TEST_DIR"
cat > "$TEST_DIR/agent.yaml" <<'EOF'
name: explicit
core_version: 2.5.0
runtime:
  image: test
EOF
EXPECTED_VER="2.5.0" run_shim -C "$TEST_DIR" generate
if [ $SHIM_EXIT -eq 0 ]; then
  pass "Explicit version 2.5.0 resolved"
else
  fail "Explicit version: exit=$SHIM_EXIT, stderr=$(cat "$SHIM_STDERR")"
fi

# Test: core_version: latest resolves via curl
echo "  Testing core_version: latest..."
TEST_DIR="$WORK/vertest2"
mkdir -p "$TEST_DIR"
cat > "$TEST_DIR/agent.yaml" <<'EOF'
name: latest-test
core_version: latest
runtime:
  image: test
EOF
EXPECTED_VER="8.8.8" run_shim -C "$TEST_DIR" generate
if [ $SHIM_EXIT -eq 0 ]; then
  pass "core_version: latest resolved to 8.8.8 via fake curl"
else
  fail "Latest resolution: exit=$SHIM_EXIT, stderr=$(cat "$SHIM_STDERR")"
fi

# Test: missing core_version falls back to latest
echo "  Testing missing core_version falls back to latest..."
TEST_DIR="$WORK/vertest3"
mkdir -p "$TEST_DIR"
cat > "$TEST_DIR/agent.yaml" <<'EOF'
name: no-version
runtime:
  image: test
EOF
EXPECTED_VER="8.8.8" run_shim -C "$TEST_DIR" generate
if [ $SHIM_EXIT -eq 0 ] && grep -q "Warning: core_version not set" "$SHIM_STDERR"; then
  pass "Missing core_version warns and falls back to latest"
else
  fail "Missing version: exit=$SHIM_EXIT, stderr=$(cat "$SHIM_STDERR")"
fi

# ============================================================
# Section 3: Version resolution — fleet mode
# ============================================================
echo ""
echo "--- Section: Version resolution (fleet mode) ---"

# Test: fleet.yaml with pinned version
echo "  Testing fleet.yaml with pinned version..."
TEST_DIR="$WORK/fleet1"
mkdir -p "$TEST_DIR/worker-01"
cat > "$TEST_DIR/fleet.yaml" <<'EOF'
agents:
  - worker-01
EOF
cat > "$TEST_DIR/worker-01/agent.yaml" <<'EOF'
name: worker-01
core_version: 3.3.3
runtime:
  image: test
EOF
EXPECTED_VER="3.3.3" run_shim -C "$TEST_DIR" generate
if [ $SHIM_EXIT -eq 0 ]; then
  pass "Fleet with pinned version 3.3.3 resolved"
else
  fail "Fleet pinned: exit=$SHIM_EXIT, stderr=$(cat "$SHIM_STDERR")"
fi

# Test: fleet.yaml with core_version: latest
echo "  Testing fleet.yaml with latest..."
TEST_DIR="$WORK/fleet2"
mkdir -p "$TEST_DIR/agent-a"
cat > "$TEST_DIR/fleet.yaml" <<'EOF'
agents:
  - agent-a
EOF
cat > "$TEST_DIR/agent-a/agent.yaml" <<'EOF'
name: agent-a
core_version: latest
runtime:
  image: test
EOF
EXPECTED_VER="8.8.8" run_shim -C "$TEST_DIR" generate
if [ $SHIM_EXIT -eq 0 ]; then
  pass "Fleet with latest resolved to 8.8.8"
else
  fail "Fleet latest: exit=$SHIM_EXIT, stderr=$(cat "$SHIM_STDERR")"
fi

# Test: fleet.yaml with missing first agent dir
echo "  Testing fleet.yaml with missing agent directory..."
TEST_DIR="$WORK/fleet3"
mkdir -p "$TEST_DIR"
cat > "$TEST_DIR/fleet.yaml" <<'EOF'
agents:
  - nonexistent
EOF
EXPECTED_VER="" run_shim -C "$TEST_DIR" generate
if [ $SHIM_EXIT -ne 0 ] && grep -q "missing agent.yaml" "$SHIM_STDERR"; then
  pass "Fleet with missing agent dir produces clear error"
else
  fail "Fleet missing agent: exit=$SHIM_EXIT, stderr=$(cat "$SHIM_STDERR")"
fi

# Test: fleet.yaml with hyphenated agent names (the original bug)
echo "  Testing fleet.yaml with hyphenated agent names..."
TEST_DIR="$WORK/fleet4"
mkdir -p "$TEST_DIR/my-cool-agent-001"
cat > "$TEST_DIR/fleet.yaml" <<'EOF'
agents:
  - my-cool-agent-001
EOF
cat > "$TEST_DIR/my-cool-agent-001/agent.yaml" <<'EOF'
name: my-cool-agent-001
core_version: 4.4.4
runtime:
  image: test
EOF
EXPECTED_VER="4.4.4" run_shim -C "$TEST_DIR" generate
if [ $SHIM_EXIT -eq 0 ]; then
  pass "Hyphenated agent name 'my-cool-agent-001' resolved correctly"
else
  fail "Hyphenated name: exit=$SHIM_EXIT, stderr=$(cat "$SHIM_STDERR")"
fi

# ============================================================
# Section 4: Error conditions
# ============================================================
echo ""
echo "--- Section: Error conditions ---"

# Test: no config files
echo "  Testing no config files..."
TEST_DIR="$WORK/errtest1"
mkdir -p "$TEST_DIR"
EXPECTED_VER="" run_shim -C "$TEST_DIR" generate
if [ $SHIM_EXIT -ne 0 ] && grep -q "No agent.yaml or fleet.yaml" "$SHIM_STDERR"; then
  pass "No config → clear error mentioning both files"
else
  fail "No config: exit=$SHIM_EXIT, stderr=$(cat "$SHIM_STDERR")"
fi

# Test: invalid version format
echo "  Testing invalid core_version format..."
TEST_DIR="$WORK/errtest2"
mkdir -p "$TEST_DIR"
cat > "$TEST_DIR/agent.yaml" <<'EOF'
name: bad-ver
core_version: not-a-version
runtime:
  image: test
EOF
EXPECTED_VER="" run_shim -C "$TEST_DIR" generate
if [ $SHIM_EXIT -ne 0 ] && grep -q "Invalid core_version" "$SHIM_STDERR"; then
  pass "Invalid version format rejected"
else
  fail "Invalid version: exit=$SHIM_EXIT, stderr=$(cat "$SHIM_STDERR")"
fi

# Test: init command works without config files
echo "  Testing init without config files..."
TEST_DIR="$WORK/errtest3"
mkdir -p "$TEST_DIR"
EXPECTED_VER="8.8.8" run_shim -C "$TEST_DIR" init
if [ $SHIM_EXIT -eq 0 ]; then
  pass "init command works without existing config"
else
  fail "Init: exit=$SHIM_EXIT, stderr=$(cat "$SHIM_STDERR")"
fi

# ============================================================
# Section 5: version subcommand
# ============================================================
echo ""
echo "--- Section: version subcommand ---"

# Test: version with agent.yaml
echo "  Testing version output with agent.yaml..."
TEST_DIR="$WORK/vercmd1"
mkdir -p "$TEST_DIR"
cat > "$TEST_DIR/agent.yaml" <<'EOF'
name: versioned
core_version: 6.0.0
runtime:
  image: test
EOF
EXPECTED_VER="" run_shim -C "$TEST_DIR" version
OUTPUT=$(cat "$SHIM_STDOUT")
if echo "$OUTPUT" | grep -q "shim:" && echo "$OUTPUT" | grep -q "core: 6.0.0"; then
  pass "version shows shim and core version"
else
  fail "version output: $OUTPUT"
fi

# Test: version with fleet.yaml
echo "  Testing version output with fleet.yaml..."
TEST_DIR="$WORK/vercmd2"
mkdir -p "$TEST_DIR/bot"
cat > "$TEST_DIR/fleet.yaml" <<'EOF'
agents:
  - bot
EOF
cat > "$TEST_DIR/bot/agent.yaml" <<'EOF'
name: bot
core_version: 7.1.0
runtime:
  image: test
EOF
EXPECTED_VER="" run_shim -C "$TEST_DIR" version
OUTPUT=$(cat "$SHIM_STDOUT")
if echo "$OUTPUT" | grep -q "core: 7.1.0 (from bot)" && echo "$OUTPUT" | grep -q "mode: fleet"; then
  pass "version in fleet mode shows source agent and mode"
else
  fail "fleet version output: $OUTPUT"
fi

# Test: version with no config
echo "  Testing version output with no config..."
TEST_DIR="$WORK/vercmd3"
mkdir -p "$TEST_DIR"
EXPECTED_VER="" run_shim -C "$TEST_DIR" version
OUTPUT=$(cat "$SHIM_STDOUT")
if echo "$OUTPUT" | grep -q "shim:" && ! echo "$OUTPUT" | grep -q "core:"; then
  pass "version without config shows only shim version"
else
  fail "no-config version output: $OUTPUT"
fi

# ============================================================
# Section 6: Precedence and edge cases
# ============================================================
echo ""
echo "--- Section: Precedence and edge cases ---"

# Test: agent.yaml wins over fleet.yaml
echo "  Testing agent.yaml precedence over fleet.yaml..."
TEST_DIR="$WORK/prec1"
mkdir -p "$TEST_DIR/sub"
cat > "$TEST_DIR/agent.yaml" <<'EOF'
name: root
core_version: 5.0.0
runtime:
  image: test
EOF
cat > "$TEST_DIR/fleet.yaml" <<'EOF'
agents:
  - sub
EOF
cat > "$TEST_DIR/sub/agent.yaml" <<'EOF'
name: sub
core_version: 1.0.0
runtime:
  image: test
EOF
EXPECTED_VER="5.0.0" run_shim -C "$TEST_DIR" generate
if [ $SHIM_EXIT -eq 0 ]; then
  pass "agent.yaml at root wins over fleet.yaml"
else
  fail "Precedence: exit=$SHIM_EXIT, stderr=$(cat "$SHIM_STDERR")"
fi

# Test: single-quoted core_version
echo "  Testing single-quoted core_version..."
TEST_DIR="$WORK/edge1"
mkdir -p "$TEST_DIR"
cat > "$TEST_DIR/agent.yaml" <<EOF
name: quoted
core_version: '9.1.2'
runtime:
  image: test
EOF
EXPECTED_VER="9.1.2" run_shim -C "$TEST_DIR" generate
if [ $SHIM_EXIT -eq 0 ]; then
  pass "Single-quoted version resolved"
else
  fail "Single-quoted: exit=$SHIM_EXIT, stderr=$(cat "$SHIM_STDERR")"
fi

# Test: version with trailing whitespace
echo "  Testing core_version with trailing whitespace..."
TEST_DIR="$WORK/edge2"
mkdir -p "$TEST_DIR"
printf 'name: whitespace\ncore_version: 1.1.1   \nruntime:\n  image: test\n' > "$TEST_DIR/agent.yaml"
EXPECTED_VER="1.1.1" run_shim -C "$TEST_DIR" generate
if [ $SHIM_EXIT -eq 0 ]; then
  pass "Trailing whitespace stripped from version"
else
  fail "Whitespace: exit=$SHIM_EXIT, stderr=$(cat "$SHIM_STDERR")"
fi

# Test: fleet.yaml with indented agents (spaces before hyphen)
echo "  Testing fleet.yaml with extra indentation..."
TEST_DIR="$WORK/edge3"
mkdir -p "$TEST_DIR/deep-agent"
cat > "$TEST_DIR/fleet.yaml" <<'EOF'
agents:
    - deep-agent
EOF
cat > "$TEST_DIR/deep-agent/agent.yaml" <<'EOF'
name: deep-agent
core_version: 2.2.2
runtime:
  image: test
EOF
EXPECTED_VER="2.2.2" run_shim -C "$TEST_DIR" generate
if [ $SHIM_EXIT -eq 0 ]; then
  pass "Extra indentation in fleet.yaml handled"
else
  fail "Indentation: exit=$SHIM_EXIT, stderr=$(cat "$SHIM_STDERR")"
fi

# ============================================================
# Summary
# ============================================================
echo ""
TOTAL=$((PASS + FAIL))
echo "=== Results: $PASS/$TOTAL passed ==="
[ "$FAIL" -eq 0 ] || exit 1
