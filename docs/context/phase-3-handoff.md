# Hand-off: Phase 3 — Orchestrator Pipeline State Machine

> From: Phase 2 session (stage-aware OpenCode runtime)
> To: Phase 3 session (orchestrator pipeline state machine)
> Date: 2026-04-25

---

## What Phase 2 Accomplished

Phase 2 taught the OpenCode runner to serve different pipeline stages with stage-appropriate agent intent. The key commit is:

```
3e823a6 feat(pipeline,agent): stage-aware OpenCode runtime
```

### Changes made

| File | What changed |
|------|-------------|
| `internal/pipeline/runner.go` | `Run()` now accepts `types.Stage`. `buildStagePrompt()` wraps base prompt with stage-specific framing (plan/execute/verify). Integrates `StageRecorder` via context. `classifyFailure()` maps errors to typed `StageFailureKind`. |
| `internal/pipeline/runner_test.go` | 4 new tests covering stage prompts, stage recording, and agent-start failure artifact writing. |
| `internal/agent/opencode_test.go` | `TestOpenCodeRunner_ContextAgentOverridesDefault` verifies context-driven agent selection. |
| `internal/orchestrator/orchestrator.go` | One-line change: `runner.Run(..., types.StageExecute, ...)` — temporary until Phase 3 orchestrates the full loop. |

### Current behavior

The orchestrator still runs a **single implicit execute stage** per issue. It:
1. Claims the issue
2. Creates workspace
3. Runs `pipeline.Runner.Run(ctx, issue, attempt, StageExecute, emit)`
4. On success → moves issue to `in_review`
5. On failure → enqueues retry

The pipeline runner now supports stages, but the orchestrator doesn't call it multiple times yet.

---

## What Phase 3 Must Do

Read these spec docs **first**:
1. `docs/specs/orchestrator-owned-pipeline/lifecycle.md` — Success path, failure branches, what happens at `in_review`
2. `docs/specs/orchestrator-owned-pipeline/event-taxonomy.md` — Control-plane events, stage events, agent runtime events, review events
3. `docs/specs/orchestrator-owned-pipeline/artifact-schema.md` — Issue summary, attempt metadata, review handoff
4. `docs/specs/orchestrator-owned-pipeline/stage-contracts.md` — All stage contracts

### Core objective

Teach the orchestrator to run the full pipeline: **plan → execute → verify → human review**.

Each stage is a separate call to `pipeline.Runner.Run()`. If a stage fails, retry that stage (not the whole pipeline from scratch). If a stage passes, advance to the next stage.

### Expected lifecycle

```
todo -> in_progress -> plan -> execute -> verify -> in_review -> done
```

| Step | Board state | Pipeline action | Artifacts |
|------|-------------|-----------------|-----------|
| 1 | `todo` | Poll discovers issue | `issue.json`, `summary.json` |
| 2 | `in_progress` | Claim, create workspace, begin attempt | `meta.json`, `preflight/*` |
| 3 | `in_progress` | Run **plan** stage | `stages/plan/*` |
| 4 | `in_progress` | Run **execute** stage | `stages/execute/*` |
| 5 | `in_progress` | Run **verify** stage | `stages/verify/*` |
| 6 | `in_review` | All stages passed → hand off to human | `review/handoff.md` |
| 7 | `done` | Human approves | `review/decision.json` |

### Failure behavior

- **Plan fails** → retry plan (same attempt, or new attempt depending on policy)
- **Execute fails** → retry execute (workspace stays intact, attempt artifacts preserved)
- **Verify fails** → do NOT move to `in_review`. Retry or queue for retry.

---

## Key Code Areas to Modify

### 1. `internal/orchestrator/orchestrator.go` — Main loop

Current `startRun()` spawns one goroutine that calls `runner.Run(..., StageExecute, ...)`. You need to:

- Run plan first, check result
- If plan passes → run execute
- If execute passes → run verify
- If verify passes → move to `in_review`
- If any stage fails → classify failure, decide retry policy, update state

**Important**: The orchestrator currently cancels the run context on timeout/stall. With multi-stage runs, decide whether timeout is per-stage or per-pipeline. The spec suggests per-stage timeouts.

The `RunState` struct already has a `Stage` field (`types.Stage`). `StateManager.UpdateStage()` exists. Use these to track which stage is currently running.

### 2. `internal/orchestrator/state.go` — State tracking

`RunState` already tracks:
- `Phase` (RunPhase)
- `Stage` (types.Stage)
- `Attempt`
- `TokensIn/Out`

You may need to add:
- Per-stage start times for timeout tracking
- Stage completion tracking (which stages finished successfully)

### 3. `internal/pipeline/runner.go` — Already ready

`Runner.Run(ctx, issue, attempt, stage, emit)` works. You just need to call it 3 times in sequence.

Key note: `Workspace.Create()` is idempotent. Calling it before plan, execute, and verify is safe — it will reuse the same worktree.

### 4. `internal/diagnostics/stages.go` — Already ready

`AttemptRecorder.BeginStage()` and `StageRecorder.Finish()` are implemented. The orchestrator doesn't need to change recorder calls — the pipeline runner handles all stage recording internally.

