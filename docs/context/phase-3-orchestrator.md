# Phase 3: Orchestrator

> **Status:** Ready to implement | **Prerequisite:** Phase 1 ✅, Phase 2 ✅

The brain — ties tracker, workspace, and agent together. Manages the full lifecycle of an issue from claim to completion or retry.

## Current Project State

```
internal/
├── types/types.go       ✅ Issue, AgentProcess, AgentEvent, BackoffEntry, AgentRunner interface
├── agent/
│   ├── opencode.go      ✅ OpenCodeRunner implements AgentRunner
│   └── events.go        ✅ Agent event constants (session.status, etc.)
├── config/config.go     ✅ Config struct with OpenCode.Profile, OpenCode.Agent, OpenCode.ConfigDir
├── tracker/local.go     ✅ LocalTracker implements IssueTracker
├── workspace/manager.go ✅ Manager with Create(), Cleanup(), Exists()
└── orchestrator/        ❌ EMPTY - files to be created here
```

## Goals
- [ ] Define typed `OrchestratorEvent` for high-level events (separate from `AgentEvent`)
- [ ] Implement exponential backoff with jitter for retries
- [ ] Build state management for active runs
- [ ] Build main polling loop: poll → claim → dispatch → monitor → retry
- [ ] Emit events for TUI consumption

## Files to Create

```
internal/orchestrator/
├── events.go        # OrchestratorEvent type + constants
├── backoff.go      # BackoffManager
├── state.go        # StateManager for active runs
└── orchestrator.go # Main Orchestrator struct + loop
```

---

## 3.1 Event System (`internal/orchestrator/events.go`)

### Why Separate from AgentEvent?

`AgentEvent` (from Phase 2) = low-level events from OpenCode server (session.status, tokens.updated, etc.)

`OrchestratorEvent` (this phase) = high-level events from the orchestrator (IssueClaimed, WorkspaceCreated, AgentStarted, etc.)

### Type Definition

```go
package orchestrator

import "time"

// OrchestratorEvent represents a high-level event in the orchestrator lifecycle.
type OrchestratorEvent struct {
    Type      string
    IssueID   string
    Timestamp time.Time
    Payload   interface{}
}

// Event type constants
const (
    EventPollStarted        = "poll_started"
    EventIssueClaimed       = "issue.claimed"
    EventWorkspaceCreated   = "workspace.created"
    EventPromptBuilt        = "prompt.built"
    EventAgentStarted       = "agent.started"
    EventTokensUpdated      = "tokens.updated"
    EventAgentOutput       = "agent.output"
    EventAgentFinished      = "agent.finished"
    EventIssueCompleted     = "issue.completed"
    EventIssueRetrying     = "issue.retrying"
    EventBackoffQueued     = "backoff.queued"
    EventStallDetected     = "stall.detected"
    EventTimeoutDetected   = "timeout.detected"
    EventPollCompleted     = "poll.completed"
)
```

### Event Payloads

| Event | Payload Type |
|-------|-------------|
| `EventPollStarted` | `struct{}` |
| `EventIssueClaimed` | `IssueClaimedPayload { Issue }` |
| `EventWorkspaceCreated` | `WorkspaceCreatedPayload { IssueID, Path }` |
| `EventPromptBuilt` | `PromptBuiltPayload { IssueID, Length int }` |
| `EventAgentStarted` | `AgentStartedPayload { IssueID, PID, SessionID }` |
| `EventTokensUpdated` | `TokensUpdatedPayload { IssueID, TokensIn, TokensOut }` |
| `EventAgentOutput` | `AgentOutputPayload { IssueID, Text }` |
| `EventAgentFinished` | `AgentFinishedPayload { IssueID, Success bool, Error string }` |
| `EventIssueCompleted` | `IssueCompletedPayload { IssueID }` |
| `EventIssueRetrying` | `IssueRetryingPayload { IssueID, Attempt, RetryAt }` |
| `EventBackoffQueued` | `BackoffQueuedPayload { IssueID, Attempt, RetryAt }` |
| `EventStallDetected` | `StallDetectedPayload { IssueID, LastEventAge time.Duration }` |
| `EventTimeoutDetected` | `TimeoutDetectedPayload { IssueID, Elapsed time.Duration }` |

---

## 3.2 Backoff Logic (`internal/orchestrator/backoff.go`)

### BackoffEntry

Already defined in `types/types.go`:
```go
type BackoffEntry struct {
    IssueID  string
    Attempt  int
    RetryAt  time.Time
    Error    string
}
```

