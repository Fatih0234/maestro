# Contrabass-PI

A minimal orchestrator for OpenCode coding agents. Poll a local board, create workspaces, dispatch agents through a plan-execute-verify pipeline, and monitor progress via TUI.

## Quick Start

```bash
# Inside any git project
go install github.com/fatihkarahan/contrabass-pi/cmd/contrabass@latest

contrabass init                          # set up WORKFLOW.md + board
contrabass board create "Fix login bug"  # add an issue
contrabass                               # start the orchestrator
```

## Pipeline

Issues flow through stages. The orchestrator handles retries at the stage level.

```
todo → plan → execute → verify → in_review → done
```

| Stage | What the agent does |
|-------|---------------------|
| **plan** | Analyze the issue and produce an implementation plan |
| **execute** | Apply the plan as code changes |
| **verify** | Confirm the changes satisfy the issue |
| **human review** | Human inspects and approves/rejects |

On success, the orchestrator marks the issue `in_review` and keeps the worktree intact for manual inspection. No auto-merge, no auto-cleanup.

## CLI

| Command | Description |
|---------|-------------|
| `contrabass init` | Set up the current directory as a contrabass project |
| `contrabass` | Start the orchestrator (discovers `WORKFLOW.md`) |
| `contrabass --no-tui` | Run headless |
| `contrabass --dry-run` | One poll cycle, then exit |
| `contrabass --log-level debug` | Verbose logging |
| `contrabass board create "title"` | Add an issue |
| `contrabass board list --all` | List all issues |
| `contrabass board show CB-1` | Show issue details |
| `contrabass board approve CB-1` | Mark done |
| `contrabass board reject CB-1` | Return to todo |
| `contrabass board retry CB-1` | Retry a failed issue |

## Configuration

`WORKFLOW.md` is a markdown file with YAML front matter. The YAML configures the orchestrator; the markdown body is the prompt template.

```yaml
---
max_concurrency: 1
poll_interval_ms: 3000
agent_timeout_ms: 300000
stall_timeout_ms: 120000

tracker:
  type: internal
  board_dir: .contrabass/projects/default/board
  issue_prefix: CB

agent:
  type: opencode

opencode:
  binary_path: opencode serve
  profile: ""      # your OpenCode profile
  agent: ""        # default agent name
  agents:          # per-stage agents (optional)
    plan: plan
    execute: build
    verify: review

workspace:
  base_dir: .
  branch_prefix: contrabass/
---

# Task

Implement the following issue: {{ issue.title }}

{{ issue.description }}
```

## Project Structure

```
cmd/contrabass/          # CLI entry point
internal/
  config/               # WORKFLOW.md parser
  tracker/              # Local board (file-based)
  workspace/            # Git worktree manager
  agent/                # OpenCode runner (HTTP + SSE)
  pipeline/             # Stage-aware runner
  orchestrator/         # Poll → claim → stage loop → retry
  diagnostics/          # Persistent run recorder
  tui/                  # Charm Bubble Tea UI
  types/                # Shared types + pipeline types
  util/                 # String utilities
docs/
  context/              # Architecture + implementation guides
  references/contrabass/ # Full Contrabass source (reference only, gitignored)
```

## Documentation

- [`docs/context/what-contrabass-is.md`](./docs/context/what-contrabass-is.md) — high-level architecture
- [`docs/context/minimal-contrabass.md`](./docs/context/minimal-contrabass.md) — implementation guide synced to the Go code

## License

MIT
