# Contrabass-PI — Project Digest

> A video-ready walkthrough of the entire system: what it is, what parts it has, what each part does, and the full workflow from startup to shutdown.

---

## 1. What Is This?

Contrabass-PI is a **minimal orchestrator for AI coding agents**. It sits between you (the developer) and an AI coding agent (OpenCode). You write issues in a local board, and Contrabass feeds them one-by-one through a three-stage pipeline — **plan → execute → verify** — using isolated git worktrees. When all stages pass, the issue lands in `in_review` for you to inspect and approve/reject manually.

Think of it as a **CI-like loop for AI-generated code**, but with a human review gate instead of auto-merge.

**Key design decisions:**
- No auto-merge, no auto-cleanup. The orchestrator hands off to you.
- Stage-level retry with exponential backoff. If `verify` fails, only `verify` is retried.
- Everything is persisted to disk (JSON + markdown artifacts) so every run is reproducible and debuggable.
- Works entirely with local files — no external services needed.

---

## 2. The Folder Structure (High-Level)

```
contrabass-pi/
│
├── cmd/contrabass/           # CLI entry point (main.go, board.go, init.go)
│
├── internal/
│   ├── types/                # Core data types shared across all packages
│   │   ├── types.go          #   Issue, IssueState, AgentRunner, AgentEvent, etc.
│   │   ├── pipeline.go       #   Stage, StageManifest, StageResult, StageFailureKind, ReviewDecision
│   │   └── context.go        #   StageContext (plumbed via context.Context for agent selection)
│   │
│   ├── config/               # WORKFLOW.md parser
│   │   └── config.go         #   YAML front matter → Config struct, defaults, validation
│   │
│   ├── tracker/              # Local file-based issue tracker
│   │   └── local.go          #   Board as JSON files: manifest.json + issues/*.json
│   │
│   ├── workspace/            # Git worktree management
│   │   └── manager.go        #   Create/reuse worktrees per issue, idempotent, atomic
│   │
│   ├── agent/                # Agent runner implementations
│   │   ├── opencode.go       #   OpenCode HTTP+SSE runner (start server, session, prompt, stream)
│   │   ├── events.go         #   Event type constants + extraction helpers (ExtractTextContent, ExtractTokens)
│   │   └── fakerunner.go     #   Deterministic fake runner for tests
│   │
│   ├── pipeline/             # Per-issue stage lifecycle
│   │   └── runner.go         #   Run(issue, attempt, stage) — workspace, prompt, agent, artifacts
│   │
│   ├── orchestrator/         # Main event loop — the brain
│   │   ├── orchestrator.go   #   Poll loop, dispatch, stage sequence, timeout/stall detection
│   │   ├── state.go          #   In-memory run state (what's running right now)
│   │   ├── events.go         #   Orchestrator event types + payload structs
│   │   └── backoff.go        #   Exponential backoff with jitter for retries
│   │
│   ├── diagnostics/          # Persistent run records (the audit trail)
│   │   ├── recorder.go       #   Recorder, AttemptRecorder — file tree management, atomic writes
│   │   └── stages.go         #   StageRecorder — per-stage artifacts, review handoff/decision
│   │
│   ├── tui/                  # Terminal UI (Charm Bubble Tea)
│   │   └── model.go          #   Full TUI model: running table, review queue, backoff queue, event log
│   │
│   └── util/                 # Shared utilities
│       └── strings.go        #   SanitizeBranchName, ExpandPrompt (template substitution)
│
├── docs/
│   └── context/              # Architecture docs (what-contrabass-is.md, minimal-contrabass.md)
│
├── WORKFLOW.md               # Default config for the orchestrator (YAML front matter + prompt template)
├── Makefile                  # build, install, test, clean
├── go.mod / go.sum           # Go module (3 deps: bubbletea, lipgloss, yaml.v2)
└── .github/workflows/ci.yml  # CI: go test + go build on push/PR
```

**12 internal packages. ~38 Go files. 3 external dependencies.** No database, no message queue, no external services.

---

## 3. The Artifacts — What Gets Created

### 3.1 At init time (`contrabass init`)

