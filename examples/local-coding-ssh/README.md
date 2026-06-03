# Local Coding + SSH Example

Extends the base `local-coding` example with SSH access into the agent container on port 2222.

## Prerequisites

Generate an SSH key pair for agent access:

```bash
ssh-keygen -t ed25519 -f ssh_key -N ""
```

This creates `ssh_key` (private) and `ssh_key.pub` (public). The private key stays on your machine; the public key is mounted into the container as an authorized key.

Both files are gitignored — do not commit real keys.

## Setup

```bash
cd examples/local-coding-ssh

# Generate the SSH key pair (if not already done)
ssh-keygen -t ed25519 -f ssh_key -N ""

# Generate build artifacts
agent-sandbox generate

# Create .env from the example
cp .env.example .env
# Edit .env and fill in:
#   STX_LLM_GATEWAY_API_KEY=your-api-key

# Build and run
agent-sandbox compose up --build
```

## Connecting via SSH

```bash
ssh -i ssh_key -p 2222 agent@localhost
```

## What's Included

- **external-services** — gateway intercepts HTTPS requests to `agent-gateway.stx-ai.net` via MITM and injects your real API key.
- **ssh** — starts an OpenSSH server on port 2222 inside the container, using your generated public key for authentication.
- **custom-runtime** — overlays codex configuration (model catalog, provider settings) into the agent's home directory.
