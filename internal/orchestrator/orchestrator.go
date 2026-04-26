// Package orchestrator provides the main orchestrator that ties together
// tracker, workspace, and agent components.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/config"
	"github.com/fatihkarahan/contrabass-pi/internal/diagnostics"
	"github.com/fatihkarahan/contrabass-pi/internal/pipeline"
	"github.com/fatihkarahan/contrabass-pi/internal/types"
	"github.com/fatihkarahan/contrabass-pi/internal/util"
	"github.com/fatihkarahan/contrabass-pi/internal/workspace"
)

// Orchestrator ties together tracker, workspace, and agent components.
// It manages the full lifecycle of an issue from claim to completion or retry.
type Orchestrator struct {
	Config      *config.Config
	Tracker     types.IssueTracker
	Workspace   workspace.WorkspaceManager // interface
	AgentRunner types.AgentRunner
	Recorder    *diagnostics.Recorder
	Events      chan types.OrchestratorEvent
	State       *StateManager
	Backoff     *BackoffManager

	// Internal
	ctx        context.Context
	cancel     context.CancelFunc
	promptTmpl string

	// Goroutine tracking for graceful shutdown
	wg           sync.WaitGroup
	mu           sync.Mutex                    // protects running map
	running      map[string]context.CancelFunc // issueID -> cancel func for that run's context
	closed       bool
	shutdownOnce sync.Once
}

// New creates a new Orchestrator.
func New(cfg *config.Config, tr types.IssueTracker, ws workspace.WorkspaceManager, runner types.AgentRunner) *Orchestrator {
	ctx, cancel := context.WithCancel(context.Background())

	// Use max_retry_backoff_ms from config, or default to 4 minutes
	maxBackoff := 4 * time.Minute
	if cfg.MaxRetryBackoffMs > 0 {
		maxBackoff = time.Duration(cfg.MaxRetryBackoffMs) * time.Millisecond
	}

	return &Orchestrator{
		Config:      cfg,
		Tracker:     tr,
		Workspace:   ws,
		AgentRunner: runner,
		Events:      make(chan types.OrchestratorEvent, 256),
		State:       NewStateManager(),
		Backoff:     NewBackoffManager(maxBackoff),
		ctx:         ctx,
		cancel:      cancel,
		promptTmpl:  cfg.Content,
		running:     make(map[string]context.CancelFunc),
	}
}

// SetRecorder wires a persistent diagnostics recorder into the orchestrator.
func (o *Orchestrator) SetRecorder(recorder *diagnostics.Recorder) {
	o.Recorder = recorder
}