```
project-root/
├── WORKFLOW.md                          # Config + prompt template
└── .contrabass/
    └── projects/<name>/
        └── board/
            ├── manifest.json            # Board metadata (schema version, next issue number)
            └── issues/                  # Empty initially
```

### 3.2 When you create an issue (`contrabass board create "..."`)

```
.contrabass/projects/<name>/board/
└── issues/
    └── CB-1.json                        # {id, title, description, state: "todo", labels, ...}
```

### 3.3 At runtime (for each issue attempt)

Workspaces are created as **sibling directories outside the repo**:

```
../<repo>.worktrees/
└── CB-1/                                # git worktree on branch contrabass/CB-1
```

Run diagnostics are persisted **beside the board**:

```
.contrabass/projects/<name>/
├── board/                               # (issues live here)
└── runs/                                # Run diagnostics
    ├── _orchestrator/
    │   └── events.jsonl                 # Global event log
    └── CB-1/
        ├── issue.json                   # Snapshot of the issue at run time
        ├── summary.json                 # Issue-level summary (state, attempts, review status)
        └── attempts/
            └── 001/
                ├── meta.json            # Attempt metadata (PID, session, timings, outcome)
                ├── prompt.md            # The full prompt sent to the agent
                ├── events.jsonl         # All events during this attempt
                ├── stdout.log           # Agent's stdout
                ├── stderr.log           # Agent's stderr
                ├── preflight/           # Git state BEFORE the run
                │   ├── git-status.txt
                │   └── git-worktree-list.txt
                ├── stages/
                │   ├── plan/
                │   │   ├── manifest.json   # Stage control file (status, timings, paths)
                │   │   ├── prompt.md       # Stage-specific prompt
                │   │   ├── response.md     # Agent's text response
                │   │   ├── result.json     # Stage outcome (passed/failed, failure kind, etc.)
                │   │   ├── events.jsonl    # Stage-level event log
                │   │   ├── stdout.log
                │   │   └── stderr.log
                │   ├── execute/
                │   │   ├── (same as plan) +
                │   │   └── diff.patch      # The git diff produced by this stage
                │   └── verify/
                │       └── (same as plan)
                ├── review/
                │   ├── handoff.md          # Human-readable review package
                │   ├── notes.md            # Optional review notes
                │   └── decision.json       # Approved/Rejected/NeedsChanges
                └── postflight/
                    ├── git-status.txt
                    └── git-worktree-list.txt
```

**Every JSON write is atomic** (write to `.tmp` then `rename`).

---

## 4. The Parts — What Each Does

### 4.1 `internal/types/` — The Nouns

Defines all the data types that flow through the system. This package has zero dependencies on other `internal/` packages — everything depends on it.

| Type | Purpose |
|------|---------|
| `Issue` | A task from the board (ID, title, description, state, labels) |
| `IssueState` | `todo → in_progress → retry_queued → in_review → done` |
| `Stage` | Pipeline stages: `plan`, `execute`, `verify`, `human_review` |
| `StageManifest` | Durable control data for one stage (paths, status, timings) |
| `StageResult` | Machine-readable outcome of a stage (passed/failed, failure kind) |
| `StageFailureKind` | Typed failure classification (10 kinds: timeout, stall, tool_error, etc.) |
| `ReviewDecision` | Human review decision (approved/rejected/needs_changes) |
| `AgentRunner` | Interface: `Start()`, `Stop()`, `Close()` |
| `AgentProcess` | A running agent (PID, session ID, events channel, done channel) |
| `AgentEvent` | Something the agent emitted (type + untyped payload) |
| `OrchestratorEvent` | High-level orchestrator event (type, issueID, timestamp, payload) |
| `IssueTracker` | Interface for the issue board (fetch, claim, release, update state) |
| `BackoffEntry` | A queued retry (which issue, which stage, when to retry) |
| `StageContext` | Carried via `context.Context` so the agent knows which stage and agent to use |

### 4.2 `internal/config/` — WORKFLOW.md Parser

Parses the YAML front matter from `WORKFLOW.md` into a `Config` struct. The markdown body becomes the prompt template (with `{{ issue.title }}`, `{{ issue.description }}` placeholders).

