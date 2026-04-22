# Contrabass-PI

A minimal orchestrator for OpenCode coding agents. Poll a local board, create workspaces, dispatch agents, and monitor progress via TUI.

**Contrabass-PI runs here, but agents work on a remote project.**

## Architecture

```
┌─────────────────────────────────┐
│  Contrabass-PI (this directory) │  ← Orchestrator lives here
│  ├── Board                      │  ← Issue tracking here
│  ├── Orchestrator               │  ← Manages everything
│  └── Agent runner               │  ← Spawns agents
└─────────────────────────────────┘
                │ "work in /remote/project"
                ▼
┌─────────────────────────────────┐
│  Remote Project                 │  ← Agents work here
│  ├── Git history                │  ← Commits go here
│  └── workspaces/CB-1/           │  ← Agent worktrees
└─────────────────────────────────┘
```

Configure `WORKFLOW.md` to point `workspace.base_dir` at any project you want to work on.

## Overview

Inspired by [Contrabass](https://github.com/junhoyeo/contrabass), stripped to essentials:

| Component | Purpose |
|-----------|---------|
| Config parser | Read `WORKFLOW.md` with YAML front matter |
| Local board | File-based issue tracker |
| Workspace manager | Git worktree per issue |
| OpenCode runner | HTTP + SSE to communicate with the agent |
| Orchestrator | Poll → claim → dispatch → retry loop |
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
  orchestrator/         # Poll → claim → dispatch → retry
  tui/                  # Charm Bubble Tea UI
  types/                # Shared types
docs/
  context/              # Our implementation docs
  references/contrabass/ # Contrabass (reference only)
```

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