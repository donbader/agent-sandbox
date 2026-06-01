# Local Coding Example

A sandboxed codex agent for local machine coding, with LLM API access routed through the gateway's credential injection.

## What's Included

- **static-header** (instance: stx-llm-gateway) — gateway intercepts requests to `agent-gateway.stx-ai.net` and injects your real API key via MITM. The agent only sees a dummy bearer token.
- **custom-runtime** — overlays codex configuration (model catalog, provider settings) into the agent's home directory.

## Setup

```bash
cd examples/local-coding

# Generate build artifacts
agent-sandbox generate

# Create .env from the example
cp .env.example .env
# Edit .env and fill in:
#   STX_LLM_GATEWAY_API_KEY=your-api-key

# Build and run
agent-sandbox compose up --build
```

## Architecture

```
LLM API (agent-gateway.stx-ai.net)
     ↕ (real API key injected by gateway)
  Gateway (MITM for agent-gateway.stx-ai.net)
     ↕
  Agent (codex with dummy bearer token)
```

The real API key never enters the agent's environment — it's only available to the gateway process.