**Key behavior:**
- Fills sensible defaults for everything not explicitly set
- Validates required fields (`tracker.type`, `agent.type`, `opencode.binary_path`)
- Resolves relative paths (board dir, workspace dir) against the config file's directory
- `AgentForStage(stage)` resolves per-stage agent mapping (`opencode.agents.plan`, etc.)

### 4.3 `internal/tracker/` — Local Board

A file-based issue tracker. Issues are JSON files in `.contrabass/projects/<name>/board/issues/`.

**Operations:**
- `FetchIssues()` → all non-terminal issues (excludes `done` and `in_review`, filters `retry_queued` by `retry_after`)
- `ClaimIssue(id)` → marks as `in_progress`, sets `claimed_by`
- `ReleaseIssue(id)` → returns to `todo`
- `UpdateIssueState(id, state)` → transitions state
- `CreateIssue(title, desc, labels)` → writes new JSON, increments counter in `manifest.json`
- `SetRetryQueue(id, retryAt)` → marks as `retry_queued` with retry timestamp
- `ListAllIssues()` → every issue regardless of state

All writes are atomic (temp file + rename). Mutex-protected for concurrent access.

### 4.4 `internal/workspace/` — Git Worktree Manager

Creates isolated workspaces per issue using `git worktree add`. Worktrees are placed in a **sibling directory** outside the repo (e.g., `../myproject.worktrees/CB-1/`).

**Key behavior:**
- **Idempotent** — calling `Create()` for the same issue multiple times reuses the existing worktree (important: pipeline calls it before each stage)
- Validates worktrees before reusing them (checks `.git` file, gitdir link, `git worktree list`)
- Prunes stale/dangling worktree registrations automatically
- Handles edge cases: branch already exists, orphaned worktrees, missing-but-registered entries
- Per-issue mutex to prevent races for the same issue
- **Does NOT auto-cleanup** after successful runs — the human reviewer decides when to clean up

### 4.5 `internal/agent/` — Agent Runners

#### `opencode.go` — OpenCode HTTP+SSE Runner

Manages the full `opencode serve` lifecycle:

1. **Start server** — spawns `opencode serve` process, parses stdout for "listening on http://..."
2. **Create session** — `POST /session` → gets session ID
3. **Submit prompt** — `POST /session/{id}/prompt_async` with the prompt and agent name
4. **Stream events** — `GET /event` (SSE), parses and filters events by session ID
5. **Detect completion** — `session.status` with `status.type: idle` → done
6. **Abort on demand** — `POST /session/{id}/abort`
7. **Graceful shutdown** — `SIGINT` → wait → `SIGKILL`

**Key design:**
- Server processes are reused per workspace (same workspace = same server)
- Server ports are auto-allocated (0 = OS picks) or configurable
- Agent selection is read from `StageContext` in the context (set by orchestrator before each stage)
- Stdout/stderr are captured to diagnostics recorder when available

#### `events.go` — Event Constants + Extraction

Constants for SSE event types (`session.status`, `session.error`, `message.part.updated`, etc.) and two extraction helpers:
- `ExtractTextContent(event)` — pulls text from agent output events
- `ExtractTokens(event)` — pulls token counts from status events

#### `fakerunner.go` — Test Runner

A deterministic, scripted agent runner for tests. You configure per-stage scripts with events and a done error. Used by orchestrator and pipeline tests.

### 4.6 `internal/pipeline/` — Stage Runner

Owns the lifecycle of a **single stage** for one issue. Called by the orchestrator once per stage (plan, then execute, then verify).

**`Run(ctx, issue, attempt, stage, emit)` does:**
1. Create/reuse workspace (idempotent)
2. Build a stage-specific prompt:
   - **plan**: "You are in PLANNING mode... Do NOT make any code changes yet."
   - **execute**: "You are in EXECUTION mode... Make the necessary code changes."
   - **verify**: "You are in VERIFICATION mode... Provide a pass/fail assessment."
3. Begin stage recording (via diagnostics)
4. Start agent with stage context
5. Monitor agent events (token updates, text output, completion/error)
6. Capture postflight git state + diff
7. Finalize stage recording (response, result, manifest)
8. Return `Result` (success, workspace path, branch, diff, tokens)

