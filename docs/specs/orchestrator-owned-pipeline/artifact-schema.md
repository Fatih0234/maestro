# Artifact Schema

This document defines the durable file layout for the orchestrator-owned pipeline.

## Root layout

The run recorder lives beside the board directory:

```text
.contrabass/projects/<project>/runs/
  _orchestrator/
    events.jsonl
  CB-1/
    issue.json
    summary.json
    attempts/
      001/
        meta.json
        prompt.md
        preflight/
        stages/
        review/
        postflight/
```

## Canonical files

### `issue.json`

A snapshot of the tracker issue at run start.

Required fields:

- `id`
- `title`
- `description`
- `state`
- `labels`
- `created_at`
- `updated_at`

### `summary.json`

Issue-level summary used by the TUI and human reviewers.

Recommended fields:

- `issue_id`
- `title`
- `description`
- `labels`
- `issue_state`
- `run_dir`
- `issue_dir`
- `attempts`
- `current_attempt`
- `current_stage`
- `outcome`
- `branch`
- `workspace_path`
- `start_commit`
- `final_commit`
- `started_at`
- `finished_at`
- `updated_at`
- `last_error`
- `review_state`
- `reviewed_at`
- `reviewed_by`

### `attempts/<NNN>/meta.json`

Attempt-level metadata for one retry attempt.

Recommended fields:

- `issue_id`
- `issue_title`
- `attempt`
- `outcome`
- `current_stage`
- `branch`
- `workspace_path`
- `started_at`
- `ended_at`
- `updated_at`
- `pid`
- `session_id`
- `server_url`
- `start_commit`
- `final_commit`
- `error`
- `retry_at`
- `prompt_path`
- `events_path`
- `stdout_path`
- `stderr_path`

## Stage folders

Each attempt contains stage-specific folders:

```text
attempts/001/stages/
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
```

### `stages/<stage>/manifest.json`

The stage manifest is the most important per-stage control file.

Recommended fields:

- `stage`
- `attempt`
- `status`
- `agent`
- `session_id`
- `workspace_path`
- `prompt_path`
- `response_path`
- `result_path`
- `started_at`
- `finished_at`
- `error_kind`
- `retryable`

### `stages/<stage>/result.json`

The machine-readable outcome for a stage.

Recommended fields:

- `stage`
- `status`
- `summary`
- `failure_kind`
- `retryable`
- `evidence`
- `next_action`
- `started_at`
- `finished_at`

Example:

```json
{
  "stage": "verify",
  "status": "passed",
  "summary": "tests passed and diff is aligned with the plan",
  "failure_kind": null,
  "retryable": false,
  "evidence": ["go test ./...", "git diff --stat"],
  "next_action": "review",
  "started_at": "2026-04-23T13:00:00Z",
  "finished_at": "2026-04-23T13:02:10Z"
}
```

## Review handoff

The human review gate needs a durable handoff package:

```text
attempts/001/review/
  handoff.md
  decision.json
  notes.md
```

### `review/handoff.md`

A human-readable package that explains:

- what changed
- why it changed
- what was verified
- what to inspect in the workspace
- any remaining risks

### `review/decision.json`

A small, explicit decision record.

Recommended fields:

- `decision` — `approved`, `rejected`, or `needs_changes`
- `reviewed_by`
- `reviewed_at`
- `notes`
- `follow_up_state` — `done`, `retry_queued`, or `todo`

## Event logs

### `_orchestrator/events.jsonl`

Append-only event stream for the whole orchestrator.

### `attempts/<NNN>/events.jsonl`

Append-only event stream for one attempt.

These event logs are meant to be readable by both the TUI and humans.

## Workspace evidence

The following are useful but optional:

- `preflight/git-status.txt`
- `preflight/git-worktree-list.txt`
- `postflight/git-status.txt`
- `postflight/git-worktree-list.txt`
- `postflight/diff.stat`
- `postflight/diff.patch`
- `postflight/commit.txt`

## Schema rules

1. **Immutable after write**
   - once a stage artifact is written, it should not be mutated in place

2. **Append-only logs**
   - event files should only grow

3. **Paths are recorded, not inferred**
   - if a file matters to the run, record its path in `meta.json` or `manifest.json`

4. **Review artifacts are preserved**
   - human review is part of the run history, not a separate hidden process


