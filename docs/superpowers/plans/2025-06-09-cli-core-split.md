# CLI/Core Split Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Go CLI binary with a POSIX shell shim that auto-downloads and delegates to a versioned core binary, making core the primary release artifact.

**Architecture:** A ~50-line shell script at `~/.agent-sandbox/bin/agent-sandbox` resolves `core_version` from `agent.yaml`, downloads/caches the platform-specific core tarball, and execs the `agent-sandbox-core` Go binary within it. The core binary owns all real commands (init, generate, compose, gateway-url, audit).

**Tech Stack:** POSIX shell (shim), Go (core binary), GitHub Actions (CI/release), curl (downloads)

---

## File Structure

### New Files
- `scripts/shim.sh` — the shim script (ships as `agent-sandbox` on user's PATH)
- `scripts/install.sh` — `curl | sh` installer that places the shim
- `cmd/agent-sandbox-core/main.go` — new entrypoint for the core binary (replaces `cmd/agent-sandbox/`)
- `.github/workflows/shim-release.yml` — releases the shim script itself

### Modified Files
- `.github/workflows/core-release.yml` — add host binary builds (darwin/arm64, darwin/amd64, linux/amd64, linux/arm64) + platform-specific tarballs
- `.github/workflows/ci.yml` — test shim + core binary flow
- `cmd/agent-sandbox/main.go` — final release only: upgrade command becomes migration command
- `internal/release/fetcher.go` — change cache location from OS-specific to `~/.agent-sandbox/core/`

### Removed After Migration
- `cmd/agent-sandbox/` — retired after v1.27.0 (the migration release)
- `.github/workflows/release.yml` — GoReleaser CLI release (no longer needed)
- `.goreleaser.yml` (if exists)

---

### Task 1: Write the Shim Script

**Files:**
- Create: `scripts/shim.sh`

- [ ] **Step 1: Write the shim script**

```sh
#!/bin/sh
# agent-sandbox — version-resolving shim for agent-sandbox-core
set -e

SHIM_VERSION="1.0.0"
SANDBOX_HOME="${AGENT_SANDBOX_HOME:-$HOME/.agent-sandbox}"
CACHE_DIR="$SANDBOX_HOME/core"
GITHUB_REPO="donbader/agent-sandbox"

die() { printf 'error: %s\n' "$1" >&2; exit 1; }
warn() { printf '⚠ %s\n' "$1" >&2; }

detect_platform() {
  OS=$(uname -s | tr '[:upper:]' '[:lower:]')
  ARCH=$(uname -m)
  case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *) die "Unsupported architecture: $ARCH" ;;
  esac
  echo "${OS}-${ARCH}"
}

fetch_latest_version() {
  RELEASES=$(curl -fsSL "https://api.github.com/repos/$GITHUB_REPO/releases?per_page=20") || die "Failed to query GitHub API"
  VERSION=$(echo "$RELEASES" | grep -o '"tag_name": *"core-v[^"]*"' | head -1 | sed 's/.*"core-v\([^"]*\)".*/\1/')
  [ -n "$VERSION" ] || die "No core release found"
  echo "$VERSION"
}

ensure_cached() {
  VER="$1"
  DEST="$CACHE_DIR/$VER"
  if [ -f "$DEST/.complete" ]; then
    return 0
  fi
  PLATFORM=$(detect_platform)
  ASSET="agent-sandbox-core-v${VER}-${PLATFORM}.tar.gz"
  URL="https://github.com/$GITHUB_REPO/releases/download/core-v${VER}/${ASSET}"
  printf 'Downloading core %s...\n' "$VER" >&2
  mkdir -p "$DEST"
  curl -fsSL "$URL" | tar xz -C "$DEST" || { rm -rf "$DEST"; die "Failed to download core $VER"; }
  touch "$DEST/.complete"
}

# --- Main ---

case "${1:-}" in
  upgrade)
    INSTALL_URL="https://raw.githubusercontent.com/$GITHUB_REPO/main/scripts/install.sh"
    curl -fsSL "$INSTALL_URL" | sh
    exit $?
    ;;
  version)
    echo "shim: $SHIM_VERSION"
    if [ -f agent.yaml ]; then
      VER=$(grep '^core_version:' agent.yaml | awk '{print $2}')
      [ -n "$VER" ] && echo "core: $VER" || echo "core: (not pinned)"
    else
      echo "core: (no agent.yaml)"
    fi
    exit 0
    ;;
esac

# Resolve core version
if [ -f agent.yaml ]; then
  VER=$(grep '^core_version:' agent.yaml | awk '{print $2}')
  if [ -z "$VER" ]; then
    VER=$(fetch_latest_version)
    warn "No core_version in agent.yaml. Using latest ($VER)."
    warn "Pin it: add 'core_version: $VER' to your agent.yaml"
  fi
elif [ "${1:-}" = "init" ]; then
  VER=$(fetch_latest_version)
else
  die "No agent.yaml found. Run 'agent-sandbox init' first."
fi

# Handle "latest" keyword
if [ "$VER" = "latest" ]; then
  VER=$(fetch_latest_version)
fi

ensure_cached "$VER"
exec "$CACHE_DIR/$VER/agent-sandbox-core" "$@"
```

- [ ] **Step 2: Make it executable and test locally**

Run: `chmod +x scripts/shim.sh && shellcheck scripts/shim.sh`
Expected: No errors (or only minor style notes)

- [ ] **Step 3: Commit**

```bash
git add scripts/shim.sh
git commit -m "feat: add POSIX shell shim for version-resolving CLI"
```

---

### Task 2: Write the Install Script

**Files:**
- Create: `scripts/install.sh`

- [ ] **Step 1: Write the install script**

```sh
#!/bin/sh
# Install agent-sandbox shim
set -e

SANDBOX_HOME="${AGENT_SANDBOX_HOME:-$HOME/.agent-sandbox}"
BIN_DIR="$SANDBOX_HOME/bin"
GITHUB_REPO="donbader/agent-sandbox"
SHIM_URL="https://raw.githubusercontent.com/$GITHUB_REPO/main/scripts/shim.sh"

printf 'Installing agent-sandbox...\n'

mkdir -p "$BIN_DIR"
curl -fsSL "$SHIM_URL" -o "$BIN_DIR/agent-sandbox"
chmod +x "$BIN_DIR/agent-sandbox"

printf '\nInstalled to %s/agent-sandbox\n' "$BIN_DIR"

# Check if already on PATH
case ":$PATH:" in
  *":$BIN_DIR:"*) printf 'Already on PATH.\n' ;;
  *)
    printf '\nAdd to your shell profile:\n'
    printf '  export PATH="%s:$PATH"\n' "$BIN_DIR"
    ;;
esac

printf '\nRun: agent-sandbox init\n'
```

- [ ] **Step 2: Commit**

```bash
git add scripts/install.sh
git commit -m "feat: add curl|sh installer for shim"
```

---

### Task 3: Create the Core Binary Entrypoint

**Files:**
- Create: `cmd/agent-sandbox-core/main.go`
- Create: `cmd/agent-sandbox-core/generate.go`
- Create: `cmd/agent-sandbox-core/audit.go`

This is essentially a copy of `cmd/agent-sandbox/` minus `upgrade` logic, with path resolution changed to use `os.Executable()` for finding sibling assets.

- [ ] **Step 1: Write `cmd/agent-sandbox-core/main.go`**

The core binary's main differs from the current CLI in:
1. No `upgrade` command (shim owns that)
2. Resolves its own root path via `os.Executable()` → sibling dirs are plugins/presets/templates
3. No `internal/release` usage — the shim already downloaded us to the right place

```go
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/spf13/cobra"
)

var version = "dev"

// coreRoot is the directory containing this binary and sibling assets.
var coreRoot string

func init() {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
		if err == nil {
			coreRoot = filepath.Dir(exe)
		}
	}
	if coreRoot == "" {
		coreRoot = "."
	}
}

func main() {
	var dir string

	root := &cobra.Command{
		Use:              "agent-sandbox",
		Short:            "Opinionated agent sandbox orchestrator",
		Version:          version,
		TraverseChildren: true,
	}

	root.PersistentFlags().StringVarP(&dir, "dir", "C", ".", "Project directory containing agent.yaml")

	root.AddCommand(generateCmd(&dir))
	root.AddCommand(composeCmd(&dir))
	root.AddCommand(auditCmd(&dir))
	root.AddCommand(initCmd())
	root.AddCommand(gatewayURLCmd(&dir))

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
```

The key difference: `coreRoot` replaces `internal/release.Fetch()` calls. The `generate` command uses `coreRoot` directly as its core directory.

- [ ] **Step 2: Write `cmd/agent-sandbox-core/generate.go`**

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/dotenv"
	v1 "github.com/donbader/agent-sandbox/internal/generate/v1"
	"github.com/spf13/cobra"
)

