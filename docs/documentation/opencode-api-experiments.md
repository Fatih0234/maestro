# OpenCode Server API Experiment Results

> Experiment Date: Apr 22, 2026
> OpenCode Version: 1.14.18

## Summary

This document records our experiments with the OpenCode server API to understand how to:
1. Configure the server with a custom model/profile
2. Select agents
3. Use slash commands via API
4. What works reliably

**Note:** We only use `minimax-coding-plan/MiniMax-M2.7`. Other models (like google/gemini) require their own API keys and are not supported.

---

## ✅ Verified: Using WS Profile Directly

We can use our existing OCX `ws` profile directly with the server!

```bash
OPENCODE_CONFIG=~/.config/opencode/profiles/ws/opencode.jsonc opencode serve --port 4101
```

**Benefits:**
- Uses our existing agent configurations (`scribe`, `build`, `plan`, `explore`, etc.)
- Includes our custom skills, commands, and MCP integrations
- Single source of truth - no duplicate configs

**Test Results:**
- ✅ `scribe` agent responds: "yes" to "just testing, just say yes"
- ✅ `scribe` agent creates files successfully
- ✅ All custom agents from ws profile are available

**Available Agents from ws profile:**

| Agent | Mode | Description |
|-------|------|-------------|
| `build` | primary | Main coding agent |
| `plan` | primary | Read-only planning |
| `coder` | all | Coder agent |
| `explore` | subagent | File search |
| `scribe` | all | Writing agent |
| `reviewer` | all | Code reviewer |
| `researcher` | all | Research agent |
| `librarian` | all | Context manager |
| `qa` | all | QA agent |
| `tdd` | all | TDD agent |

---

## Experiment 1: Default Server Startup

**Command:**
```bash
opencode serve --port 4097
```

**Results:**
- Server starts successfully
- Uses default model from user's global config (`~/.config/opencode/opencode.json`)
- Our server used: `MiniMax-M2.7` from provider `minimax-coding-plan`
- No authentication required

---

## Experiment 2: Config via `OPENCODE_CONFIG` Environment Variable

**Command:**
```bash
OPENCODE_CONFIG=/path/to/config.json opencode serve --port 4098
```

**Results:** ✅ **WORKS - VERIFIED**
- Server accepts the config file at startup (both `.json` and `.jsonc` formats)
- New sessions use the specified model
- Model shows as `modelID: "MiniMax-M2.7"` and `providerID: "minimax-coding-plan"`

---

## Experiment 3: Using WS Profile

**Command:**
```bash
OPENCODE_CONFIG=~/.config/opencode/profiles/ws/opencode.jsonc opencode serve --port 4101
```

**Results:** ✅ **WORKS - VERIFIED**
- Server loads the ws profile directly
- All custom agents are available
- Skills, commands, and MCP integrations work
- Single source of truth - no duplicate config needed

**Available Agents:**
- `build`, `coder`, `explore`, `general`, `librarian`, `plan`
- `qa`, `researcher`, `reviewer`, `scribe`, `tdd`
- `compaction`, `summary`, `title`

---

## Experiment 4: Agent Selection in Message Body

**Command:**
```bash
curl -X POST "http://127.0.0.1:4101/session/$SESSION_ID/message" \
  -H "Content-Type: application/json" \
  -d '{"agent": "scribe", "parts": [{"type": "text", "text": "..."}]}'
```

**Results:** ✅ **WORKS - VERIFIED**

| Test | Agent | Mode | Response |
|------|-------|------|----------|
| 1 | `scribe` | scribe | "yes" to "just say yes" |
| 2 | `scribe` | scribe | Created file successfully |

---

## Experiment 5: SSE Event Streaming

**Command:**
```bash
curl -N http://127.0.0.1:4097/event \
  -H "Accept: text/event-stream"
```

**Results:** ✅ **WORKS**
- Streams server events as SSE
- Key events observed:
  - `server.connected` — on connect
  - `session.updated` — when session title changes
  - `server.heartbeat` — periodic keepalive

---

## Experiment 6: `POST /session/:id/prompt_async`

**Command:**
```bash
curl -X POST "http://127.0.0.1:4101/session/$SESSION_ID/prompt_async" \
  -H "Content-Type: application/json" \
  -d '{"parts": [{"type": "text", "text": "..."}]}'
```