// Run starts the orchestrator loop. It blocks until the context is cancelled.
func (o *Orchestrator) Run() error {
	// Create ticker for polling
	pollInterval := time.Duration(o.Config.PollIntervalMs) * time.Millisecond
	if pollInterval <= 0 {
		pollInterval = 30 * time.Second
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	if o.ctx.Err() != nil {
		return o.shutdown()
	}

	// Run initial poll
	o.poll()

	for {
		select {
		case <-o.ctx.Done():
			return o.shutdown()
		case <-ticker.C:
			o.poll()
		}
	}
}

// RunOnce performs a single poll cycle and then shuts the orchestrator down.
func (o *Orchestrator) RunOnce() error {
	if o.ctx.Err() != nil {
		return o.shutdown()
	}

	o.poll()
	return o.shutdown()
}

// poll performs a single poll cycle.
func (o *Orchestrator) poll() {
	if o.isClosed() {
		return
	}

	o.emit(EventPollStarted, "", struct{}{})
	defer o.emit(EventPollCompleted, "", struct{}{})

	// 1. ReconcileRunning — check stalls, timeouts
	o.reconcileRunning()

	// 2. DispatchBackoff — retry ready entries
	o.dispatchBackoff()

	// 3. DispatchReady — claim and start new issues
	o.dispatchReady()
}

// reconcileRunning checks for stalls and timeouts in active runs.
func (o *Orchestrator) reconcileRunning() {
	for _, run := range o.State.GetAll() {
		if o.isClosed() {
			return
		}

		elapsed := time.Since(run.StartedAt)
		lastEventAge := time.Since(run.LastEventAt)

		agentTimeout := time.Duration(o.Config.AgentTimeoutMs) * time.Millisecond
		stallTimeout := time.Duration(o.Config.StallTimeoutMs) * time.Millisecond

		// Check timeout first
		if agentTimeout > 0 && elapsed > agentTimeout {
			o.handleTimeout(run, elapsed)
			continue
		}

		// Check stall
		if stallTimeout > 0 && lastEventAge > stallTimeout {
			o.handleStall(run, lastEventAge)
			continue
		}
	}
}

// handleTimeout handles a run that has exceeded the agent timeout.
func (o *Orchestrator) handleTimeout(run RunState, elapsed time.Duration) {
	issueID := run.Issue.ID

	// Cancel the run's context. The pipeline runner will stop the agent.
	o.mu.Lock()
	if cancel, ok := o.running[issueID]; ok {
		cancel()
		delete(o.running, issueID)
	}
	o.mu.Unlock()

	// Emit timeout event
	o.emit(EventTimeoutDetected, issueID, map[string]interface{}{
		"issue_id": issueID,
		"elapsed":  elapsed,
	})

	// Enqueue backoff
	attempt := run.Attempt + 1
	entry := o.Backoff.Enqueue(issueID, attempt, run.Stage, fmt.Sprintf("timeout after %v", elapsed))
	o.persistRetryQueue(issueID, entry.RetryAt)

	o.finalizeAttempt(issueID, run.Attempt, "timed_out", &entry.RetryAt, fmt.Errorf("timeout after %v", elapsed))

	o.emit(EventBackoffQueued, issueID, BackoffPayload{
		IssueID:     issueID,
		Attempt:     attempt,
		Stage:       run.Stage,
		RetryAt:     entry.RetryAt,
		Error:       fmt.Sprintf("timeout after %v", elapsed),
		FailureKind: types.StageFailureTimeout,
	})

	// Update phase to timed out
	o.State.Mutate(issueID, func(r *RunState) { r.Phase = types.PhaseTimedOut })

	// Remove from active state (will be retried via backoff)
	o.State.Remove(issueID)
}

// handleStall handles a run that has stalled (no recent events).
func (o *Orchestrator) handleStall(run RunState, lastEventAge time.Duration) {
	issueID := run.Issue.ID

	// Cancel the run's context. The pipeline runner will stop the agent.
	o.mu.Lock()
	if cancel, ok := o.running[issueID]; ok {
		cancel()
		delete(o.running, issueID)
	}
	o.mu.Unlock()

	// Emit stall event
	o.emit(EventStallDetected, issueID, map[string]interface{}{
		"issue_id":       issueID,
		"reason":         "stall",
		"detail":         fmt.Sprintf("no event received for %v", lastEventAge),
		"last_event_age": lastEventAge,
	})

	// Enqueue backoff
	attempt := run.Attempt + 1
	entry := o.Backoff.Enqueue(issueID, attempt, run.Stage, fmt.Sprintf("stall: no event for %v", lastEventAge))
	o.persistRetryQueue(issueID, entry.RetryAt)

	o.finalizeAttempt(issueID, run.Attempt, "stalled", &entry.RetryAt, fmt.Errorf("stall: no event for %v", lastEventAge))

	o.emit(EventBackoffQueued, issueID, BackoffPayload{
		IssueID:     issueID,
		Attempt:     attempt,
		Stage:       run.Stage,
		RetryAt:     entry.RetryAt,
		Error:       fmt.Sprintf("stall: no event for %v", lastEventAge),
		FailureKind: types.StageFailureTimeout,
	})

	// Update phase to stalled
	o.State.Mutate(issueID, func(r *RunState) { r.Phase = types.PhaseStalled })

	// Remove from active state (will be retried via backoff)
	o.State.Remove(issueID)
}

// dispatchBackoff retries ready backoff entries.
func (o *Orchestrator) dispatchBackoff() {
	if o.isClosed() {
		return
	}

	for _, entry := range o.Backoff.Ready() {
		if o.isClosed() {
			return
		}

		// Skip if at capacity — the entry stays in the backoff map with its
		// original RetryAt and will be ready again on the next poll.
		if o.Config.MaxConcurrency > 0 && o.State.Len() >= o.Config.MaxConcurrency {
			break
		}

		if o.isClosed() {
			return
		}

		// Consume the backoff entry before dispatching so it isn't dispatched
		// repeatedly on every poll while the issue is already running.
		o.Backoff.Remove(entry.IssueID)

		// Re-claim the issue
		issue, err := o.Tracker.ClaimIssue(entry.IssueID)
		if err != nil {
			// Issue no longer available for dispatch.
			continue
		}

		if o.Recorder != nil {
			_ = o.Recorder.EnsureIssue(issue)
		}

		// Start the run (reuse workspace if exists), resuming from the failed stage.
		startStage := entry.Stage
		if startStage == "" {
			startStage = types.StagePlan
		}
		o.startRun(issue, entry.Attempt, startStage)
	}
}

// dispatchReady claims and starts new issues.
func (o *Orchestrator) dispatchReady() {
	if o.isClosed() {
		return
	}

	// Skip if at capacity
	if o.Config.MaxConcurrency > 0 && o.State.Len() >= o.Config.MaxConcurrency {
		return
	}

	// Fetch unclaimed issues
	issues, err := o.Tracker.FetchIssues()
	if err != nil {
		o.emit(EventFetchError, "", map[string]interface{}{
			"operation": "FetchIssues",
			"error":     err.Error(),
		})
		return
	}

	sort.Slice(issues, func(i, j int) bool {
		if issues[i].CreatedAt.Equal(issues[j].CreatedAt) {
			return issues[i].ID < issues[j].ID
		}
		return issues[i].CreatedAt.Before(issues[j].CreatedAt)
	})

	for _, issue := range issues {
		if o.isClosed() {
			return
		}

		if issue.State == types.StateInReview || issue.State == types.StateReleased {
			continue
		}

		// Skip if at capacity
		if o.Config.MaxConcurrency > 0 && o.State.Len() >= o.Config.MaxConcurrency {
			return
		}

		// Skip if already running
		if _, inState := o.State.Get(issue.ID); inState {
			continue
		}

		// Skip if in backoff
		if _, inBackoff := o.Backoff.Get(issue.ID); inBackoff {
			continue
		}

		if o.isClosed() {
			return
		}

		// Claim the issue
		claimed, err := o.Tracker.ClaimIssue(issue.ID)
		if err != nil {
			continue
		}

		if o.Recorder != nil {
			_ = o.Recorder.EnsureIssue(claimed)
		}

		// Emit claimed
		o.emit(EventIssueClaimed, issue.ID, map[string]interface{}{"issue": claimed})

		// Start the run from the plan stage
		o.startRun(claimed, 1, types.StagePlan)
	}
}

// startRun starts a run for an issue with the given attempt number,
// beginning from startStage (plan, execute, or verify).
func (o *Orchestrator) startRun(issue types.Issue, attempt int, startStage types.Stage) {
	if startStage == "" || !startStage.Valid() {
		startStage = types.StagePlan
	}

	// Create a cancellable context for this run
	runCtx, runCancel := context.WithCancel(o.ctx)
	issueID := issue.ID

	// Track the cancel func
	o.mu.Lock()
	if o.closed {
		runCancel()
		o.mu.Unlock()
		return
	}
	o.running[issueID] = runCancel
	o.mu.Unlock()

	branchName := o.branchName(issueID)
	workspacePath := o.workspacePath(issueID)
	prompt := o.buildPrompt(issue)
	preflightStatus, preflightWorktreeList := o.captureGitState(o.workspaceBaseDir())

	if o.Recorder != nil {
		attemptRecorder, err := o.Recorder.BeginAttempt(issue, attempt, branchName, workspacePath, prompt, preflightStatus, preflightWorktreeList)
		if err == nil && attemptRecorder != nil {
			runCtx = diagnostics.WithAttemptRecorder(runCtx, attemptRecorder)
		}
	}

	// Add to active state
	o.State.Add(issueID, issue, attempt, startStage, nil)

	// Spawn goroutine to run the pipeline
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		defer runCancel()
		defer o.removeRunning(issueID)

		runner := &pipeline.Runner{
			Config:      o.Config,
			Workspace:   o.Workspace,
			AgentRunner: o.AgentRunner,
		}

		// Determine the ordered stage list starting from startStage.
		allStages := []types.Stage{types.StagePlan, types.StageExecute, types.StageVerify}
		var stages []types.Stage
		found := false
		for _, s := range allStages {
			if s == startStage {
				found = true
			}
			if found {
				stages = append(stages, s)
			}
		}
		if !found {
			stages = allStages
		}

		var totalTokensIn, totalTokensOut int64

		// Intercept runner events to update orchestrator state and emit to the TUI.
		wrappedEmit := func(event types.OrchestratorEvent) {
			if event.IssueID == issueID {
				o.State.Mutate(issueID, func(r *RunState) { r.LastEventAt = time.Now() })
				switch event.Type {
				case pipeline.EventTokensUpdated:
					if payload, ok := event.Payload.(map[string]int64); ok {
						totalTokensIn += payload["tokens_in"]
						totalTokensOut += payload["tokens_out"]
						o.State.Mutate(issueID, func(r *RunState) { r.TokensIn = totalTokensIn; r.TokensOut = totalTokensOut })
						o.emit(EventTokensUpdated, issueID, ProcessPayload{
							IssueID:   issueID,
							TokensIn:  totalTokensIn,
							TokensOut: totalTokensOut,
						})
						return
					}
				case pipeline.EventAgentOutput:
					if payload, ok := event.Payload.(map[string]string); ok {
						o.emit(EventAgentOutput, issueID, map[string]interface{}{
							"issue_id": issueID,
							"text":     payload["text"],
						})
						return
					}
				case pipeline.EventAgentStarted:
					if payload, ok := event.Payload.(map[string]interface{}); ok {
						pid, _ := payload["pid"].(int)
						sessionID, _ := payload["session_id"].(string)
						o.State.Mutate(issueID, func(r *RunState) { r.Process = &types.AgentProcess{PID: pid, SessionID: sessionID} })
						if o.Recorder != nil {
							_ = o.Recorder.UpdateAttemptLaunchInfo(issueID, attempt, pid, sessionID, "")
						}
						var currentStage types.Stage
						if rs, ok := o.State.Get(issueID); ok {
							currentStage = rs.Stage
						}
						o.emit(EventAgentStarted, issueID, ProcessPayload{
							IssueID:   issueID,
							Stage:     currentStage,
							Attempt:   attempt,
							PID:       pid,
							SessionID: sessionID,
						})
						return
					}
				}
			}
			// Forward workspace/prompt events directly.
			o.emit(event.Type, event.IssueID, event.Payload)
		}

		// Run stages sequentially.
		var lastResult *pipeline.Result
		for _, stage := range stages {
			if runCtx.Err() != nil {
				// If state was already removed by timeout/stall handler, skip.
				if _, ok := o.State.Get(issueID); !ok {
					return
				}

				// Context cancelled between stages — treat as a failure so state is cleaned up.
				err := runCtx.Err()
				o.State.Mutate(issueID, func(r *RunState) { r.Phase = types.PhaseFailed })
				o.State.Remove(issueID)

				nextAttempt := attempt + 1
				entry := o.Backoff.Enqueue(issueID, nextAttempt, stage, fmt.Sprintf("context cancelled before %s: %v", stage, err))
				o.persistRetryQueue(issueID, entry.RetryAt)
				o.finalizeAttempt(issueID, attempt, "retry_queued", &entry.RetryAt, err)

				o.emit(EventStageFailed, issueID, StagePayload{
					IssueID:     issueID,
					Stage:       stage,
					FailureKind: types.StageFailureTimeout,
					Error:       err.Error(),
					Retryable:   true,
				})
				o.emit(EventAgentFinished, issueID, AgentResultPayload{
					IssueID: issueID,
					Success: false,
					Error:   err.Error(),
				})
				o.emit(EventBackoffQueued, issueID, BackoffPayload{
					IssueID:     issueID,
					Attempt:     nextAttempt,
					Stage:       stage,
					RetryAt:     entry.RetryAt,
					Error:       err.Error(),
					FailureKind: types.StageFailureTimeout,
				})
				o.emit(EventIssueRetrying, issueID, BackoffPayload{
					IssueID:     issueID,
					Attempt:     nextAttempt,
					Stage:       stage,
					RetryAt:     entry.RetryAt,
					Error:       err.Error(),
					FailureKind: types.StageFailureTimeout,
				})
				return
			}
			if _, ok := o.State.Get(issueID); !ok {
				return
			}

			o.State.Mutate(issueID, func(r *RunState) { r.Stage = stage })

			stageAgent := ""
			if o.Config.OpenCode != nil {
				stageAgent = o.Config.OpenCode.AgentForStage(stage.String())
			}
			stageCtx := types.WithStage(runCtx, stage, stageAgent)

			o.emit(EventStageStarted, issueID, StagePayload{
				IssueID: issueID,
				Stage:   stage,
				Attempt: attempt,
				Agent:   stageAgent,
			})

			result, err := runner.Run(stageCtx, issue, attempt, stage, wrappedEmit)

			// If state was already removed by timeout/stall handler, skip.
			if _, ok := o.State.Get(issueID); !ok {
				return
			}

			if err != nil {
				// Workspace or agent start failed.
				o.State.Mutate(issueID, func(r *RunState) { r.Phase = types.PhaseFailed })
				o.State.Remove(issueID)

				nextAttempt := attempt + 1
				entry := o.Backoff.Enqueue(issueID, nextAttempt, stage, fmt.Sprintf("stage %s failed: %v", stage, err))
				o.persistRetryQueue(issueID, entry.RetryAt)
				o.finalizeAttempt(issueID, attempt, "retry_queued", &entry.RetryAt, err)

				o.emit(EventStageFailed, issueID, StagePayload{
					IssueID:     issueID,
					Stage:       stage,
					FailureKind: o.classifyStageFailure(err, stage),
					Error:       err.Error(),
					Retryable:   true,
				})
				o.emit(EventAgentFinished, issueID, AgentResultPayload{
					IssueID: issueID,
					Success: false,
					Error:   err.Error(),
				})
				o.emit(EventIssueRetrying, issueID, BackoffPayload{
					IssueID:     issueID,
					Attempt:     nextAttempt,
					Stage:       stage,
					RetryAt:     entry.RetryAt,
					Error:       fmt.Sprintf("stage %s failed: %v", stage, err),
					FailureKind: o.classifyStageFailure(err, stage),
				})
				return
			}

			if !result.Success {
				// Stage runtime failure.
				nextAttempt := attempt + 1
				o.State.Mutate(issueID, func(r *RunState) { r.Phase = types.PhaseFailed })
				o.State.Remove(issueID)

				var errMsg string
				if result.Error != nil {
					errMsg = result.Error.Error()
				}

				failureKind := o.classifyStageFailure(result.Error, stage)

				entry := o.Backoff.Enqueue(issueID, nextAttempt, stage, errMsg)
				o.persistRetryQueue(issueID, entry.RetryAt)
				o.finalizeAttempt(issueID, attempt, "retry_queued", &entry.RetryAt, result.Error)

				o.emit(EventStageFailed, issueID, StagePayload{
					IssueID:     issueID,
					Stage:       stage,
					FailureKind: failureKind,
					Error:       errMsg,
					Retryable:   true,
				})
				o.emit(EventAgentFinished, issueID, AgentResultPayload{
					IssueID: issueID,
					Success: false,
					Error:   errMsg,
				})
				o.emit(EventIssueRetrying, issueID, BackoffPayload{
					IssueID:     issueID,
					Attempt:     nextAttempt,
					Stage:       stage,
					RetryAt:     entry.RetryAt,
					Error:       errMsg,
					FailureKind: failureKind,
				})
				return
			}

			// Stage succeeded.
			o.emit(EventStageCompleted, issueID, StagePayload{
				IssueID: issueID,
				Stage:   stage,
				Summary: fmt.Sprintf("%s stage completed", stage),
			})
			lastResult = result
		}

		// All stages passed — hand off to human review.
		o.State.Mutate(issueID, func(r *RunState) { r.Phase = types.PhaseSucceeded })

		handoffBranch := branchName
		handoffWorkspace := workspacePath
		if lastResult != nil {
			if lastResult.Branch != "" {
				handoffBranch = lastResult.Branch
			}
			if lastResult.WorkspacePath != "" {
				handoffWorkspace = lastResult.WorkspacePath
			}
		}

		if attemptRecorder, ok := diagnostics.AttemptFromContext(runCtx); ok {
			_ = attemptRecorder.RecordReviewHandoff(
				fmt.Sprintf("All stages completed for %s.\n\nWorkspace: %s\nBranch: %s", issueID, handoffWorkspace, handoffBranch),
				"",
			)
		}

		if _, err := o.Tracker.UpdateIssueState(issueID, types.StateInReview); err != nil {
			nextAttempt := attempt + 1
			o.State.Mutate(issueID, func(r *RunState) { r.Phase = types.PhaseFailed })
			o.State.Remove(issueID)

			entry := o.Backoff.Enqueue(issueID, nextAttempt, types.StagePlan, fmt.Sprintf("review handoff failed: %v", err))
			o.persistRetryQueue(issueID, entry.RetryAt)
			o.finalizeAttempt(issueID, attempt, "retry_queued", &entry.RetryAt, err)

			o.emit(EventAgentFinished, issueID, AgentResultPayload{
				IssueID: issueID,
				Success: false,
				Error:   err.Error(),
			})
			o.emit(EventIssueRetrying, issueID, BackoffPayload{
				IssueID:     issueID,
				Attempt:     nextAttempt,
				Stage:       types.StagePlan,
				RetryAt:     entry.RetryAt,
				Error:       fmt.Sprintf("review handoff failed: %v", err),
				FailureKind: o.classifyStageFailure(err, types.StagePlan),
			})
			return
		}

		o.State.Remove(issueID)
		o.Backoff.Remove(issueID)

		o.finalizeAttempt(issueID, attempt, "awaiting_review", nil, nil)

		o.emit(EventAgentFinished, issueID, AgentResultPayload{
			IssueID: issueID,
			Success: true,
			Error:   "",
		})
		o.emit(EventIssueReadyForReview, issueID, map[string]interface{}{
			"issue_id":       issueID,
			"title":          issue.Title,
			"branch":         handoffBranch,
			"workspace_path": handoffWorkspace,
		})
	}()
}