### BackoffManager

```go
type BackoffManager struct {
    entries   map[string]*BackoffEntry  // keyed by IssueID
    maxDelay  time.Duration
    mu        sync.Mutex
}
```

### Formula

| Attempt | Delay (no jitter) |
|---------|-------------------|
| 1 | 30s |
| 2 | 60s |
| 3 | 120s |
| 4 | 240s (cap) |
| n | min(30*2^(n-1), 240)s |

**Jitter:** ±20% random variation to avoid thundering herd.

### Operations

```go
// NewBackoffManager creates a backoff manager with optional max delay.
func NewBackoffManager(maxDelay time.Duration) *BackoffManager

// CalculateDelay computes delay for an attempt with jitter.
func (b *BackoffManager) CalculateDelay(attempt int) time.Duration

// Enqueue adds a retry entry for an issue.
func (b *BackoffManager) Enqueue(issueID string, attempt int, errorMsg string) *BackoffEntry

// Ready returns entries whose retry time has passed.
func (b *BackoffManager) Ready() []*BackoffEntry

// Remove deletes an entry (e.g., when issue completes).
func (b *BackoffManager) Remove(issueID string)

// Get returns an entry by issue ID.
func (b *BackoffManager) Get(issueID string) (*BackoffEntry, bool)

// Len returns the number of queued entries.
func (b *BackoffManager) Len() int
```

---

## 3.3 State Management (`internal/orchestrator/state.go`)

### Why Separate from types.go?

`types.RunState` is orchestrator-internal, not needed by other packages. Keep it in orchestrator package.

### RunState

```go
package orchestrator

import (
    "github.com/fatihkarahan/contrabass-pi/internal/types"
    "time"
)

// RunState tracks an active issue execution.
type RunState struct {
    Issue       types.Issue     // The issue being run
    Attempt     int             // Current attempt number (1, 2, 3, ...)
    Process     *types.AgentProcess  // The agent process
    Phase       types.RunPhase  // Current phase
    StartedAt   time.Time       // When the run started
    LastEventAt time.Time       // When the last event was received
    TokensIn    int64           // Tokens sent to agent
    TokensOut   int64           // Tokens received from agent
    Error       string          // Last error message (if any)
}
```

### StateManager

```go
type StateManager struct {
    runs map[string]*RunState  // keyed by IssueID
    mu   sync.RWMutex
}
```

### Operations

```go
// NewStateManager creates a new state manager.
func NewStateManager() *StateManager

// Add creates a new run state.
func (s *StateManager) Add(issueID string, issue types.Issue, attempt int, proc *types.AgentProcess)

// UpdatePhase updates the phase for a run.
func (s *StateManager) UpdatePhase(issueID string, phase types.RunPhase)

// UpdateTokens updates token counts for a run.
func (s *StateManager) UpdateTokens(issueID string, tokensIn, tokensOut int64)

// UpdateLastEvent updates the last event timestamp.
func (s *StateManager) UpdateLastEvent(issueID string)

// SetError sets the error for a run.
func (s *StateManager) SetError(issueID string, err string)

// Get returns a run state.
func (s *StateManager) Get(issueID string) (*RunState, bool)

// Remove deletes a run state.
func (s *StateManager) Remove(issueID string)

// Len returns the number of active runs.
func (s *StateManager) Len() int

// GetAll returns all run states.
func (s *StateManager) GetAll() []*RunState

// GetByPhase returns runs in a specific phase.
func (s *StateManager) GetByPhase(phase types.RunPhase) []*RunState
```

---

## 3.4 Main Loop (`internal/orchestrator/orchestrator.go`)

### Orchestrator Struct

```go
package orchestrator

import (
    "context"
    "github.com/fatihkarahan/contrabass-pi/internal/types"
)

type Orchestrator struct {
    Config      *config.Config           // From config.Load()
    Tracker     tracker.IssueTracker      // LocalTracker or other
    Workspace   workspace.Manager        // Git worktree manager
    AgentRunner agent.AgentRunner        // OpenCodeRunner
    Events      chan types.OrchestratorEvent  // Output channel for TUI
    State       *StateManager
    Backoff     *BackoffManager
    
    // Internal
    ctx         context.Context
    cancel      context.CancelFunc
    promptTmpl  string  // Template from Config.Content
}
```

### Constructor

```go
// New creates a new Orchestrator.
func New(cfg *config.Config, tracker tracker.IssueTracker, ws workspace.Manager, runner agent.AgentRunner) *Orchestrator
```

