# Migration Plan: agent-fleet → agent-sandbox

## Strategy

New repo (`donbader/agent-sandbox`). agent-fleet stays in maintenance mode (security fixes only). No in-place migration — clean break.

**Principle:** Every phase produces a working `agent-sandbox generate && agent-sandbox compose up --build`. Each phase adds capabilities, never breaks what's already working.

## Phases

### Phase 0: Repo Setup ✅

**What works after this phase:** Repo exists, agent can work on it.

- [x] Create `donbader/agent-sandbox` repo
- [x] Go workspace (`go.work`)
- [x] AGENTS.md (instructions for coding agents)
- [x] README.md (project overview, phase roadmap)
- [x] Makefile (build, test, lint targets)
- [x] .gitignore
- [x] .golangci.yml
- [x] SDK module stub (`sdk/go.mod`, `sdk/plugin.go` with interfaces)
- [x] docs/ (design docs)

---

### Phase 1: Bare Container ✅

**What works after this phase:**
```bash
agent-sandbox generate && agent-sandbox compose up --build
# → codex agent running in a container (direct entrypoint, no proxy, no bridge)
```

- [x] `codex` RuntimePlugin (sets base image, installs codex CLI)
- [x] `generate` command (reads agent.yaml → writes .build/)
- [x] `compose` passthrough command
- [x] Dockerfile generation (single stage, no gateway)
- [x] docker-compose.yml generation
- [x] .env.example generation (scan ${VAR} patterns)
- [x] Integration test (`//go:build integration` docker build test)
- [x] Testing guidelines in AGENTS.md
- [x] Reference docs (ADRs, bridge protocol, docker-api-proxy)
- [x] Phase implementation guide in AGENTS.md
- [x] GoReleaser release pipeline (`.github/workflows/release.yml`)
- [x] `examples/simple/` for quick testing
- [x] `install.sh` one-liner

**Config:**
```yaml
name: coder
runtime: codex
```

---

### Phase 2: home-version-control Feature

**What works after this phase:**
```bash
agent-sandbox generate && agent-sandbox compose up --build
# → codex agent with custom packages, startup hooks, persistent home
```

- [ ] `home-version-control` FeaturePlugin (`plugins/home-version-control/plugin.go`)
- [ ] Update `internal/generate/` to merge FeatureContributions into Dockerfile
- [ ] ImageContribution.Commands wiring (RUN in Dockerfile)
- [ ] EntrypointContribution.Hooks wiring (scripts in entrypoint)
- [ ] ComposeContribution.Volumes wiring (named volumes)
- [ ] Entrypoint script (runs hooks → starts agent)
- [ ] Home override directory (./home/ → /opt/home-override/ → cp on start)
- [ ] `examples/home-vc/` example

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

---

### Phase 3: Gateway (Network Enforcement)

**What works after this phase:**
```bash
agent-sandbox generate && agent-sandbox compose up --build
# → codex agent with transparent proxy (all traffic passthrough, iptables enforced)
```

- [ ] Gateway Go module (`gateway/`)
- [ ] TCP listener + SNI extraction
- [ ] Passthrough mode (pipe bytes to destination)
- [ ] DNS resolver (intercept UDP port 53)
- [ ] go:embed gateway source in CLI
- [ ] Multi-stage Dockerfile (compile gateway + runtime)
- [ ] Entrypoint: iptables setup → gateway start → hooks → agent start
- [ ] Gateway runs as `gateway` user (agent cannot kill it)
- [ ] Integration test (verify traffic routes through gateway)

---

### Phase 4: Bridge + Telegram

**What works after this phase:**
```bash
agent-sandbox generate && agent-sandbox compose up --build
# → codex agent reachable via Telegram (send message → agent responds)
```

- [ ] Bridge TypeScript runtime (`bridge/`)
- [ ] Agent process spawning (child process management)
- [ ] Channel plugin loader (dynamic import from /opt/bridge/plugins/)
- [ ] `telegram` FeaturePlugin (`plugins/telegram/plugin.go`)
- [ ] GatewayContribution: MITM on api.telegram.org, inject bot token
- [ ] BridgeContribution: embed TypeScript channel plugin (grammy)
- [ ] MITM logic in gateway (TLS termination, HTTP interception)
- [ ] Sandbox CA generation
- [ ] BridgeContribution wiring (extract TypeScript to .build/)
- [ ] Entrypoint: gateway → bridge → agent (process tree)
- [ ] `examples/telegram/` example

---

### Phase 5: All Remaining Features

**What works after this phase:**
```bash
agent-sandbox generate && agent-sandbox compose up --build
# → Full-featured agent: GitHub PAT, Docker, mcp-oauth, static-header
```

- [ ] `github` FeaturePlugin (PAT injection via gateway MITM)
- [ ] `docker` FeaturePlugin (DinD sidecar, DockerHandler, spawned container egress)
- [ ] `mcp-oauth` FeaturePlugin (OAuth2 dynamic client registration)
- [ ] `static-header` FeaturePlugin (generic header injection)
- [ ] `claude-code` RuntimePlugin
- [ ] `pi` RuntimePlugin
- [ ] Security hardening (cap_drop, no-new-privileges, hidepid, file permissions)
- [ ] `examples/full/` example (all features)

---

### Phase 6: CLI Polish + Multi-Agent

**What works after this phase:**
```bash
agent-sandbox init                      # interactive scaffold
agent-sandbox validate                  # config check
agent-sandbox generate && agent-sandbox compose up --build
agent-sandbox upgrade                   # self-update
```

- [ ] `init` command (interactive, detect gh auth, suggest features)
- [ ] `validate` command (config check + helpful errors)
- [ ] `plugins` command (list/info)
- [ ] `upgrade` command (self-update)
- [ ] fleet.yaml support (multi-agent, shared features)
- [ ] `examples/multi-agent/` example

---

### Phase 7: CI + Polish

- [ ] GitHub Actions CI (lint, test, build on PR)
- [ ] README with quickstart (update)
- [ ] Migration guide for agent-fleet users

**Note:** GoReleaser release pipeline and install.sh were added in Phase 1.

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

- [ ] Tag final release (v0.12.0)
- [ ] Update README: "maintenance mode, see agent-sandbox for new development"
- [ ] Keep repo for reference
- [ ] No new features, security fixes only
