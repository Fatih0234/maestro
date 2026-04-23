# What Contrabass Is

> A project-level orchestrator for AI coding agents — manage work, not agents

Contrabass is a Go reimplementation of OpenAI's Symphony with a Charm TUI stack. It orchestrates coding agents against an issue tracker and visualizes progress in a terminal UI built with the Charm ecosystem.

> Note: this repository currently implements the minimal single-agent slice only: local board tracking, sibling worktree management, the OpenCode runner, persistent diagnostics, and the Charm TUI. Team mode, external trackers, and the web dashboard are deferred.

## Core Concept

Contrabass operates on **issues** (tasks to be done) rather than directly on agents. The orchestrator:
1. Polls an issue tracker for unclaimed issues
2. Claims an issue and creates a workspace (git worktree)
3. Renders a prompt using issue data and the `WORKFLOW.md` template
4. Launches an agent runner to execute the task
5. Monitors progress, handles failures, retries with backoff
6. Hands successful runtime completion off to human review by moving the issue to `in_review`
7. Persists run diagnostics and reports to the UI

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                        CLI / Runtime                            │
│  ├── Single-agent run mode                                      │
│  ├── Headless mode                                              │
│  └── Dry-run mode                                               │
├─────────────────────────────────────────────────────────────────┤
│  Orchestrator                                                   │
│  ├── Poll tracker for issues                                    │
│  ├── Claim and dispatch to agents                               │
│  ├── Manage retry backoff (exponential + jitter)               │
│  ├── Detect stalls and timeouts                                 │
│  └── Emit events to TUI                                         │
├──────────────────┬──────────────────┬──────────────────────────┤
│ Trackers         │ Agent Runner     │ Workspace Manager         │
│  └── Local Board │  └── OpenCode    │  sibling worktree dir     │
│                  │                  │  outside the repo tree    │
├──────────────────┴──────────────────┴──────────────────────────┤
│  Diagnostics + UI                                               │
│  ├── Run records (`.contrabass/projects/<project>/runs/`)       │
│  └── Charm TUI (Bubble Tea)                                     │
└─────────────────────────────────────────────────────────────────┘
```

## Key Components

### 1. Config Parser (`internal/config/`)

Parses `WORKFLOW.md` files with YAML front matter:

```yaml
---
max_concurrency: 3
poll_interval_ms: 2000
max_retry_backoff_ms: 240000
agent_timeout_ms: 900000
stall_timeout_ms: 60000
tracker:
  type: internal
agent:
  type: opencode
opencode:
  binary_path: opencode serve
  port: 9090
workspace:
  base_dir: .
  branch_prefix: opencode/
---

# Your prompt template
Issue: {{ issue.title }}
Description: {{ issue.description }}
```

Template bindings: `{{ issue.id }}`, `{{ issue.title }}`, `{{ issue.description }}`, `{{ issue.labels }}`

### 2. Trackers (`internal/tracker/`)

| Tracker | Type | Description |
|---------|------|-------------|
| Local Board | Internal | File-based `.contrabass/orchestrator/board/` or `.contrabass/projects/<project>/board/` |

The **Local Board** is a file-based tracker that stores issues as JSON files:
- `.contrabass/projects/<project>/board/manifest.json` — board metadata
- `.contrabass/projects/<project>/board/issues/CB-1.json` — individual issue
- `.contrabass/projects/<project>/runs/` — persistent run diagnostics and attempt snapshots

States: `todo`, `in_progress`, `retry_queued`, `in_review`, `done`

### 3. Workspace Manager (`internal/workspace/`)

Creates git worktrees per issue:
- Path: sibling worktree dir outside the repo tree (for example `../contrabass-snake.worktrees/CB-1/`)
- Uses `git worktree add` to create isolated branches
- Tracks active workspaces in memory
- Leaves worktrees intact after runtime success for human review
- Cleanup is explicit and human-driven after review/merge

### 4. Runtime Diagnostics (`internal/diagnostics/`)

Persistent run records live beside the board directory:
- `_orchestrator/events.jsonl` — orchestrator event log
- `<issue-id>/issue.json` — issue snapshot
- `<issue-id>/summary.json` — issue-level run summary
- `<issue-id>/attempts/<NNN>/...` — attempt-level prompt, event, stdout/stderr, and git snapshots

These records are the source of truth for review, debugging, and post-run inspection.

### 5. Agent Runners (`internal/agent/`)

Each runner implements `AgentRunner`:

```go
type AgentRunner interface {
    Start(ctx context.Context, issue types.Issue, workspace, prompt string) (*AgentProcess, error)
    Stop(proc *AgentProcess) error
    Close() error
}
```

Current runtime uses the **OpenCode** runner:
- manages the `opencode serve` process lifecycle
- creates HTTP sessions at `POST /session`
- submits prompts at `POST /session/{id}/prompt_async`
- streams events via SSE at `GET /event`
- aborts via `POST /session/{id}/abort`

### 6. Orchestrator (`internal/orchestrator/`)

Main event loop that:
- polls the tracker at configurable intervals
- claims unclaimed issues
- creates workspaces and renders prompts
- starts the agent runner
- watches for completion, stalls, and timeouts
- enqueues retries with exponential backoff
- marks successful runtime completion as `in_review`
- emits events to the TUI

### 7. TUI Layer (`internal/tui/`)

Bubble Tea UI that shows:
- running sessions
- event log
- backoff queue
- review handoff state

## Runtime Flow

1. Parse `WORKFLOW.md` → `WorkflowConfig`
2. Create tracker (local board)
3. Create workspace manager (sibling worktree dir)
4. Create diagnostics recorder (writes `.contrabass/projects/<project>/runs/`)
5. Create OpenCode runner
6. Run orchestrator loop:
   - poll tracker for issues
   - claim an issue
   - create a git worktree workspace
   - render the prompt from the template
   - start the agent process
   - stream events until completion
   - on success: move the issue to `in_review` and keep the workspace + run records
   - on failure: enqueue a backoff retry
   - emit events to the TUI
7. On signal: graceful, idempotent shutdown

## CLI Flags

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

- `--config` loads the workflow file (defaults to `WORKFLOW.md`)
- `--dry-run` executes one poll cycle and exits
- `--no-tui` skips the Bubble Tea UI
- `--log-level debug|info|warn|error` filters output; invalid values exit with an error before startup

## Deferred / Out of Scope

- Multi-agent teams
- External trackers (Linear, GitHub Issues)
- Web dashboard
- Config hot-reload

## File Structure

```
contrabass/
├── cmd/contrabass/          # CLI entry point
├── internal/
│   ├── agent/               # Agent runners
│   ├── config/              # Config parsing
│   ├── diagnostics/         # Run records
│   ├── orchestrator/        # Polling + dispatch
│   ├── tracker/             # Local board
│   ├── tui/                 # Terminal UI
│   ├── types/               # Shared types
│   └── workspace/           # Git worktree management
└── docs/
    ├── context/             # Implementation context
    └── remote-project-orchestration.md
```

## Key Dependencies

- Go 1.26.1+
- `github.com/charmbracelet/bubbletea`
- `github.com/charmbracelet/lipgloss`
- `github.com/charmbracelet/log`
- `github.com/spf13/cobra`
- `gopkg.in/yaml.v2`

## Notes

- runtime success means **human review**, not business completion
- the orchestrator does **not** auto-merge, auto-clean up, or auto-close issues after runtime success
- the sibling worktree dir keeps the remote project tree clean
- persistent diagnostics make review and debugging reproducible
