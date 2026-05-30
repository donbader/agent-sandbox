# ADR-001: Use Docker + Own Proxy Instead of OpenShell

## Status
Accepted

## Context
[NVIDIA OpenShell](https://github.com/NVIDIA/OpenShell) provides agent sandboxing with Landlock, seccomp, network namespaces, and an L7 egress proxy with credential injection. It's the most complete open-source agent sandbox available.

We evaluated OpenShell as our sandbox layer and found it covers most of our needs (credential injection, default-deny egress, OAuth token refresh). However, one critical limitation blocks adoption:

**OpenShell does not support allow-all traffic.** Its network policy validator explicitly rejects `*` and `**` as standalone host patterns. There is no permissive mode or way to disable network policy.

For coding agents in development, this is a dealbreaker. Agents need to:
- Download packages from arbitrary registries (npm, pip, go, cargo)
- Read documentation from any website
- Access random APIs during development
- Browse the internet for answers

Requiring users to pre-list every host the agent might need is impractical and would constantly break the agent's workflow.

## Decision
Use Docker containers with our own Go transparent proxy instead of OpenShell.

## Consequences

**Positive:**
- Support `host: ["*"]` catch-all rule (essential for dev agents)
- Full control over proxy logic and egress rules
- Simpler deployment (Docker Compose only, no OpenShell installation)
- Docker API Proxy integrates naturally (just another container)

**Negative:**
- No Landlock filesystem isolation (Docker provides basic container isolation only)
- No seccomp beyond Docker's default profile
- Must build and maintain our own proxy (credential injection, OAuth refresh, TLS MITM)
- Less battle-tested security than OpenShell's multi-layer approach

**Mitigations:**
- Docker's default seccomp profile blocks most dangerous syscalls
- Container network isolation (internal network) prevents direct internet access
- iptables transparent proxy is kernel-enforced (agent cannot bypass)
- Can add custom seccomp profiles later via Docker's `--security-opt`
