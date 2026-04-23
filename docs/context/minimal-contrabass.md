# Minimal Contrabass for OpenCode

> A minimal orchestrator for OpenCode coding agents with local board tracker and persistent run diagnostics

## Scope

This is a stripped-down version of Contrabass that focuses on:
- ✅ Single-agent orchestrator (no team system)
- ✅ Local board tracker (file-based, no external service)
- ✅ OpenCode agent runner
- ✅ Persistent run diagnostics
- ✅ Git worktree workspaces
- ✅ WORKFLOW.md config parser
- ✅ Charm TUI (Bubble Tea)
- ❌ ~~Multi-agent teams~~ (deferred)
- ❌ ~~Linear/GitHub trackers~~ (deferred)
- ❌ ~~OMX/OMC runners~~ (deferred)
- ❌ ~~Web dashboard~~ (deferred)

## Philosophy

**Keep it simple.** The goal is a working orchestrator that:
1. Polls a local board for issues
2. Creates a workspace for each issue
3. Runs OpenCode to execute the task
4. Shows progress in a TUI

No external dependencies beyond OpenCode, Git, and Go.

## Directory Structure

```
.
├── cmd/
│   └── contrabass/
│       └── main.go           # CLI entry, TUI, headless modes
├── internal/
│   ├── config/
│   │   └── config.go         # WORKFLOW.md parser
│   ├── tracker/
│   │   └── local.go          # Local board (file-based)
│   ├── workspace/
│   │   └── manager.go        # Git worktree management
│   ├── agent/
│   │   └── opencode.go       # OpenCode HTTP+SSE runner
│   ├── diagnostics/
│   │   └── recorder.go       # Persistent run records
│   ├── orchestrator/
│   │   ├── orchestrator.go   # Main loop, dispatch
│   │   ├── events.go        # Event types
│   │   └── backoff.go       # Retry logic
│   └── tui/
│       ├── model.go          # Main TUI model
│       ├── table.go          # Session table
│       └── events.go         # Orchestrator event bridge
├── context/                  # Documentation (this repo)
│   ├── what-contrabass-is.md
│   └── minimal-contrabass.md
├── WORKSPACE.md              # Placeholder
└── go.mod
```

## Core Components

### 1. Config Parser

Parse `WORKFLOW.md` with YAML front matter:

```yaml
---
max_concurrency: 3
poll_interval_ms: 2000
max_retry_backoff_ms: 240000
agent_timeout_ms: 900000
stall_timeout_ms: 60000
tracker:
  type: internal
  board_dir: .contrabass/board
  issue_prefix: CB
agent:
  type: opencode
opencode:
  binary_path: opencode serve
  port: 9090
  model: minimax-coding-plan/MiniMax-M2.7
workspace:
  base_dir: .
  branch_prefix: opencode/
---

# Task

{{ issue.title }}

{{ issue.description }}
```

**Supported config fields:**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `max_concurrency` | int | 3 | Max concurrent agents |
| `poll_interval_ms` | int | 30000 | Poll interval in ms |
| `max_retry_backoff_ms` | int | 240000 | Max retry backoff in ms |
| `agent_timeout_ms` | int | 900000 | Agent timeout in ms |
| `stall_timeout_ms` | int | 60000 | Stall detection timeout in ms |
| `tracker.type` | string | internal | Tracker type |
| `tracker.board_dir` | string | .contrabass/orchestrator/board | Local board path |
| `tracker.issue_prefix` | string | CB | Issue ID prefix |
| `agent.type` | string | opencode | Agent type |
| `opencode.binary_path` | string | opencode serve | OpenCode binary |
| `opencode.port` | int | 0 | Server port (0 = auto) |
| `opencode.password` | string | "" | Server password |
| `opencode.profile` | string | "" | Profile name (e.g., "ws", "omo-power") - maps to `~/.config/opencode/profiles/<profile>/opencode.jsonc` |
| `opencode.agent` | string | "" | Default agent (e.g., "scribe", "build", "plan", "explore", "coder") |
| `opencode.config_dir` | string | "" | Optional custom .opencode directory |
| `opencode.model` | string | "" | **Deprecated** - model is now set in profile config |
| `workspace.base_dir` | string | . | Workspace root |
| `workspace.branch_prefix` | string | opencode/ | Branch name prefix |

> **Note on model:** The `opencode.model` field is deprecated. Model is now set via the profile config (`~/.config/opencode/profiles/<profile>/opencode.jsonc`). Use `opencode.profile` to select a profile containing your desired model.

### 2. Local Board Tracker

File-based issue storage:

