# CLI & UX

## Commands

```bash
agent-sandbox init [--runtime codex]    # interactive scaffold
agent-sandbox generate                  # read config → write .build/ artifacts
agent-sandbox validate                  # check config + suggest fixes
agent-sandbox plugins                   # list available
agent-sandbox plugins info <name>       # plugin details
agent-sandbox upgrade                   # self-update
agent-sandbox compose ...               # docker compose passthrough
```

### Compose Passthrough

Auto-injects `-f .build/docker-compose.yml` and `--project-name`. All compose commands work:

```bash
agent-sandbox compose up --build        # build + start
agent-sandbox compose up --build -d     # detached
agent-sandbox compose down              # stop + remove
agent-sandbox compose logs -f coder     # stream logs
agent-sandbox compose exec coder bash   # shell into agent
agent-sandbox compose ps                # status
agent-sandbox compose restart coder     # restart container
agent-sandbox compose build coder       # rebuild image only
```

### Typical Workflow

```bash
agent-sandbox generate                  # generate artifacts
agent-sandbox compose up --build -d     # build + start detached
agent-sandbox compose logs -f           # watch logs
```

After config changes:
```bash
agent-sandbox generate                  # regenerate
agent-sandbox compose up --build -d     # rebuild + restart
```

## UX Design

### Progressive Disclosure

```yaml
# Minimal (works immediately):
name: coder
runtime: codex

# Add credentials:
features:
  - plugin: github
    token: "${GITHUB_PAT}"

# Add channels:
features:
  - plugin: telegram
    bot_token: "${BOT_TOKEN}"
    allowed_users: ["me"]

# Full power:
features:
  - plugin: github
    token: "${GITHUB_PAT}"
  - plugin: docker
  - plugin: telegram
    bot_token: "${BOT_TOKEN}"
    allowed_users: ["me"]
  - plugin: custom-runtime
    commands: ["apt-get install -y ripgrep fd-find"]
    entrypoint_hooks: [./scripts/sync-dotfiles.sh]
    runtime_volumes: ["agent-home:/home/agent"]
```

### Interactive Init

Auto-detects `gh auth token`, suggests plugins based on runtime, creates `.env` with detected credentials.

### Smart Validation

```bash
$ agent-sandbox validate
⚠ runtime 'codex' typically needs 'openai' plugin for API access.
✓ Config valid (1 warning)
```

### Helpful Errors

```bash
✗ Plugin 'github' failed: token is invalid or expired
  Fix: gh auth refresh && agent-sandbox up
```

## DX (Plugin Authors)

### Scaffold

```bash
$ agent-sandbox plugin new my-corp-api
Created plugins/my-corp-api/ (go.mod, plugin.go, plugin_test.go, README.md)
```

### Testing

```go
func TestContribute(t *testing.T) {
    p := New()
    contrib, err := p.Contribute(sdk.ContributeContext{
        AgentName: "test",
        Config:    map[string]any{"token": "ghp_test"},
    })
    require.NoError(t, err)
    assert.Equal(t, []string{"github.com", "*.github.com"}, contrib.EgressRules[0].Hosts)
}

func TestHandler(t *testing.T) {
    p := github.New()
    contrib, _ := p.Contribute(sdk.ContributeContext{
        AgentName: "test",
        Config:    map[string]any{"token": "ghp_real"},
    })
    handler, _ := contrib.Gateway.NewHandler(map[string]any{"token": "ghp_real"})
    req := httptest.NewRequest("GET", "https://api.github.com/repos", nil)
    handler.HandleRequest(req)
    assert.Equal(t, "token ghp_real", req.Header.Get("Authorization"))
}
```

### Integration Test Helper

```go
sb := sdktest.NewTestSandbox(t, github.New(), telegram.New())
defer sb.Cleanup()
resp := sb.HTTPGet("https://api.github.com/user")
assert.Equal(t, 200, resp.StatusCode)
```
