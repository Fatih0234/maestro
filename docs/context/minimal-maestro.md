# Minimal Maestro for OpenCode

> A minimal orchestrator for OpenCode coding agents with local board tracker, persistent run diagnostics, and an orchestrator-owned pipeline.

## Scope

This is a stripped-down version of Maestro that focuses on:
- ✅ Single-agent orchestrator (no team system)
- ✅ Orchestrator-owned pipeline: **plan → execute → verify → human review**
- ✅ Local board tracker (file-based, no external service)
- ✅ OpenCode agent runner
- ✅ Persistent run diagnostics with stage artifacts
- ✅ Git worktree workspaces
- ✅ WORKFLOW.md config parser with per-stage agent selection
- ✅ Charm TUI (Bubble Tea)
- ❌ ~~Multi-agent teams~~ (deferred)
- ❌ ~~Linear/GitHub trackers~~ (deferred)
- ❌ ~~OMX/OMC runners~~ (deferred)
- ❌ ~~Web dashboard~~ (deferred)

## Philosophy

**Keep it simple.** The goal is a working orchestrator that:
1. Polls a local board for issues
2. Creates a workspace for each issue
3. Runs the pipeline: plan → execute → verify
4. Shows progress in a TUI
5. Hands off to human review on success

No external dependencies beyond OpenCode, Git, and Go.

## Directory Structure

```
.
├── cmd/
│   └── maestro/
│       └── main.go           # CLI entry, TUI, headless modes
├── internal/
│   ├── config/
│   │   └── config.go         # WORKFLOW.md parser
│   ├── tracker/
│   │   └── local.go          # Local board (file-based)
│   ├── workspace/
│   │   └── manager.go        # Git worktree management
│   ├── agent/
│   │   ├── opencode.go       # OpenCode HTTP+SSE runner
│   │   ├── events.go         # Agent event constants + extraction helpers
│   │   └── fakerunner.go     # Fake agent runner for tests
│   ├── diagnostics/
│   │   ├── recorder.go       # Persistent run records
│   │   └── stages.go         # StageRecorder + review handoff/decision
│   ├── pipeline/
│   │   └── runner.go         # Stage-aware runner (plan → execute → verify)
│   ├── orchestrator/
│   │   ├── orchestrator.go   # Main loop, dispatch, stage sequence
│   │   ├── events.go         # Orchestrator event types + payloads
│   │   ├── state.go          # In-memory run state tracking
│   │   └── backoff.go        # Retry logic with stage-scoped resume
│   ├── tui/
│   │   ├── model.go          # Main TUI model + event application
│   │   ├── table.go          # Session table rendering
│   │   └── events.go         # Orchestrator event bridge
│   ├── types/
│   │   ├── types.go          # Core types (Issue, RunAttempt, AgentRunner, etc.)
│   │   ├── pipeline.go       # Pipeline types (Stage, StageManifest, StageResult, etc.)
│   │   └── context.go        # StageContext for context propagation
│   └── util/
│       └── strings.go        # String utilities
├── docs/
│   ├── context/              # Implementation guides (this file, what-maestro-is.md)
│   ├── specs/
│   │   └── orchestrator-owned-pipeline/  # Authoritative pipeline spec
│   └── references/maestro/ # Reference implementation
└── go.mod
```

## Core Components

### 1. Config Parser

Parse `WORKFLOW.md` with YAML front matter:

```yaml
---
max_concurrency: 3
poll_interval_ms: 2000
max_retry_backoff_ms: 240000
agent_timeout_ms: 900000
stall_timeout_ms: 60000
tracker:
  type: internal
  board_dir: .maestro/projects/default/board
  issue_prefix: CB
agent:
  type: opencode
opencode:
  binary_path: opencode serve
  port: 9090
  profile: ""
  agent: ""
  agents:
    plan: plan
    execute: build
    verify: review
  config_dir: ""
workspace:
  base_dir: .
  branch_prefix: opencode/
---

# Task

{{ issue.title }}

{{ issue.description }}
```