func generateCmd(dir *string) *cobra.Command {
	var coreFlag string

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate build artifacts from agent.yaml or fleet.yaml",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectDir, err := filepath.Abs(*dir)
			if err != nil {
				return fmt.Errorf("resolve dir: %w", err)
			}

			// Determine core directory: --core flag > self-location
			coreDir := coreRoot
			if coreFlag != "" {
				abs, err := filepath.Abs(coreFlag)
				if err != nil {
					return fmt.Errorf("resolve --core path: %w", err)
				}
				if _, err := os.Stat(abs); err != nil {
					return fmt.Errorf("--core path does not exist: %s", abs)
				}
				coreDir = abs
				fmt.Fprintf(os.Stderr, "Using local core override: %s\n", abs)
			}

			// Load .env file so secrets are available for auth-header baking.
			dotenv.Load(filepath.Join(projectDir, ".env"))

			// Try single-agent first, then fleet mode
			cfg, loadErr := config.Load(projectDir)
			if loadErr == nil {
				return generateSingleAgent(cfg, projectDir, coreDir)
			}

			// Try fleet mode
			_, agents, fleetErr := config.LoadFleetAgents(projectDir)
			if fleetErr != nil {
				return fmt.Errorf("cannot load agent.yaml or fleet.yaml:\n  agent: %w\n  fleet: %v", loadErr, fleetErr)
			}

			return generateFleet(agents, projectDir, coreDir)
		},
	}

	cmd.Flags().StringVar(&coreFlag, "core", "", "Path to local core directory (overrides self-location)")
	return cmd
}

