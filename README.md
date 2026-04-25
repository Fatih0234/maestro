# Contrabass-PI

A minimal orchestrator for OpenCode coding agents. Poll a local board, create workspaces, dispatch agents, and monitor progress via TUI.

**Contrabass-PI runs here, but agents work on a remote project.**

## Architecture

```
┌─────────────────────────────────┐
│  Contrabass-PI (this directory) │  ← Orchestrator lives here
│  ├── Board                      │  ← Issue tracking here
│  ├── Run records                │  ← Persistent diagnostics
│  ├── Orchestrator               │  ← Owns plan → execute → verify
│  └── Agent runner               │  ← Spawns agents per stage
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

- `--config WORKFLOW.md` — workflow file to load (defaults to `WORKFLOW.md`)
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
  board_dir: .contrabass/orchestrator/board
  issue_prefix: CB

agent:
  type: opencode

opencode:
  binary_path: opencode serve
  profile: ws

workspace:
  base_dir: /path/to/project
  branch_prefix: opencode/
---

# Prompt template

Implement the following issue: {{ issue.title }}

{{ issue.description }}
```

### GitHub Issues tracker

```yaml
---
max_concurrency: 3
poll_interval_ms: 30000

tracker:
  type: github
  owner: my-org
  repo: my-repo
  token: ghp_xxxxxxxxxxxx
  label_prefix: contrabass
  assignee_bot: contrabass-bot

agent:
  type: opencode

opencode:
  binary_path: opencode serve
  profile: ws

workspace:
  base_dir: /path/to/project
  branch_prefix: opencode/
---

# Prompt template

Implement the following issue: {{ issue.title }}

{{ issue.description }}
```

When using the GitHub tracker:
- Open issues with no assignee are picked up as new work
- The bot claims issues by assigning itself
- `in_review` adds a `contrabass:review` label and posts a handoff comment
- `done` closes the issue
- Retry-queued issues are skipped until their backoff time passes

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