// removeRunning removes the cancel func for an issue from the running map.
func (o *Orchestrator) removeRunning(issueID string) {
	o.mu.Lock()
	delete(o.running, issueID)
	o.mu.Unlock()
}

// persistRetryQueue writes the retry timestamp to the tracker.
// This is best-effort: the in-memory backoff manager is the source of truth
// for dispatch. If the tracker fails to persist (network error, disk full,
// etc.), the retry still happens at the scheduled time but won't be visible
// to external systems (e.g., no GitHub label/comment).
func (o *Orchestrator) persistRetryQueue(issueID string, retryAt time.Time) {
	if _, err := o.Tracker.SetRetryQueue(issueID, retryAt); err != nil {
		// Best-effort: log via event channel if space, otherwise swallow.
		select {
		case o.Events <- types.OrchestratorEvent{
			Type:      "retry_queue_persist_failed",
			IssueID:   issueID,
			Timestamp: time.Now().UTC(),
			Payload:   map[string]string{"error": err.Error()},
		}:
		default:
		}
	}
}

// isClosed reports whether shutdown has been requested or completed.
func (o *Orchestrator) isClosed() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.closed
}

// buildPrompt builds the prompt from the template using issue data.
func (o *Orchestrator) buildPrompt(issue types.Issue) string {
	template := o.promptTmpl
	if template == "" {
		return issue.Description
	}

	// Simple template substitution
	template = strings.ReplaceAll(template, "{{ issue.id }}", issue.ID)
	template = strings.ReplaceAll(template, "{{ issue.identifier }}", issue.Identifier)
	template = strings.ReplaceAll(template, "{{ issue.title }}", issue.Title)
	template = strings.ReplaceAll(template, "{{ issue.description }}", issue.Description)

	// Handle labels - comma-separated
	labels := ""
	if len(issue.Labels) > 0 {
		labels = strings.Join(issue.Labels, ", ")
	}
	template = strings.ReplaceAll(template, "{{ issue.labels }}", labels)

	return strings.TrimSpace(template)
}