func generateSingleAgent(cfg *config.Config, projectDir, coreDir string) error {
	g := v1.NewGeneratorWithCore(projectDir, coreDir)
	if err := g.RunWithConfig(cfg, projectDir); err != nil {
		return err
	}

	_ = ensureSchemaComment(filepath.Join(projectDir, "agent.yaml"), ".build/schema.json")
	fmt.Fprintf(os.Stderr, "Generated .build/ in %s\n", projectDir)
	return nil
}

func generateFleet(agents []config.FleetAgent, projectDir, coreDir string) error {
	g := v1.NewGeneratorWithCore(projectDir, coreDir)
	if err := g.RunFleet(agents); err != nil {
		return err
	}

	_ = ensureSchemaComment(filepath.Join(projectDir, "fleet.yaml"), ".build/fleet-schema.json")
	for _, agent := range agents {
		agentYAML := filepath.Join(agent.Dir, "agent.yaml")
		relSchema, err := filepath.Rel(agent.Dir, filepath.Join(projectDir, ".build", "schema.json"))
		if err != nil {
			relSchema = ".build/schema.json"
		}
		_ = ensureSchemaComment(agentYAML, relSchema)
	}

	fmt.Fprintf(os.Stderr, "Generated .build/ for %d agents in %s\n", len(agents), projectDir)
	return nil
}
```

- [ ] **Step 3: Copy compose, gateway-url, init, audit commands**

Copy `composeCmd`, `gatewayURLCmd`, `initCmd` (and helpers) from `cmd/agent-sandbox/main.go` into the new `cmd/agent-sandbox-core/` package. The only change to `initCmd`: write `core_version: <version>` where `version` is the core binary's own version (injected via ldflags at build time).

Copy `audit.go` from `cmd/agent-sandbox/audit.go`.

- [ ] **Step 4: Verify it builds**

Run: `go build ./cmd/agent-sandbox-core/`
Expected: Clean build, produces `agent-sandbox-core` binary

- [ ] **Step 5: Run existing tests**

Run: `go test ./...`
Expected: All pass (internal packages unchanged)

- [ ] **Step 6: Commit**

```bash
git add cmd/agent-sandbox-core/
git commit -m "feat: add agent-sandbox-core binary (shim-delegated CLI)"
```

---

### Task 4: Update Core Release Workflow

**Files:**
- Modify: `.github/workflows/core-release.yml`

The core release must now:
1. Build host binaries for 4 platforms (darwin/arm64, darwin/amd64, linux/amd64, linux/arm64)
2. Package platform-specific tarballs (each includes host binary + gateway binaries + plugins + presets + templates)
3. Publish 4 tarball assets per release

- [ ] **Step 1: Rewrite core-release.yml**

```yaml
name: Core Release