**Supported config fields:**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `max_concurrency` | int | 3 | Max concurrent agents |
| `poll_interval_ms` | int | 30000 | Poll interval in ms |
| `max_retry_backoff_ms` | int | 240000 | Max retry backoff in ms |
| `agent_timeout_ms` | int | 900000 | Agent timeout in ms |
| `stall_timeout_ms` | int | 60000 | Stall detection timeout in ms |
| `tracker.type` | string | internal | Tracker type |
| `tracker.board_dir` | string | .maestro/projects/default/board | Local board path |
| `tracker.issue_prefix` | string | CB | Issue ID prefix |
| `agent.type` | string | opencode | Agent type |
| `opencode.binary_path` | string | opencode serve | OpenCode binary |
| `opencode.port` | int | 0 | Server port (0 = auto) |
| `opencode.password` | string | "" | Server password |
| `opencode.profile` | string | "" | Profile name |
| `opencode.agent` | string | "" | Default agent name |
| `opencode.agents` | map[string]string | {} | Per-stage agent mapping |
| `opencode.config_dir` | string | "" | Optional custom .opencode directory |
| `workspace.base_dir` | string | . | Workspace root |
| `workspace.branch_prefix` | string | opencode/ | Branch name prefix |

Per-stage agent selection: `opencode.agents` maps stage names (`plan`, `execute`, `verify`) to agent names. If a stage is not mapped, `opencode.agent` is used. This is resolved at runtime by `OpenCodeConfig.AgentForStage(stage)`.

### 2. Local Board Tracker

File-based issue storage inside each project:

```
my-project/
├── WORKFLOW.md              # Orchestrator config
└── .maestro/
    └── board/               # Issues for this project
        ├── manifest.json
        └── issues/
            └── CB-*.json
```

**manifest.json:**
```json
{
  "schema_version": "1",
  "issue_prefix": "CB",
  "next_issue_number": 3,
  "created_at": "2024-01-01T00:00:00Z",
  "updated_at": "2024-01-01T00:00:00Z"
}
```

**Issue JSON (CB-1.json):**
```json
{
  "id": "CB-1",
  "identifier": "CB-1",
  "title": "Implement login",
  "description": "Add OAuth login flow...",
  "state": "todo",
  "labels": ["feature"],
  "created_at": "2024-01-01T00:00:00Z",
  "updated_at": "2024-01-01T00:00:00Z"
}
```

**States:** `todo`, `in_progress`, `retry_queued`, `in_review`, `done`

**Operations:**
- `FetchIssues()` → list all non-terminal issues (`todo`, `in_progress`, and ready `retry_queued`), excluding `in_review` and `done`
- `ClaimIssue(id)` → mark as in_progress, set claimed_by
- `ReleaseIssue(id)` → mark as todo, clear claimed_by
- `UpdateIssueState(id, state)` → update state
- `PostComment(id, body)` → append to comments file

Issue states serialize as string labels (`todo`, `in_progress`, etc.) rather than integers for durability.

### 3. Runtime Records

The recorder stores run diagnostics in `.maestro/runs/` (sibling to the board directory).

Typical contents:

```bash
.maestro/runs/
├── _orchestrator/
│   └── events.jsonl
└── CB-1/
    ├── issue.json
    ├── summary.json
    └── attempts/
        └── 001/
            ├── meta.json
            ├── prompt.md
            ├── events.jsonl
            ├── stdout.log
            ├── stderr.log
            ├── preflight/
            │   ├── git-status.txt
            │   └── git-worktree-list.txt
            ├── stages/
            │   ├── plan/
            │   │   ├── manifest.json
            │   │   ├── prompt.md
            │   │   ├── response.md
            │   │   ├── result.json
            │   │   ├── events.jsonl
            │   │   ├── stdout.log
            │   │   └── stderr.log
            │   ├── execute/
            │   │   ├── manifest.json
            │   │   ├── prompt.md
            │   │   ├── response.md
            │   │   ├── result.json
            │   │   ├── diff.patch
            │   │   ├── events.jsonl
            │   │   ├── stdout.log
            │   │   └── stderr.log
            │   └── verify/
            │       ├── manifest.json
            │       ├── prompt.md
            │       ├── response.md
            │       ├── result.json
            │       ├── events.jsonl
            │       ├── stdout.log
            │       └── stderr.log
            ├── review/
            │   ├── handoff.md
            │   ├── decision.json
            │   └── notes.md
            └── postflight/
                ├── git-status.txt
                └── git-worktree-list.txt
```

These files are part of the source of truth for review and debugging.

### 4. Workspace Manager

Git worktree-based workspaces (default: a sibling `<repo>.worktrees/` directory outside the repo tree):