### 5. `internal/config/config.go` — Agent selection per stage

`OpenCodeConfig.AgentForStage(stage)` already exists and returns the agent name for a given stage string. The orchestrator already uses this in `startRun()`:

```go
stageAgent := ""
if o.Config.OpenCode != nil {
    stageAgent = o.Config.OpenCode.AgentForStage(types.StageExecute.String())
}
runCtx = types.WithStage(runCtx, types.StageExecute, stageAgent)
```

You'll need to do this for each stage (plan, execute, verify) before calling `runner.Run()`.

---

## Event Emissions

The orchestrator should emit these events (see `internal/orchestrator/events.go` for existing constants):

| When | Event | Payload fields |
|------|-------|---------------|
| Stage starts | `stage.started` | `stage`, `attempt`, `agent` |
| Stage completes | `stage.completed` | `stage`, `result_path`, `summary` |
| Stage fails | `stage.failed` | `stage`, `failure_kind`, `error`, `retryable` |
| Review ready | `issue.ready_for_review` | `branch`, `workspace_path`, `summary_path` |

Some of these may already be emitted by the pipeline runner — check `pipeline.EventWorkspaceCreated`, `pipeline.EventPromptBuilt`, `pipeline.EventAgentStarted`, etc. The orchestrator wraps these.

---

## Retry Policy per Stage

From `stage-contracts.md`:

| Stage | Retryable? | Notes |
|-------|-----------|-------|
| Plan | Yes | Unless config/prompt error |
| Execute | Yes | Unless workspace error or persistent config problem |
| Verify | Yes | If due to changed code or flaky checks |

The current `BackoffManager` queues retries at the issue level. For Phase 3, decide:
- Do we retry the **same stage** within the same attempt?
- Or do we increment the attempt number and restart from plan?

**Recommendation**: For minimal first version, retry the **same stage** (don't restart from plan). Only increment attempt on repeated failures or if the workspace is corrupted.

---

## Testing Strategy

Follow `docs/specs/orchestrator-owned-pipeline/test-matrix.md`:

1. **Fake runner component tests** — Use `mockAgentRunner` (see `pipeline/runner_test.go`) to script:
   - plan → execute → verify happy path
   - plan failure → retry
   - execute failure → retry
   - verify failure → blocks review

2. **Filesystem integration tests** — Assert artifact layout:
   - `attempts/001/stages/plan/manifest.json` + `result.json`
   - `attempts/001/stages/execute/manifest.json` + `result.json` + `diff.patch`
   - `attempts/001/stages/verify/manifest.json` + `result.json`

3. **Orchestrator state tests** — `orchestrator_test.go` already has mocks. Extend to verify stage transitions.

---

## Gotchas & Context

1. **The orchestrator currently hardcodes `StageExecute`** in `startRun()`. This is your entry point — replace the single `runner.Run()` call with a stage loop.

2. **Timeout/stall detection** is currently pipeline-wide (from `runCtx` start). You may want per-stage timeouts. The config already has `AgentTimeoutMs` and `StallTimeoutMs`.

3. **Workspace is preserved after success** by design. The orchestrator does NOT auto-cleanup or auto-merge. Human review is a gate.

4. **The TUI displays `types.Stage`** (not `RunPhase`). This was done in pre-Phase-2 cleanup. TUI changes for Phase 3 should be minimal — just ensure stage transitions are visible.

5. **Recorder handles stage artifacts** — don't duplicate artifact writing in the orchestrator. The pipeline runner writes `stages/<stage>/manifest.json`, `result.json`, etc. The orchestrator should only update the tracker board state and emit events.

6. **Token tracking** is cumulative per issue run. `StateManager.UpdateTokens()` adds to existing counts. This is fine for multi-stage — tokens accumulate across plan + execute + verify.

---

## Suggested Implementation Order

1. Read all spec docs listed above
2. Modify `orchestrator.startRun()` to run stages sequentially instead of one execute call
3. Update `StateManager` if needed for per-stage tracking
4. Add orchestrator-level event emissions for `stage.started`, `stage.completed`, `stage.failed`
5. Wire retry logic per stage (start with: fail → backoff same stage)
6. Write tests:
   - Happy path: plan → execute → verify → in_review
   - Plan failure → retry plan
   - Execute failure → retry execute
   - Verify failure → no review handoff
7. Run `go test ./...` until clean
8. Commit with Conventional Commits style

---

## Files to Touch (likely)

- `internal/orchestrator/orchestrator.go` — main loop and stage orchestration
- `internal/orchestrator/state.go` — possibly extend RunState for stage tracking
- `internal/orchestrator/events.go` — possibly add new event type constants
- `internal/orchestrator/orchestrator_test.go` — stage transition tests
- `internal/pipeline/runner.go` — possibly minor tweaks (already mostly ready)
- `internal/tui/model.go` / `table.go` — ensure stage visibility (Phase 4 concern, but check)

---

## Verification Command

```bash
go test ./...
```

All tests must pass before claiming Phase 3 complete.

---

## Reference Commit

```bash
git show 3e823a6 --stat
```

This is the Phase 2 commit. Use it to understand the current runner API if anything is unclear.
