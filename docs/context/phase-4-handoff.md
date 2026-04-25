# Hand-off: Phase 4 — TUI Visibility and Review Queue

> From: Phase 3 session (orchestrator pipeline state machine + post-review fixes)
> To: Phase 4 session (TUI visibility and review queue)
> Date: 2026-04-25

---

## What Phase 3 Accomplished

Phase 3 taught the orchestrator to run the full pipeline: **plan → execute → verify → human review**. Key commits:

```
f529156 feat(orchestrator): pipeline state machine with plan → execute → verify
9faf6cd fix(orchestrator): clean up context cancellation, classify failures, and use runner result for handoff
```

### Changes made

| File | What changed |
|------|-------------|
| `internal/orchestrator/orchestrator.go` | `startRun()` now runs stages sequentially (plan → execute → verify). Stage-aware retry: fails resume from the failed stage, not from plan. Context cancellation between stages cleans up state properly. `classifyStageFailure()` checks context errors and error message content before falling back to stage-based classification. Review handoff uses the last stage's runner result for branch/workspace path. |
| `internal/orchestrator/events.go` | Added `EventStageStarted`, `EventStageCompleted`, `EventStageFailed` with typed payloads (`StageStartedPayload`, `StageCompletedPayload`, `StageFailedPayload`). |
| `internal/orchestrator/backoff.go` | `Enqueue()` now accepts `types.Stage` so retries resume from the correct stage. |
| `internal/orchestrator/state.go` | `StateManager.Add()` now accepts `stage types.Stage` instead of hardcoding `StageExecute`. |
| `internal/orchestrator/orchestrator_test.go` | Added `MultiStage_HappyPath`, `MultiStage_PlanFailureRetriesPlan`, `MultiStage_ExecuteFailureRetriesExecute`, `MultiStage_VerifyFailureBlocksReview`. |
| `internal/tui/model.go` | TUI already handles stage events: `EventStageStarted` updates `Stage` and `Status="running"`, `EventStageCompleted` updates `LastEvent`, `EventStageFailed` updates `Status="failed"`. |

### Current behavior

The orchestrator:
1. Claims an issue and creates a workspace
2. Runs **plan** stage via `pipeline.Runner.Run(..., StagePlan, ...)`
3. If plan passes → runs **execute** stage
4. If execute passes → runs **verify** stage
5. If verify passes → moves issue to `in_review`, emits `EventIssueReadyForReview`
6. If any stage fails → enqueues retry for that specific stage, emits `EventStageFailed` + `EventIssueRetrying`

All tests pass (`go test ./...` ✅).

---

## What Phase 4 Must Do

Read these spec docs **first**:
1. `docs/specs/orchestrator-owned-pipeline/event-taxonomy.md` — Event names, required payload fields, payload guidance
2. `docs/specs/orchestrator-owned-pipeline/lifecycle.md` — What happens at `in_review`, human rejection flow
3. `docs/specs/orchestrator-owned-pipeline/artifact-schema.md` — `summary.json`, review handoff, `review/decision.json`

### Core objective

Make the TUI a first-class visibility tool for the pipeline. A human should be able to glance at the TUI and understand:
- Which issues are running and **which stage** they're on
- Which issues are **ready for review** and where to find them
- Which issues are **queued for retry** and why
- A clear **event log** of stage transitions

### Expected TUI sections

The TUI already has four sections (see `internal/tui/model.go`). Phase 4 should enhance them:

#### 1. Running agents table (`renderTable`)

Already exists. Needs enhancement:
- Show **attempt number** in the table (currently only in `AgentRow.Attempt`, not rendered)
- Show **stage** as a compact label (already partially done — `compactStage()` exists and table renders `Stage`)
- Show **status glyph** per stage (running=●, done=✓, failed=✗, timeout=⏱, stalled=⚠)

Current table columns: `Issue`, `Title`, `Phase`, `PID`, `Tokens`, `Last` (age). Note the column header says "Phase" but actually renders `Stage`.

#### 2. Review queue (`renderReviewQueue`)

Already exists. Needs enhancement:
- Show **how long** the issue has been waiting for review
- Show **which stages completed** (plan/execute/verify summary)
- Show **failure reason** if the issue was rejected and is back in the queue