The runner emits events that the orchestrator intercepts and forwards to the TUI.

### 4.7 `internal/orchestrator/` — The Brain

The central loop that ties everything together.

#### `orchestrator.go` — Main Loop

```
┌──────────────────────────────────────────────────────────────┐
│  Every N seconds (poll_interval_ms):                         │
│                                                              │
│  1. reconcileRunning()                                       │
│     ├─ Check for timeouts (agent_timeout_ms)                 │
│     └─ Check for stalls (stall_timeout_ms)                   │
│                                                              │
│  2. dispatchBackoff()                                        │
│     ├─ Check which retries are ready (retry_at ≤ now)        │
│     ├─ Re-claim the issue                                    │
│     └─ Start run FROM THE FAILED STAGE, not from plan        │
│                                                              │
│  3. dispatchReady()                                          │
│     ├─ Fetch unclaimed issues from tracker                   │
│     ├─ Skip if at max_concurrency                            │
│     ├─ Skip if already running or in backoff queue            │
│     ├─ Claim the issue                                       │
│     └─ Start run from plan stage                             │
└──────────────────────────────────────────────────────────────┘
```

**`startRun()` spawns a goroutine that runs the stage sequence:**

```
plan → execute → verify
  │        │         │
  └─fail───┴─fail────┴─fail──→ enqueue backoff (for that specific stage)
                                │
                           all pass
                                │
                           move issue to in_review
                           write review handoff
                           keep worktree intact
```

**Key behaviors:**
- **Stage-scoped retry**: `BackoffEntry.Stage` remembers which stage failed. On retry, the pipeline resumes from that stage, not from scratch.
- **Atomic finalization**: Timeout/stall detection and pipeline completion use `State.Mutate()` with phase checks so only one path persists the outcome (no double events).
- **Cancellable contexts**: Each run has its own `context.WithCancel`. On timeout/stall/shutdown, the context is cancelled, which propagates to the agent runner.
- **Graceful shutdown**: Signals trigger `Stop()` → cancel all run contexts → wait for goroutines → stop all agent processes → close recorder → close event channel.

#### `state.go` — Run State Manager

An in-memory map of `issueID → RunState` with mutex protection. Tracks:
- Which issue is running
- Current attempt number and pipeline stage
- Agent process reference (PID, session ID)
- Phase (preparing_workspace → building_prompt → ... → succeeded/failed)
- Token counts, start time, last event time

**`Mutate(issueID, fn)`** is the single entry point for atomic updates — replaces 6 separate setter methods.

#### `events.go` — Event Types

~20 event type constants (`poll_started`, `issue.claimed`, `stage.started`, `stage.completed`, `stage.failed`, `agent.finished`, `backoff.queued`, `stall.detected`, etc.) and 4 payload structs (`ProcessPayload`, `StagePayload`, `BackoffPayload`, `AgentResultPayload`).

#### `backoff.go` — Retry Logic

Exponential backoff with jitter:
- Attempt 1 → 30s (±20%)
- Attempt 2 → 60s (±20%)
- Attempt 3 → 120s (±20%)
- Attempt 4+ → capped at `max_retry_backoff_ms` (default 4 min)

Each entry remembers the **stage to resume from**, so retries are surgical.

### 4.8 `internal/diagnostics/` — Persistent Run Records

The audit trail. Everything the orchestrator does is written to disk.

**Three levels of recording:**

| Level | Type | Responsibility |
|-------|------|----------------|
| Global | `Recorder` | Global event log (`_orchestrator/events.jsonl`), issue registry |
| Attempt | `AttemptRecorder` | Per-attempt dirs, meta.json, preflight/postflight snapshots, stdout/stderr |
| Stage | `StageRecorder` | Per-stage dirs, manifest.json, prompt.md, response.md, result.json, events.jsonl |

**Key features:**
- All JSON writes are atomic (temp file + rename)
- Stdout/stderr from the agent process are captured live via `io.Writer`
- Review handoff (`review/handoff.md`) and review decision (`review/decision.json`) are written as separate artifacts
- `LoadAttemptRecorder()` can reopen an existing attempt to write a review decision (used by `board approve/reject`)

