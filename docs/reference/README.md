# Reference Documents

These documents are ported from the predecessor project (agent-fleet) for historical context and implementation reference.

## Architecture Decision Records (ADRs)

| ADR | Decision |
|-----|----------|
| [001](adr/001-no-openshell.md) | No OpenShell — doesn't support allow-all traffic |
| [002](adr/002-transparent-proxy.md) | Transparent proxy via iptables — kernel-enforced, no HTTP_PROXY needed |
| [003](adr/003-go-proxy.md) | Go proxy — single binary, same language, full control |
| [004](adr/004-composable-egress-presets.md) | Composable egress — ordered evaluation, first match wins |
| [005](adr/005-all-credentials-through-proxy.md) | All credentials through proxy — unified injection model |

## Protocol & Design

| Document | Purpose |
|----------|---------|
| [channel-manager-protocol.md](channel-manager-protocol.md) | ACP protocol spec (channel manager ↔ agent stdin/stdout JSON) |
| [docker-api-proxy.md](docker-api-proxy.md) | Docker API validation in gateway (block dangerous ops) |
