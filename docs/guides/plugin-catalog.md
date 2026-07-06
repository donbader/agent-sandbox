# Plugin Catalog

Plugins extend agent containers with additional capabilities — credential injection, SSH access, home directory customization, and more. They are declared in each agent's `agent.yaml` (inside the agent subdirectory) under the `installations` key:

```yaml
installations:
  - plugin: github-pat
    options:
      token: ${GITHUB_PAT}
```

Each plugin contributes build steps, gateway configuration, or runtime modifications without requiring changes to the CLI itself. Plugins are fetched from GitHub Releases and executed as TypeScript at gateway startup.

---

## github-pat

Injects a GitHub Personal Access Token into requests to GitHub APIs. The gateway intercepts outbound requests to `api.github.com` and `github.com`, adding the token as an `Authorization` header. The agent never sees the real credential — only dummy env vars are set at build time.

### Options

| Option  | Type   | Required | Description                                        |
|---------|--------|----------|----------------------------------------------------|
| `token` | string | yes      | GitHub PAT env var reference (e.g. `${GITHUB_PAT}`) |

### Example

```yaml
installations:
  - plugin: github-pat
    options:
      token: ${GITHUB_PAT}
```

### How it works

- Sets `GH_TOKEN=dummy` and `GITHUB_TOKEN=dummy` in the container so CLI tools (gh, git) attempt authenticated requests.
- The gateway middleware (`github-auth.ts`) rewrites outbound requests to `api.github.com` and `github.com`, injecting the real token via header.
- The actual PAT never enters the container environment.

---

## mcp-oauth

Provides OAuth token management for MCP (Model Context Protocol) servers. Handles the full OAuth flow — login, callback, token storage — so agents can authenticate with MCP-compatible services without manual intervention.

### Options

| Option         | Type   | Required | Description                                                    |
|----------------|--------|----------|----------------------------------------------------------------|
| `providers`    | object | yes      | Map of provider name to MCP config (each needs at least `mcp_url`) |
| `callback_url` | string | no       | Public callback URL (derived from request if not set)          |

### Example

```yaml
installations:
  - plugin: mcp-oauth
    options:
      providers:
        linear:
          mcp_url: https://mcp.linear.app/sse
        notion:
          mcp_url: https://mcp.notion.so/sse
      callback_url: https://my-host.example.com/callback
```

### How it works

- Registers gateway routes at `/login` and `/callback` to handle the OAuth dance.
- Stores tokens in a persistent Docker volume (`oauth-tokens:/data/plugins/mcp-oauth`).
- The gateway middleware intercepts requests to each configured `mcp_url` and injects the stored OAuth token.
- If `callback_url` is omitted, the plugin derives it from the inbound request.

### Notes

- Each entry in `providers` must include at minimum a `mcp_url` key.
- Token persistence survives container restarts via the named volume.

---

## ssh

Installs and configures an OpenSSH server inside the agent container, allowing remote shell access for debugging or interactive use.

### Options

| Option            | Type    | Required | Default | Description                                       |
|-------------------|---------|----------|---------|---------------------------------------------------|
| `port`            | integer | no       | 2222    | SSH port to expose                                |
| `authorized_keys` | string  | yes      | —       | Path to public key file (relative to project root) |

### Example

```yaml
installations:
  - plugin: ssh
    options:
      port: 2222
      authorized_keys: keys/id_ed25519.pub
```

### How it works

- Installs `openssh-server` in the container image.
- Generates a fresh host key (ed25519) at build time.
- Disables password authentication — only key-based auth is allowed.
- Installs the injected public key to `/etc/ssh/authorized_keys.d/agent` (outside `/home/agent`, so it survives home-volume mounts).
- Configures `AuthorizedKeysFile` to accept both the injected key and any user-seeded `~/.ssh/authorized_keys`.
- Starts `sshd` as a pre-entrypoint process before the agent launches.
- Publishes the SSH port on the gateway and forwards it to the agent.

### Caveats

