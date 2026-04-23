# Contrabass-PI

A minimal orchestrator for OpenCode coding agents. Poll a local board, create workspaces, dispatch agents, and monitor progress via TUI.

**Contrabass-PI runs here, but agents work on a remote project.**

## Architecture

```
┌─────────────────────────────────┐
│  Contrabass-PI (this directory) │  ← Orchestrator lives here
│  ├── Board                      │  ← Issue tracking here
│  ├── Run records                │  ← Persistent diagnostics
│  ├── Orchestrator               │  ← Manages everything
│  └── Agent runner               │  ← Spawns agents
└─────────────────────────────────┘
                │ "work in /remote/project"
                ▼
┌─────────────────────────────────┐
│  Remote Project                 │  ← Agents work here
│  ├── Git history                │  ← Commits go here
│  └── sibling worktree dir       │  ← e.g. ../<repo>.worktrees/CB-1
└─────────────────────────────────┘
```

Configure `WORKFLOW.md` to point `workspace.base_dir` at any project you want to work on.

## Lifecycle (human-review handoff)

Contrabass-PI separates **runtime completion** from **business completion**:

1. issue is claimed and run in an isolated worktree
2. agent finishes runtime execution
3. orchestrator marks issue as `in_review`
4. the worktree and run records are preserved for manual inspection
5. human merges and later moves the issue to `done`

By design, orchestrator success does **not** auto-merge, auto-cleanup, or auto-close the issue.

Ctrl+C and SIGTERM stop the orchestrator cleanly; shutdown is idempotent.

## Runtime records

For each managed project, persistent run diagnostics live under `.contrabass/projects/<project>/runs/`.
They include the orchestrator event stream, the issue and summary snapshots, and per-attempt prompt/output/git snapshots.

These records are part of the source of truth when reviewing a finished run.

## Overview

Inspired by [Contrabass](https://github.com/junhoyeo/contrabass), stripped to essentials:

| Component | Purpose |
|-----------|---------|
| Config parser | Read `WORKFLOW.md` with YAML front matter |
| Local board | File-based issue tracker |
| Run records | Persistent run diagnostics under `.contrabass/projects/<project>/runs/` |
| Workspace manager | Git worktree per issue (outside repo tree by default) |
| OpenCode runner | HTTP + SSE to communicate with the agent |
| Orchestrator | Poll → claim → dispatch → retry → hand off for review |
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
  diagnostics/          # Persistent run recorder
  orchestrator/         # Poll → claim → dispatch → retry
  tui/                  # Charm Bubble Tea UI
  types/                # Shared types
docs/
  context/              # Our implementation docs
  references/contrabass/ # Contrabass (reference only)
```

## Runtime and CLI

- `--config WORKFLOW.md` — workflow file to load (defaults to `WORKFLOW.md`)
- `--dry-run` — run exactly one poll cycle and exit
- `--no-tui` — run headless without Bubble Tea
- `--log-level debug|info|warn|error` — severity filter; invalid values exit with an error

---

## Git Workflow

Commit after every working piece. Brief explanations:

```bash
git add .
git commit -m "Config parser now reads YAML front matter"
```

Git tracks decisions — use it to understand the codebase history.

---

## Rules

1. **No magic** — every function must have a clear purpose
2. **Test early, test often** — run the code as we build
3. **Contrabass is reference only** — understand, don't copy
4. **Minimal first** — get the core flow working before adding features
