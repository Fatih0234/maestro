# Pre-Phase-2 Code Review

Date: 2026-04-25
Scope: Full codebase review before starting orchestrator-owned pipeline Phase 2
Criteria:
1. Simplify while keeping the same features
2. Add structural complexity that makes Phase 2–5 significantly easier

---

## Executive Summary

The codebase is in good shape. Phase 1 of the orchestrator-owned pipeline produced a solid
foundation: typed stage contracts, a durable artifact schema, and a recorder that can reconstruct
state from disk. The test suite passes and the existing single-agent flow works.

However, there are **five specific simplifications** and **four structural improvements** that
should be addressed before wiring stage-aware runtime. Doing them now prevents tech debt from
compounding when the orchestrator grows from "one monolithic run" to "plan → execute → verify".

---

## Part A: What's Solid (Do Not Touch)

| Component | Verdict | Why |
|---|---|---|
| `internal/types/pipeline.go` | ✅ Excellent | Stage, StageState, StageFailureKind, ReviewDecision are well-typed and JSON-stable. |
| `internal/diagnostics/recorder.go` + `stages.go` | ✅ Excellent | Atomic writes, directory layout matches spec, read helpers for reconstruction. |
| `internal/tracker/local.go` | ✅ Solid | File-based board with manifest, proper locking, state conversions. |
| `internal/workspace/manager.go` | ✅ Solid | Git worktree logic, branch reuse, idempotent create, cleanup. |
| `internal/config/config.go` | ✅ Clean | Front-matter parser, validation, path resolution. |
| `internal/agent/opencode.go` | ✅ Good | SSE parsing, server lifecycle, session management. |
| Tests | ✅ Good | Component tests with fakes, filesystem assertions, deterministic. |

---

## Part B: Simplifications (Same Features, Less Complexity)

### B1. `RunPhase` vs `Stage` — The TUI Shows the Wrong Abstraction

**Problem:** The TUI (`internal/tui/model.go`, `table.go`) displays `types.RunPhase`
(`PreparingWorkspace`, `LaunchingAgentProcess`, `StreamingTurn`, etc.). These are
*agent implementation details*, not *pipeline progress*. The user cares about
`plan` → `execute` → `verify`, not whether the agent is initializing its session.

**Impact:** When Phase 2 introduces stages, the TUI will need to display `Stage` values.
Keeping `RunPhase` in the TUI model means maintaining two parallel display systems.

**Fix:** Replace `Phase types.RunPhase` with `Stage types.Stage` in TUI row structs.
`RunPhase` can remain in `RunState` for internal agent monitoring, but the TUI should
consume `stage.started` / `stage.completed` events and show pipeline stages.

**Files:** `internal/tui/model.go`, `internal/tui/table.go`

---

### B2. Duplicated `sanitizeBranchName`

**Problem:** `sanitizeBranchName` exists in both:
- `internal/orchestrator/orchestrator.go`
- `internal/workspace/manager.go`

They are identical. `sanitizeFileName` also exists only in workspace but the orchestrator
does its own sanitization implicitly via `sanitizeBranchName`.

**Fix:** Move both to `internal/types` or a small `internal/util` package. The orchestrator
should not reimplement workspace naming rules.

**Files:** `internal/orchestrator/orchestrator.go`, `internal/workspace/manager.go`

---

### B3. `workspaceLocator` Interface Hack

**Problem:** `WorkspaceManager` interface defines `Create`, `Cleanup`, `MergeToMain`.
But the orchestrator needs `Path(issueID)` and `BaseDir()` for git state capture.
Instead of adding them to the interface, the orchestrator does a runtime type assertion:

```go
type workspaceLocator interface {
    Path(issueID string) string
    BaseDir() string
}
```

This is unnecessary indirection. Every workspace implementation will have a path concept.

**Fix:** Add `Path(issueID string) string` and `BaseDir() string` to `WorkspaceManager`.
Remove the `workspaceLocator` interface and the `o.Workspace.(workspaceLocator)` assertions.

**Files:** `internal/workspace/manager.go`, `internal/orchestrator/orchestrator.go`

---

### B4. `dispatchBackoff` Resets Retry Times at Capacity

**Problem:** In `orchestrator.go`:

```go
for _, entry := range o.Backoff.Ready() {
    if o.State.Len() >= o.Config.MaxConcurrency {
        o.Backoff.Enqueue(entry.IssueID, entry.Attempt, entry.Error)
        return
    }
    o.Backoff.Remove(entry.IssueID)
    // ... dispatch
}
```

When at capacity, the code **re-enqueues** the backoff entry. `Enqueue` recalculates the
retry delay from scratch, so a ready issue that should retry in 0s gets bumped to 30s/60s/etc.

**Fix:** If at capacity, `continue` or `break` without re-enqueueing. The entry is already
in the backoff map with its original `RetryAt`. It will be ready again on the next poll.

**Files:** `internal/orchestrator/orchestrator.go`

---

### B5. `handleAgentDone` Duplicates Postflight Logic

**Problem:** Success path, failure path, timeout path, and stall path all repeat the same
sequence:
1. `captureGitState(o.workspaceBaseDir())`
2. `captureCommit(o.ctx, o.workspacePath(issueID))`
3. `o.Recorder.FinalizeAttempt(...)`
4. Emit events

**Fix:** Extract a `finalizeRun(issueID, attempt, outcome, runErr)` helper that handles
the common postflight + recorder + event emission. The individual handlers should only
do what is unique to their path (e.g., moving board state to `in_review` on success).

**Files:** `internal/orchestrator/orchestrator.go`

---

## Part C: Structural Improvements (Worth the Complexity)

### C1. Config Needs Per-Stage Agent Selection

