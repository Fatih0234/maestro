# What Maestro Is

> A project-level orchestrator for AI coding agents — manage work, not agents

Maestro is a Go reimplementation of OpenAI's Symphony with a Charm TUI stack. It orchestrates coding agents against an issue tracker and visualizes progress in a terminal UI built with the Charm ecosystem.

> Note: this repository currently implements the minimal single-agent slice only: local board tracking, sibling worktree management, the OpenCode runner, persistent diagnostics, and the Charm TUI. Team mode, external trackers, and the web dashboard are deferred.

## Core Concept

Maestro operates on **issues** (tasks to be done) rather than directly on agents. The orchestrator:
1. Polls an issue tracker for unclaimed issues
2. Claims an issue and creates a workspace (git worktree)
3. Runs the **plan → execute → verify** pipeline, one stage at a time
4. Each stage uses a stage-specific prompt and can use a different agent
5. Monitors progress, handles failures, retries with backoff at the **stage level**
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
│  ├── Claim and dispatch to pipeline stages                      │
│  ├── Plan → Execute → Verify sequence                           │
│  ├── Manage retry backoff per stage (exponential + jitter)     │
│  ├── Detect stalls and timeouts                                 │
│  └── Emit events to TUI                                         │
├──────────────────┬──────────────────┬──────────────────────────┤
│ Trackers         │ Agent Runner     │ Workspace Manager         │
│  └── Local Board │  └── OpenCode    │  sibling worktree dir     │
│                  │                  │  outside the repo tree    │
├──────────────────┴──────────────────┴──────────────────────────┤
│  Pipeline Runner                                                │
│  ├── Plan stage (read-only analysis)                            │
│  ├── Execute stage (write-capable editing)                      │
│  └── Verify stage (reviewer-style validation)                   │
├─────────────────────────────────────────────────────────────────┤
│  Diagnostics + UI                                               │
│  ├── Run records (`.maestro/projects/<project>/runs/`)       │
│  │   ├── stages/plan/manifest.json + result.json                │
│  │   ├── stages/execute/manifest.json + diff.patch              │
│  │   ├── stages/verify/manifest.json + result.json              │
│  │   └── review/handoff.md + decision.json                      │
│  └── Charm TUI (Bubble Tea)                                     │
└─────────────────────────────────────────────────────────────────┘
```

## Pipeline Stages

The orchestrator-owned pipeline is defined in `docs/specs/orchestrator-owned-pipeline/`.

```
todo -> in_progress -> plan -> execute -> verify -> in_review -> done
```

| Stage | Purpose | Artifacts Written |
|-------|---------|-------------------|
| **plan** | Analyze the issue and produce a concrete implementation plan | `stages/plan/prompt.md`, `response.md`, `manifest.json`, `result.json` |
| **execute** | Apply the plan as code changes in the workspace | `stages/execute/prompt.md`, `response.md`, `diff.patch`, `manifest.json`, `result.json` |
| **verify** | Confirm the change satisfies the issue and plan | `stages/verify/prompt.md`, `response.md`, `manifest.json`, `result.json` |
| **human review** | Human inspects evidence and makes a decision | `review/handoff.md`, `decision.json` |

If any stage fails, the orchestrator retries **that stage** (not the whole pipeline) after a backoff delay.

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
opencode:
  binary_path: opencode serve
  port: 9090
  agent: build
  agents:
    plan: plan
    execute: build
    verify: review
workspace:
  base_dir: .
  branch_prefix: opencode/
---

# Your prompt template
Issue: {{ issue.title }}
Description: {{ issue.description }}
```

Template bindings: `{{ issue.id }}`, `{{ issue.title }}`, `{{ issue.description }}`, `{{ issue.labels }}`

Per-stage agent selection: `opencode.agents` maps stage names to agent names. If a stage is not mapped, the top-level `opencode.agent` is used.

### 2. Trackers (`internal/tracker/`)

| Tracker | Type | Description |
|---------|------|-------------|
| Local Board | Internal | File-based `.maestro/projects/<project>/board/` |

The **Local Board** is a file-based tracker that stores issues as JSON files:
- `.maestro/projects/<project>/board/manifest.json` — board metadata
- `.maestro/projects/<project>/board/issues/CB-1.json` — individual issue
- `.maestro/projects/<project>/runs/` — persistent run diagnostics and attempt snapshots

