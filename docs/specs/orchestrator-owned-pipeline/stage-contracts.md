# Stage Contracts

This document defines the contract for each pipeline stage in the orchestrator-owned model.

## Contract shape

Every stage must define:

- **Purpose** — why the stage exists
- **Inputs** — what the stage may read
- **Outputs** — what the stage must write
- **Success criteria** — what counts as success
- **Failure kinds** — how the orchestrator classifies failures
- **Retry policy** — whether the stage can be rerun

## Common invariants

These apply to all stages:

1. **Inputs are persisted before the stage starts**
   - prompt, issue snapshot, workspace path, and stage metadata must be written to disk

2. **Outputs are persisted before state transitions**
   - a stage is not considered complete until its artifact files are written

3. **Stage boundaries are explicit**
   - one stage should not depend on hidden context from a later stage

4. **Stage results are replayable**
   - given the same inputs, a stage should be rerunnable without ambiguity

5. **Failures are typed**
   - the orchestrator must know whether a failure is transient, workspace-related, execution-related, verification-related, or review-related

## Stage 1: Plan

### Purpose

Turn an issue into a concrete implementation plan before any edits are made. This stage uses a read-only OpenCode session.

### Inputs

- issue snapshot
- workflow prompt body
- current board state
- workspace path
- previous attempt summary, if any
- previous failure notes, if any

### Output

A plan artifact that answers:

- what should change
- which files are likely to change
- what risks exist
- how success will be verified
- what the next stage should do

### Success criteria

- plan artifact exists on disk
- plan output is readable and structured
- the plan includes implementation steps and validation steps
- no workspace mutation is required for success

### Failure kinds

- `prompt_error` — workflow or prompt rendering failed
- `session_start_error` — the OpenCode session could not start
- `timeout` — stage exceeded the allowed time
- `stall` — no useful progress or agent output was observed
- `model_failure` — the model returned an unusable plan

### Retry policy

Plan is retryable unless the failure indicates a permanent configuration or prompt problem.

## Stage 2: Execute

### Purpose

Apply the implementation plan in the workspace using a write-capable OpenCode session.

### Inputs

- issue snapshot
- plan artifact
- workspace path
- prompt body
- stage-specific agent configuration
- previous attempt artifacts

### Output

An execution artifact set that should include:

- code changes in the workspace
- logs for the session
- diff or commit snapshot
- a concise result summary

### Success criteria

- workspace changes exist and are attributable to the attempt
- the stage produces a clear summary of what changed
- artifact files capture the runtime and the resulting diff
- the workspace remains intact for verification and human review

### Failure kinds

- `workspace_error` — worktree creation or access failed
- `session_start_error` — the agent could not start
- `tool_error` — the agent could not perform required work
- `timeout` — execution took too long
- `stall` — the agent stopped making progress

### Retry policy

Retryable unless the failure is caused by an invalid workspace or a persistent configuration problem.

## Stage 3: Verify

### Purpose

Confirm that the change satisfies the issue and the execution plan using a reviewer-style OpenCode session plus any deterministic checks the workflow requires.

### Inputs

- issue snapshot
- plan artifact
- execution diff or commit snapshot
- test or check commands
- workspace path

### Output

A verification artifact that records:

- pass/fail status
- checks that were run
- evidence collected
- any remaining concerns
- whether the issue should be handed to human review

### Success criteria

- verification explicitly passes
- evidence is recorded
- the result is reproducible from the saved artifacts
- the run is ready to move to `in_review`

### Failure kinds

- `verification_failed` — checks failed or the result is incomplete
- `tool_error` — verification tooling failed
- `timeout` — verification took too long
- `stall` — verification stopped progressing

### Retry policy

Usually retryable if the failure is due to the changed code or flaky checks.

## Stage 4: Human review gate

### Purpose

Transfer a verified run to a human for final approval.

### Inputs

- issue summary
- plan artifact
- execution diff
- verification artifact
- review handoff note

### Output

One of:

- `approved` — human accepts the work and marks the issue done
- `rejected` — human wants more work
- `needs_changes` — human wants the issue reopened or retried

### Success criteria

- the human can understand the change without additional context
- the review handoff is enough to make a decision
- the board state is updated explicitly by the human or by the human-review flow

### Failure kinds

- `handoff_error` — the review package could not be generated
- `decision_missing` — the issue reached review but no decision was recorded

### Retry policy

Not an agent retry. This stage is a human gate.

## Stage result schema

Every stage should produce a result document with at least:

```json
{
  "stage": "plan",
  "status": "passed",
  "summary": "short human-readable summary",
  "failure_kind": null,
  "retryable": false,
  "started_at": "2026-04-23T13:00:00Z",
  "finished_at": "2026-04-23T13:01:00Z"
}
```

Recommended status values:

- `passed`
- `failed`
- `blocked`
- `retrying`
- `skipped`


