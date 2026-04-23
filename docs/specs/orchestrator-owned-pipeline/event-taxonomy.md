# Event Taxonomy

This document defines the orchestrator event vocabulary for the pipeline.

## Naming rules

- use lower-case dot-separated names
- prefer `subject.action` or `noun.verb` patterns
- include `stage`, `attempt`, `session_id`, and `workspace_path` in payloads when relevant
- agent runtime events should be translated into orchestrator events instead of being exposed raw

## Canonical event envelope

The orchestrator already uses a simple envelope:

```go
type OrchestratorEvent struct {
    Type      string
    IssueID   string
    Timestamp time.Time
    Payload   any
}
```

This spec keeps that shape and standardizes the event names.

## Control-plane events

| Event type | When emitted | Required payload fields |
|---|---|---|
| `poll.started` | The orchestrator begins a poll cycle | `poll_interval_ms`, `queue_depth`, `max_concurrency` |
| `poll.completed` | The poll cycle ends | `duration_ms`, `issues_seen` |
| `issue.claimed` | An issue is claimed for work | `attempt`, `issue_state` |
| `workspace.created` | A workspace/worktree is created or reused | `branch`, `workspace_path`, `reused` |
| `prompt.built` | A stage prompt is rendered | `stage`, `prompt_path`, `length` |
| `issue.ready_for_review` | Verification passed and the run is waiting on a human | `branch`, `workspace_path`, `summary_path` |
| `issue.completed` | The human marks the issue done | `reviewed_by`, `reviewed_at` |
| `issue.retry_queued` | The orchestrator queues a retry | `attempt`, `retry_at`, `failure_kind` |

## Stage events

| Event type | When emitted | Required payload fields |
|---|---|---|
| `stage.started` | A stage begins | `stage`, `attempt`, `agent` |
| `stage.progress` | A stage makes visible progress | `stage`, `message` |
| `stage.completed` | A stage finishes successfully | `stage`, `result_path`, `summary` |
| `stage.failed` | A stage fails | `stage`, `failure_kind`, `error`, `retryable` |
| `stage.skipped` | A stage is intentionally skipped | `stage`, `reason` |

Recommended stage names:

- `plan`
- `execute`
- `verify`
- `human_review`

## Agent runtime events

These are the low-level OpenCode-derived events translated into orchestrator-visible state.

| Event type | When emitted | Required payload fields |
|---|---|---|
| `agent.started` | The stage session has launched | `stage`, `pid`, `session_id` |
| `agent.output` | The agent produced a meaningful output chunk | `stage`, `text` |
| `agent.tokens_updated` | Token usage changed | `stage`, `tokens_in`, `tokens_out` |
| `agent.finished` | The agent session ended | `stage`, `success`, `error` |
| `agent.stalled` | The agent stopped making progress | `stage`, `last_event_age_ms` |
| `agent.timed_out` | The agent exceeded the timeout budget | `stage`, `elapsed_ms` |

## Review events

| Event type | When emitted | Required payload fields |
|---|---|---|
| `review.ready` | The run is ready for human inspection | `workspace_path`, `summary_path` |
| `review.approved` | A human approves the change | `reviewed_by`, `notes` |
| `review.rejected` | A human rejects the change | `reviewed_by`, `notes`, `follow_up_state` |

## Recovery and retry events

| Event type | When emitted | Required payload fields |
|---|---|---|
| `retry.due` | A retry becomes runnable | `attempt`, `retry_at` |
| `retry.skipped` | A retry is not dispatched | `reason` |
| `stall.detected` | A run is considered stalled | `stage`, `last_event_age_ms` |
| `timeout.detected` | A run exceeded the timeout budget | `stage`, `elapsed_ms` |
| `shutdown.started` | Shutdown begins | `reason` |
| `shutdown.completed` | Shutdown completes | `duration_ms` |

## Payload guidance

### For stage events

Always include:

- `stage`
- `attempt`
- `issue_id`
- `workspace_path` when available

### For agent events

Always include:

- `stage`
- `session_id`
- `pid` when available
- `server_url` when available

### For review events

Always include:

- `reviewed_by`
- `reviewed_at`
- `notes` or a link to the review note

## Why this taxonomy works

- it is readable in the TUI
- it maps cleanly to persisted artifacts
- it keeps agent noise separate from orchestrator state
- it supports retry, review, and resume flows without guesswork


