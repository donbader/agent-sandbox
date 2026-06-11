# Unified Project Model: Everything is a Fleet

**Date:** 2025-06-11
**Status:** Draft
**Scope:** Refactor config loading, generation, and CLI commands to eliminate fleet/single-agent branching

## Problem

The codebase has pervasive if-single-else-fleet branching:

- `generate.go` tries `config.Load()`, falls back to `config.LoadFleetAgents()`
- `gateway-url` only calls `config.Load()` — broken for fleet mode
- `audit.go` duplicates the same try-single-then-fleet pattern
- `BuildCompose` vs `BuildFleetCompose` — two codepaths doing the same thing with different params
- `.build/` layout differs (flat vs nested) creating inconsistency

Every new command or feature must handle both modes, doubling the surface area for bugs.

## Design

### Core Principle

There is no single-agent mode. Every project is a fleet. A single agent is a fleet of one. The only valid project structure is `fleet.yaml` + agent subdirectories. The standalone `agent.yaml`-at-project-root format is removed.

### New Types (`internal/config/`)

```go
// Project is the unified representation of any agent-sandbox project.
// Loaded from fleet.yaml + agent subdirectories.
type Project struct {
    Dir    string  // absolute path to project root
    Agents []Agent // always len >= 1
}

// Agent pairs a resolved config with its source directory.
type Agent struct {
    Name   string  // from config.Name
    Dir    string  // absolute path to agent's directory
    Config *Config // fully resolved (shared merged, defaults applied)
}

// AgentByName returns the agent with the given name, or an error listing available agents.
func (p *Project) AgentByName(name string) (*Agent, error)

// SingleAgent returns the only agent if len==1, or an error prompting for --agent flag.
func (p *Project) SingleAgent() (*Agent, error)

// ResolveAgent returns the targeted agent: uses explicit name if provided,
// otherwise returns the single agent or errors with available names.
func (p *Project) ResolveAgent(name string) (*Agent, error)
```

### Unified Loader

```go
// LoadProject loads fleet.yaml from dir and all referenced agent configs.
// Returns a Project with one or more agents, ready for downstream use.
func LoadProject(dir string) (*Project, error)
```

Logic:
1. Read `fleet.yaml` from `dir`
2. For each agent name listed, load `<dir>/<agent-name>/agent.yaml`
3. Merge shared installations and gateway services into each agent config
4. Return `Project` with all agents

The existing `Load()` remains as an internal helper for loading a single `agent.yaml` from a given directory (used per-agent within `LoadProject`). `LoadFleetAgents()` is replaced by `LoadProject`. The standalone `config.Load(projectRoot)` pattern used by commands is eliminated.

### Build Directory Layout

Always nested:

```
.build/
  docker-compose.yml        ← unified compose for all agents
  schema.json               ← JSON schema for agent.yaml validation
  <agent-name>/
    Dockerfile
    entrypoint.sh
    config.yaml             ← gateway config
    gateway/
      Dockerfile
      package.json
      src/
      plugins/
```

Since there is no standalone single-agent format, there is no "flat" layout to migrate from. `.build/` is always structured this way.

### Compose Generation

Collapse `BuildCompose` and `BuildFleetCompose` into a single function:

```go
// BuildProjectCompose generates docker-compose.yml for a Project.
func BuildProjectCompose(agents []ComposeAgentEntry, projectDir string) (string, error)
```

Key behavior changes from current fleet mode:
- **Gateway ports always exposed** — every agent's gateway gets `ports: ["8080"]` (docker assigns a random host port). This is required for `gateway-url` to discover the mapped port via `docker compose port`. Note: this is the gateway's HTTP listener port used for health checks and port discovery, not a security concern — the gateway only proxies requests matching configured service rules.
- **Network aliases** — each agent service gets network alias `<name>`, each gateway service gets network alias `<name>-gateway`. These are compose network aliases only (container-to-container DNS), not service names.

The existing `BuildCompose` becomes a thin wrapper or is removed entirely. `BuildFleetCompose` is replaced by `BuildProjectCompose`.

### Generator Changes (`internal/generate/v1/`)

Replace `RunWithConfig` and `RunFleet` with a single method:

```go
// RunProject executes the full generation pipeline for any project.
func (g *Generator) RunProject(project *config.Project) error
```

Implementation:
1. Create `.build/`
2. For each agent in `project.Agents`:
   - Create `.build/<agent.Name>/`
   - Call `generateAgent(agent.Config, agent.Dir, agentBuildDir)`
   - Call `writeGatewayBuild(agentBuildDir, ...)`
3. Build unified compose via `BuildProjectCompose`
4. Write `docker-compose.yml`, schema, gateway types

The old `Run()`, `RunWithConfig()`, `RunFleet()` methods are removed.

### Command Changes

#### `generate`