Current output:
```
Ready for Human Review                             [1]
  CB-1  branch=opencode/CB-1
      /path/to/workspace
```

#### 3. Backoff queue (`renderBackoffQueue`)

Already exists. Needs enhancement:
- Show **which stage failed** (e.g., "execute failed: tool error")
- Show **ETA** with countdown (already partially done — `RetryIn` updates every second)
- Show **failure kind** if available

Current output:
```
Backoff Queue                                      [1]
  CB-1  retry in 2m15s (attempt 2)
```

#### 4. Event log (`renderEventLog`)

Already exists. Needs enhancement:
- Make stage transitions **readable** (e.g., "CB-1 plan started" instead of "stage.started")
- Include **stage name** in agent events (currently only shows event type)
- Color-code events by severity

Current output:
```
  [Events]
  12:14:41 CB-1 stage.started
  12:14:42 CB-1 agent.started
  12:14:43 CB-1 tokens.updated
```

---

## Key Code Areas to Modify

### 1. `internal/tui/model.go` — Main TUI model

Current state maps:
```go
agents   map[string]AgentRow
reviews  map[string]ReviewRow
backoffs map[string]BackoffRow
```

You may want to add fields to these structs for richer display:

**AgentRow** already has:
- `IssueID`, `Title`, `Stage`, `Status`, `PID`, `Age`, `TokensIn/Out`, `SessionID`, `LastEvent`, `StartTime`, `Attempt`

**BackoffRow** currently has:
- `IssueID`, `Attempt`, `RetryIn`, `RetryAt`, `Error`

Missing: `Stage` (which stage failed), `FailureKind` (typed failure reason).

**ReviewRow** currently has:
- `IssueID`, `Branch`, `WorkspacePath`, `ReadyAt`

Missing: `Summary` (stage completion summary), `StagesCompleted` (which stages passed).

### 2. `internal/tui/table.go` — Session table rendering

Current columns: `Issue`, `Title`, `Phase`, `PID`, `Tokens`, `Last`.

Consider:
- Adding `Attempt` column or merging it into `Issue` (e.g., "CB-1#2")
- Making `Phase` actually show the phase (currently shows stage)
- The `compactStage()` helper already maps `types.Stage` to short strings (Plan/Exec/Verify/Review)

### 3. `internal/orchestrator/events.go` — Event taxonomy completeness

The spec (`event-taxonomy.md`) defines events that may not all be emitted yet. Check which ones are missing:

| Event | Status | Where emitted |
|-------|--------|--------------|
| `poll.started` | ✅ | `orchestrator.go` |
| `poll.completed` | ✅ | `orchestrator.go` |
| `issue.claimed` | ✅ | `orchestrator.go` |
| `workspace.created` | ✅ | `pipeline/runner.go` (wrapped) |
| `prompt.built` | ✅ | `pipeline/runner.go` (wrapped) |
| `issue.ready_for_review` | ✅ | `orchestrator.go` |
| `issue.completed` | ❓ | Not sure if emitted |
| `issue.retry_queued` | ✅ | `orchestrator.go` (as `EventIssueRetrying`) |
| `stage.started` | ✅ | `orchestrator.go` |
| `stage.progress` | ❌ | **NOT emitted** — may not be needed for minimal |
| `stage.completed` | ✅ | `orchestrator.go` |
| `stage.failed` | ✅ | `orchestrator.go` |
| `stage.skipped` | ❌ | **NOT emitted** — may not be needed for minimal |
| `agent.started` | ✅ | `pipeline/runner.go` (wrapped) |
| `agent.output` | ✅ | `pipeline/runner.go` (wrapped) |
| `agent.tokens_updated` | ✅ | `pipeline/runner.go` (wrapped) |
| `agent.finished` | ✅ | `pipeline/runner.go` (wrapped) |
| `agent.stalled` | ❌ | **NOT emitted** — currently only `EventStallDetected` from orchestrator |
| `agent.timed_out` | ❌ | **NOT emitted** — currently only `EventTimeoutDetected` from orchestrator |
| `review.ready` | ❌ | **NOT emitted** — currently only `EventIssueReadyForReview` |
| `review.approved` | ❌ | **NOT emitted** — no human review action mechanism yet |
| `review.rejected` | ❌ | **NOT emitted** — no human review action mechanism yet |
| `retry.due` | ❌ | **NOT emitted** |
| `retry.skipped` | ❌ | **NOT emitted** |
| `stall.detected` | ✅ | `orchestrator.go` |
| `timeout.detected` | ✅ | `orchestrator.go` |