**Problem:** `OpenCodeConfig` has one `Agent` string. The pipeline spec requires different
agents for different stages:
- `plan` → "plan" agent (read-only, reasoning-focused)
- `execute` → "coder" agent (write-capable)
- `verify` → "scribe" or "reviewer" agent (validation-focused)

**Fix:** Add a stage-to-agent mapping in config:

```go
type OpenCodeConfig struct {
    // ... existing fields ...
    Agents map[string]string `yaml:"agents"` // stage -> agent name
}
```

With YAML like:
```yaml
opencode:
  agents:
    plan: plan
    execute: coder
    verify: scribe
```

Fallback to the top-level `agent` field if a stage is not mapped.

**Files:** `internal/config/config.go`, `WORKFLOW.md` docs

---

### C2. `AgentRunner` Needs Stage Context

**Problem:** Current signature:
```go
Start(ctx context.Context, issue Issue, workspace, prompt string) (*AgentProcess, error)
```

There is no way to tell the runner "this is the plan stage, use the plan agent".
The runner currently reads `r.agent` (a single string) from its own struct.

**Fix:** Two options, ranked by preference:

**Option A (Preferred):** Pass stage context via the existing context using a value key,
similar to how `AttemptRecorder` is passed:

```go
type stageContextKey struct{}

func WithStage(ctx context.Context, stage types.Stage, agent string) context.Context {
    return context.WithValue(ctx, stageContextKey{}, StageContext{Stage: stage, Agent: agent})
}
```

The runner reads `StageFromContext(ctx)` and selects the appropriate agent/profile.
This avoids changing the `AgentRunner` interface, which keeps all existing tests and mocks valid.

**Option B:** Extend the interface:
```go
type StageAgentRunner interface {
    AgentRunner
    StartStage(ctx context.Context, stage types.Stage, issue Issue, workspace, prompt string) (*AgentProcess, error)
}
```

Option A is simpler and keeps the interface stable.

**Files:** `internal/types/types.go`, `internal/agent/opencode.go`

---

### C3. Orchestrator is Too Large — Introduce `PipelineRunner`

**Problem:** `orchestrator.go` is ~550 lines and does everything: polling, dispatch,
backoff, timeout/stall detection, prompt building, agent monitoring, event emission,
git capture. When Phase 3 adds explicit `plan → execute → verify` transitions, this file
will become unmaintainable.

**Fix:** Extract a `PipelineRunner` (or `IssuePipeline`) that owns the lifecycle of ONE
issue from claim through review. The orchestrator stays responsible for:
- Polling the tracker
- Concurrency limits
- Backoff queue management
- Delegating to `PipelineRunner.Run(ctx, issue, attempt)`

The `PipelineRunner` handles:
- Workspace creation
- Stage loop (plan → execute → verify)
- Stage-scoped retry decisions
- Review handoff generation
- Finalizing the attempt

This is the most important structural change. It makes the orchestrator a thin coordinator
and keeps stage logic in one place.

**Suggested package:** `internal/pipeline/runner.go`

**Files:** New `internal/pipeline/`, `internal/orchestrator/orchestrator.go`

---

### C4. `RunState` Should Track `CurrentStage`

**Problem:** `RunState` tracks `Phase types.RunPhase` but not `CurrentStage types.Stage`.
When the orchestrator is running the plan stage, the state manager has no way to answer
"what pipeline stage is CB-1 in?"

**Fix:** Add `Stage types.Stage` to `RunState`. Update it when stage transitions happen.
This lets `reconcileRunning` emit `stage.failed` instead of generic `agent.finished`, and
lets the TUI display the correct stage without guessing.

**Files:** `internal/orchestrator/state.go`, `internal/orchestrator/orchestrator.go`

---

## Part D: Minor Issues (Nice to Fix)

| Issue | Location | Severity |
|---|---|---|
| `extractTextContent` is in orchestrator but parses agent events | `orchestrator.go` | Low — move to `agent/events.go` |
| `orchestrator.go` imports `agent` only for `agent.ExtractTokens` | `orchestrator.go` | Low — the event should carry token deltas |
| `MergeToMain` runs `git checkout main` in base dir, which modifies the orchestrator's own working tree if base_dir points at the orchestrator repo | `workspace/manager.go` | Medium — this was noted in previous debugging. For remote projects it's fine, but the method name is misleading if it mutates the orchestrator's own repo. Consider `MergeBranch(ctx, targetBranch, sourceBranch)` instead. |
| `MockTracker.FetchIssues` has its own filtering logic that duplicates tracker logic | `mock_test.go` | Low — tests pass, but the mock drifts from real behavior over time |

---

## Part E: Recommended Order of Work

If you agree with this review, the cleanup can be done in two short PRs before Phase 2:

### PR 1: Simplifications (no behavior change)
1. Move `sanitizeBranchName` to shared location, remove duplication
2. Add `Path`/`BaseDir` to `WorkspaceManager` interface, remove `workspaceLocator`
3. Fix `dispatchBackoff` capacity bug
4. Refactor `handleAgentDone` duplicated postflight into helper
5. Replace `RunPhase` display with `Stage` in TUI (event consumers only)

### PR 2: Structural Foundation for Phase 2
1. Add per-stage agent config to `OpenCodeConfig`
2. Add `Stage` to `RunState`
3. Introduce `internal/pipeline/` package with `PipelineRunner` scaffold
4. Wire stage context through `context.Context` to agent runner

After these two PRs, Phase 2 (stage-aware OpenCode runtime) and Phase 3 (orchestrator state
machine) become straightforward incremental additions instead of messy refactors.

---

## Verdict

The codebase does **not** need a rewrite. It needs:
- **~100 lines of cleanup** (Part B)
- **~200 lines of structural scaffolding** (Part C)

Then Phase 2–5 can be built on top without fighting the existing architecture.