### 4.9 `internal/tui/` — Terminal UI

Built with Charm's Bubble Tea framework. Shows four sections:

| Section | What it shows |
|---------|--------------|
| **Header** | Stats: running agents, review queue count, backoff queue count |
| **Running table** | Each running agent: issue ID, title, current stage, PID, tokens, age, attempt # |
| **Review queue** | Issues ready for review: branch, workspace path, time waiting, stage completion status |
| **Backoff queue** | Issues waiting to retry: failed stage, failure reason, ETA, attempt # |
| **Event log** | Scrollable log of all orchestrator events with timestamps |

**Event bridge:** `StartEventBridge()` reads from `orch.Events` channel and sends each event as a `tea.Msg` to the TUI program. The TUI never blocks the orchestrator — it updates its in-memory maps on each event.

### 4.10 `internal/util/` — Utilities

- `SanitizeBranchName(id)` — safe for git branch names
- `SanitizeFileName(name)` — safe for filesystem
- `ExpandPrompt(template, issue)` — substitutes `{{ issue.title }}`, `{{ issue.description }}`, etc.

---

## 5. The Workflow — End to End

### 5.1 Startup Sequence

```
1. CLI parses flags (--config, --no-tui, --dry-run, --log-level)
2. Resolves WORKFLOW.md path (search upward from cwd, stop at git root)
3. Config.Load() → parse YAML front matter, validate, resolve paths
4. Create tracker.LocalTracker (file-based board)
5. Create workspace.Manager (sibling worktree dir)
6. Create diagnostics.Recorder (writes to .contrabass/projects/<name>/runs/)
7. Create agent.OpenCodeRunner (binary path, profile, agent config)
8. Create orchestrator.Orchestrator (wires everything together)
9. Set recorder on orchestrator
10. Start TUI program (or headless logger)
11. Start event bridge (pipes orch.Events to TUI)
12. Run orchestrator loop (blocks until shutdown)
```

### 5.2 Poll Cycle (happens every `poll_interval_ms`)

```
poll()
├── emit("poll_started")
│
├── reconcileRunning()
│   └── for each active run:
│       ├── exceeded agent_timeout_ms? → cancel context, enqueue backoff, finalize
│       └── no events for stall_timeout_ms? → cancel context, enqueue backoff, finalize
│
├── dispatchBackoff()
│   └── for each ready backoff entry (retry_at ≤ now):
│       ├── skip if at capacity
│       ├── re-claim the issue
│       ├── remove from backoff map
│       └── startRun(issue, attempt, startStage) ← start from the failed stage
│
├── dispatchReady()
│   └── for each unclaimed issue (todo or retry_queued with retry_at passed):
│       ├── skip if at capacity
│       ├── skip if already running or in backoff
│       ├── claim the issue
│       └── startRun(issue, attempt=1, stage=plan)
│
└── emit("poll_completed")
```

### 5.3 Running a Single Issue (inside `startRun()`)

```
startRun(issue, attempt, startStage)
│
├── Create cancellable context for this run
├── Record preflight git state
├── BeginAttempt in diagnostics recorder
├── Add to in-memory state
│
└── Spawn goroutine:
    │
    ├── For each stage in [plan, execute, verify] starting from startStage:
    │   │
    │   ├── Set stage in state
    │   ├── Resolve agent for this stage (opencode.agents.plan, etc.)
    │   ├── Attach StageContext to context
    │   ├── emit("stage.started")
    │   │
    │   ├── pipeline.Runner.Run(ctx, issue, attempt, stage, emit)
    │   │   ├── workspace.Create() (idempotent — reuse if exists)
    │   │   ├── Build stage-specific prompt
    │   │   ├── BeginStage in diagnostics
    │   │   ├── agent.Start(ctx, issue, workspace, prompt)
    │   │   ├── Monitor events (tokens, text, completion)
    │   │   ├── Capture postflight (git status, diff)
    │   │   ├── Finalize stage recording (response, result, manifest)
    │   │   └── Return Result (success/failure)
    │   │
    │   ├── If failed:
    │   │   ├── Enqueue backoff for THIS stage
    │   │   ├── persistRetryQueue on tracker
    │   │   ├── finalizeAttempt
    │   │   ├── emit("stage.failed", "agent.finished", "backoff.queued")
    │   │   └── RETURN (don't try next stage)
    │   │
    │   └── If success:
    │       └── emit("stage.completed")
    │
    ├── All stages passed → HANDOFF TO HUMAN REVIEW
    │   ├── Write review handoff (handoff.md)
    │   ├── UpdateIssueState → in_review
    │   ├── emit("issue.ready_for_review", "agent.finished")
    │   └── Remove from active state & backoff map
    │
    └── On context cancellation between stages:
        └── Same as failure — enqueue backoff, finalize, emit events
```