```
.contrabass/
├── orchestrator/
│   ├── WORKFLOW.md          # Active orchestrator config
│   └── board/               # Issues for current project
│       ├── manifest.json
│       └── issues/
│           └── CB-*.json
└── projects/
    └── <project-name>/       # Per-project config + issues
        ├── WORKFLOW.md
        └── board/
```

**manifest.json:**
```json
{
  "schema_version": "1",
  "issue_prefix": "CB",
  "next_issue_number": 3,
  "created_at": "2024-01-01T00:00:00Z",
  "updated_at": "2024-01-01T00:00:00Z"
}
```

**Issue JSON (CB-1.json):**
```json
{
  "id": "CB-1",
  "identifier": "CB-1",
  "title": "Implement login",
  "description": "Add OAuth login flow...",
  "state": "todo",
  "labels": ["feature"],
  "created_at": "2024-01-01T00:00:00Z",
  "updated_at": "2024-01-01T00:00:00Z"
}
```

**States:** `todo`, `in_progress`, `retry_queued`, `in_review`, `done`

**Operations:**
- `FetchIssues()` → list all non-terminal issues (`todo`, `in_progress`, and ready `retry_queued`), excluding `in_review` and `done`
- `ClaimIssue(id)` → mark as in_progress, set claimed_by
- `ReleaseIssue(id)` → mark as todo, clear claimed_by
- `UpdateIssueState(id, state)` → update state
- `PostComment(id, body)` → append to comments file

### Runtime Records

When the board lives under `.contrabass/projects/<project>/board/`, the recorder stores run diagnostics in the sibling `.contrabass/projects/<project>/runs/` directory.

Typical contents:

```bash
.contrabass/projects/<project>/runs/
├── _orchestrator/
│   └── events.jsonl
└── CB-1/
    ├── issue.json
    ├── summary.json
    └── attempts/
        └── 001/
            ├── meta.json
            ├── prompt.md
            ├── events.jsonl
            ├── stdout.log
            ├── stderr.log
            ├── preflight/
            │   ├── git-status.txt
            │   └── git-worktree-list.txt
            └── postflight/
                ├── git-status.txt
                └── git-worktree-list.txt
```

These files are part of the source of truth for review and debugging.

### 3. Workspace Manager

Git worktree-based workspaces (default: a sibling `<repo>.worktrees/` directory outside the repo tree):

```bash
../<repo>.worktrees/
├── CB-1/          # Branch: opencode/CB-1
│   └── (repo files)
├── CB-2/          # Branch: opencode/CB-2
│   └── (repo files)
└── ...
```

**Operations:**
- `Create(issue)` → `git worktree add ../<repo>.worktrees/CB-1 -b opencode/CB-1`
- `Cleanup(issueID)` → `git worktree remove ../<repo>.worktrees/CB-1` (human-driven after review; not automatic on runtime success)
- `Exists(issueID)` → check if workspace exists

### 4. OpenCode Agent Runner

Manages `opencode serve` process:

1. **Start server** (per workspace):
   ```bash
   opencode serve --port 9090
   # Parse: "listening on http://127.0.0.1:9090"
   ```

2. **Create session**:
   ```bash
   POST http://127.0.0.1:9090/session
   → { "id": "sess-123" }
   ```

3. **Submit task**:
   ```bash
   POST http://127.0.0.1:9090/session/sess-123/prompt_async
   Content-Type: application/json
   { "parts": [{ "type": "text", "text": "..." }] }
   → 204 No Content
   ```

4. **Stream events** (SSE):
   ```bash
   GET http://127.0.0.1:9090/event
   Accept: text/event-stream

   event: session.status
   data: {"type":"session.status","properties":{"sessionID":"sess-123","status":{"type":"idle"}}}
   ```

5. **Abort** (if needed):
   ```bash
   POST http://127.0.0.1:9090/session/sess-123/abort
   → 200 OK
   ```

6. **Stop server**:
   ```bash
   kill -INT <pid>
   ```

**Key SSE events:**
- `session.status` with `status.type: idle` → session done
- `session.error` → session failed
- `server.heartbeat` → ignore

### 5. Orchestrator

Main loop:

```
┌─────────────────────────────────────────────────────────┐
│  for {                                                  │
│    poll interval tick                                   │
│    ├─ FetchIssues()                                    │
│    ├─ ReconcileRunning()  ← check stalls, timeouts     │
│    ├─ DispatchBackoff()   ← retry ready issues         │
│    └─ DispatchReady()     ← claim new issues           │
│  }                                                      │
└─────────────────────────────────────────────────────────┘
```

**DispatchReady:**
- Skip if at max concurrency
- Skip if issue already managed
- Claim issue → Create workspace → Render prompt → Start agent
- On success, move the issue to `in_review` and keep the worktree + run records intact for human review

**DispatchBackoff:**
- Check retry timestamps
- Re-claim issue → Re-start agent

