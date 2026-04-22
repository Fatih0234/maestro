# What Contrabass Is

> A project-level orchestrator for AI coding agents — manage work, not agents

Contrabass is a Go reimplementation of OpenAI's Symphony with a Charm TUI stack. It orchestrates coding agents against an issue tracker and visualizes progress in a terminal UI built with the Charm ecosystem (Bubble Tea, Lip Gloss, Log).

## Core Concept

Contrabass operates on **issues** (tasks to be done) rather than directly on agents. The orchestrator:
1. Polls an issue tracker for unclaimed issues
2. Claims an issue and creates a workspace (git worktree)
3. Renders a prompt using issue data and WORKFLOW.md template
4. Launches an agent runner to execute the task
5. Monitors progress, handles failures, retries with backoff
6. Updates tracker state and reports to UI

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                         CLI (Cobra)                              │
│  ├── Run Mode (single agent orchestrator)                       │
│  └── Team Mode (multi-agent phased pipeline)                    │
├─────────────────────────────────────────────────────────────────┤
│  Orchestrator (main polling loop)                               │
│  ├── Poll tracker for issues                                     │
│  ├── Claim and dispatch to agents                                │
│  ├── Manage retry backoff (exponential + jitter)                │
│  ├── Detect stalls and timeouts                                  │
│  └── Emit events to TUI/dashboard                                │
├──────────────────┬──────────────────┬──────────────────────────┤
│   Trackers       │   Agent Runners   │   Workspace Manager      │
│  ├── Linear      │  ├── Codex        │  └── Git worktrees       │
│  ├── GitHub      │  ├── OpenCode     │  under workspaces/       │
│  └── Local Board │  ├── OMX          │                            │
│                  │  └── OMC          │                            │
├──────────────────┴──────────────────┴──────────────────────────┤
│  UI Layer                                                         │
│  ├── Charm TUI (Bubble Tea + Lip Gloss)                         │
│  └── Embedded Web Dashboard (React + SSE)                        │
└─────────────────────────────────────────────────────────────────┘
```

## Key Components

### 1. Config Parser (`internal/config/`)

Parses `WORKFLOW.md` files with YAML front matter:

```yaml
---
max_concurrency: 3
poll_interval_ms: 2000
tracker:
  type: internal
agent:
  type: opencode
opencode:
  binary_path: opencode serve
  port: 9090
---

# Your prompt template
Issue: {{ issue.title }}
Description: {{ issue.description }}
```

Template bindings: `{{ issue.title }}`, `{{ issue.description }}`, `{{ issue.url }}`

### 2. Trackers (`internal/tracker/`)

| Tracker | Type | Description |
|---------|------|-------------|
| Linear | External | GraphQL API to linear.app |
| GitHub | External | REST API to GitHub Issues |
| Local Board | Internal | File-based `.contrabass/board/` |

The **Local Board** is a file-based tracker that stores issues as JSON files:
- `.contrabass/board/manifest.json` — board metadata
- `.contrabass/board/issues/CB-1.json` — individual issue
- `.contrabass/board/comments/CB-1.jsonl` — comment stream

States: `todo`, `in_progress`, `retry`, `done`

### 3. Workspace Manager (`internal/workspace/`)

Creates git worktrees per issue:
- Path: `workspaces/<issue-id>/`
- Uses `git worktree add` to create isolated branches
- Tracks active workspaces in memory
- Cleans up worktrees when done

### 4. Agent Runners (`internal/agent/`)

Each runner implements `AgentRunner` interface:
```go
type AgentRunner interface {
    Start(ctx context.Context, issue types.Issue, workspace string, prompt string) (*AgentProcess, error)
    Stop(proc *AgentProcess) error
    Close() error
}
```

| Runner | Binary | Protocol |
|--------|--------|----------|
| Codex | `codex app-server` | JSONL over stdin/stdout |
| OpenCode | `opencode serve` | HTTP REST + SSE events |
| OMX | `omx` | Team runtime polling |
| OMC | `omc` | Team runtime polling |

**OpenCode runner** specifics:
- Manages `opencode serve` process lifecycle
- Creates HTTP sessions at `POST /session`
- Submits prompts at `POST /session/{id}/prompt_async`
- Streams events via SSE at `GET /event`
- Aborts via `POST /session/{id}/abort`

### 5. Orchestrator (`internal/orchestrator/`)

Main event loop that:
- Polls tracker at configurable intervals
- Claims unclaimed issues
- Creates workspaces and renders prompts
- Starts agent runners
- Watches for completion/errors
- Enqueues retries with exponential backoff
- Emits events to channels

Key types:
```go
type IssueState int  // Unclaimed, Claimed, Running, RetryQueued, Released
type RunPhase int     // PreparingWorkspace, BuildingPrompt, LaunchingAgentProcess, 
                      // InitializingSession, StreamingTurn, Finishing, 
                      // Succeeded, Failed, TimedOut, Stalled
