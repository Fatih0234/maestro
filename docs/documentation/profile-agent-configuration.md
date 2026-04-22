# Profile and Agent Configuration

> **Status:** Implemented (Apr 22, 2026)

## Overview

Phase 2 introduced **profile and agent configuration** for the OpenCode runner. This allows selecting which OpenCode profile to use and which agent to run per task.

## What Was Added

### Config Schema Changes

In `WORKFLOW.md`, the `opencode` section now supports:

```yaml
opencode:
  binary_path: opencode serve
  port: 9090
  profile: ws           # NEW: profile name
  agent: scribe         # NEW: default agent
  config_dir: .opencode # NEW: optional custom directory
```

| Field | Description |
|-------|-------------|
| `profile` | Profile name (e.g., "ws", "omo-power") - maps to `~/.config/opencode/profiles/<name>/opencode.jsonc` |
| `agent` | Default agent (e.g., "scribe", "build", "plan", "explore", "coder") |
| `config_dir` | Optional custom `.opencode` directory |

### Code Changes

**`internal/config/config.go`**
- Added `Profile`, `Agent`, `ConfigDir` fields to `OpenCodeConfig`
- Deprecated `Model` field (model is now in profile config)
- Added validation that profile path exists when specified

**`internal/agent/opencode.go`**
- Added `profile`, `agent`, `configDir` fields to `OpenCodeRunner`
- Injects `OPENCODE_CONFIG` and `OPENCODE_CONFIG_DIR` env vars on server startup
- Includes `agent` field in prompt submission body

### How It Works

1. **Server startup:** When OpenCode server starts, it receives environment variables:
   ```bash
   OPENCODE_CONFIG=~/.config/opencode/profiles/ws/opencode.jsonc
   OPENCODE_CONFIG_DIR=/optional/custom/path  # if specified
   ```

2. **Prompt submission:** Message body includes agent selection:
   ```json
   {
     "agent": "scribe",
     "parts": [{"type": "text", "text": "..."}]
   }
   ```

3. **Validation:** Profile path is validated at config load time. If the profile doesn't exist, config validation fails.

## Available Profiles

From `~/.config/opencode/profiles/`:
- `ws` - Workspace profile (default, has all custom integrations)
- `omo-power` - Power profile
- `omo-slim` - Slim profile
- `omo-browser` - Browser profile
- `supervisor` - Supervisor profile

## Available Agents (from ws profile)

| Agent | Mode | Description |
|-------|------|-------------|
| `build` | primary | Main coding agent (default) |
| `plan` | primary | Read-only planning |
| `scribe` | all | Writing/documentation |
| `coder` | all | Coder agent |
| `explore` | subagent | File search |
| `reviewer` | all | Code reviewer |
| `researcher` | all | Research |
| `librarian` | all | Context manager |
| `qa` | all | QA agent |
| `tdd` | all | TDD agent |

## Example Usage

```yaml
# WORKFLOW.md
---
opencode:
  binary_path: opencode serve
  profile: ws
  agent: scribe
---

# Task
Create documentation for the new API endpoint
```

## Impact on the App

This change enables:
- **Per-task agent selection** - Different issues can use different agents
- **Profile-based configuration** - All OpenCode settings in one profile
- **Agent permissions** - Each agent has specific permissions from the profile

This was a key decision: instead of configuring model, API keys, MCP integrations, skills, commands, and agents separately, we leverage OpenCode's existing profile system.

## Tests Added

- `TestOpenCodeRunner_PromptSubmissionIncludesAgent` - Verifies agent is sent in request
- `TestOpenCodeRunner_PromptSubmissionNoAgentWhenEmpty` - Verifies no agent when not configured
- `TestValidate_OpenCodeProfileNotFound` - Validates profile existence
- `TestParse_ProfileAndAgent` - Validates config parsing

## Previous Decision Context

This implementation resolves the **"Config Server Decision"** from our experiments:
- **Option A was chosen:** Use `OPENCODE_CONFIG` env var pointing to profile
- **Runtime PATCH rejected:** Doesn't persist for new sessions
- **Model field deprecated:** Model is now in profile config