```go
func generateCmd(dir *string) *cobra.Command {
    // ...
    project, err := config.LoadProject(projectDir)
    if err != nil { return err }
    
    g := v1.NewGeneratorWithCore(projectDir, coreDir)
    return g.RunProject(project)
}
```

No branching.

#### `gateway-url`

Add `--agent` flag:

```go
func gatewayURLCmd(dir *string) *cobra.Command {
    var agentName string
    cmd := &cobra.Command{
        Use:   "gateway-url",
        Short: "Print the gateway's public URL (resolves dynamic port)",
        RunE: func(cmd *cobra.Command, args []string) error {
            project, err := config.LoadProject(*dir)
            if err != nil { return err }
            
            agent, err := project.ResolveAgent(agentName)
            if err != nil { return err }
            
            gatewayService := agent.Name + "-gateway"
            // ... docker compose port <gatewayService> 8080
        },
    }
    cmd.Flags().StringVar(&agentName, "agent", "", "Agent name (required for multi-agent projects)")
    return cmd
}
```

When `--agent` is empty and the project has exactly one agent, it works without the flag. When there are multiple agents, it errors with a helpful message listing available agent names.

#### `audit`

Same pattern — `LoadProject`, iterate `project.Agents`. No fleet-specific branching.

#### `compose` (passthrough)

Unchanged — it already just forwards to `docker compose -f .build/docker-compose.yml`. The compose file itself is always unified.

### `--agent` Flag Pattern

For commands that need to target a specific agent, use `project.ResolveAgent(flagValue)`:

```go
func (p *Project) ResolveAgent(name string) (*Agent, error) {
    if name != "" {
        return p.AgentByName(name)
    }
    if len(p.Agents) == 1 {
        return &p.Agents[0], nil
    }
    names := make([]string, len(p.Agents))
    for i, a := range p.Agents { names[i] = a.Name }
    return nil, fmt.Errorf("multiple agents in project, use --agent to specify one of: %s",
        strings.Join(names, ", "))
}
```

### Backward Compatibility

| Aspect | Before | After | Migration |
|--------|--------|-------|-----------|
| Project structure | `agent.yaml` at root OR `fleet.yaml` + subdirs | `fleet.yaml` + subdirs only | Move `agent.yaml` into a subdirectory, create `fleet.yaml` |
| `.build/` layout | flat (single) or nested (fleet) | always nested | Re-run `generate` |
| Gateway port exposed | single=yes, fleet=no | always yes | None |
| `config.Load()` | used by commands directly | internal helper only | Commands use `LoadProject` |
| `config.LoadFleetAgents()` | used by commands directly | removed, replaced by `LoadProject` | Commands use `LoadProject` |

### Migration for Existing Single-Agent Projects

Users with a standalone `agent.yaml` at project root must restructure:

**Before:**
```
my-project/
  agent.yaml        ← name: my-agent, runtime, gateway, etc.
  .env
```

**After:**
```
my-project/
  fleet.yaml        ← agents: [my-agent]
  my-agent/
    agent.yaml      ← same content as before
  .env
```

The `init` command will be updated to always generate the fleet structure. A migration note in the changelog is sufficient — no automated migration tool needed since it's a trivial file move.

### What Does NOT Change

- `agent.yaml` per-agent format — unchanged (just lives in a subdirectory now)
- `fleet.yaml` format — unchanged
- Plugin system — unchanged
- The `docker compose` passthrough mechanism
- `coreRoot` detection and embedded assets
- Plugin resolution, contribution merging, Dockerfile/entrypoint generation

### What Gets Updated

- `init` command — always generates fleet structure (fleet.yaml + subdirectory)
- Examples — restructured to fleet format
- Docs/README — updated references

## Testing Strategy

- **Unit tests:** Verify `LoadProject` produces correct `Project` from `fleet.yaml` with 1 agent and N agents
- **Generator tests:** Verify `RunProject` produces correct `.build/<name>/` structure for 1-agent and N-agent projects
- **Compose tests:** Verify `BuildProjectCompose` produces correct YAML including gateway port exposure and network aliases
- **Integration tests:** Verify `gateway-url --agent=<name>` resolves correctly against a running fleet
- **Existing tests:** Update to use `LoadProject` and nested `.build/<name>/` paths in assertions

## Implementation Order

1. Add `Project`, `Agent` types and `LoadProject` to `internal/config/`
2. Add `ResolveAgent` / `SingleAgent` / `AgentByName` methods
3. Add `BuildProjectCompose` (unifying the two compose builders)
4. Add `RunProject` to generator (unifying `RunWithConfig` + `RunFleet`)
5. Update `generate` command to use `LoadProject` + `RunProject`
6. Update `gateway-url` command with `--agent` flag + `ResolveAgent`
7. Update `audit` command
8. Update `init` command to always generate fleet structure
9. Migrate examples to fleet structure
10. Update tests
11. Remove dead code (`BuildCompose`, `BuildFleetCompose`, `RunWithConfig`, `RunFleet`, `Run`, `LoadFleetAgents`)