For Phase 4, you don't need to emit all missing events. Focus on the ones the TUI needs. The spec says:
> "Agent runtime events should be translated into orchestrator events instead of being exposed raw."

The current orchestrator already wraps pipeline events. That's fine.

### 4. `internal/orchestrator/orchestrator.go` — Event emission enrichment

Currently the orchestrator emits events with minimal payloads. For richer TUI display, consider enriching:

- `EventStageFailed` payload already has `FailureKind` — use it in the TUI
- `EventIssueRetrying` payload has `Attempt` and `RetryAt` — TUI already uses these
- `EventBackoffQueued` payload has `Attempt` and `RetryAt` — TUI already uses these

### 5. `internal/orchestrator/backoff.go` — Backoff entry enrichment

The `BackoffEntry` struct now has `Stage` (added in Phase 3). The TUI can display which stage failed:

```go
type BackoffEntry struct {
    IssueID string
    Attempt int
    Stage   Stage     // ← new in Phase 3
    RetryAt time.Time
    Error   string
}
```

Use this in the TUI's backoff queue rendering.

---

## Specific TUI Improvements to Make

### Running table: show attempt and stage more clearly

Current:
```
Issue    Title                      Phase    PID        Tokens     Last
● CB-1   Fix login bug              Exec     12345     1.5K/2.3K   2m
```

Desired:
```
Issue     Title                      Stage   PID        Tokens     Age   Attempt
● CB-1    Fix login bug              Verify  12345     1.5K/2.3K   5m    #2
```

### Review queue: show wait time and workspace

Current:
```
Ready for Human Review                             [1]
  CB-1  branch=opencode/CB-1
      /path/to/workspace
```

Desired:
```
┌ Ready for Human Review ────────────────────────────────────────[1]─┐
│ CB-1  Fix login bug                                              │
│   branch:  opencode/CB-1                                         │
│   workspace: /path/to/workspace                                  │
│   ready:   12m ago                                               │
│   stages:  ✓ plan  ✓ execute  ✓ verify                           │
└──────────────────────────────────────────────────────────────────┘
```

### Backoff queue: show failure stage and reason

Current:
```
Backoff Queue                                      [1]
  CB-1  retry in 2m15s (attempt 2)
```

Desired:
```
┌ Backoff Queue ─────────────────────────────────────────────────[1]─┐
│ CB-1  retry in 2m15s  (attempt #2)                               │
│   stage:   execute failed                                        │
│   reason:  tool error: go build failed                           │
└──────────────────────────────────────────────────────────────────┘
```

### Event log: human-readable stage transitions

Current:
```
  [Events]
  12:14:41 CB-1 stage.started
  12:14:42 CB-1 agent.started
  12:14:43 CB-1 tokens.updated
```

Desired:
```
  [Events]
  12:14:41 CB-1  [plan] started
  12:14:42 CB-1  [plan] agent started (pid: 12345)
  12:14:43 CB-1  [plan] tokens: 500/1,200
  12:15:10 CB-1  [plan] completed
  12:15:11 CB-1  [execute] started
```

---

## The Spec's Exact Requirements for Phase 4

From `phase-map.md`:

> ### What this phase should produce
> - stage-aware running rows
> - a review-ready queue
> - a retry queue with ETA and failure reason
> - a clearer event log for stage transitions

From `event-taxonomy.md`:

> ### Payload guidance
> For stage events: Always include `stage`, `attempt`, `issue_id`, `workspace_path` when available.
> For agent events: Always include `stage`, `session_id`, `pid` when available, `server_url` when available.

The TUI already receives `Stage` in `AgentStartedPayload`, `StageStartedPayload`, `StageCompletedPayload`, `StageFailedPayload`. Use these consistently.

---

## Gotchas & Context

1. **The table column says "Phase" but renders `Stage`**. This is a naming inconsistency from Phase 2 cleanup. The header should probably say "Stage" since that's what we track now. `RunPhase` (LaunchingAgent, StreamingTurn, etc.) is tracked internally but not displayed.

