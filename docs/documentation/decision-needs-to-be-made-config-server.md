# Config Server Decision

> Status: **RESOLVED** based on experiments (Apr 22, 2026)

## Decision

**Use `OPENCODE_CONFIG` environment variable pointing to our existing ws profile.**

This is the most reliable method based on our experiments.

---

## What We Only Support

**Model:** `minimax-coding-plan/MiniMax-M2.7`

We do NOT support other models (google/gemini, anthropic, openai, etc.) because:
- They require their own API keys
- We only have access to minimax
- The google/gemini tests showed `ProviderAuthError` failures

---

## Options Considered

| Option | Description | Status |
|--------|-------------|--------|
| **A: Env var pointing to ws profile** | `OPENCODE_CONFIG=~/.config/opencode/profiles/ws/opencode.jsonc` | ✅ **CHOSEN** |
| B: Env var pointing to simple config | `OPENCODE_CONFIG=/path/to/minimax.json` | ✅ Works but less features |
| C: Runtime PATCH | `PATCH /config` after server starts | ❌ Unreliable - doesn't persist |
| D: Per-message model | Send model in message body | ❌ API expects object, not string |

---

## Why WS Profile is the Best Choice

1. **Single source of truth** — We already maintain `~/.config/opencode/profiles/ws/`
2. **All custom agents** — `scribe`, `build`, `plan`, `explore`, `coder`, etc.
3. **Custom skills** — All our skills from `ws/skills/` are available
4. **Custom commands** — Commands from `ws/commands/` work
5. **MCP integrations** — context7, exa, gh_grep already configured
6. **Agent permissions** — Each agent has specific permissions already defined

---

## What We Learned from Experiments

### ✅ Works (Verified)
1. **`OPENCODE_CONFIG` with ws profile** — Sets up everything in one shot
2. **`agent` field in message body** — Can select any agent from ws profile
3. **`/command` endpoint** — Can invoke slash commands
4. **`POST /session/:id/prompt_async`** — Async prompt submission (204 No Content)
5. **`GET /event`** — SSE streaming for session events

### ❌ Does NOT Work
1. **`PATCH /config`** — Returns success but doesn't persist for new sessions
2. **Model in message body** — API schema expects object, not string
3. **Non-Minimax models** — Google/gemini fail with `ProviderAuthError`

---

## Implementation Plan

### 1. Config Schema

In `internal/config/config.go`, update `OpenCodeConfig`:

```go
type OpenCodeConfig struct {
    BinaryPath  string `yaml:"binary_path"`  // Path to opencode binary
    Port        int    `yaml:"port"`           // Server port (0 = auto)
    Password    string `yaml:"password"`        // Server auth password
    Agent       string `yaml:"agent"`           // Agent to use (scribe, build, etc.)
    // Note: ConfigPath removed - we use ws profile directly
}
```

**Simplification:** Since we always use the ws profile, we don't need a configurable `ConfigPath`. We hardcode the path to `~/.config/opencode/profiles/ws/opencode.jsonc`.

### 2. Server Startup

In `internal/agent/opencode.go`, inject ws profile path:

```go
func (r *OpenCodeRunner) startServer(...) {
    // ... existing code ...

    cmd := exec.Command(argv[0], argv[1:]...)
    cmd.Dir = workDir

    // Use ws profile directly
    wsProfilePath := os.ExpandEnv("$HOME/.config/opencode/profiles/ws/opencode.jsonc")
    cmd.Env = append(cmd.Env, "OPENCODE_CONFIG="+wsProfilePath)
    
    // Inherit existing environment (PATH, auth keys, etc.)
    for _, e := range os.Environ() {
        if !strings.HasPrefix(e, "OPENCODE_") {
            cmd.Env = append(cmd.Env, e)
        }
    }
}
```

### 3. Message Submission

When submitting prompts, optionally include agent:

```go
body := map[string]interface{}{
    "parts": []map[string]interface{}{
        {"type": "text", "text": prompt},
    },
}

// Set agent if specified in config (default: "build")
if r.agent != "" {
    body["agent"] = r.agent
}
```

---

## WORKFLOW.md Examples

### Default (Build Agent)
```yaml
opencode:
  binary_path: opencode serve
  port: 9090
  agent: build
```

### With Scribe Agent
```yaml
opencode:
  binary_path: opencode serve
  port: 9090
  agent: scribe
```

### With Plan Agent (Read-Only)
```yaml
opencode:
  binary_path: opencode serve
  port: 9090
  agent: plan
```

### With Explore Agent
```yaml
opencode:
  binary_path: opencode serve
  port: 9090
  agent: explore
```

---

## Available Agents (from WS Profile)

| Agent | Mode | Description |
|-------|------|-------------|
| `build` | primary | Main coding agent (default) |
| `plan` | primary | Read-only planning agent |
| `coder` | all | Coder agent |
| `explore` | subagent | File search specialist |
| `scribe` | all | Writing/documentation |
| `reviewer` | all | Code reviewer |
| `researcher` | all | Research agent |
| `librarian` | all | Context manager |
| `qa` | all | QA agent |
| `tdd` | all | TDD agent |

---

## Available Commands (from WS Profile)

From `GET /command` endpoint:
- Custom commands from `ws/commands/` directory
- `verify-done`, `code-review`, `plan-protocol`
- `find-docs`, `skill-creator`, etc.

---

## Config File Path

```bash
# WS Profile Location
~/.config/opencode/profiles/ws/opencode.jsonc
```

The ws profile contains:
- `opencode.jsonc` — Main config with agents, MCP, permissions
- `agents/` — Custom agent prompts
- `commands/` — Slash commands
- `skills/` — Skills directory
- `plugins/` — OpenCode plugins

---

## Files to Modify

1. `internal/config/config.go` — Add `Agent` field, simplify config
2. `internal/agent/opencode.go` — Inject ws profile path, pass agent in message

---

## Testing

See `docs/documentation/opencode-api-experiments.md` for full experiment log.

Quick test:
```bash
OPENCODE_CONFIG=~/.config/opencode/profiles/ws/opencode.jsonc opencode serve --port 9090
```