// finalizeAttempt captures postflight git state and finalizes the attempt
// in the recorder. It is a no-op when the recorder is nil.
func (o *Orchestrator) finalizeAttempt(issueID string, attempt int, outcome string, retryAt *time.Time, runErr error) {
	postflightStatus, postflightWorktrees := o.captureGitState(o.workspaceBaseDir())
	finalCommit := o.captureCommit(o.ctx, o.workspacePath(issueID))
	if o.Recorder != nil {
		_ = o.Recorder.FinalizeAttempt(issueID, attempt, outcome, finalCommit, retryAt, runErr, postflightStatus, postflightWorktrees)
	}
}

// emit sends an event to the Events channel (non-blocking).
func (o *Orchestrator) emit(eventType, issueID string, payload interface{}) {
	event := types.OrchestratorEvent{
		Type:      eventType,
		IssueID:   issueID,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}

	if o.Recorder != nil {
		_ = o.Recorder.RecordEvent(event)
	}

	select {
	case o.Events <- event:
	default:
		// Channel full, skip event
	}
}

// shutdown gracefully shuts down the orchestrator.
func (o *Orchestrator) shutdown() error {
	o.shutdownOnce.Do(func() {
		o.mu.Lock()
		o.closed = true
		o.mu.Unlock()

		// Cancel main context - signals all run contexts
		o.cancel()

		// Wait for all monitorAgent goroutines to finish
		done := make(chan struct{})
		go func() {
			o.wg.Wait()
			close(done)
		}()

		// Give goroutines a moment to clean up
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			// Timeout waiting, force stop agents
		}

		// Stop all remaining agent processes
		for _, run := range o.State.GetAll() {
			if run.Process != nil && o.AgentRunner != nil {
				_ = o.AgentRunner.Stop(run.Process)
			}
		}

		// Close agent runner
		if o.AgentRunner != nil {
			_ = o.AgentRunner.Close()
		}

		// Close recorder after all agents have stopped.
		if o.Recorder != nil {
			_ = o.Recorder.Close()
		}

		// Close event channel
		close(o.Events)
	})
	return nil
}