2. **Token tracking is cumulative**. `totalTokensIn` and `totalTokensOut` accumulate across all stages in a single attempt. This is correct — the TUI shows total tokens for the whole attempt.

3. **Backoff entries are replaced, not accumulated**. `BackoffManager.Enqueue()` replaces the existing entry for an issue. So there's always at most one backoff entry per issue. The TUI's `backoffs` map will also have at most one entry per issue.

4. **Review queue entries persist until `EventIssueCompleted`**. When an issue is marked `done`, the orchestrator should emit `EventIssueCompleted`, and the TUI removes it from `reviews`. Currently the TUI handles this but there may be no CLI mechanism for a human to mark an issue done.

5. **Event log auto-scrolls to bottom**. `pushEvent()` sets `scrollPos = len(m.eventLog) - 1`. This is fine but may be annoying if the user is scrolling back. Consider only auto-scrolling if the user is already near the bottom.

6. **The TUI uses `refreshInterval = time.Second`** for updating derived fields (ages, backoff countdowns). This is already in place.

7. **No human review action in TUI yet**. The spec mentions `review.approved` and `review.rejected` events, but there's no keyboard shortcut or command to approve/reject an issue. For Phase 4, you may want to add a keybinding (e.g., `a` to approve selected review item, `r` to reject) or defer this to a later phase.

---

## Suggested Implementation Order

1. Read all spec docs listed above
2. Enhance `AgentRow`, `BackoffRow`, `ReviewRow` structs with any missing display fields
3. Update `applyOrchestratorEvent()` to populate new fields
4. Update `renderTable()` to show attempt number and fix "Phase" → "Stage" header
5. Update `renderReviewQueue()` to show wait time and stage completion summary
6. Update `renderBackoffQueue()` to show failed stage and failure kind
7. Update `renderEventLog()` to format stage transitions more readably
8. Consider adding keybindings for review actions (approve/reject) — or defer
9. Write tests for TUI event application
10. Run `go test ./...` until clean
11. Commit with Conventional Commits style

---

## Files to Touch (likely)

- `internal/tui/model.go` — event application, rendering, state structs
- `internal/tui/table.go` — column headers, row formatting
- `internal/orchestrator/events.go` — possibly add missing payload fields
- `internal/orchestrator/orchestrator.go` — possibly enrich event payloads
- `internal/types/types.go` — possibly extend `BackoffEntry` or add display helpers

---

## Verification Command

```bash
go test ./...
```

All tests must pass before claiming Phase 4 complete.

---

## Reference Commits

```bash
git show f529156 --stat   # Phase 3: pipeline state machine
git show 9faf6cd --stat   # Phase 3 fix: context cancellation, failure classification, handoff
```

These show the current state of the orchestrator and TUI code you'll be enhancing.

---

## Current TUI Event Handling Summary

Here's a quick reference of how the TUI currently handles each orchestrator event:

| Event | TUI Action | File/Line |
|-------|-----------|-----------|
| `EventAgentStarted` | Create `AgentRow`, set Stage from payload | `model.go:~368` |
| `EventTokensUpdated` | Update `TokensIn/Out`, set `LastEvent="tokens updated"` | `model.go:~380` |
| `EventStageStarted` | Update `Stage`, `Status="running"`, `LastEvent="X started"` | `model.go:~392` |
| `EventStageCompleted` | Update `Stage`, `LastEvent="X completed"` | `model.go:~401` |
| `EventStageFailed` | Update `Stage`, `Status="failed"`, `LastEvent="X failed"` | `model.go:~410` |
| `EventAgentFinished` | If success: delete from agents. If failure: `Status="failed"` | `model.go:~420` |
| `EventBackoffQueued` | Create `BackoffRow` | `model.go:~433` |
| `EventIssueReadyForReview` | Create `ReviewRow`, delete from agents/backoffs | `model.go:~440` |
| `EventIssueCompleted` | Delete from agents/reviews/backoffs | `model.go:~455` |
| `EventTimeoutDetected` | `Status="timeout"` | `model.go:~460` |
| `EventStallDetected` | `Status="stalled"` | `model.go:~466` |
| `EventPollCompleted` | Update `stats.RunningAgents` | `model.go:~473` |

All of these are in `applyOrchestratorEvent()`. You'll likely extend many of them.
