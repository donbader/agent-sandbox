# Workdir Single Source of Truth — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a top-level `workdir` field to `agent.yaml` that serves as the single source of truth for the agent's working directory, replacing all hardcoded paths.

**Architecture:** The `workdir` field is parsed from config, resolved using the existing builtin template variable system (`{{ .AGENT_HOME }}`), and propagated to Dockerfile WORKDIR, channel-manager config JSON, session-manager, and wrapper-commands.

**Tech Stack:** Go (config/generate), TypeScript (channel-manager), Go text templates (Dockerfiles)

---

### Task 1: Add `workdir` field to AgentConfig struct

**Files:**
- Modify: `internal/config/config.go:55-61`

- [ ] **Step 1: Add the Workdir field to AgentConfig**

```go
// AgentConfig represents an agent.yaml file.
type AgentConfig struct {
	Name     string         `yaml:"name" schema:"Agent name" required:"true" examples:"my-agent"`
	Runtime  string         `yaml:"runtime" schema:"Runtime plugin name" required:"true" enum:"codex,claude-code,pi"`
	LogLevel string         `yaml:"log_level" schema:"Log verbosity level" default:"info" enum:"info,debug"`
	Gateway  *bool          `yaml:"gateway" schema:"Enable transparent gateway proxy" default:"true"`
	Workdir  string         `yaml:"workdir" schema:"Working directory for the agent. Supports {{ .AGENT_HOME }} template variable." examples:"{{ .AGENT_HOME }}/workspace"`
	Features []FeatureEntry `yaml:"features" schema:"Feature plugins and their configuration"`
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./...` from `/Users/corey/Projects/agent-sandbox`
Expected: Clean build, no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "feat: add workdir field to AgentConfig"
```

---

### Task 2: Resolve `workdir` in the builtin variable system

**Files:**
- Modify: `internal/generate/builtins.go:50-66`
- Modify: `internal/generate/dockerfile.go:9-32` (DockerfileBuilder struct)

The `Generator` struct already has access to the config's workdir via `g.Config.Workdir` (we need to check how config flows to Generator). The resolution happens in `resolveFeatureBuiltins()` — extend it to also resolve workdir and store the result on the Generator.

- [ ] **Step 1: Check how config flows to Generator**

Read `internal/generate/generator.go` to find the `Generator` struct and how it's constructed. We need to know where to store the resolved workdir.

- [ ] **Step 2: Add Workdir field to Generator and resolve it**

In `internal/generate/builtins.go`, extend `resolveFeatureBuiltins()` to also resolve the workdir:

```go
// resolveFeatureBuiltins resolves built-in variables in all feature contribution string values.
func (g *Generator) resolveFeatureBuiltins() {
	for _, f := range g.Features {
		for i, cmd := range f.Commands {
			f.Commands[i] = resolveBuiltins(cmd, g.Runtime)
		}
		for i, hook := range f.EntrypointHooks {
			f.EntrypointHooks[i] = resolveBuiltins(hook, g.Runtime)
		}
		for i, vol := range f.Volumes {
			f.Volumes[i] = resolveBuiltins(vol, g.Runtime)
		}
		if f.HomeOverride != "" {
			f.HomeOverride = resolveBuiltins(f.HomeOverride, g.Runtime)
		}
	}

	// Resolve workdir — default to AGENT_HOME if not specified
	agentHome := fmt.Sprintf("/home/%s", g.Runtime.User)
	if g.Workdir == "" {
		g.Workdir = agentHome
	} else {
		g.Workdir = resolveBuiltins(g.Workdir, g.Runtime)
	}
	g.AgentHome = agentHome
}
```

Note: This requires adding `"fmt"` to the imports if not already present, and `Workdir`/`AgentHome` fields to the `Generator` struct. The `Workdir` field should be populated from `g.Config.Workdir` during Generator construction.

- [ ] **Step 3: Verify it compiles**

Run: `go build ./...` from `/Users/corey/Projects/agent-sandbox`
Expected: Clean build.

- [ ] **Step 4: Commit**

```bash
git add internal/generate/builtins.go internal/generate/generator.go
git commit -m "feat: resolve workdir using builtin variable system"
```

---

### Task 3: Propagate workdir to DockerfileBuilder and templates

**Files:**
- Modify: `internal/generate/dockerfile.go:9-32` (add `Workdir` and `AgentHome` fields)
- Modify: `internal/generate/dockerfile.go:35-68` (populate new fields in `NewDockerfileBuilder`)
- Modify: `internal/generate/templates/Dockerfile.agent.tmpl:40`
- Modify: `internal/generate/templates/Dockerfile.single.tmpl:30`

- [ ] **Step 1: Add Workdir and AgentHome to DockerfileBuilder**

```go
// DockerfileBuilder holds pre-computed data for rendering Dockerfile templates.
type DockerfileBuilder struct {
	Variant         string   // "single", "gateway", "agent"
	BaseImage       string
	User            string
	Install         []string
	FeatureCmds     []string
	HasEntrypoint   bool
	HasHomeOverride bool
	HasHooks        bool
	Cmd             []string
	VolumePaths     []string
	Workdir         string
	AgentHome       string

	// Gateway variant fields
	GatewayBuildImage string
	GatewayBinaryPath string
	HasMITM           bool

	// Agent variant fields (with gateway)
	ChannelManager bool
	CMBuildImage   string
	CMInstallCmd   string
	CMBuildCmd     string
	CMDistDir      string
}
```

- [ ] **Step 2: Populate Workdir and AgentHome in NewDockerfileBuilder**

Add these lines to `NewDockerfileBuilder` after the initial struct fields:

```go
func NewDockerfileBuilder(g *Generator, variant string) *DockerfileBuilder {
	b := &DockerfileBuilder{
		Variant:         variant,
		BaseImage:       g.Runtime.BaseImage,
		User:            g.Runtime.User,
		Install:         g.Runtime.Install,
		HasEntrypoint:   g.needsEntrypoint(),
		HasHomeOverride: g.hasHomeOverride(),
		HasHooks:        g.hasHooks(),
		Cmd:             g.Runtime.Cmd,
		VolumePaths:     g.collectVolumePaths(),
		Workdir:         g.Workdir,
		AgentHome:       g.AgentHome,
	}
	// ... rest unchanged
```

- [ ] **Step 3: Update Dockerfile.agent.tmpl**

Replace line 40:
```
WORKDIR /home/{{ .User }}
```

With:
```
{{ if ne .Workdir .AgentHome -}}
RUN mkdir -p {{ .Workdir }} && chown {{ .User }}:{{ .User }} {{ .Workdir }}
{{ end -}}
WORKDIR {{ .Workdir }}
```

- [ ] **Step 4: Update Dockerfile.single.tmpl**

Replace line 30:
```
WORKDIR /home/{{ .User }}
```

With:
```
{{ if ne .Workdir .AgentHome -}}
RUN mkdir -p {{ .Workdir }} && chown {{ .User }}:{{ .User }} {{ .Workdir }}
{{ end -}}
WORKDIR {{ .Workdir }}
```

- [ ] **Step 5: Verify it compiles and templates render**

Run: `go build ./...` from `/Users/corey/Projects/agent-sandbox`
Expected: Clean build.

- [ ] **Step 6: Commit**

```bash
git add internal/generate/dockerfile.go internal/generate/templates/Dockerfile.agent.tmpl internal/generate/templates/Dockerfile.single.tmpl
git commit -m "feat: propagate resolved workdir to Dockerfile templates"
```

---

### Task 4: Emit `cwd` in channel-manager-config.json

**Files:**
- Modify: `internal/generate/channel_manager.go:240-268`

- [ ] **Step 1: Add cwd to the config map in writeChannelConfig**

```go
// writeChannelConfig generates .build/channel-manager-config.json.
func (g *Generator) writeChannelConfig() error {
	channel := ""
	for _, f := range g.Features {
		if f.ChannelName != "" {
			channel = f.ChannelName
			break
		}
	}

	// ACP command runs directly (channel-manager already runs as agent user)
	config := map[string]any{
		"channel":     channel,
		"acp_command": g.Runtime.AcpCmd,
		"cwd":         g.Workdir,
	}

	// Pass plugin-specific config to channel-manager (generic — no plugin knowledge here)
	for _, f := range g.Features {
		for k, v := range f.ChannelConfig {
			config[k] = v
		}
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling channel-manager config: %w", err)
	}

	path := filepath.Join(g.OutDir, "channel-manager-config.json")
	return os.WriteFile(path, append(data, '\n'), 0644)
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./...` from `/Users/corey/Projects/agent-sandbox`
Expected: Clean build.

- [ ] **Step 3: Commit**

```bash
git add internal/generate/channel_manager.go
git commit -m "feat: emit cwd field in channel-manager-config.json"
```

---

### Task 5: Update channel-manager TypeScript to use config.cwd

**Files:**
- Modify: `channel-manager/src/index.ts:34`
- Modify: `channel-manager/src/wrapper-commands.ts:35`
- Modify: `internal/plugins/telegram/channel/session-manager.ts:35`

- [ ] **Step 1: Remove fallback in index.ts**

Change line 34 from:
```typescript
    cwd: config.cwd ?? "/workspace",
```

To:
```typescript
    cwd: config.cwd,
```

Add a validation check after config parsing (after line 24):

```typescript
  if (!config.cwd) {
    log.fatal("cwd is required in channel-manager config");
    process.exit(1);
  }
```

- [ ] **Step 2: Update wrapper-commands.ts to accept cwd parameter**

The wrapper-commands module needs access to the configured cwd. Update the context interface and `handleSh` function:

```typescript
export interface WrapperCommandContext {
  agentCmd: string[];
  perfHistory: number[];
  cwd: string;
}
```

Update `handleSh` to receive the cwd from context. Change the `handleWrapperCommand` function:

```typescript
export function handleWrapperCommand(text: string, ctx: WrapperCommandContext): string | null {
  const trimmed = text.trim();

  if (trimmed === "/sh") return "Usage: /sh <command>";
  if (trimmed.startsWith("/sh ")) return handleSh(trimmed.slice(4).trim(), ctx.cwd);
  if (trimmed === "/diagnose") return handleDiagnose(ctx);

  return null;
}

function handleSh(cmd: string, cwd: string): string {
  if (!cmd) return "Usage: /sh <command>";
  try {
    const output = execSync(cmd, {
      timeout: 30_000,
      maxBuffer: 1024 * 1024,
      encoding: "utf-8",
      cwd,
    });
    return output.trim().slice(0, 4000) || "(no output)";
  } catch (err: unknown) {
    const e = err as { status?: number; stdout?: string; stderr?: string };
    const output = (e.stdout || "") + (e.stderr || "");
    return `Exit ${e.status ?? "?"}:\n${output.trim().slice(0, 4000)}`;
  }
}
```

- [ ] **Step 3: Update index.ts to pass cwd to wrapper context**

In `index.ts`, update the prompt interceptor call (around line 53):

```typescript
    const wrapperResult = handleWrapperCommand(text, {
      agentCmd: config.acp_command,
      perfHistory: agent.perfHistory,
      cwd: config.cwd,
    });
```

- [ ] **Step 4: Update session-manager.ts to accept cwd from config**

The session-manager needs to receive cwd from the channel-manager config rather than hardcoding it. Update the constructor and `getOrCreate`:

```typescript
export class SessionManager {
  private sessions = new Map<number, string>();
  private agent: AcpAgent;
  private readonly maxSessions: number;
  private readonly cwd: string;

  constructor(agent: AcpAgent, cwd: string, maxSessions = DEFAULT_MAX_SESSIONS) {
    this.agent = agent;
    this.cwd = cwd;
    this.maxSessions = maxSessions;
  }

  /** Get existing session or create a new one for the given chat. */
  async getOrCreate(chatId: number): Promise<string> {
    const existing = this.sessions.get(chatId);
    if (existing) {
      // Refresh LRU position
      this.sessions.delete(chatId);
      this.sessions.set(chatId, existing);
      return existing;
    }

    const conn = this.agent.getConnection();
    if (!conn) throw new Error("Agent not connected");

    const result = await conn.newSession({ cwd: this.cwd, mcpServers: [] });
    const sessionId = result.sessionId;
    this.sessions.set(chatId, sessionId);
    this.evict();
    log.info({ chatId, sessionId: sessionId.slice(0, 8) }, "created session");
    return sessionId;
  }
  // ... rest unchanged
```

- [ ] **Step 5: Find and update the callsite that constructs SessionManager**

Search for where `new SessionManager(` is called in the telegram channel plugin. It needs to pass `config.cwd` (or equivalent from the channel config). The telegram channel.ts file likely receives the config object and constructs the SessionManager — update it to pass `cwd`.

- [ ] **Step 6: Verify TypeScript compiles**

Run: `cd channel-manager && npm run build` (or the equivalent build command)
Expected: Clean build.

- [ ] **Step 7: Commit**

```bash
git add channel-manager/src/index.ts channel-manager/src/wrapper-commands.ts internal/plugins/telegram/channel/session-manager.ts internal/plugins/telegram/channel/channel.ts
git commit -m "feat: use config.cwd as single source of truth in channel-manager"
```

---

### Task 6: Add `workdir` to JSON schema generation

**Files:**
- Modify: `internal/generate/schema.go:37-61`

- [ ] **Step 1: Add workdir property to the schema**

The schema is already auto-generated from struct tags in `buildAgentSchema()`. However, it currently only handles `name`, `runtime`, `gateway`, and `features` explicitly. Check if the `AgentConfig` struct tags are enough for auto-generation.

Looking at the code: `buildAgentSchema()` hardcodes the properties rather than reflecting on `AgentConfig`. Add `workdir`:

```go
func buildAgentSchema() map[string]any {
	featureItemSchemas := collectFeatureItemSchemas()

	schema := map[string]any{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"title":   "agent-sandbox agent.yaml",
		"type":    "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Agent name",
			},
			"runtime": map[string]any{
				"type":        "string",
				"description": "Runtime plugin name",
				"enum":        []any{"codex", "claude-code", "pi"},
			},
			"gateway": map[string]any{
				"type":        "boolean",
				"description": "Enable transparent gateway proxy",
				"default":     true,
			},
			"workdir": map[string]any{
				"type":        "string",
				"description": "Working directory for the agent. Supports {{ .AGENT_HOME }} template variable. Defaults to {{ .AGENT_HOME }}.",
				"examples":    []any{"{{ .AGENT_HOME }}/workspace", "/opt/workspace"},
			},
			"features": map[string]any{
				"type":        "array",
				"description": "Feature plugins and their configuration",
				"items": map[string]any{
					"oneOf": featureItemSchemas,
				},
			},
		},
		"required": []string{"name", "runtime"},
	}

	return schema
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./...` from `/Users/corey/Projects/agent-sandbox`
Expected: Clean build.

- [ ] **Step 3: Commit**

```bash
git add internal/generate/schema.go
git commit -m "feat: add workdir to agent.yaml JSON schema"
```

---

### Task 7: Update examples to use `{{ .AGENT_HOME }}`

**Files:**
- Modify: `examples/multi-agent/coder/agent.yaml:15`

- [ ] **Step 1: Update multi-agent coder example**

Change `runtime_volumes` from:
```yaml
    runtime_volumes:
      - "coder-home:/home/agent"
```

To:
```yaml
    runtime_volumes:
      - "coder-home:{{ .AGENT_HOME }}"
```

Also add `workdir` to at least one example to demonstrate the feature. Update `examples/telegram-vibe/agent.yaml`:

```yaml
# yaml-language-server: $schema=.build/schema.json
name: coder
runtime: codex
log_level: debug
workdir: "{{ .AGENT_HOME }}/workspace"
features:
  - plugin: telegram
    bot_token: ${TELEGRAM_BOT_TOKEN}
    access_control:
      allowed_users: ["@${TELEGRAM_USERNAME}"]
      require_mention: false

  - plugin: external-services
    services:
      - url: https://agent-gateway.stx-ai.net
        headers:
          Authorization: Bearer ${STX_LLM_GATEWAY_API_KEY}

  - plugin: mcp-oauth
    providers:
      notion:
        mcp_url: https://mcp.notion.com/mcp

  - plugin: custom-runtime
    home_override: "./home"
```

- [ ] **Step 2: Commit**

```bash
git add examples/
git commit -m "docs: update examples to use AGENT_HOME template variable"
```

---

### Task 8: Update documentation

**Files:**
- Modify: `docs/configuration.md`
- Modify: `docs/plugins.md`
- Modify: `README.md`

- [ ] **Step 1: Update docs to reference {{ .AGENT_HOME }} and workdir**

Replace hardcoded `/home/agent` references in documentation with `{{ .AGENT_HOME }}` where showing config examples, and document the new `workdir` field in `docs/configuration.md`.

- [ ] **Step 2: Commit**

```bash
git add docs/ README.md
git commit -m "docs: document workdir field and AGENT_HOME template variable"
```

---

### Task 9: End-to-end verification

- [ ] **Step 1: Run full build**

Run: `go build ./...` from `/Users/corey/Projects/agent-sandbox`
Expected: Clean build.

- [ ] **Step 2: Run tests**

Run: `go test ./...` from `/Users/corey/Projects/agent-sandbox`
Expected: All tests pass.

- [ ] **Step 3: Run generate on an example to verify output**

Run: `go run ./cmd/... generate` from `/Users/corey/Projects/agent-sandbox/examples/telegram-vibe`
Expected: Generated `Dockerfile` has `WORKDIR /home/agent/workspace` and `channel-manager-config.json` has `"cwd": "/home/agent/workspace"`.

- [ ] **Step 4: Run generate on example without workdir to verify backward compat**

Run: `go run ./cmd/... generate` from `/Users/corey/Projects/agent-sandbox/examples/local-coding`
Expected: Generated `Dockerfile` has `WORKDIR /home/agent` (default), `channel-manager-config.json` has `"cwd": "/home/agent"`.

- [ ] **Step 5: Commit any fixes**

If any issues found, fix and commit.