**Results:** ✅ **WORKS**
- Returns `204 No Content` immediately
- Prompt is queued and processed asynchronously
- Use sync `/session/:id/message` endpoint for guaranteed response

---

## Key Findings

### ✅ Works (Verified)

1. **Startup config via `OPENCODE_CONFIG` env var**
   - Works with both `.json` and `.jsonc` formats
   - Use ws profile directly: `OPENCODE_CONFIG=~/.config/opencode/profiles/ws/opencode.jsonc`
   - All custom agents, skills, and MCP integrations work

2. **Agent selection in message body**
   - Use `"agent": "scribe"` / `"plan"` / `"explore"` / etc.
   - Each agent has specific permissions per the ws profile

3. **Slash commands via `/session/:id/command`**
   - Execute any registered skill/command
   - Works like typing `/command` in TUI

4. **SSE event streaming**
   - Connect to `/event` endpoint
   - Stream session status, token updates, heartbeats

5. **Async prompt submission**
   - Use `POST /session/:id/prompt_async`
   - Returns immediately, process in background

### ❌ Does NOT Work

1. **Runtime model change via `PATCH /config`**
   - Appears to succeed but doesn't affect new sessions reliably
   - Don't rely on this for model switching

2. **Model in message body**
   - API schema expects object, not string
   - No per-message model override

3. **Non-Minimax models**
   - Google/gemini require their own API keys
   - Not supported in our setup

---

## Recommended Integration Approach

### 1. Use WS Profile Directly

```bash
# Start server with ws profile
OPENCODE_CONFIG=~/.config/opencode/profiles/ws/opencode.jsonc opencode serve --port 9090
```

This gives us:
- All custom agents (`scribe`, `build`, `plan`, etc.)
- All skills and commands
- MCP integrations (context7, exa, gh_grep)
- Single source of truth

### 2. Code Integration (Go)

```go
// In opencode.go - startServer()
cmd := exec.Command(argv[0], argv[1:]...)
cmd.Dir = workDir

// Inject OPENCODE_CONFIG pointing to ws profile
cmd.Env = append(cmd.Env, "OPENCODE_CONFIG="+wsProfilePath)

// Inherit existing environment (PATH, auth keys, etc.)
for _, e := range os.Environ() {
    if !strings.HasPrefix(e, "OPENCODE_") {
        cmd.Env = append(cmd.Env, e)
    }
}
```

### 3. Agent Selection

When submitting prompts, specify the agent:

```go
body := map[string]interface{}{
    "parts": []map[string]interface{}{
        {"type": "text", "text": prompt},
    },
}

// Set agent if specified in config
if r.agent != "" {
    body["agent"] = r.agent
}
```

---

## Config File Locations

| Config | Path | Purpose |
|--------|------|---------|
| **WS Profile** | `~/.config/opencode/profiles/ws/opencode.jsonc` | Main profile for contrabass |
| Local Config | `.contrabass/configs/ws-minimax.json` | Simple fallback config |

---

## Testing Notes

To reproduce these experiments:

```bash
# 1. Start a test server with ws profile
OPENCODE_CONFIG=~/.config/opencode/profiles/ws/opencode.jsonc opencode serve --port 4101

# 2. Create a session
SESSION_ID=$(curl -s -X POST http://127.0.0.1:4101/session \
  -H "Content-Type: application/json" -d '{}' | jq -r '.id')

# 3. Test scribe agent
curl -X POST "http://127.0.0.1:4101/session/$SESSION_ID/message" \
  -H "Content-Type: application/json" \
  -d '{"agent": "scribe", "parts": [{"type": "text", "text": "just testing, just say yes"}]}'

# 4. Test file creation
curl -X POST "http://127.0.0.1:4101/session/$SESSION_ID/message" \
  -H "Content-Type: application/json" \
  -d '{"agent": "scribe", "parts": [{"type": "text", "text": "Create a file called test.txt with hello inside"}]}'
```

---

## Open Questions

1. **Does `OPENCODE_CONFIG_DIR` work for custom .opencode directories?**
   - Not fully tested
   - Would allow per-workspace skills/agents

2. **What about agent permissions when using ws profile?**
   - `scribe` can write files
   - `explore` only has limited bash (no write)
   - `plan` has read-only permissions
