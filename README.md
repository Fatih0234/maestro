# Contrabass-PI

A minimal orchestrator for OpenCode coding agents. Poll a local board, create workspaces, dispatch agents, and monitor progress via TUI.

Run it from inside any git project.

## Architecture

Each project owns its own board and run records. The orchestrator is a CLI tool you invoke from the project directory.

```
my-project/
├── WORKFLOW.md              ← Orchestrator config (YAML front matter + prompt template)
├── .contrabass/             ← Metadata (board tracked, runs gitignored)
│   └── projects/
│       └── my-project/
│           ├── board/         ← Local issue tracker (file-based)
│           │   ├── manifest.json
│           │   └── issues/CB-*.json
│           └── runs/          ← Persistent run diagnostics
│               ├── _orchestrator/events.jsonl
│               └── CB-1/
│                   ├── issue.json
│                   ├── summary.json
│                   └── attempts/001/...
├── src/
└── ...

../my-project.worktrees/   ← Sibling worktrees (outside repo, clean)
    └── CB-1/              ← Isolated workspace per issue
```

**To set up a new project:** `cd my-project && contrabass init`
**To run the orchestrator:** `contrabass` (auto-discovers `WORKFLOW.md`)

## Orchestrator-Owned Pipeline

Contrabass-PI implements an **orchestrator-owned pipeline** where the orchestrator controls the lifecycle and the agent only provides the runtime for each stage.

See the authoritative spec in [`docs/specs/orchestrator-owned-pipeline/`](./docs/specs/orchestrator-owned-pipeline/).

### Pipeline stages

```
todo -> in_progress -> plan -> execute -> verify -> in_review -> done
```

| Stage | Purpose | Agent mode |
|-------|---------|------------|
| **plan** | Turn the issue into a concrete implementation plan | Read-only analysis |
| **execute** | Apply the plan in the workspace | Write-capable editing |
| **verify** | Confirm the change satisfies the issue | Reviewer-style validation |
| **human review** | Human approves, rejects, or requests changes | Human gate |

Each stage:
- has a clear contract (inputs, outputs, success criteria)
- writes durable artifacts to disk
- can be retried independently without restarting the whole pipeline
- uses a stage-specific agent when configured

### Lifecycle (human-review handoff)

Contrabass-PI separates **runtime completion** from **business completion**:

1. issue is claimed and run in an isolated worktree
2. **plan** stage produces an implementation plan
3. **execute** stage applies the plan as code changes
4. **verify** stage checks that the changes satisfy the issue
5. orchestrator marks issue as `in_review`
6. the worktree and run records are preserved for manual inspection
7. human merges and later moves the issue to `done`

By design, orchestrator success does **not** auto-merge, auto-cleanup, or auto-close the issue.

Ctrl+C and SIGTERM stop the orchestrator cleanly; shutdown is idempotent.

## Runtime records

Persistent run diagnostics live beside the board under `.contrabass/runs/`.
They include the orchestrator event stream, the issue and summary snapshots, and per-attempt prompt/output/git snapshots.

These records are part of the source of truth when reviewing a finished run.

## Overview