```bash
../<repo>.worktrees/
├── CB-1/          # Branch: opencode/CB-1
│   └── (repo files)
├── CB-2/          # Branch: opencode/CB-2
│   └── (repo files)
└── ...
```

**Operations:**
- `Create(issue)` → `git worktree add ../<repo>.worktrees/CB-1 -b opencode/CB-1`
- `Cleanup(issueID)` → `git worktree remove ../<repo>.worktrees/CB-1` (human-driven after review; not automatic on runtime success)
- `Exists(issueID)` → check if workspace exists

Workspace creation is idempotent — calling it multiple times for the same issue reuses the existing worktree. This is essential because the pipeline calls it before each stage.

### 5. OpenCode Agent Runner

Manages `opencode serve` process:

1. **Start server** (per workspace):
   ```bash
   opencode serve --port 9090
   # Parse: "listening on http://127.0.0.1:9090"
   ```

2. **Create session**:
   ```bash
   POST http://127.0.0.1:9090/session
   → { "id": "sess-123" }
   ```

3. **Submit task**:
   ```bash
   POST http://127.0.0.1:9090/session/sess-123/prompt_async
   Content-Type: application/json
   { "parts": [{ "type": "text", "text": "..." }] }
   → 204 No Content
   ```

4. **Stream events** (SSE):
   ```bash
   GET http://127.0.0.1:9090/event
   Accept: text/event-stream

   event: session.status
   data: {"type":"session.status","properties":{"sessionID":"sess-123","status":{"type":"idle"}}}
   ```

5. **Abort** (if needed):
   ```bash
   POST http://127.0.0.1:9090/session/sess-123/abort
   → 200 OK
   ```

6. **Stop server**:
   ```bash
   kill -INT <pid>
   ```

**Key SSE events:**
- `session.status` with `status.type: idle` → session done
- `session.error` → session failed
- `server.heartbeat` → ignore

**Agent selection:** The runner reads `types.StageContext` from the context to determine which agent to use for the current stage. This is set by the orchestrator before each stage call.

### 6. Pipeline Runner

The pipeline runner (`internal/pipeline/runner.go`) owns the lifecycle of one stage:

```go
func (r *Runner) Run(ctx context.Context, issue types.Issue, attempt int, stage types.Stage, emit func(types.OrchestratorEvent)) (*Result, error)
```

For each stage it:
1. Creates (or reuses) the workspace
2. Builds a stage-specific prompt:
   - **plan**: "You are in PLANNING mode... Do NOT make any code changes yet."
   - **execute**: "You are in EXECUTION mode... Make the necessary code changes."
   - **verify**: "You are in VERIFICATION mode... Provide a pass/fail assessment."
3. Starts the agent with the stage context
4. Monitors events until completion or context cancellation
5. Captures postflight git state (status, worktree list, diff, commit)
6. Writes stage artifacts via `diagnostics.StageRecorder`
7. Returns a `Result` with success/error, workspace path, branch, diff, tokens

The runner is called three times per issue (plan → execute → verify) by the orchestrator.

### 7. Orchestrator

Main loop:

```
┌─────────────────────────────────────────────────────────┐
│  for {                                                  │
│    poll interval tick                                   │
│    ├─ FetchIssues()                                    │
│    ├─ ReconcileRunning()  ← check stalls, timeouts     │
│    ├─ DispatchBackoff()   ← retry ready issues         │
│    └─ DispatchReady()     ← claim new issues           │
│  }                                                      │
└─────────────────────────────────────────────────────────┘
```

**DispatchReady:**
- Skip if at max concurrency
- Skip if issue already managed
- Claim issue → Create workspace → Start pipeline from **plan** stage
- On success, move the issue to `in_review` and keep the worktree + run records intact for human review

**DispatchBackoff:**
- Check retry timestamps
- Re-claim issue → Resume from the **failed stage** (not from plan)
- The `BackoffEntry` stores the stage to resume from

**ReconcileRunning:**
- Check for stale agents (no events recently)
- Check for timeout (configurable)
- On stall/timeout: Stop agent, enqueue backoff for the current stage

**Stage sequence in `startRun()`:**
```
plan → execute → verify
```

If plan fails → retry plan.  
If execute fails → retry execute.  
If verify fails → retry verify.  
If all pass → move to `in_review`.