### 5.4 Human Review Flow

```
1. User runs: contrabass board list --all
   → sees CB-1 in "in_review" state

2. User runs: contrabass board show CB-1
   → sees issue details, stage completion, review handoff, workspace path

3. User inspects the worktree at ../<repo>.worktrees/CB-1/
   → reviews the diff, runs tests, makes manual edits if needed

4. User makes decision:
   ├── contrabass board approve CB-1 --message "LGTM"
   │   ├── UpdateIssueState → done
   │   ├── Write review/decision.json (approved)
   │   └── Done! Issue is closed.
   │
   └── contrabass board reject CB-1 --message "Needs more tests"
       ├── UpdateIssueState → todo
       ├── Write review/decision.json (rejected)
       └── Issue goes back to the pool for a new attempt from plan
```

### 5.5 Shutdown Sequence

```
1. Signal received (SIGINT/SIGTERM) or TUI quit (q/Ctrl+C)
2. orch.Stop() → set closed=true, cancel main context
3. Main context cancellation propagates to all run contexts
4. Each run goroutine:
   ├── Detects ctx.Err()
   ├── Atomically checks phase (only finalize if not already done)
   ├── Enqueues backoff for the current stage
   ├── Finalizes attempt
   └── Returns
5. WaitGroup.Wait() for all goroutines (5s timeout)
6. For each remaining active run: stop the agent process
7. agentRunner.Close() → stops all managed server processes
8. recorder.Close() → closes all open file handles
9. Close Events channel
10. Exit
```

---

## 6. The States — What Can an Issue Be In?

### Board states (persisted to tracker JSON files):

```
                  ┌─────────┐
                  │   todo  │  ← created, or rejected after review
                  └────┬────┘
                       │ claim
                  ┌────▼────────┐
                  │ in_progress │  ← actively being worked on
                  └────┬────────┘
                       │
           ┌───────────┼───────────┐
           │ failure   │ success   │ timeout/stall
           ▼           ▼           ▼
    ┌─────────────┐ ┌──────────┐ ┌─────────────┐
    │retry_queued │ │in_review │ │retry_queued │
    └──────┬──────┘ └────┬─────┘ └──────┬──────┘
           │ retry        │ approve      │ retry
           │ (after       │              │ (after
           │  backoff)    │              │  backoff)
           └──────────────┘              │
                          │ reject       │
                    ┌─────▼─────┐        │
                    │   done    │ ◄──────┘
                    └───────────┘
```

### Run phases (in-memory, per-attempt):

```
preparing_workspace → building_prompt → launching_agent_process
    → initializing_session → streaming_turn → finishing
        → succeeded | failed | timed_out | stalled
```

---

## 7. The Event Flow

All components communicate through a single channel: `Orchestrator.Events` (buffered, capacity 256).

```
Agent (SSE events)
  │
  ▼
pipeline.Runner (monitors, extracts tokens/text, records to diagnostics)
  │
  ▼
orchestrator.startRun goroutine (intercepts, updates state, re-emits typed events)
  │
  ▼
Orchestrator.Events channel
  │
  ├──▶ diagnostics.Recorder (appends to global + attempt event logs)
  │
  └──▶ TUI Event Bridge (wraps in tea.Msg, sends to Bubble Tea program)
         │
         ▼
       Model.Update() (updates in-memory maps based on event type)
         │
         ▼
       Model.View() (renders tables, queues, logs)
```

**Event types and when they fire:**