### Main Loop

```go
// Run starts the orchestrator loop. Blocks until context is cancelled.
func (o *Orchestrator) Run(ctx context.Context) error
```

**Loop pseudocode:**
```
tick := time.NewTicker(pollInterval)
defer tick.Stop()

for {
    select {
    case <-ctx.Done():
        return o.shutdown()
    case <-tick.C:
        o.poll()
    }
}
```

### poll() Method

```go
func (o *Orchestrator) poll() {
    o.emit(EventPollStarted, nil, struct{}{})
    defer o.emit(EventPollCompleted, nil, struct{}{})
    
    // 1. ReconcileRunning() — check stalls, timeouts
    o.reconcileRunning()
    
    // 2. DispatchBackoff() — retry ready entries  
    o.dispatchBackoff()
    
    // 3. DispatchReady() — claim and start new issues
    o.dispatchReady()
}
```

### reconcileRunning()

```go
func (o *Orchestrator) reconcileRunning() {
    for _, run := range o.State.GetAll() {
        elapsed := time.Since(run.StartedAt)
        lastEventAge := time.Since(run.LastEventAt)
        
        // Check timeout
        if elapsed > o.Config.AgentTimeoutMs {
            o.handleStallOrTimeout(run, "timeout", fmt.Sprintf("ran for %v", elapsed))
            continue
        }
        
        // Check stall
        if lastEventAge > o.Config.StallTimeoutMs {
            o.handleStallOrTimeout(run, "stall", fmt.Sprintf("no event for %v", lastEventAge))
            continue
        }
    }
}
```

### handleStallOrTimeout()

```go
func (o *Orchestrator) handleStallOrTimeout(run *RunState, reason string, detail string) {
    // 1. Stop agent process
    if run.Process != nil {
        _ = o.AgentRunner.Stop(run.Process)
    }
    
    // 2. Emit event
    o.emit(EventStallDetected, run.Issue.ID, StallDetectedPayload{
        IssueID: run.Issue.ID,
        Reason: reason,
        Detail: detail,
    })
    
    // 3. Enqueue backoff
    attempt := run.Attempt + 1
    entry := o.Backoff.Enqueue(run.Issue.ID, attempt, detail)
    
    // 4. Emit backoff queued
    o.emit(EventBackoffQueued, run.Issue.ID, BackoffQueuedPayload{
        IssueID: run.Issue.ID,
        Attempt: attempt,
        RetryAt: entry.RetryAt,
    })
    
    // 5. Remove from active state
    o.State.Remove(run.Issue.ID)
}
```

### dispatchBackoff()

```go
func (o *Orchestrator) dispatchBackoff() {
    for _, entry := range o.Backoff.Ready() {
        // Skip if at capacity
        if o.State.Len() >= o.Config.MaxConcurrency {
            return
        }
        
        // Re-claim the issue
        issue, err := o.Tracker.ClaimIssue(entry.IssueID)
        if err != nil {
            o.Backoff.Remove(entry.IssueID)
            continue
        }
        
        // Start the run (reuse workspace if exists)
        o.startRun(issue, entry.Attempt)
    }
}
```

### dispatchReady()

```go
func (o *Orchestrator) dispatchReady() {
    // Skip if at capacity
    if o.State.Len() >= o.Config.MaxConcurrency {
        return
    }
    
    // Fetch unclaimed issues
    issues, err := o.Tracker.FetchIssues()
    if err != nil {
        return
    }
    
    for _, issue := range issues {
        // Skip if already running or in backoff
        if o.State.Len() >= o.Config.MaxConcurrency {
            return
        }
        if _, inState := o.State.Get(issue.ID); inState {
            continue
        }
        if _, inBackoff := o.Backoff.Get(issue.ID); inBackoff {
            continue
        }
        
        // Claim the issue
        claimed, err := o.Tracker.ClaimIssue(issue.ID)
        if err != nil {
            continue
        }
        
        // Emit claimed
        o.emit(EventIssueClaimed, issue.ID, IssueClaimedPayload{Issue: claimed})
        
        // Start the run
        o.startRun(claimed, 1)
    }
}
```

### startRun() — The Core Dispatch Logic

