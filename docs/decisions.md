# Decisions

## Key Design Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | Two plugin types (Runtime + Feature) | Runtime is singular (base image). Features are additive. Config reads naturally. |
| 2 | Runtime plugins are pure data (YAML) | No CLI upgrade needed for runtime fixes. CLI is a generic template engine. |
| 3 | Feature plugins are hybrid (YAML + code) | Metadata is data. Gateway handlers need Go. Bridge needs TypeScript. Code compiles during Docker build, not CLI build. |
| 4 | Universal gateway binary | Build once per container, configure per-agent. Handlers compiled during Docker build. |
| 5 | Bridge always entrypoint | Agent is always a child process. No WrapCmd hack. |
| 6 | Allow-all egress default | Dev agents need unrestricted installs. MITM only where needed. |
| 7 | Gateway inside each container | Self-contained. Per-agent config without routing complexity. |
| 8 | Plugin updates independent of CLI | CLI ships embedded defaults. Local `plugins/` dir overrides. No CLI upgrade for plugin fixes. |
| 9 | Home override via /opt staging | Volume hides /home/agent. Staging + entrypoint cp ensures configs win. |
| 10 | Channels are channel-manager sub-plugins | Messaging is channel-manager's concern. TypeScript in plugin's `channel/` dir. |
| 11 | All credentials through gateway | Even channel-manager gets dummy tokens. Real creds never in container env. |
| 12 | Gateway code compiles during Docker build | go:embed gateway source in CLI. Multi-stage Dockerfile compiles it. User doesn't need Go. |
| 13 | Optional fleet.yaml | Single agent first-class. Multi-agent additive. |
| 14 | UDP restricted | DNS redirected to gateway resolver. All other UDP dropped. Prevents tunneling. |
| 15 | Inline runtime definition | Users can define custom runtimes directly in agent.yaml without creating plugin files. |

## Why Data-Driven Plugins (ADR)

**Problem:** In the original compile-time design, any plugin fix (e.g., wrong CMD flag) required a CLI binary release. Users had to upgrade CLI + rebuild containers for trivial changes.

**Decision:** Split plugins into data (YAML/templates) and code (Go/TypeScript):
- Data (runtime.yaml, feature.yaml) → read by CLI at generate time → no compilation
- Code (gateway/*.go, channel/*.ts) → compiled/bundled during Docker build → not in CLI binary

**Consequences:**
- CLI binary is smaller (no plugin Go code compiled in)
- Plugin fixes don't require CLI upgrades
- Users can override any plugin locally
- Gateway handlers are still type-safe Go (compiled during Docker build)
- Slightly more complex generate logic (read YAML + resolve files vs call Go function)

## Comparison with agent-fleet

| Aspect | agent-fleet | agent-sandbox |
|--------|-------------|---------------|
| Config | fleet.yaml + agent.yaml | One agent.yaml (fleet optional) |
| Egress rules | User writes manually | Auto-derived from plugins |
| Runtime | Provider + render.sh | runtime.yaml (pure data) |
| Extensibility | Shell scripts | YAML + Go/TypeScript |
| Home customization | user_base + init_scripts + Dockerfile | `home/` dir (auto-override) |
| Packages | Write Dockerfile template | YAML list in config |
| Docker access | Egress rule provider | `docker: true` |
| Deploy | generate → compose up | generate → compose up |
| Egress default | Deny | Allow all |
| Gateway | Sidecar container | Inside agent container |
| Plugin updates | Requires CLI upgrade | Independent of CLI |

## Open Questions

1. **Plugin versioning** — How to version plugin YAML schemas?
2. **Custom egress restrictions** — Option for default-deny mode?
3. **Plugin registry** — Remote plugin sources beyond local override?
4. **Resource limits** — CPU/memory per agent? Configurable?
5. **Health checks** — Detect agent crash vs idle?
6. **Auth flow** — Codex device flow, claude login — how to handle first-time auth?

## Maintainability

### Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Gateway source embedded in CLI | ~20MB added to binary | Accept it. Single binary distribution is worth the size. Bridge TypeScript adds ~5MB. |
| Plugin YAML schema changes | Existing plugins break | Semantic versioning on schema. Additive only. |
| Multi-stage build adds time | First build ~60s | Docker layer cache. Gateway stage rarely changes. Subsequent builds ~5s. |
| Two languages (Go + TypeScript) | Higher maintenance | Clear boundary: Go = proxy/gateway. TypeScript = messaging/channel-manager. No overlap. |
| Gateway fix requires rebuild | Slow rollout | Edit gateway source locally, rebuild container. No CLI upgrade needed. |

### Upgrade Path

| Change | User action |
|--------|-------------|
| New built-in plugin | `agent-sandbox upgrade` (optional — can also add locally) |
| Plugin data fix (runtime.yaml) | Override locally OR `agent-sandbox upgrade` |
| Gateway handler fix | Override `plugins/<name>/gateway/` locally, rebuild |
| Channel plugin fix | Override `plugins/<name>/channel/` locally, rebuild |
| CLI template engine fix | `agent-sandbox upgrade` |
| Config schema change | Edit agent.yaml → re-generate |

### What NOT to Build (YAGNI)

- Plugin marketplace (until >20 community plugins exist)
- Hot reload (rebuild is fast enough)
- Web UI dashboard (CLI + Telegram is sufficient)
- Multi-host orchestration (use k8s)
- Plugin dependency resolution (keep plugins independent)
- Config migration tool (simple YAML, manual edit is fine)
- Remote plugin registry (local override is sufficient for now)