Inspired by [Contrabass](https://github.com/junhoyeo/contrabass), stripped to essentials:

| Component | Purpose |
|-----------|---------|
| Config parser | Read `WORKFLOW.md` with YAML front matter |
| Local board | File-based issue tracker |
| Run records | Persistent run diagnostics under `.contrabass/runs/` |
| Workspace manager | Git worktree per issue (outside repo tree by default) |
| OpenCode runner | HTTP + SSE to communicate with the agent |
| Pipeline runner | Stage-aware runner: plan → execute → verify |
| Orchestrator | Poll → claim → stage loop → retry → hand off for review |
| Charm TUI | Bubble Tea interface |

Everything else (teams, external trackers, web dashboard) is deferred.

### Reference

| Path | Purpose |
|------|---------|
| `docs/context/` | Implementation guides |
| `docs/references/contrabass/` | Contrabass source (reference only, gitignored) |
| `docs/remote-project-orchestration.md` | How to orchestrate remote projects |

---

## Project Structure

```
cmd/contrabass/          # CLI entry point
internal/
  config/               # WORKFLOW.md parser
  tracker/              # Local board (file-based)
  workspace/            # Git worktree manager
  agent/                # OpenCode runner (HTTP + SSE)
  diagnostics/          # Persistent run recorder + stage artifacts
  pipeline/             # Stage-aware runner (plan → execute → verify)
  orchestrator/         # Poll → claim → stage loop → retry
  tui/                  # Charm Bubble Tea UI
  types/                # Shared types + pipeline types
  util/                 # String utilities
docs/
  context/              # Implementation docs
  specs/                # Design specs (orchestrator-owned-pipeline)
  references/contrabass/ # Contrabass (reference only)
```

## Runtime and CLI

- `contrabass init` — set up the current directory as a contrabass project (requires git)
- `contrabass` — start the orchestrator (auto-discovers `WORKFLOW.md` or `.contrabass/WORKFLOW.md` in cwd)
- `--config WORKFLOW.md` — workflow file to load (defaults to `WORKFLOW.md`, falls back to `.contrabass/WORKFLOW.md`)
- `--dry-run` — run exactly one poll cycle and exit
- `--no-tui` — run headless without Bubble Tea
- `--log-level debug|info|warn|error` — severity filter; invalid values exit with an error

---

## Configuration (`WORKFLOW.md`)

`WORKFLOW.md` is a markdown file with YAML front matter. The YAML section configures the orchestrator; the markdown body is used as the prompt template.

### Local board tracker (default)

```yaml
---
max_concurrency: 3
poll_interval_ms: 30000

tracker:
  type: internal
  board_dir: .contrabass/projects/default/board
  issue_prefix: CB

agent:
  type: opencode

opencode:
  binary_path: opencode serve
  profile: ws

workspace:
  base_dir: .
  branch_prefix: opencode/
---

# Prompt template

Implement the following issue: {{ issue.title }}

{{ issue.description }}
```

### Human review board commands

When the orchestrator reaches `in_review`, a human decides whether to approve, reject, or retry:

```bash
# List issues awaiting review
contrabass board list --state in_review

# List all issues
contrabass board list --all

# Show the full review package for an issue
contrabass board show CB-1

# Approve an issue and mark it done
contrabass board approve CB-1 --message "LGTM, merged manually"

# Reject an issue and return it to todo
contrabass board reject CB-1 --message "Needs tests for edge cases"

# Manually retry a failed or rejected issue
contrabass board retry CB-1
```

All state-transition commands fail fast if the issue is not in the expected state.

---

## Git Workflow

Commit after every working piece. Brief explanations:

```bash
git add .
git commit -m "Config parser now reads YAML front matter"
```

Git tracks decisions — use it to understand the codebase history.

---

## Docs and Specs

| Path | Purpose |
|------|---------|
| `docs/specs/orchestrator-owned-pipeline/` | Authoritative pipeline spec: stages, artifacts, events, lifecycle |
| `docs/context/what-contrabass-is.md` | High-level architecture and concepts |
| `docs/context/minimal-contrabass.md` | Implementation guide synced to the Go code |
| `docs/context/migration-from-single-agent.md` | What changed when we moved from single-stage to pipeline |
| `docs/remote-project-orchestration.md` | How to orchestrate remote projects |
| `docs/references/contrabass/` | Full Contrabass source (reference only) |

If you are starting a fresh session, read the spec first (`docs/specs/orchestrator-owned-pipeline/README.md`), then `docs/context/minimal-contrabass.md`.

## Rules

1. **No magic** — every function must have a clear purpose
2. **Test early, test often** — run the code as we build
3. **Contrabass is reference only** — understand, don't copy
4. **Minimal first** — get the core flow working before adding features