**ReconcileRunning:**
- Check for stale agents (no events recently)
- Check for timeout (configurable)
- On stall/timeout: Stop agent, enqueue backoff

**Backoff strategy:**
- Attempt 1: retry in 30s
- Attempt 2: retry in 60s
- Attempt 3: retry in 120s
- ...exponential, max 4 minutes

### 6. Charm TUI

Bubble Tea model:

**Model fields:**
```go
type model struct {
    issues    []types.Issue
    running   map[string]*runEntry
    backoff   []types.BackoffEntry
    stats     orchestrator.Stats
    events    []string  // event log
}
```

**View:**
```
┌──────────────────────────────────────────────────────────────────┐
│ Contrabass                    Running: 2/3    Tokens: 1.2K/3.4K │
├──────────────────────────────────────────────────────────────────┤
│ Issue      Title                    Phase        Tokens  Last   │
├──────────────────────────────────────────────────────────────────┤
│ CB-1       Implement login         Streaming    1.1K    2s ago  │
│ CB-2       Fix pagination         Initializing 234     5s ago   │
├──────────────────────────────────────────────────────────────────┤
│ Backoff Queue                                                [2] │
│   CB-3  retry in 45s (attempt 2)                                  │
├──────────────────────────────────────────────────────────────────┤
│ [Events]                                                       │
│ 14:32:01 CB-1 agent started (pid 12345, session sess-123)        │
│ 14:32:05 CB-1 tokens updated (tokens_in=512)                    │
└──────────────────────────────────────────────────────────────────┘
```

**Key bindings:**
- `q` / `ctrl+c` — quit
- `↑↓` — scroll event log
- `r` — refresh (force poll)

## Implementation Order

### Phase 1: Core Foundation
1. **go.mod setup** — dependencies only
2. **Config parser** — parse WORKFLOW.md
3. **Local tracker** — file-based issue management
4. **Workspace manager** — git worktrees

### Phase 2: Agent Integration
5. **OpenCode runner** — server lifecycle, HTTP, SSE
6. **AgentProcess** — events channel, done channel

### Phase 3: Orchestrator
7. **Main loop** — poll, dispatch, backoff
8. **Event system** — emit, bridge to TUI

### Phase 4: TUI
9. **Bubble Tea model** — state, update, view
10. **Event bridge** — orchestrator → TUI

### Phase 5: Polish
11. **Error handling** — graceful shutdown, recovery
12. **CLI flags** — config, no-tui, dry-run, log-level
13. **Tests** — basic test coverage

## Key Types

```go
// Issue states
const (
    Unclaimed    IssueState = iota
    Claimed
    Running
    RetryQueued
    InReview
    Released
)

// Run phases
const (
    PreparingWorkspace RunPhase = iota
    BuildingPrompt
    LaunchingAgentProcess
    InitializingSession
    StreamingTurn
    Finishing
    Succeeded
    Failed
    TimedOut
    Stalled
)

// Core types
type Issue struct {
    ID          string
    Identifier  string
    Title       string
    Description string
    State       IssueState
    Labels      []string
    URL         string
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type RunAttempt struct {
    IssueID     string
    Phase       RunPhase
    Attempt     int
    PID         int
    StartTime   time.Time
    TokensIn    int64
    TokensOut   int64
    SessionID   string
    WorkspacePath string
}

type BackoffEntry struct {
    IssueID  string
    Attempt  int
    RetryAt  time.Time
    Error    string
}

// Agent interface
type AgentRunner interface {
    Start(ctx context.Context, issue Issue, workspace string, prompt string) (*AgentProcess, error)
    Stop(proc *AgentProcess) error
    Close() error
}

type AgentProcess struct {
    PID       int
    SessionID string
    Events    chan AgentEvent  // streams events
    Done      chan error       // closed on completion
}
```

## CLI Interface

```bash
# Run with TUI
./contrabass --config WORKFLOW.md

# Run headless
./contrabass --config WORKFLOW.md --no-tui

# Run with custom log level
./contrabass --config WORKFLOW.md --log-level debug

# Dry run (exactly one poll cycle, then exit)
./contrabass --config WORKFLOW.md --dry-run
```

## Future Extensions (Out of Scope)

When ready to add:
1. **Teams** — multi-agent coordination with phase pipeline
2. **External trackers** — Linear, GitHub Issues
3. **Other agents** — Codex, OMX, OMC
4. **Web dashboard** — React + SSE API
5. **Config hot-reload** — fsnotify on WORKFLOW.md

## Similar Projects

- [OpenAI Symphony](https://github.com/openai/symphony) — Original Elixir implementation
- [Contrabass](https://github.com/junhoyeo/contrabass) — Full Go implementation (this is derived from)