```go
func (o *Orchestrator) startRun(issue types.Issue, attempt int) {
    // 1. Create workspace
    wsPath, err := o.Workspace.Create(issue)
    if err != nil {
        o.handleStartError(issue, attempt, err, "workspace creation failed")
        return
    }
    o.emit(EventWorkspaceCreated, issue.ID, WorkspaceCreatedPayload{
        IssueID: issue.ID,
        Path:    wsPath,
    })
    
    // 2. Build prompt from template
    prompt := o.buildPrompt(issue)
    o.emit(EventPromptBuilt, issue.ID, PromptBuiltPayload{
        IssueID: issue.ID,
        Length:  len(prompt),
    })
    
    // 3. Start agent (OpenCodeRunner.Start handles server lifecycle)
    proc, err := o.AgentRunner.Start(o.ctx, issue, wsPath, prompt)
    if err != nil {
        o.handleStartError(issue, attempt, err, "agent start failed")
        return
    }
    
    // 4. Add to state
    o.State.Add(issue.ID, issue, attempt, proc)
    
    // 5. Emit agent started
    o.emit(EventAgentStarted, issue.ID, AgentStartedPayload{
        IssueID:   issue.ID,
        PID:       proc.PID,
        SessionID: proc.SessionID,
    })
    
    // 6. Spawn goroutine to monitor this agent
    go o.monitorAgent(issue.ID, proc)
}
```

### buildPrompt() — Template Rendering

```go
func (o *Orchestrator) buildPrompt(issue types.Issue) string {
    template := o.promptTmpl
    // Simple template substitution
    template = strings.ReplaceAll(template, "{{ issue.id }}", issue.ID)
    template = strings.ReplaceAll(template, "{{ issue.identifier }}", issue.Identifier)
    template = strings.ReplaceAll(template, "{{ issue.title }}", issue.Title)
    template = strings.ReplaceAll(template, "{{ issue.description }}", issue.Description)
    return strings.TrimSpace(template)
}
```

### monitorAgent() — Event Stream Handler

```go
func (o *Orchestrator) monitorAgent(issueID string, proc *types.AgentProcess) {
    defer func() {
        if r := recover(); r != nil {
            // Channel closed, process done
        }
    }()
    
    for {
        select {
        case <-o.ctx.Done():
            return
        case event, ok := <-proc.Events:
            if !ok {
                // Events channel closed - check done channel
                select {
                case err := <-proc.Done:
                    o.handleAgentDone(issueID, err)
                default:
                }
                return
            }
            o.handleAgentEvent(issueID, event)
        case err := <-proc.Done:
            o.handleAgentDone(issueID, err)
            return
        }
    }
}
```

### handleAgentEvent()

```go
func (o *Orchestrator) handleAgentEvent(issueID string, event types.AgentEvent) {
    // Update last event timestamp
    o.State.UpdateLastEvent(issueID)
    
    // Extract and update tokens
    if tokensIn, tokensOut := agent.ExtractTokens(event); tokensIn > 0 || tokensOut > 0 {
        o.State.UpdateTokens(issueID, tokensIn, tokensOut)
        o.emit(EventTokensUpdated, issueID, TokensUpdatedPayload{
            IssueID:   issueID,
            TokensIn:  tokensIn,
            TokensOut: tokensOut,
        })
    }
    
    // Handle other event types as needed
    switch event.Type {
    case agent.EventTypeMessageUpdated:
        // Extract text content and emit for TUI display
        if text := extractTextContent(event); text != "" {
            o.emit(EventAgentOutput, issueID, AgentOutputPayload{
                IssueID: issueID,
                Text:   text,
            })
        }
    }
}
```

### handleAgentDone()

```go
func (o *Orchestrator) handleAgentDone(issueID string, runErr error) {
    run, ok := o.State.Get(issueID)
    if !ok {
        return
    }
    
    // Update state
    o.State.UpdateLastEvent(issueID)
    
    if runErr == nil {
        // Success
        o.State.Remove(issueID)
        _ = o.Tracker.UpdateIssueState(issueID, types.StateInReview) // runtime success hands off to human review
        // workspace cleanup is human-driven after review/merge
        o.Backoff.Remove(issueID)
        
        o.emit(EventAgentFinished, issueID, AgentFinishedPayload{
            IssueID: issueID,
            Success: true,
            Error:   "",
        })
        o.emit(EventIssueCompleted, issueID, IssueCompletedPayload{IssueID: issueID})
    } else {
        // Failure - enqueue retry
        attempt := run.Attempt + 1
        o.State.Remove(issueID)
        
        entry := o.Backoff.Enqueue(issueID, attempt, runErr.Error())
        
        o.emit(EventAgentFinished, issueID, AgentFinishedPayload{
            IssueID: issueID,
            Success: false,
            Error:   runErr.Error(),
        })
        o.emit(EventIssueRetrying, issueID, IssueRetryingPayload{
            IssueID: issueID,
            Attempt: attempt,
            RetryAt: entry.RetryAt,
        })
    }
}
```