on:
  push:
    tags:
      - "core-v*"

permissions:
  contents: write

jobs:
  release:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        include:
          - goos: darwin
            goarch: arm64
          - goos: darwin
            goarch: amd64
          - goos: linux
            goarch: amd64
          - goos: linux
            goarch: arm64
    steps:
      - uses: actions/checkout@v6

      - uses: actions/setup-go@v5
        with:
          go-version: '1.26'

      - name: Extract version from tag
        id: version
        run: echo "version=${GITHUB_REF_NAME#core-}" >> "$GITHUB_OUTPUT"

      - name: Build host binary
        run: |
          GOOS=${{ matrix.goos }} GOARCH=${{ matrix.goarch }} CGO_ENABLED=0 \
            go build -ldflags "-X main.version=${{ steps.version.outputs.version }}" \
            -o agent-sandbox-core \
            ./cmd/agent-sandbox-core/

      - name: Build gateway binaries
        run: |
          mkdir -p gateway-bin
          GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o gateway-bin/gateway-linux-amd64 ./core/gateway/cmd/gateway/
          GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o gateway-bin/gateway-linux-arm64 ./core/gateway/cmd/gateway/

      - name: Smoke test gateway
        if: matrix.goos == 'linux' && matrix.goarch == 'amd64'
        run: |
          mkdir -p /tmp/smoke-test/plugins/github-pat/src
          cp core/plugins/github-pat/src/github-auth.ts /tmp/smoke-test/plugins/github-pat/src/

          cat > /tmp/smoke-test/config.yaml << 'EOF'
          listen: ":8443"
          dns_listen: ":15353"
          mitm_domains: []
          EOF

          cat > /tmp/smoke-test/plugins.yaml << 'EOF'
          plugins:
            - name: github-pat
              dir: /tmp/smoke-test/plugins/github-pat
              options:
                token: "test-token"
              gateway:
                middlewares:
                  - script: "./src/github-auth.ts"
                    domains: ["api.github.com"]
          EOF

          GATEWAY_CONFIG=/tmp/smoke-test/config.yaml \
          GATEWAY_PLUGINS_CONFIG=/tmp/smoke-test/plugins.yaml \
          ./gateway-bin/gateway-linux-amd64 &
          GW_PID=$!

          for i in $(seq 1 10); do
            if wget -q -O /dev/null http://localhost:8080/health 2>/dev/null; then
              echo "Gateway healthy"
              kill $GW_PID
              exit 0
            fi
            sleep 0.5
          done
          kill $GW_PID 2>/dev/null
          exit 1

      - name: Package platform tarball
        run: |
          VERSION="${{ steps.version.outputs.version }}"
          PLATFORM="${{ matrix.goos }}-${{ matrix.goarch }}"
          STAGING="$(mktemp -d)"

          # Host binary
          cp agent-sandbox-core "$STAGING/"

          # Templates
          cp -r internal/generate/templates "$STAGING/templates"
          find "$STAGING/templates" -name '*.go' -delete

          # Plugins
          cp -r core/plugins "$STAGING/plugins"

          # Presets
          cp -r core/presets "$STAGING/presets"

          # Gateway binaries
          mkdir -p "$STAGING/gateway/bin"
          cp gateway-bin/gateway-linux-amd64 "$STAGING/gateway/bin/"
          cp gateway-bin/gateway-linux-arm64 "$STAGING/gateway/bin/"

          # SDK
          cp -r core/sdk "$STAGING/sdk"

          # Create tarball
          ASSET="agent-sandbox-core-${VERSION}-${PLATFORM}.tar.gz"
          tar -czf "$ASSET" -C "$STAGING" .
          echo "asset=$ASSET" >> "$GITHUB_ENV"

      - name: Upload artifact
        uses: actions/upload-artifact@v4
        with:
          name: tarball-${{ matrix.goos }}-${{ matrix.goarch }}
          path: ${{ env.asset }}

  publish:
    needs: release
    runs-on: ubuntu-latest
    steps:
      - name: Download all artifacts
        uses: actions/download-artifact@v4
        with:
          path: dist/

      - name: Create GitHub Release
        uses: softprops/action-gh-release@v2
        with:
          name: "Core ${{ needs.release.outputs.version }}"
          body: |
            Core release for agent-sandbox.
            Contains host binary, gateway binaries, plugins, presets, templates, and SDK.
          files: dist/**/*.tar.gz
