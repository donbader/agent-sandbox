# Migration Plan: agent-fleet → agent-sandbox

## Strategy

New repo (`donbader/agent-sandbox`). agent-fleet stays in maintenance mode (security fixes only). No in-place migration — clean break.

**Principle:** Every phase produces a working `agent-sandbox generate && agent-sandbox compose up --build`. Each phase adds capabilities, never breaks what's already working.

## Phases

### Phase 0: Repo Setup

**What works after this phase:** Repo exists, agent can work on it.

**Scope:**
- Create `donbader/agent-sandbox` repo
- Go workspace (`go.work`)
- AGENTS.md (instructions for coding agents)
- README.md (project overview, phase roadmap)
- Makefile (build, test, lint targets)
- .gitignore
- .golangci.yml
- SDK module stub (`sdk/go.mod`, `sdk/plugin.go` with interfaces)
- docs/ (copy design docs from agent-fleet PR #27)

**Exit criteria:** Repo cloned, `go work sync` succeeds, agent has AGENTS.md to follow.

---

### Phase 1: Bare Container

**What works after this phase:**
```bash
agent-sandbox generate && agent-sandbox compose up --build
# → codex agent running in a container (direct entrypoint, no proxy, no bridge)
```

**Scope:**
- `codex` RuntimePlugin (sets base image, installs codex CLI)
- `generate` command (reads agent.yaml → writes .build/)
- `compose` passthrough command
- Dockerfile generation (single stage, no gateway)
- docker-compose.yml generation
- .env.example generation (scan ${VAR} patterns)

**Config:**
```yaml
name: coder
runtime: codex
```

**What's missing:** No gateway (unrestricted network). No bridge (codex runs directly). No channels. No customization.

---

### Phase 2: home-version-control Feature

**What works after this phase:**
```bash
agent-sandbox generate && agent-sandbox compose up --build
# → codex agent with custom packages, startup hooks, persistent home
```

**Scope:**
- `home-version-control` FeaturePlugin
- ImageContribution.Commands wiring (RUN in Dockerfile)
- EntrypointContribution.Hooks wiring (scripts in entrypoint)
- ComposeContribution.Volumes wiring (named volumes)
- Home override directory (./home/ → /opt/home-override/ → cp on start)
- Entrypoint script (runs hooks → starts agent)

**Port from agent-fleet:** `runtimes/codex/entrypoint.sh` (override logic)

**Config:**
```yaml
name: coder
runtime: codex
features:
  home-version-control:
    commands:
      - "apt-get install -y ripgrep fd-find"
    entrypoint_hooks:
      - ./scripts/sync-dotfiles.sh
    runtime_volumes:
      - "agent-home:/home/agent"
```

**What's missing:** No network enforcement. No credential injection. No channels.

---

### Phase 3: Gateway (Network Enforcement)

**What works after this phase:**
```bash
agent-sandbox generate && agent-sandbox compose up --build
# → codex agent with transparent proxy (all traffic passthrough, iptables enforced)
```

**Scope:**
- Gateway binary (TCP proxy, SNI extraction, passthrough mode)
- Multi-stage Dockerfile (compile gateway + runtime)
- Entrypoint: iptables setup → gateway start → hooks → agent start
- DNS resolver (redirects UDP DNS to gateway)
- go:embed gateway source in CLI
- Gateway runs as `gateway` user (agent cannot kill it)

**Port from agent-fleet:** `pkg/gateway/` (proxy.go, sni.go)

**Config:** Same as Phase 2 (no config change — gateway is always-on infrastructure).

**What's missing:** No MITM, no credential injection. All traffic passes through unchanged.

---

### Phase 4: Bridge + Telegram

**What works after this phase:**
```bash
agent-sandbox generate && agent-sandbox compose up --build
# → codex agent reachable via Telegram (send message → agent responds)
```

**Scope:**
- Bridge runtime (TypeScript: spawn agent, plugin loader)
- `telegram` FeaturePlugin (contributes gateway rules + bridge TypeScript)
- BridgeContribution wiring (extract TypeScript to .build/, bridge-config.json)
- Entrypoint: gateway → bridge → agent (process tree)
- Telegram bot token injection via gateway (URL rewrite, MITM on api.telegram.org)
- MITM logic in gateway (TLS termination, HTTP interception)
- Sandbox CA generation

**Port from agent-fleet:** `pkg/gateway/mitm.go`, `runtimes/channels-bridge/src/`

**Config:**
```yaml
name: coder
runtime: codex
features:
  home-version-control:
    commands: ["apt-get install -y ripgrep"]
  telegram:
    bot_token: "${TELEGRAM_BOT_TOKEN}"
    allowed_users: ["donbader"]
```

**What's missing:** No GitHub credential injection, no Docker access, no multi-agent.

---

### Phase 5: All Remaining Features

**What works after this phase:**
```bash
agent-sandbox generate && agent-sandbox compose up --build
# → Full-featured agent: GitHub PAT, Docker, mcp-oauth, static-header
```

**Scope:**
- `github` FeaturePlugin (PAT injection via gateway MITM)
- `docker` FeaturePlugin (DinD sidecar, DockerHandler, spawned container egress)
- `mcp-oauth` FeaturePlugin (OAuth2 dynamic client registration)
- `static-header` FeaturePlugin (generic header injection)
- Additional RuntimePlugins: `claude-code`, `pi`
- Security hardening (cap_drop, no-new-privileges, hidepid, file permissions)

**Port from agent-fleet:** `pkg/gateway/mitm.go` (PAT injection logic)

**Config:**
```yaml
name: coder
runtime: codex
features:
  github:
    token: "${GITHUB_PAT}"
  docker: true
  telegram:
    bot_token: "${TELEGRAM_BOT_TOKEN}"
    allowed_users: ["donbader"]
  home-version-control:
    commands: ["apt-get install -y ripgrep fd-find"]
    runtime_volumes: ["agent-home:/home/agent"]
```

**What's missing:** No init wizard, no validate, no upgrade, no multi-agent.

---

### Phase 6: CLI Polish + Multi-Agent

**What works after this phase:**
```bash
agent-sandbox init                      # interactive scaffold
agent-sandbox validate                  # config check
agent-sandbox generate && agent-sandbox compose up --build
agent-sandbox upgrade                   # self-update
```

**Scope:**
- `init` command (interactive, detect gh auth, suggest features)
- `validate` command (config check + helpful errors)
- `plugins` command (list/info)
- `upgrade` command (self-update)
- fleet.yaml support (multi-agent, shared features)

**Port from agent-fleet:** `cmd/agent-fleet/cmd/init.go`, `pkg/selfupdate/`

---

### Phase 7: CI + Polish

**Scope:**
- GitHub Actions CI (lint, test, build on PR)
- install.sh one-liner
- README with quickstart
- Migration guide for agent-fleet users

**Note:** GoReleaser release pipeline was added in Phase 1 (`.github/workflows/release.yml` + `.goreleaser.yml`). Tag any commit on main to trigger a release.

---

## Code Reuse Summary

| agent-fleet source | agent-sandbox destination | Phase | Reuse % |
|-------------------|--------------------------|-------|---------|
| `pkg/gateway/` (proxy, sni) | `gateway/` | 3 | 80% |
| `pkg/gateway/mitm.go` | `gateway/mitm.go` | 4, 5 | 80% |
| `runtimes/channels-bridge/src/` | `bridge/src/` | 4 | 70% |
| `pkg/compose/` | `internal/compose/` | 1 | 50% |
| `pkg/selfupdate/` | `internal/selfupdate/` | 6 | 90% |
| `cmd/agent-fleet/cmd/` | `cmd/agent-sandbox/cmd/` | 1, 6 | 40% |
| `runtimes/codex/` | `plugins/codex/` | 1 | 30% |
| `runtimes/codex/entrypoint.sh` | `plugins/home-vc/` | 2 | 50% |
| `pkg/config/` | `internal/config/` | 1 | 20% |

## What Gets Dropped

- `runtimes/*/render.sh` — replaced by plugin Contribute()
- `pkg/provider/resolver.go` — no remote providers, all compiled in
- `images/gateway/` — gateway source embedded in CLI
- `agent-fleet tools ctx` — no render scripts to support
- Template injection / user_base — replaced by home-version-control
- Default-deny egress model — replaced by allow-all + MITM where needed

## agent-fleet Disposition

- Tag final release (v0.12.0)
- Update README: "maintenance mode, see agent-sandbox for new development"
- Keep repo for reference
- No new features, security fixes only

## Estimated Effort

| Phase | Deliverable | Size | Dependencies |
|-------|-------------|------|-------------|
| 0. Repo Setup | Agent can work | 1 day | None |
| 1. Bare Container | `generate` + `compose up` | 3 days | Phase 0 |
| 2. home-vc | Packages + hooks + volumes | 2 days | Phase 1 |
| 3. Gateway | Transparent proxy | 3 days | Phase 2 |
| 4. Bridge + Telegram | Remote messaging | 3 days | Phase 3 |
| 5. All Features | GitHub, Docker, OAuth, hardening | 5 days | Phase 4 |
| 6. CLI + Multi-Agent | Full CLI, fleet.yaml | 3 days | Phase 5 |
| 7. CI + Release | Automated releases | 1 day | Phase 6 |

Total: ~3 weeks sequential.
