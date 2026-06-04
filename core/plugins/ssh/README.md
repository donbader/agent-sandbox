# ssh

SSH server inside the agent container for remote development access (IDE, debugging, file transfer).

## How It Works

Installs OpenSSH server at build time. On container startup, sshd launches in the background before the agent process starts. Only public key authentication is allowed — no passwords.

A new host key is generated at build time. If you need a persistent host key (to avoid fingerprint warnings across rebuilds), mount one via `runtime.volumes`.

## Usage

```yaml
# agent.yaml
installations:
  - plugin: ssh
    options:
      port: 2222
      authorized_keys: "./ssh_key.pub"
```

Then connect:

```bash
ssh -p 2222 agent@localhost
```

## Options

| Option | Type | Required | Default | Description |
|--------|------|----------|---------|-------------|
| `port` | integer | no | `2222` | SSH port to expose on the host |
| `authorized_keys` | string | yes | — | Path to public key file (relative to project root) |

## What It Contributes

- **Runtime (build):** Installs openssh-server, configures sshd (key-only auth, custom port), copies authorized_keys
- **Runtime (pre_entrypoint):** Starts sshd daemon before agent CMD
- **Ports:** Exposes the SSH port on the host

## Persistent Host Key (optional)

To avoid SSH fingerprint warnings after rebuilds:

```bash
ssh-keygen -t ed25519 -f .ssh_host_key -N '' -C ''
```

```yaml
# agent.yaml
runtime:
  volumes:
    - "./.ssh_host_key:/etc/ssh/ssh_host_ed25519_key:ro"
```

Add `.ssh_host_key` to `.gitignore`.