// Stop stops the orchestrator.
func (o *Orchestrator) Stop() {
	o.mu.Lock()
	if !o.closed {
		o.closed = true
		o.cancel()
	}
	o.mu.Unlock()
}

func (o *Orchestrator) workspacePath(issueID string) string {
	return o.Workspace.Path(issueID)
}

func (o *Orchestrator) workspaceBaseDir() string {
	return o.Workspace.BaseDir()
}

func (o *Orchestrator) branchName(issueID string) string {
	return o.Config.Workspace.BranchPrefix + util.SanitizeBranchName(issueID)
}

func (o *Orchestrator) captureGitState(baseDir string) (string, string) {
	if strings.TrimSpace(baseDir) == "" {
		return "", ""
	}
	return captureGitOutput(o.ctx, baseDir, "status", "--short"), captureGitOutput(o.ctx, baseDir, "worktree", "list")
}

func (o *Orchestrator) captureCommit(ctx context.Context, dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// classifyStageFailure maps a runtime error to a typed stage failure kind.
func (o *Orchestrator) classifyStageFailure(err error, stage types.Stage) types.StageFailureKind {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return types.StageFailureTimeout
	}
	if err == nil {
		return types.StageFailureToolError
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "workspace") {
		return types.StageFailureWorkspaceError
	}
	if strings.Contains(msg, "session start") || strings.Contains(msg, "agent start") {
		return types.StageFailureSessionStartError
	}
	switch stage {
	case types.StagePlan:
		return types.StageFailureModelFailure
	case types.StageExecute:
		return types.StageFailureToolError
	case types.StageVerify:
		return types.StageFailureVerification
	default:
		return types.StageFailureToolError
	}
}

func captureGitOutput(ctx context.Context, dir string, args ...string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text != "" {
			return text + "\nerror: " + err.Error()
		}
		return "error: " + err.Error()
	}
	return text
}