---

## 3.5 Prompt Template Format

The `WORKFLOW.md` body (Config.Content) is the prompt template:

```markdown
# Issue: {{ issue.title }}

{{ issue.description }}

---
Priority: {{ issue.labels | join: ", " }}
```

### Supported Template Variables

| Variable | Description |
|----------|-------------|
| `{{ issue.id }}` | Issue ID (e.g., "CB-1") |
| `{{ issue.identifier }}` | Same as ID |
| `{{ issue.title }}` | Issue title |
| `{{ issue.description }}` | Issue description |
| `{{ issue.labels }}` | Comma-separated labels |

---

## 3.6 Helper Functions

### emit()

```go
func (o *Orchestrator) emit(eventType, issueID string, payload interface{}) {
    select {
    case o.Events <- OrchestratorEvent{
        Type:      eventType,
        IssueID:   issueID,
        Timestamp: time.Now(),
        Payload:   payload,
    }:
    default:
        // Channel full, skip event
    }
}
```

### extractTextContent()

```go
func extractTextContent(event types.AgentEvent) string {
    payload, ok := event.Payload.(map[string]interface{})
    if !ok {
        return ""
    }
    // Try to extract text from various event structures
    if part, ok := payload["part"].(map[string]interface{}); ok {
        if text, ok := part["text"].(string); ok {
            return text
        }
    }
    return ""
}
```

---

## 3.7 Graceful Shutdown

```go
func (o *Orchestrator) shutdown() error {
    // Cancel context
    o.cancel()
    
    // Stop all running agents
    for _, run := range o.State.GetAll() {
        if run.Process != nil {
            _ = o.AgentRunner.Stop(run.Process)
        }
    }
    
    // Close agent runner
    _ = o.AgentRunner.Close()
    
    // Close event channel
    close(o.Events)
    
    return nil
}
```

---

## Dependency Chain

```
Phase 1:
  types/types.go      ─────────────────────────────────────────┐
  config/config.go    ───────────────────────────────────────┐  │
  tracker/local.go    ──────────────────────────────────────┐  │  │
  workspace/manager.go ───────────────────────────────┐  │  │  │
Phase 2:                                                        │
  agent/opencode.go  ──────────────────────────────┐  │  │  │  │
  agent/events.go    ───────────────────────────┐  │  │  │  │  │
                                                │  │  │  │  │  │
                 orchestrator/ ←────────────────┴──┴──┴──┴──┘  │
                 (events.go, backoff.go, state.go, orchestrator.go)
```

---

## Config Fields Used

| Field | Usage |
|-------|-------|
| `MaxConcurrency` | Skip dispatch when at capacity |
| `PollIntervalMs` | Tick interval |
| `AgentTimeoutMs` | Stall detection |
| `StallTimeoutMs` | Stall detection |
| `MaxRetryBackoffMs` | Backoff cap |
| `OpenCode.Profile` | Passed to AgentRunner (via config) |
| `OpenCode.Agent` | Passed to AgentRunner (via config) |
| `OpenCode.ConfigDir` | Passed to AgentRunner (via config) |

---

## Testing Strategy

1. **Unit test BackoffManager** — verify delay calculation and jitter
2. **Unit test StateManager** — verify add/update/remove operations
3. **Unit test buildPrompt()** — verify template substitution
4. **Integration test with mock tracker** — full flow: claim → workspace → agent start
5. **Integration test backoff flow** — simulate failure, verify retry scheduled

---

## Verification Checklist

- [ ] Backoff delays follow formula with jitter
- [ ] ReadyEntries() returns only entries where RetryAt <= now
- [ ] ReconcileRunning() detects stalls (> StallTimeoutMs since LastEventAt)
- [ ] ReconcileRunning() detects timeouts (> AgentTimeoutMs since StartedAt)
- [ ] dispatchReady() respects MaxConcurrency limit
- [ ] dispatchReady() skips issues already running or in backoff
- [ ] Agent events flow through to OrchestratorEvent emissions
- [ ] handleAgentDone() removes from State and enqueues backoff on error
- [ ] handleAgentDone() marks complete and cleans up on success
- [ ] Graceful shutdown stops all agents and closes channels
- [ ] All 13 event types are emitted at appropriate times
