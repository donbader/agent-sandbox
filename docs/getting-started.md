# Getting Started

## Installation

```bash
curl -fsSL https://raw.githubusercontent.com/donbader/agent-sandbox/main/scripts/install.sh | sh
```

Add `~/.agent-sandbox/bin` to your PATH:

```bash
export PATH="$HOME/.agent-sandbox/bin:$PATH"
```

Verify:

```bash
agent-sandbox version
```

## Create a Project

**Option A: Interactive scaffold**

```bash
mkdir my-agent && cd my-agent
agent-sandbox init
```

`init` asks for an agent name and runtime, then writes `agent.yaml` and `.env.example`.

**Option B: Manual config**

```bash
mkdir my-agent && cd my-agent
```

Create `agent.yaml`:

```yaml
# yaml-language-server: $schema=.build/schema.json
name: coder
core_version: latest

runtime:
  image: "@builtin/codex"

gateway:
  services:
    - url: https://api.openai.com
      headers:
        Authorization: Bearer ${OPENAI_API_KEY}

installations:
  - plugin: "@builtin/github-pat"
    options:
      token: "${GITHUB_PAT}"
```

Create `.env` with your secrets:

```bash
OPENAI_API_KEY=sk-xxxx
GITHUB_PAT=ghp_xxxx
```

## Generate and Run

```bash
# Generate build artifacts (.build/ directory)
agent-sandbox generate

# Build and start containers
agent-sandbox compose up --build -d

# Check logs
agent-sandbox compose logs -f
```

## Verify Security

Once containers are running, verify the sandbox contract:

```bash
agent-sandbox audit
```

This checks that secrets are isolated, iptables rules are active, the CA cert is trusted, and the agent can reach external HTTPS through the gateway.

## Use the Agent

Exec into the container as the agent user:

```bash
agent-sandbox compose exec -it --user agent coder codex
```

Replace `coder` with your agent's `name` from `agent.yaml`.

## Customize

**Add packages to the agent image:**

```yaml
runtime:
  image: "@builtin/codex"
  extra_builds:
    - "RUN apt-get update && apt-get install -y ripgrep fd-find && rm -rf /var/lib/apt/lists/*"
```

**Persist the home directory across restarts:**

```yaml
installations:
  - plugin: "@builtin/home-override"
    options:
      home_directory: "./home"
      volume: true
```

**Add SSH access for IDE debugging:**

```yaml
installations:
  - plugin: "@builtin/ssh"
    options:
      authorized_keys: "./ssh_key.pub"
```

## Next Steps

- [Configuration Reference](configuration.md) — full agent.yaml schema
- [Plugins](plugins.md) — all available plugins
- [Fleet Mode](guides/fleet-mode.md) — run multiple agents
- [Security](security.md) — how isolation works
- [Migration Guide](reference/migration.md) — upgrading from older CLI versions