```

Note: This is the structure — exact version output passing between jobs may need `outputs` wiring.

- [ ] **Step 2: Verify workflow syntax**

Run: `actionlint .github/workflows/core-release.yml` (if available) or review manually.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/core-release.yml
git commit -m "feat: build platform-specific core tarballs with host binary"
```

---

### Task 5: Write the Migration Upgrade Command

**Files:**
- Modify: `cmd/agent-sandbox/main.go` (the old CLI, for v1.27.0 final release)

- [ ] **Step 1: Rewrite `upgradeCmd` as migration command**

Replace the current `upgradeCmd` in `cmd/agent-sandbox/main.go`:

```go
func upgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade",
		Short: "Migrate to the new shim-based CLI",
		RunE: func(cmd *cobra.Command, args []string) error {
			sandboxHome := os.Getenv("AGENT_SANDBOX_HOME")
			if sandboxHome == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("finding home directory: %w", err)
				}
				sandboxHome = filepath.Join(home, ".agent-sandbox")
			}

			binDir := filepath.Join(sandboxHome, "bin")
			shimPath := filepath.Join(binDir, "agent-sandbox")

			// Check if already migrated
			if _, err := os.Stat(shimPath); err == nil {
				fmt.Println("Already migrated to shim-based CLI.")
				fmt.Printf("Shim located at: %s\n", shimPath)
				return nil
			}

			fmt.Println("Migrating to shim-based agent-sandbox...")
			fmt.Println()

			// Download and install shim
			installURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/main/scripts/install.sh", upgradeRepo)
			fmt.Printf("Downloading installer from %s\n", installURL)

			installCmd := exec.Command("sh", "-c", fmt.Sprintf("curl -fsSL '%s' | sh", installURL))
			installCmd.Stdout = os.Stdout
			installCmd.Stderr = os.Stderr
			if err := installCmd.Run(); err != nil {
				return fmt.Errorf("installation failed: %w", err)
			}

			fmt.Println()
			fmt.Println("Migration complete.")
			fmt.Printf("Add %s to your PATH (before your current binary location).\n", binDir)
			fmt.Println("Then remove the old agent-sandbox binary:")

			execPath, _ := os.Executable()
			if execPath != "" {
				execPath, _ = filepath.EvalSymlinks(execPath)
				fmt.Printf("  rm %s\n", execPath)
			}

			return nil
		},
	}
}
```

- [ ] **Step 2: Run tests**

Run: `go build ./cmd/agent-sandbox/ && go test ./...`
Expected: Builds and tests pass

