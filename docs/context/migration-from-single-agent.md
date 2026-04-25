# Migration Notes: Single-Agent Flow → Orchestrator-Owned Pipeline

> What changed when we moved from a single implicit execute stage to the plan → execute → verify pipeline.

## Overview

Before the pipeline refactor, Contrabass-PI ran one agent session per issue: the orchestrator claimed an issue, built a prompt, started the agent, and when the agent finished it moved the issue to `in_review`. There were no explicit stage boundaries — everything happened in one opaque run.

After the refactor, the orchestrator owns a three-stage pipeline: **plan → execute → verify**. Each stage is a separate agent session with its own prompt, artifacts, and retry policy. The human review gate remains the same, but the evidence preserved for review is much richer.

## Behavioral Changes

### 1. From one stage to three stages

| Before | After |
|--------|-------|
| Single implicit "execute" stage | Explicit `plan` → `execute` → `verify` stages |
| One agent session per issue | Up to three agent sessions per issue (one per stage) |
| One prompt per issue | Stage-specific prompts (planning mode, execution mode, verification mode) |
| Success = agent finished without error | Success = all three stages pass |

### 2. Stage-scoped retry

| Before | After |
|--------|-------|
| Failure always restarted from scratch | Failure resumes from the **failed stage** |
| Retry discards previous attempt history | Each attempt preserves all stage artifacts |
| Backoff queue had no stage context | `BackoffEntry.Stage` records which stage to retry |

Example: if `execute` fails, the next attempt re-runs `execute` (and then `verify`), not `plan`.

### 3. Typed failures

| Before | After |
|--------|-------|
| Errors were opaque strings | Errors are classified into `StageFailureKind` |
| No distinction between transient and permanent failures | `Retryable` flag on `StageResult` indicates whether to retry |
| TUI showed "failed" with no detail | TUI shows failure kind (e.g., `tool_error`, `verification_failed`) |

Failure kinds: `prompt_error`, `session_start_error`, `timeout`, `stall`, `model_failure`, `workspace_error`, `tool_error`, `verification_failed`, `handoff_error`, `decision_missing`.

### 4. Artifact richness

| Before | After |
|--------|-------|
| `attempts/001/meta.json`, `prompt.md`, `stdout.log` | Plus `stages/plan/`, `stages/execute/`, `stages/verify/` directories |
| No diff captured per stage | `stages/execute/diff.patch` records the code change |
| No structured stage results | `stages/<stage>/result.json` with `status`, `summary`, `failure_kind`, `evidence` |
| Review handoff was a state transition | `review/handoff.md` is a durable, human-readable package |
| No explicit review decision record | `review/decision.json` stores `approved` / `rejected` / `needs_changes` |

### 5. Agent selection

| Before | After |
|--------|-------|
| One `opencode.agent` for all work | Per-stage agent mapping via `opencode.agents` |
| No way to use a planner agent for planning | `agents: { plan: "plan", execute: "build", verify: "review" }` |
| Agent name passed via config only | Agent name passed via `types.StageContext` in context |

### 6. Prompt construction

| Before | After |
|--------|-------|
| Single prompt from `WORKFLOW.md` template | Base prompt + stage-specific framing wrapper |
| Agent had to infer whether to plan or code | Prompt explicitly states "You are in PLANNING mode" / "EXECUTION mode" / "VERIFICATION mode" |

The pipeline runner (`internal/pipeline/runner.go`) wraps the base prompt:
- **plan**: "Analyze the following issue and produce a concrete implementation plan. Do NOT make any code changes yet."
- **execute**: "Make the necessary code changes to fulfill the requirements."
- **verify**: "Verify that the changes satisfy the issue. Provide a pass/fail assessment with evidence."

### 7. State tracking

| Before | After |
|--------|-------|
| `RunPhase` enum tracked agent lifecycle phases | `types.Stage` tracks pipeline progress |
| TUI showed `RunPhase` (e.g., `streaming_turn`) | TUI shows `Stage` (e.g., `Plan`, `Exec`, `Verify`) |
| `StateManager` tracked one phase per run | `StateManager.UpdateStage()` tracks current pipeline stage |

`RunPhase` still exists internally for agent lifecycle tracking, but the user-visible state is `types.Stage`.

### 8. Event taxonomy

| Before | After |
|--------|-------|
| `agent.started`, `agent.finished` | Plus `stage.started`, `stage.completed`, `stage.failed` |
| Events had no stage context | Stage events carry `stage`, `attempt`, `agent` fields |
| TUI event log showed raw event types | TUI formats events as `[plan] started`, `[execute] failed: tool_error` |

New events added:
- `stage.started`
- `stage.completed`
- `stage.failed`
- `issue.ready_for_review`

## Config Changes

### New field: `opencode.agents`