```

### 6. Team System (`internal/team/`)

Multi-agent coordination with phased pipeline:

```
Plan → PRD → Exec → Verify → (Fix → Exec)* → Complete/Failed
```

**Coordinator** manages the phase machine and worker goroutines.

**PhaseMachine** validates transitions:
- Plan → PRD → Exec → Verify → Fix/Complete/Failed
- Fix loops bounded by `max_fix_loops` config

**TaskRegistry** handles task lifecycle:
- Claim with lease tokens (time-based)
- Release on completion/failure
- Renew leases during long tasks

**HeartbeatMonitor** detects stale workers:
- Writes heartbeat files to `.contrabass/state/team/{team}/heartbeats/`
- Compares timestamps against `claim_lease_seconds` threshold

**GovernancePolicy** enforces rules:
- Max concurrent tasks per worker
- Phase gates
- Worker capacity limits

### 7. UI Layer

**Charm TUI** (Bubble Tea + Lip Gloss):
- Model/Update/View pattern (Elm architecture)
- Header with metrics
- Running session table
- Event log
- Backoff queue display

**Web Dashboard** (React + embedded):
- State snapshot API (`GET /api/v1/state`)
- SSE events (`GET /api/v1/events`)
- Metrics, sessions, retry queue tables

## File Structure

```
contrabass/
├── cmd/contrabass/          # CLI entry point
│   ├── main.go             # Cobra root + run modes
│   ├── team.go             # Team subcommand (run, status, cancel)
│   ├── team_root.go        # Team root execution
│   └── board.go            # Board management
├── internal/
│   ├── agent/               # Agent runners
│   │   ├── codex.go        # Codex JSONL protocol
│   │   ├── opencode.go     # OpenCode HTTP+SSE
│   │   ├── omx.go          # OMX team runtime
│   │   ├── omc.go          # OMC team runtime
│   │   └── runner.go       # AgentProcess, interface
│   ├── config/             # Config parsing
│   │   ├── config.go       # WorkflowConfig struct + methods
│   │   ├── parser.go      # WORKFLOW.md parser
│   │   └── watcher.go      # fsnotify live reload
│   ├── orchestrator/       # Main loop
│   │   ├── orchestrator.go # Polling + dispatch
│   │   ├── state.go        # State transitions
│   │   ├── events.go       # Event types
│   │   └── snapshot.go    # State snapshots
│   ├── team/               # Multi-agent system
│   │   ├── coordinator.go # Phase coordination
│   │   ├── phases.go       # Phase machine
│   │   ├── tasks.go        # Task registry
│   │   ├── heartbeat.go   # Worker health
│   │   └── governance.go  # Concurrency rules
│   ├── tracker/            # Issue trackers
│   │   ├── local.go       # Local board (file-based)
│   │   ├── linear.go      # Linear API
│   │   └── github.go      # GitHub Issues API
│   ├── workspace/          # Git worktree management
│   │   └── manager.go     # Create/cleanup worktrees
│   ├── tui/                # Terminal UI
│   │   ├── model.go       # Main TUI model
│   │   ├── table.go       # Sessions table
│   │   └── events.go      # Event bridge
│   └── types/              # Shared types
│       ├── types.go        # Issue, RunAttempt, etc.
│       └── team.go         # TeamTask, WorkerState, etc.
├── packages/dashboard/      # React web dashboard
└── packages/landing/        # Astro landing site
```

## Key Dependencies

```
Go 1.25+
├── charm.land/bubbletea/v2     # TUI framework
├── charm.land/lipgloss/v2      # Styling
├── charm.land/bubbles/v2       # Components
├── github.com/charmbracelet/log # Structured logging
├── github.com/spf13/cobra      # CLI
├── github.com/osteele/liquid    # Template rendering
└── github.com/fsnotify/fsnotify # File watching
```

## Runtime Flow

```
1. Parse WORKFLOW.md → WorkflowConfig
2. Create tracker (local/linear/github)
3. Create workspace manager (git worktrees)
4. Create agent runner (opencode/codex/etc)
5. Run orchestrator loop:
   ├─ Poll tracker for issues
   ├─ Claim unclaimed issue
   ├─ Create git worktree workspace
   ├─ Render prompt from template
   ├─ Start agent process
   ├─ Stream events until completion
   ├─ On failure: enqueue backoff retry
   └─ Emit events to TUI/dashboard
6. On signal: graceful shutdown
```

## CLI Flags

```bash
contrabass --config WORKFLOW.md [flags]

--config string      path to WORKFLOW.md file (required)
--dry-run            exit after first poll cycle
--log-file string    log output path (default "contrabass.log")
--log-level string   log level (debug/info/warn/error)
--no-tui             headless mode — skip TUI, log events to stdout
--port int           web dashboard port (0 = disabled)
```

## Team Subcommand

```bash
contrabass team run --config workflow.md [flags]

--tasks string       path to tasks JSON file
--issue string       internal board issue ID
--max-workers int    override max workers
--worker-mode string goroutine|tmux
```

## Notes

- **JSONL framing**: Codex uses newline-delimited JSON (one object per line), not Content-Length headers
- **Error code -32001**: Handle server overload gracefully
- **Charm v2**: Use vanity import paths (`charm.land/bubbletea/v2`), not `github.com/charmbracelet/...`
- **Lip Gloss v2**: `View()` returns `string`, not `io.Writer`; `AdaptiveColor` removed
- **Goroutine vs tmux**: Team workers can run in-process (goroutine) or in separate tmux panes (tmux)