- [ ] **Step 3: Commit**

```bash
git add cmd/agent-sandbox/main.go
git commit -m "feat: upgrade command migrates to shim-based CLI (v1.27.0)"
```

---

### Task 6: Update Cache Location in `internal/release/`

**Files:**
- Modify: `internal/release/fetcher.go`

The shim manages downloads now, but the core binary still uses `internal/release` during development (with `--core` flag this is bypassed). For consistency, align the cache base to `~/.agent-sandbox/core/`.

- [ ] **Step 1: Update `cacheBase()` function**

Change the `cacheBase()` function to use `~/.agent-sandbox/core/` (matching the shim's cache location):

```go
func cacheBase() string {
	if override := os.Getenv("AGENT_SANDBOX_CACHE"); override != "" {
		return filepath.Join(override, "core")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".agent-sandbox", "core")
}
```

- [ ] **Step 2: Run tests**

Run: `go test ./internal/release/...`
Expected: Pass

- [ ] **Step 3: Commit**

```bash
git add internal/release/fetcher.go
git commit -m "refactor: align cache location to ~/.agent-sandbox/core/"
```

---

### Task 7: Update CI Workflow

**Files:**
- Modify: `.github/workflows/ci.yml`

CI should now:
1. Build `agent-sandbox-core` (not `agent-sandbox`)
2. Test the shim → core flow (optional, can be integration test)
3. Keep `--core=./core` path for fast gateway builds during CI

- [ ] **Step 1: Update build step in CI**

Change the Go build job from:
```yaml
go build -o agent-sandbox ./cmd/agent-sandbox/
```
to:
```yaml
go build -o agent-sandbox-core ./cmd/agent-sandbox-core/
```

Keep the `--core=./core` local override for examples/sandbox jobs (no change needed there — `agent-sandbox-core generate --core=./core` works the same way).

- [ ] **Step 2: Add shim shellcheck step**

```yaml
- name: Lint shim
  run: shellcheck scripts/shim.sh scripts/install.sh
```

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: build agent-sandbox-core, add shellcheck for shim"
```

---

### Task 8: Add Shim Release Workflow

**Files:**
- Create: `.github/workflows/shim-release.yml`

The shim itself is released as a raw script. When `scripts/shim.sh` changes on `main`, publish it as a release asset (or just serve from `main` branch directly via raw GitHub URL).

- [ ] **Step 1: Write the workflow**

Since the shim is served directly from `main` via raw URL (`https://raw.githubusercontent.com/.../main/scripts/shim.sh`), we don't strictly need a release workflow. The install script and `upgrade` command both fetch from `main`.

However, for versioned shim releases (so old shims can detect a newer shim is available):

```yaml
name: Shim Release

on:
  push:
    branches: [main]
    paths: ['scripts/shim.sh']

permissions:
  contents: write

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6

      - name: Extract shim version
        id: version
        run: |
          VER=$(grep '^SHIM_VERSION=' scripts/shim.sh | cut -d'"' -f2)
          echo "version=$VER" >> "$GITHUB_OUTPUT"

      - name: Create/Update shim release
        uses: softprops/action-gh-release@v2
        with:
          tag_name: "shim-v${{ steps.version.outputs.version }}"
          name: "Shim v${{ steps.version.outputs.version }}"
          body: "Updated agent-sandbox shim script."
          files: scripts/shim.sh
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/shim-release.yml
git commit -m "ci: add shim release workflow"
```

---

### Task 9: Update Documentation

**Files:**
- Modify: `docs/getting-started.md`
- Modify: `docs/reference/cli.md`
- Modify: `docs/configuration.md`
- Modify: `docs/internals/build-pipeline.md`
- Modify: `docs/troubleshooting.md`
- Modify: `AGENTS.md`
- Modify: `README.md`
- Create: `docs/internals/cli-core-split.md`
- Create: `docs/reference/migration.md`

- [ ] **Step 1: Update `docs/getting-started.md`**

Replace binary download instructions with:
```markdown
## Installation

```bash
curl -fsSL https://raw.githubusercontent.com/donbader/agent-sandbox/main/scripts/install.sh | sh
```

Add `~/.agent-sandbox/bin` to your PATH, then:

```bash
agent-sandbox init
agent-sandbox generate
agent-sandbox compose up --build -d
```
```

- [ ] **Step 2: Update `docs/reference/cli.md`**

Document the two layers:
- Shim commands: `upgrade`, `version`
- Core commands: `init`, `generate`, `compose`, `gateway-url`, `audit`
- Version resolution behavior (agent.yaml → core_version field)

- [ ] **Step 3: Update `docs/configuration.md`**

Add documentation for `core_version` field:
```markdown
## core_version

Required. Specifies which core release to use. The shim downloads and caches this version automatically.

```yaml
core_version: v0.13.0  # pin to specific version
core_version: latest    # always use newest (not recommended for teams)
```
```

- [ ] **Step 4: Write `docs/internals/cli-core-split.md`**

Architecture explanation covering:
- Why the split (plugin updates without CLI upgrades, per-project versioning)
- How version resolution works
- Filesystem layout (`~/.agent-sandbox/`)
- How releases relate (core releases vs shim releases)

- [ ] **Step 5: Write `docs/reference/migration.md`**

Step-by-step migration guide:
1. Run `agent-sandbox upgrade` (existing users)
2. Update PATH
3. Remove old binary
4. Add `core_version` to existing `agent.yaml` files

- [ ] **Step 6: Update `docs/internals/build-pipeline.md`**

Document the new release process:
- Core tarball: platform-specific, includes host binary + gateway + assets
- Shim: raw script from `main`, optionally tagged releases
- No more GoReleaser CLI releases

- [ ] **Step 7: Update `docs/troubleshooting.md`**

Add shim-specific troubleshooting:
- "command not found" → PATH not set
- "Failed to download core" → network issues, check GitHub status
- "No core release found" → repo may be private, check GITHUB_TOKEN
- Version mismatch → check `agent.yaml` `core_version` field
- Cache corruption → `rm -rf ~/.agent-sandbox/core/<version>` and retry

- [ ] **Step 8: Update `AGENTS.md`**

Update project structure section, commands section, and build instructions to reflect `cmd/agent-sandbox-core/` and the shim.

- [ ] **Step 9: Update `README.md`**

Update install and quickstart sections.

- [ ] **Step 10: Commit all docs**

```bash
git add docs/ AGENTS.md README.md
git commit -m "docs: update all documentation for CLI/core split"
```

---

### Task 10: Clean Up Old CLI Release

**Files:**
- Remove: `.github/workflows/release.yml` (after v1.27.0 is shipped)
- Remove: `.goreleaser.yml` (if exists)
- Remove: `cmd/agent-sandbox/` (after migration period)

- [ ] **Step 1: Verify v1.27.0 is released and working**

Manually verify that `agent-sandbox upgrade` on the old binary correctly installs the shim.

- [ ] **Step 2: Remove old release workflow**

```bash
git rm .github/workflows/release.yml
git rm -f .goreleaser.yml
git commit -m "chore: remove old GoReleaser CLI release workflow"
```

- [ ] **Step 3: Remove old CLI entrypoint (after migration period)**

```bash
git rm -r cmd/agent-sandbox/
git commit -m "chore: remove legacy CLI entrypoint (superseded by shim + core)"
```

---

## Execution Order

Tasks 1-3 can be developed in parallel (shim, install script, core binary).
Task 4 (release workflow) depends on Task 3.
Task 5 (migration) depends on Tasks 1-2.
Task 6 (cache location) is independent.
Task 7 (CI) depends on Tasks 1, 3.
Task 8 (shim release) depends on Task 1.
Task 9 (docs) depends on all prior tasks being stable.
Task 10 (cleanup) happens after v1.27.0 is released and migration period passes.

```
Tasks 1, 2, 3, 6  (parallel)
    ↓
Tasks 4, 5, 7, 8  (parallel, after deps)
    ↓
Task 9  (docs)
    ↓
Task 10  (cleanup, manual gate)
```
