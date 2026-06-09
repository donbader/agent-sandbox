# Troubleshooting

## Common Issues

### `agent-sandbox generate` fails with "runtime not found"

The CLI can't find a runtime plugin matching your `runtime:` field.

**Fix:** Check spelling in `agent.yaml`. Available built-in runtimes: `codex`, `claude-code`, `pi`. For custom runtimes, use the inline definition in agent.yaml.

### Container won't start: "gateway host not found"

The agent container can't resolve the gateway container hostname at startup.

**Fix:** Make sure both services are on the same Docker network. Run `agent-sandbox generate` to regenerate the compose file, then `agent-sandbox compose up --build`.

### Agent can't reach the internet (connection timeouts)

All traffic routes through the gateway container. If the gateway isn't running or is unhealthy, outbound connections fail.

**Checks:**
```bash
# Verify both containers are running
agent-sandbox compose ps

# Check gateway logs
agent-sandbox compose logs gateway

# Verify gateway is healthy from inside agent
agent-sandbox compose exec agent ping -c1 gateway
```

### MITM certificate errors (TLS handshake failure)

The sandbox CA certificate isn't trusted inside the agent container. This affects MITM'd hosts only (e.g., github.com when using the github-pat plugin).

**Checks:**
```bash
# Verify CA cert is installed
agent-sandbox compose exec agent ls /usr/local/share/ca-certificates/sandbox-ca.crt

# Verify it's in the trust store
agent-sandbox compose exec agent update-ca-certificates --fresh
```

If using a custom base image, make sure `ca-certificates` is installed and `update-ca-certificates` runs after the sandbox CA is copied.

### Telegram bot not responding

**Checks:**
1. Verify `TELEGRAM_BOT_TOKEN` is set in your `.env` file
2. Check channel manager logs: `agent-sandbox compose logs agent | grep -i telegram`
3. Verify `allowed_users` in your config matches your Telegram username
4. Make sure you've started a conversation with the bot first (send `/start`)

### Environment variables not injected

The CLI scans for `${VAR}` patterns and generates `.env.example`. Actual values must be in a `.env` file next to your `agent.yaml`.

**Fix:**
```bash
cp .env.example .env
# Edit .env with real values
```

### Container rebuilds are slow

Docker layer caching helps. The gateway stage rarely changes, so only the agent stage rebuilds on config changes.

**Tips:**
- Don't modify `agent.yaml` fields that affect early Dockerfile layers (runtime, base packages) frequently
- Use `runtime_volumes` for large persistent data instead of home-override (avoids COPY on every rebuild)

### "Permission denied" on entrypoint hooks

Hook scripts must be executable.

**Fix:**
```bash
chmod +x ./scripts/my-hook.sh
```

### Agent exits immediately (no channel configured)

Without a channel plugin (e.g., telegram), the default CMD is `sleep infinity`. If your agent exits immediately, check:

1. Your runtime's `cmd` field in runtime.yaml
2. Whether you have a channel configured but the channel manager is crashing (check logs)

## Shim Issues

### "command not found" after install

Your PATH doesn't include `~/.agent-sandbox/bin`.

**Fix:**
```bash
export PATH="$HOME/.agent-sandbox/bin:$PATH"
```

Add this to your shell profile (`~/.bashrc`, `~/.zshrc`, etc.) to persist it. Or re-run the install script which will suggest the correct line.

### "Failed to download core"

The shim can't reach GitHub to download the core binary.

**Checks:**
- Verify network connectivity: `curl -I https://github.com`
- Check [GitHub Status](https://www.githubstatus.com/)
- If behind a corporate proxy, ensure `HTTPS_PROXY` is set

### "No core release found"

GitHub API returned no matching release for the requested version.

**Checks:**
- GitHub API rate limit may be exhausted — wait or set `GITHUB_TOKEN` in your environment
- Try again in a few minutes
- Verify the version exists: `gh release list -R donbader/agent-sandbox | grep core-v`

### "Invalid core_version"

The `core_version` field in `agent.yaml` isn't a valid value.

**Fix:** Set `core_version` to either `latest` or a valid semver tag (e.g. `v0.13.0`):

```yaml
core_version: v0.13.0
```

### Cache corruption

If a core download was interrupted or files are corrupted:

```bash
rm -rf ~/.agent-sandbox/core/<version>
agent-sandbox generate   # re-downloads automatically
```

## Getting Help

If your issue isn't listed here:

1. Check logs: `agent-sandbox compose logs`
2. Inspect generated artifacts: `ls .build/`
3. Verify config: review `agent.yaml` against [Configuration docs](configuration.md)
4. Open an issue: https://github.com/donbader/agent-sandbox/issues