- **Requires `SYS_CHROOT` capability** — the plugin adds `cap_add: [SYS_CHROOT]` to the container.
- **Disables user namespace isolation** (`skip_userns: true`). This is required for sshd to function but reduces container isolation. Be aware of the security trade-off.
- The host key is regenerated on every image build, so SSH clients will see a changed fingerprint after rebuilds.
- If you seed your own key via `~/.ssh/authorized_keys` (e.g. through a home volume), ensure the `.ssh` directory is mode 700 and the file is mode 600 — sshd's `StrictModes` will reject keys with wrong permissions.

---

## home-override

Seeds the agent's home directory with custom configuration files (dotfiles, tool configs, etc.). Supports two modes: ephemeral bind-mount or persistent named volume.

### Options

| Option           | Type    | Required | Default | Description                                                              |
|------------------|---------|----------|---------|--------------------------------------------------------------------------|
| `home_directory` | string  | yes      | —       | Path to local directory with agent home contents (relative to project root) |
| `volume`         | boolean | no       | false   | Persist home directory across restarts using a named Docker volume        |

### Example (ephemeral bind-mount)

```yaml
installations:
  - plugin: home-override
    options:
      home_directory: config/agent-home
```

### Example (persistent volume)

```yaml
installations:
  - plugin: home-override
    options:
      home_directory: config/agent-home
      volume: true
```

### How it works

**Bind-mount mode** (`volume: false`, default):
- Mounts `home_directory` directly to `/home/agent` in the container.
- Changes inside the container write back to the host directory.
- Every restart reflects the current state of the host directory.

**Volume mode** (`volume: true`):
- Creates a named Docker volume (`<agent-name>-home`) mounted at `/home/agent`.
- On every container start, files from `home_directory` are copied (re-synced) into the volume via `cp -a /opt/home-seed/. "$AGENT_HOME/"`.
- Agent-generated state (shell history, caches) persists across restarts in the volume.
- Updated config files from the host are re-applied on each start, but they won't remove files the agent created.

### Notes

- Volume mode is recommended when the agent generates state you want to keep (e.g. command history, local caches) while still being able to push config updates from the host.
- Bind-mount mode is simpler but means the agent's writes affect your host filesystem.

---

## agent-manager-acp

Runs an agent manager process that spawns and manages the coding agent via the ACP (Agent Communication Protocol) over stdio. This enables session management, message routing, and lifecycle control through the channel manager.

### Options

| Option        | Type   | Required | Default | Description                                                                              |
|---------------|--------|----------|---------|------------------------------------------------------------------------------------------|
| `acp_command` | array  | yes      | —       | Command to spawn the agent via ACP over stdio (e.g. `[codex-acp]` or `[claude, --dangerously-skip-permissions]`) |
| `acp_install` | string | no       | `true`  | Shell command to install the ACP adapter binary (e.g. `npm install -g @zed-industries/codex-acp@0.15.0`) |

### Example

```yaml
installations:
  - plugin: agent-manager-acp
    options:
      acp_command: [codex-acp]
      acp_install: "npm install -g @zed-industries/codex-acp@0.15.0"
```

### Example (Claude Code)

```yaml
installations:
  - plugin: agent-manager-acp
    options:
      acp_command: [claude, --dangerously-skip-permissions]
      acp_install: "npm install -g @anthropic-ai/claude-code"
```

### How it works

1. At build time, the `acp_install` command runs (cached via npm mount) to install the ACP adapter.
2. The agent-manager Node.js application is compiled and installed at `/opt/agent-manager/`.
3. A config file is written with the `acp_command` and working directory.
4. At runtime, the agent manager spawns the agent process using the configured command over stdio.
5. The channel manager communicates with the agent manager via the ACP protocol — sending messages, receiving responses, and managing session lifecycle.

### Notes

- The ACP protocol uses stdio (stdin/stdout) for communication between the manager and the spawned agent process. See `docs/reference/channel-manager-protocol.md` for protocol details.
- The `acp_install` default is `"true"` (a no-op) — set it explicitly if the adapter binary isn't already in the base image.
- The `acp_command` is written as a JSON array into the runtime config, so arguments with spaces are handled correctly.
