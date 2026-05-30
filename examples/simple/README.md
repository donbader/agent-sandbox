# Simple Example

Minimal agent-sandbox setup — a single codex agent with no features.

## Usage

```bash
cd examples/simple

# Generate build artifacts
agent-sandbox generate

# Start the agent
agent-sandbox compose up --build -d

# Check logs
agent-sandbox compose logs -f

# Stop
agent-sandbox compose down
```

## What this does

- Builds a Docker image with codex CLI installed
- Runs as `agent` user in `/home/agent`
- No gateway (unrestricted network)
- No bridge (codex runs directly as entrypoint)
- Ephemeral (no persistent volumes)
