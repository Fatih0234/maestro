# Issue Lifecycle

This document describes the exact lifecycle of a single issue in the orchestrator-owned pipeline.

## Success path

```text
todo -> in_progress -> plan -> execute -> verify -> in_review -> done
```

## Step-by-step lifecycle

| Step | Board state | Pipeline state | Artifacts written | Events emitted | Human action |
|---|---|---|---|---|---|
| 1. issue is discovered | `todo` | none | `issue.json`, `summary.json` refreshed | `poll.started` | none |
| 2. issue is claimed | `in_progress` | `claiming` | attempt `meta.json` is created | `issue.claimed` | none |
| 3. workspace is prepared | `in_progress` | `plan` begins | `preflight/*`, `workspace` metadata | `workspace.created`, `stage.started(plan)` | none |
| 4. plan completes | `in_progress` | `plan` completed | `stages/plan/*` | `stage.completed(plan)` | none |
| 5. execution starts | `in_progress` | `execute` begins | `stages/execute/*` | `stage.started(execute)` | none |
| 6. execution completes | `in_progress` | `execute` completed | diff, logs, commit snapshot | `stage.completed(execute)` | none |
| 7. verification starts | `in_progress` | `verify` begins | `stages/verify/*` | `stage.started(verify)` | none |
| 8. verification passes | `in_progress` | `verify` completed | verification result artifacts | `stage.completed(verify)` | none |
| 9. review handoff is prepared | `in_review` | `human_review` gate | `review/handoff.md` and related files | `issue.ready_for_review`, `review.ready` | inspect the work |
| 10. human approves | `done` | complete | `review/decision.json` updated | `review.approved`, `issue.completed` | mark the issue done |

## What happens at `in_review`

Once verification passes:

- the orchestrator preserves the worktree
- the orchestrator preserves all run artifacts
- the issue is moved to `in_review`
- the TUI should show the issue in the review queue
- the human decides whether to mark the issue `done`

## If the human rejects the change

A rejection should not destroy evidence.

Typical outcomes:

- `retry_queued` if more work is required and the orchestrator should retry later
- `todo` if the work should be reopened as a fresh run
- `in_progress` if the human wants the current run to continue in place

The exact choice should be explicit in the review decision record.

## Failure branches

### Plan fails

- the attempt is marked failed
- a retry is queued if the failure is retryable
- the issue remains visible in the board and the run history

### Execute fails

- the workspace stays intact
- the attempt artifacts are preserved
- the issue is queued for retry if appropriate

### Verify fails

- the issue does **not** move to `in_review`
- the verification artifacts explain why the gate failed
- the orchestrator may queue a fix or retry depending on policy

## Why this lifecycle is useful

- the board reflects human-facing status
- the pipeline reflects machine-facing progress
- review is explicit, not implicit
- every transition can be explained from durable artifacts