| Event | Emitted when |
|-------|-------------|
| `poll_started` / `poll_completed` | Each poll cycle |
| `issue.claimed` | Issue is successfully claimed |
| `workspace.created` | Worktree is ready |
| `prompt.built` | Prompt is generated |
| `stage.started` | A pipeline stage begins |
| `agent.started` | Agent process is launched (includes PID, session ID) |
| `tokens.updated` | Token counts change |
| `agent.output` | Agent produces text output |
| `stage.completed` | A stage finishes successfully |
| `stage.failed` | A stage fails (includes failure kind) |
| `agent.finished` | The run is complete (success or failure) |
| `backoff.queued` | A retry is scheduled |
| `issue.retrying` | Issue is re-enqueued for retry |
| `stall.detected` | Agent hasn't produced events for too long |
| `timeout.detected` | Agent exceeded the time limit |
| `issue.ready_for_review` | All stages passed, waiting for human |

---

## 8. Key Interfaces

Two interfaces define the extensibility points:

```go
// What the orchestrator needs from an issue tracker
type IssueTracker interface {
    FetchIssues() ([]Issue, error)
    ClaimIssue(id string) (Issue, error)
    ReleaseIssue(id string) (Issue, error)
    GetIssue(id string) (Issue, error)
    UpdateIssueState(id string, state IssueState) (Issue, error)
    SetRetryQueue(id string, retryAt time.Time) (Issue, error)
    ListAllIssues() ([]Issue, error)
    CreateIssue(title, description string, labels []string) (Issue, error)
}

// What the orchestrator needs from an agent
type AgentRunner interface {
    Start(ctx context.Context, issue Issue, workspace, prompt string) (*AgentProcess, error)
    Stop(proc *AgentProcess) error
    Close() error
}
```

Currently only `LocalTracker` and `OpenCodeRunner` are implemented, but the design allows swapping in GitHub Issues or another agent without changing the orchestrator.

---

## 9. Design Principles in Practice

| Principle | How it's applied |
|-----------|-----------------|
| **Deep modules** | `orchestrator.go` (~500 lines) tells one narrative: stage loop → events → finalize. `recorder.go` (~500 lines) handles all file tree complexity behind a simple interface. |
| **Information hiding** | `StateManager.Mutate()` is the single atomic update path. Callers never touch the map directly. |
| **Pull complexity down** | The orchestrator handles timeout/stall detection, retry logic, and event wiring so `pipeline.Runner` stays simple (just run one stage). |
| **Illegal states unrepresentable** | `StageFailureKind` is a typed enum. `StageContext` is the only way to pass stage info to the agent runner. |
| **One word per concept** | "Stage" always means pipeline stage. "Attempt" always means a retry attempt. "Run" always means an active execution. |
| **Kill dead code** | The `StateManager`'s old setter methods (`SetError`, `GetByPhase`, etc.) are already removed. Shallow files (`tui/events.go`, `web/events.go`, `tui/table.go`) are already merged. |

---

## 10. What's NOT in This Project (Deferred)

- **Multi-agent teams** — currently single-agent only
- **External trackers** — only local file-based board (no GitHub Issues, Linear)
- **Web dashboard** — only Charm TUI
- **Other agent types** — only OpenCode (no Codex, OMC)
- **Auto-merge** — the human must approve and merge manually
- **Config hot-reload** — requires restart
- **`internal/web/`** — no web server (removed, was in earlier versions)
- **`github.go` tracker** — not implemented (this project is local-only)

---

## 11. Quick Reference: How To...

| Task | Command |
|------|---------|
| Initialize a project | `contrabass init` |
| Add an issue | `contrabass board create "Fix bug"` |
| List all issues | `contrabass board list --all` |
| Show issue details | `contrabass board show CB-1` |
| Start orchestrator (TUI) | `contrabass` |
| Start headless | `contrabass --no-tui` |
| Single poll cycle | `contrabass --dry-run` |
| Verbose logging | `contrabass --log-level debug` |
| Approve an issue | `contrabass board approve CB-1` |
| Reject an issue | `contrabass board reject CB-1 --message "why"` |
| Force retry now | `contrabass board retry CB-1` |
| Build from source | `make build` |
| Run tests | `make test` |