States: `todo`, `in_progress`, `retry_queued`, `in_review`, `done`

### 3. Workspace Manager (`internal/workspace/`)

Creates git worktrees per issue:
- Path: sibling worktree dir outside the repo tree (for example `../maestro-snake.worktrees/CB-1/`)
- Uses `git worktree add` to create isolated branches
- Tracks active workspaces in memory
- Leaves worktrees intact after runtime success for human review
- Cleanup is explicit and human-driven after review/merge

### 4. Runtime Diagnostics (`internal/diagnostics/`)

Persistent run records live beside the board directory:
- `_orchestrator/events.jsonl` — orchestrator event log
- `<issue-id>/issue.json` — issue snapshot
- `<issue-id>/summary.json` — issue-level run summary
- `<issue-id>/attempts/<NNN>/meta.json` — attempt metadata
- `<issue-id>/attempts/<NNN>/stages/<stage>/manifest.json` — stage control file
- `<issue-id>/attempts/<NNN>/stages/<stage>/result.json` — stage outcome
- `<issue-id>/attempts/<NNN>/review/handoff.md` — human-readable review package
- `<issue-id>/attempts/<NNN>/review/decision.json` — explicit review decision

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
- agent selection is driven by `types.StageContext` carried in the context

### 6. Pipeline Runner (`internal/pipeline/`)

Owns the per-issue stage lifecycle:
- `Run(ctx, issue, attempt, stage, emit)` executes one stage end-to-end
- Creates/reuses workspace (idempotent across stages)
- Builds stage-specific prompts (planning mode, execution mode, verification mode)
- Starts the agent and monitors events until completion
- Writes stage artifacts via the diagnostics recorder
- Classifies failures into typed `StageFailureKind`

### 7. Orchestrator (`internal/orchestrator/`)

Main event loop that:
- polls the tracker at configurable intervals
- claims unclaimed issues
- runs plan → execute → verify sequentially via `pipeline.Runner`
- watches for completion, stalls, and timeouts
- enqueues retries with exponential backoff at the **failed stage**
- marks successful runtime completion as `in_review`
- emits events to the TUI

### 8. TUI Layer (`internal/tui/`)

Bubble Tea UI that shows:
- running sessions with current **stage** and attempt number
- event log with stage transitions
- backoff queue with ETA, failed stage, and failure reason
- review-ready queue with wait time and completed stages

## Runtime Flow

1. Parse `WORKFLOW.md` → `WorkflowConfig`
2. Create tracker (local board)
3. Create workspace manager (sibling worktree dir)
4. Create diagnostics recorder (writes `.maestro/projects/<project>/runs/`)
5. Create OpenCode runner
6. Run orchestrator loop:
   - poll tracker for issues
   - claim an issue
   - create a git worktree workspace
   - **plan stage**: render planning prompt, run agent, write plan artifacts
   - **execute stage**: render execution prompt, run agent, write execute artifacts + diff
   - **verify stage**: render verification prompt, run agent, write verify artifacts
   - on all stages passing: move issue to `in_review`, write review handoff
   - on any stage failure: enqueue backoff for that stage, preserve attempt artifacts
   - emit events to the TUI
7. On signal: graceful, idempotent shutdown

## CLI Flags

```bash
# Run with TUI
./maestro --config WORKFLOW.md

# Run headless
./maestro --config WORKFLOW.md --no-tui

# Run with custom log level
./maestro --config WORKFLOW.md --log-level debug

# Dry run (exactly one poll cycle, then exit)
./maestro --config WORKFLOW.md --dry-run
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
maestro/
├── cmd/maestro/          # CLI entry point
├── internal/
│   ├── agent/               # Agent runners
│   ├── config/              # Config parsing
│   ├── diagnostics/         # Run records + stage artifact recording
│   ├── orchestrator/        # Polling + dispatch + stage loop
│   ├── pipeline/            # Stage-aware runner (plan → execute → verify)
│   ├── tracker/             # Local board
│   ├── tui/                 # Terminal UI
│   ├── types/               # Shared types + pipeline types
│   └── workspace/           # Git worktree management
└── docs/
    ├── context/             # Implementation context
    ├── specs/               # Design specs
    └── references/maestro/  # Reference implementation
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
- stage-scoped retries keep iteration tight: if verify fails, only verify is retried
