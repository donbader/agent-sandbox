# ssh

SSH server inside the agent container for remote development access (IDE, debugging, file transfer).

## How It Works

Installs OpenSSH server at build time. On container startup, sshd launches in the background before the agent process starts. Only public key authentication is allowed — no passwords.

A new host key is generated at build time. If you need a persistent host key (to avoid fingerprint warnings across rebuilds), mount one via `runtime.volumes`.

## Usage

```yaml
# agent.yaml
installations:
  - plugin: "@builtin/ssh"
    options:
      port: 2222
      authorized_keys: "@fleet/ssh_key.pub"
```

Then connect:

```bash
ssh -p 2222 agent@localhost
```

## Options

| Option | Type | Required | Default | Description |
|--------|------|----------|---------|-------------|
| `port` | integer | no | `2222` | SSH port to expose on the host |
| `authorized_keys` | project-path | yes | — | Path to public key file. Must use `@fleet/` prefix (e.g. `@fleet/ssh_key.pub`). |

The `authorized_keys` file must be your **client** public key (e.g. `~/.ssh/id_ed25519.pub`) — the key of whoever connects. It is not the server host key.

## What It Contributes

- **Runtime (build):** Installs openssh-server, configures sshd (key-only auth, custom port), installs the injected key to `/etc/ssh/authorized_keys.d/agent`
- **Runtime (pre_entrypoint):** Starts sshd daemon before agent CMD
- **Gateway (ingress):** Publishes the SSH port on the gateway and forwards it to the agent

## Authorized keys and the home volume

sshd is configured with `AuthorizedKeysFile .ssh/authorized_keys /etc/ssh/authorized_keys.d/%u`, so it accepts **either**:

1. The key from the `authorized_keys` option, installed at build time to `/etc/ssh/authorized_keys.d/agent` (outside `/home/agent`, so it survives a home-volume mount).
2. A key you seed yourself at `/home/agent/.ssh/authorized_keys`.

This matters when combined with `@builtin/home-override` using `volume: true`: that plugin mounts a persistent volume over `/home/agent`, which would mask anything copied into `/home/agent/.ssh` at build time. Installing the injected key under `/etc/ssh` avoids that masking entirely. To use option 2 with a home volume, place the key in your fleet `home/.ssh/authorized_keys` so it seeds the volume.

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