**Backoff strategy:**
- Attempt 1: retry in 30s
- Attempt 2: retry in 60s
- Attempt 3: retry in 120s
- ...exponential, max 4 minutes
- Jitter: ±20% random variation

### 8. Diagnostics Recorder

The recorder (`internal/diagnostics/recorder.go` + `stages.go`) persists every decision and artifact:

- `EnsureIssue(issue)` creates/updates `issue.json` and `summary.json`
- `BeginAttempt(...)` creates the attempt directory with preflight snapshots
- `BeginStage(manifest, prompt)` creates the stage directory and writes `manifest.json` + `prompt.md`
- `StageRecorder.Finish(result, response, diff)` writes `response.md`, `result.json`, updates `manifest.json`
- `RecordReviewHandoff(body, notes)` writes `review/handoff.md` and `notes.md`
- `RecordReviewDecision(decision)` writes `review/decision.json` and updates summary
- `FinalizeAttempt(...)` writes postflight snapshots and finalizes `meta.json`

All JSON writes are atomic (write to `.tmp` then rename).

### 9. Charm TUI

Bubble Tea model shows four sections:

**Running agents table:**
```
Issue     Title                      Stage   PID        Tokens     Age   Attempt
● CB-1    Fix login bug              Verify  12345     1.5K/2.3K   5m    #2
```

**Review queue:**
```
┌ Ready for Human Review ────────────────────────────────────────[1]─┐
│ CB-1  Fix login bug                                              │
│   branch:  opencode/CB-1                                         │
│   workspace: /path/to/workspace                                  │
│   ready:   12m ago                                               │
│   stages:  ✓ plan  ✓ execute  ✓ verify                           │
└──────────────────────────────────────────────────────────────────┘
```

**Backoff queue:**
```
┌ Backoff Queue ─────────────────────────────────────────────────[1]─┐
│ CB-1  retry in 2m15s  (attempt #2)                               │
│   stage:   execute failed                                        │
│   reason:  tool error: go build failed                           │
└──────────────────────────────────────────────────────────────────┘
```

**Event log:**
```
  [Events]
  12:14:41 CB-1  [plan] started (attempt #1)
  12:14:42 CB-1  [plan] agent started (pid: 12345)
  12:15:10 CB-1  [plan] completed
  12:15:11 CB-1  [execute] started
```

**Key bindings:**
- `q` / `ctrl+c` — quit
- `↑↓` — scroll event log
- `r` — refresh (force poll)

## Implementation Order

### Phase 1: Core contracts and artifact schema
1. **go.mod setup** — dependencies only
2. **Pipeline types** — `Stage`, `StageManifest`, `StageResult`, `StageFailureKind`, `ReviewDecision`
3. **Diagnostics recorder** — `Recorder`, `AttemptRecorder`, `StageRecorder`
4. **Issue state JSON serialization** — board-state strings

### Phase 2: Stage-aware OpenCode runtime
5. **Config parser** — add `opencode.agents` per-stage mapping
6. **Pipeline runner** — `Run()` accepts `types.Stage`, builds stage prompts, records stage artifacts
7. **Agent context** — `StageContext` via context for agent selection

### Phase 3: Orchestrator pipeline state machine
8. **Main loop** — run plan → execute → verify sequentially
9. **Stage-scoped retry** — `BackoffEntry` stores stage, resume from failed stage
10. **Typed failure classification** — `classifyStageFailure()` maps errors to `StageFailureKind`
11. **Review handoff** — preserve worktree, write `review/handoff.md`

### Phase 4: TUI visibility and review queue
12. **Stage-aware rows** — show current stage, attempt number
13. **Review queue** — show wait time, completed stages
14. **Backoff queue** — show failed stage, failure kind, ETA
15. **Event log** — human-readable stage transitions

### Phase 5: Tests and smoke harness
16. **Fake runner coverage** — happy path, plan failure, execute failure, verify failure
17. **Artifact assertions** — stage files exist, review handoff written
18. **Retry regressions** — stall, timeout, stage-scoped retry
19. **End-to-end smoke** — `in_review` and `done` paths

### Phase 6: Docs and migration notes
20. **Update implementation docs** — this file, what-maestro-is.md, README.md
21. **Migration notes** — what changed from single-stage to pipeline
22. **Spec alignment** — ensure docs link to `docs/specs/orchestrator-owned-pipeline/`

