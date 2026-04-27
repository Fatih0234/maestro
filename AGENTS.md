# Minimal Contrabass — Agent Context

> A minimal orchestrator for OpenCode coding agents. Poll a local board, create git worktree workspaces, dispatch agents through a plan-execute-verify pipeline, and monitor progress via Charm Bubble Tea TUI.

## Current Status

This is a **working implementation**, not a from-scratch build. All core phases are complete: config parser, local board tracker, git worktree workspaces, OpenCode agent runner (HTTP + SSE), plan-execute-verify pipeline runner, orchestrator with stage-scoped retry/backoff/stall detection, persistent diagnostics recorder with full artifact tree, and TUI with running table, review queue, backoff queue, and event log.

**Deferred / out of scope:** multi-agent teams, external trackers (GitHub/Linear), web dashboard, other agent types (Codex/OMC), config hot-reload, auto-merge.

For exhaustive system detail, see [PROJECT_DIGEST.md](./PROJECT_DIGEST.md). For architecture overview, see [docs/context/what-contrabass-is.md](./docs/context/what-contrabass-is.md). For implementation guide mapped to code phases, see [docs/context/minimal-contrabass.md](./docs/context/minimal-contrabass.md).

## Architecture — 5 Packages to Know

| Package | What it does | Key files |
|---------|-------------|-----------|
| `internal/types/` | Core data types and interfaces. Zero internal deps — everything depends on this. | `types.go`, `pipeline.go`, `context.go` |
| `internal/orchestrator/` | The brain: poll loop, dispatch, stage sequence, timeout/stall detection, backoff. | `orchestrator.go`, `state.go`, `backoff.go`, `events.go` |
| `internal/pipeline/` | Runs a single stage end-to-end: workspace, prompt, agent, artifacts. Called once per stage by the orchestrator. | `runner.go` |
| `internal/agent/` | OpenCode runner: manages `opencode serve` process, HTTP sessions, SSE event streaming. Fake runner for tests. | `opencode.go`, `events.go`, `fakerunner.go` |
| `internal/diagnostics/` | Persistent run records: every attempt, every stage, every event written to disk as JSON + markdown. | `recorder.go`, `stages.go` |

Supporting packages: `config/` (WORKFLOW.md YAML front-matter parser), `tracker/` (local file-based board), `workspace/` (git worktree manager), `tui/` (Bubble Tea UI), `util/` (string utilities, prompt template expansion).

## Key Design Decisions

- **No auto-merge, no auto-cleanup.** The orchestrator runs plan→execute→verify, then moves the issue to `in_review` and stops. The human inspects the worktree, then approves (`done`) or rejects (`todo`) via CLI.
- **Stage-scoped retry.** If `verify` fails, only `verify` is retried — not the whole pipeline. Exponential backoff with ±20% jitter.
- **Everything persisted.** Every run creates a full artifact tree under `.contrabass/projects/<name>/runs/` including preflight git state, per-stage manifests/results/diffs, postflight, and review handoff/decision.
- **Sibling worktrees.** Workspaces live outside the repo tree (`../<repo>.worktrees/CB-1/`) to keep the main repo clean.
- **Per-stage agent selection.** `opencode.agents.plan`, `.execute`, `.verify` can point to different OpenCode agent profiles.
- **Two extensibility interfaces:** `IssueTracker` and `AgentRunner`. Currently only `LocalTracker` and `OpenCodeRunner` are implemented, but swapping in GitHub Issues or another agent requires no orchestrator changes.

## Pipeline

```
todo → in_progress → plan → execute → verify → in_review → done
                           ↑          │          │
                           └─ retry ──┘          │
                                      └─ reject─┘
```

## CLI Commands

| Command | Description |
|---------|-------------|
| `contrabass init` | Set up WORKFLOW.md + `.contrabass/` board in current directory |
| `contrabass` | Start orchestrator (TUI mode, auto-discovers WORKFLOW.md) |
| `contrabass --no-tui` | Headless mode |
| `contrabass --dry-run` | Single poll cycle, then exit |
| `contrabass --log-level debug` | Verbose logging |
| `contrabass board create "title"` | Add an issue |
| `contrabass board list --all` | List all issues |
| `contrabass board show CB-1` | Show issue details + review handoff |
| `contrabass board approve CB-1 --message "LGTM"` | Mark done |
| `contrabass board reject CB-1 --message "why"` | Return to todo |
| `contrabass board retry CB-1` | Force retry immediately |
| `contrabass doctor` | Environment diagnostics |

## Working Conventions

### When changing code:
1. **Always check `./docs/references/contrabass`** — the full Contrabass implementation is the reference. We are building a minimal version, but alignment with the reference design prevents drift.
2. **Follow the simplification mandate** — prefer deep modules, hide implementation details, pull complexity downward, split with caution, use precise names, kill dead code, navigate shallowly (no `a.getB().getC().doThing()`).
3. **Tests must pass before and after.** Run `make test` or `go test ./...`. The fake runner provides deterministic scripted agent behavior for orchestrator and pipeline tests.
4. **Use `edit` for surgical changes** — old text must match exactly. Use `write` only for new files or complete rewrites.
5. **Git workflow is a deliberate side-goal** — practice meaningful commits with clear, conventional messages (`feat(...)`, `fix(...)`, `refactor(...)`, `chore(...)`).

### Testing
```bash
make test      # go test ./...
make build     # builds ./contrabass
make install   # go install to $GOPATH/bin
make clean     # remove binary
```

## CI

`.github/workflows/ci.yml` runs `go test ./...` and `go build ./cmd/contrabass` on every push and pull request. This is standard GitHub Actions configuration and must remain tracked in git.