```yaml
# Before
opencode:
  agent: build

# After
opencode:
  agent: build           # fallback default
  agents:
    plan: plan           # agent for plan stage
    execute: build       # agent for execute stage
    verify: review       # agent for verify stage
```

If `opencode.agents` is omitted, the top-level `opencode.agent` is used for all stages.

### No breaking changes to existing fields

All existing config fields (`max_concurrency`, `poll_interval_ms`, `workspace.base_dir`, etc.) work exactly as before. The pipeline is a behavioral change, not a config-breaking change.

## Artifact Layout Changes

### Before (single stage)

```
runs/CB-1/attempts/001/
  meta.json
  prompt.md
  stdout.log
  stderr.log
  events.jsonl
  preflight/
  postflight/
```

### After (pipeline)

```
runs/CB-1/attempts/001/
  meta.json
  prompt.md
  stdout.log
  stderr.log
  events.jsonl
  preflight/
  postflight/
  stages/
    plan/
      manifest.json
      prompt.md
      response.md
      result.json
      events.jsonl
      stdout.log
      stderr.log
    execute/
      manifest.json
      prompt.md
      response.md
      result.json
      diff.patch
      events.jsonl
      stdout.log
      stderr.log
    verify/
      manifest.json
      prompt.md
      response.md
      result.json
      events.jsonl
      stdout.log
      stderr.log
  review/
    handoff.md
    decision.json
    notes.md
```

The old files (`meta.json`, `stdout.log`, etc.) at the attempt root still exist. The stage folders are additive.

## Code Changes

### New packages

| Package | Purpose |
|---------|---------|
| `internal/pipeline/` | `Runner.Run(ctx, issue, attempt, stage, emit)` — executes one stage |
| `internal/types/pipeline.go` | `Stage`, `StageManifest`, `StageResult`, `StageFailureKind`, `ReviewDecision`, etc. |
| `internal/types/context.go` | `StageContext` for passing stage + agent through `context.Context` |

### Modified packages

| Package | What changed |
|---------|-------------|
| `internal/orchestrator/orchestrator.go` | `startRun()` now loops over stages; `classifyStageFailure()` added; review handoff uses runner result |
| `internal/orchestrator/backoff.go` | `Enqueue()` accepts `types.Stage` for resume-from-stage |
| `internal/orchestrator/events.go` | Added `EventStageStarted`, `EventStageCompleted`, `EventStageFailed` with typed payloads |
| `internal/orchestrator/state.go` | `Add()` accepts `stage types.Stage`; `UpdateStage()` added |
| `internal/config/config.go` | Added `OpenCodeConfig.Agents` map and `AgentForStage()` method |
| `internal/diagnostics/stages.go` | New file: `StageRecorder`, `RecordReviewHandoff()`, `RecordReviewDecision()`, load helpers |
| `internal/agent/opencode.go` | Reads `StageContext` from context to select agent per stage |
| `internal/tui/model.go` | Handles stage events, shows stage in table, shows failure kind in backoff queue |

### Removed / deprecated

Nothing was removed. The single-stage behavior is replaced by the pipeline, but all types and interfaces remain compatible. `RunPhase` is still used internally for agent lifecycle tracking.

## Migration Checklist

If you have an existing Contrabass-PI setup:

- [ ] **Config**: Optionally add `opencode.agents` to use different agents per stage
- [ ] **Board states**: No change — `todo`, `in_progress`, `retry_queued`, `in_review`, `done` are the same
- [ ] **Issue JSON**: No change — issues are backward-compatible
- [ ] **Run records**: Old run records without stage folders are still valid; new runs will have stage folders
- [ ] **WORKFLOW.md templates**: No change — the pipeline wraps your template, it doesn't replace it
- [ ] **TUI**: No manual migration — the TUI adapts automatically to stage events

## Debugging After Migration

If a run looks wrong after the migration, use the new debugging order:

1. Open `summary.json` — check `current_stage`, `outcome`, `review_state`
2. Inspect `attempts/<NNN>/meta.json` — check `current_stage`, `error`
3. Inspect `attempts/<NNN>/stages/<stage>/manifest.json` — check `status`, `error_kind`, `retryable`
4. Read `stages/<stage>/prompt.md` and `response.md` — understand model vs orchestrator fault
5. Check `stages/<stage>/result.json` — structured outcome with `summary`, `failure_kind`, `evidence`
6. Read `attempts/<NNN>/events.jsonl` and `_orchestrator/events.jsonl`
7. Inspect workspace with `git status`, `git diff`, `git log`

See `docs/specs/orchestrator-owned-pipeline/debugging-playbook.md` for the full guide.

## Reference

- Spec: `docs/specs/orchestrator-owned-pipeline/`
- Implementation: `docs/context/minimal-contrabass.md`
- Old single-stage docs: preserved in git history (pre-pipeline commits)