## Key Types

```go
// Issue states
const (
    StateUnclaimed    IssueState = iota   // "todo"
    StateClaimed                          // internal
    StateRunning                          // "in_progress"
    StateRetryQueued                      // "retry_queued"
    StateInReview                         // "in_review"
    StateReleased                         // "done"
)

// Pipeline stages
type Stage string
const (
    StagePlan        Stage = "plan"
    StageExecute     Stage = "execute"
    StageVerify      Stage = "verify"
    StageHumanReview Stage = "human_review"
)

// Stage state
type StageState string
const (
    StageStateRunning  StageState = "running"
    StageStatePassed   StageState = "passed"
    StageStateFailed   StageState = "failed"
    StageStateBlocked  StageState = "blocked"
    StageStateRetrying StageState = "retrying"
    StageStateSkipped  StageState = "skipped"
)

// Stage failure kinds
type StageFailureKind string
const (
    StageFailurePromptError       StageFailureKind = "prompt_error"
    StageFailureSessionStartError StageFailureKind = "session_start_error"
    StageFailureTimeout           StageFailureKind = "timeout"
    StageFailureStall             StageFailureKind = "stall"
    StageFailureModelFailure      StageFailureKind = "model_failure"
    StageFailureWorkspaceError    StageFailureKind = "workspace_error"
    StageFailureToolError         StageFailureKind = "tool_error"
    StageFailureVerification      StageFailureKind = "verification_failed"
    StageFailureHandoffError      StageFailureKind = "handoff_error"
    StageFailureDecisionMissing   StageFailureKind = "decision_missing"
)

// Core types
type Issue struct {
    ID          string
    Identifier  string
    Title       string
    Description string
    State       IssueState
    Labels      []string
    URL         string
    RetryAfter  *time.Time
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type StageManifest struct {
    Stage         Stage
    Attempt       int
    Status        StageState
    Agent         string
    SessionID     string
    WorkspacePath string
    PromptPath    string
    ResponsePath  string
    ResultPath    string
    EventsPath    string
    StdoutPath    string
    StderrPath    string
    DiffPath      string
    StartedAt     time.Time
    FinishedAt    *time.Time
    ErrorKind     StageFailureKind
    Retryable     bool
}

type StageResult struct {
    Stage       Stage
    Status      StageState
    Summary     string
    FailureKind StageFailureKind
    Retryable   bool
    Evidence    []string
    NextAction  string
    StartedAt   time.Time
    FinishedAt  time.Time
}

type ReviewDecision struct {
    Decision      ReviewDecisionKind
    ReviewedBy    string
    ReviewedAt    time.Time
    Notes         string
    FollowUpState ReviewFollowUpState
}

type BackoffEntry struct {
    IssueID string
    Attempt int
    Stage   Stage     // which stage to resume from
    RetryAt time.Time
    Error   string
}

// Agent interface
type AgentRunner interface {
    Start(ctx context.Context, issue Issue, workspace string, prompt string) (*AgentProcess, error)
    Stop(proc *AgentProcess) error
    Close() error
}

type AgentProcess struct {
    PID       int
    SessionID string
    Events    chan AgentEvent
    Done      chan error
    ServerURL string
}

// Orchestrator event
type OrchestratorEvent struct {
    Type      string
    IssueID   string
    Timestamp time.Time
    Payload   any
}
```

## CLI Interface

```bash
# Initialize a project (creates .maestro/ + WORKFLOW.md)
maestro init

# Run with TUI (auto-discovers WORKFLOW.md or .maestro/WORKFLOW.md)
maestro

# Run with explicit config
maestro --config WORKFLOW.md

# Run headless
maestro --no-tui

# Run with custom log level
maestro --log-level debug

# Dry run (exactly one poll cycle, then exit)
maestro --dry-run
```

## Future Extensions (Out of Scope)

When ready to add:
1. **Teams** — multi-agent coordination with phase pipeline
2. **External trackers** — Linear, GitHub Issues
3. **Other agents** — Codex, OMX, OMC
4. **Web dashboard** — React + SSE API
5. **Config hot-reload** — fsnotify on WORKFLOW.md

## Similar Projects

- [OpenAI Symphony](https://github.com/openai/symphony) — Original Elixir implementation
- [Maestro](https://github.com/junhoyeo/maestro) — Full Go implementation (this is derived from)
