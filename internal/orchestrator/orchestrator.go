// Package orchestrator provides the main orchestrator that ties together
// tracker, workspace, and agent components.
package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/agent"
	"github.com/fatihkarahan/contrabass-pi/internal/config"
	"github.com/fatihkarahan/contrabass-pi/internal/types"
	"github.com/fatihkarahan/contrabass-pi/internal/workspace"
)

// Orchestrator ties together tracker, workspace, and agent components.
// It manages the full lifecycle of an issue from claim to completion or retry.
type Orchestrator struct {
	Config      *config.Config
	Tracker     types.IssueTracker
	Workspace   workspace.WorkspaceManager // interface
	AgentRunner types.AgentRunner
	Events      chan types.OrchestratorEvent
	State       *StateManager
	Backoff     *BackoffManager

	// Internal
	ctx       context.Context
	cancel    context.CancelFunc
	promptTmpl string

	// Goroutine tracking for graceful shutdown
	wg     sync.WaitGroup
	mu     sync.Mutex // protects running map
	running map[string]context.CancelFunc // issueID -> cancel func for that run's context
	closed  bool
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

// Run starts the orchestrator loop. It blocks until the context is cancelled.
func (o *Orchestrator) Run() error {
	// Create ticker for polling
	pollInterval := time.Duration(o.Config.PollIntervalMs) * time.Millisecond
	if pollInterval <= 0 {
		pollInterval = 30 * time.Second
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

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

// poll performs a single poll cycle.
func (o *Orchestrator) poll() {
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
func (o *Orchestrator) handleTimeout(run *RunState, elapsed time.Duration) {
	issueID := run.Issue.ID

	// Cancel the run's context
	o.mu.Lock()
	if cancel, ok := o.running[issueID]; ok {
		cancel()
		delete(o.running, issueID)
	}
	o.mu.Unlock()

	// Stop agent process
	if run.Process != nil {
		_ = o.AgentRunner.Stop(run.Process)
	}

	// Emit timeout event
	o.emit(EventTimeoutDetected, issueID, TimeoutDetectedPayload{
		IssueID: issueID,
		Elapsed: elapsed,
	})

	// Enqueue backoff
	attempt := run.Attempt + 1
	entry := o.Backoff.Enqueue(issueID, attempt, fmt.Sprintf("timeout after %v", elapsed))

	o.emit(EventBackoffQueued, issueID, BackoffQueuedPayload{
		IssueID: issueID,
		Attempt: attempt,
		RetryAt: entry.RetryAt,
	})

	// Update phase to timed out
	o.State.UpdatePhase(issueID, types.PhaseTimedOut)

	// Remove from active state (will be retried via backoff)
	o.State.Remove(issueID)
}

// handleStall handles a run that has stalled (no recent events).
func (o *Orchestrator) handleStall(run *RunState, lastEventAge time.Duration) {
	issueID := run.Issue.ID

	// Cancel the run's context
	o.mu.Lock()
	if cancel, ok := o.running[issueID]; ok {
		cancel()
		delete(o.running, issueID)
	}
	o.mu.Unlock()

	// Stop agent process
	if run.Process != nil {
		_ = o.AgentRunner.Stop(run.Process)
	}

	// Emit stall event
	o.emit(EventStallDetected, issueID, StallDetectedPayload{
		IssueID:     issueID,
		Reason:      "stall",
		Detail:      fmt.Sprintf("no event received for %v", lastEventAge),
		LastEventAge: lastEventAge,
	})

	// Enqueue backoff
	attempt := run.Attempt + 1
	entry := o.Backoff.Enqueue(issueID, attempt, fmt.Sprintf("stall: no event for %v", lastEventAge))

	o.emit(EventBackoffQueued, issueID, BackoffQueuedPayload{
		IssueID: issueID,
		Attempt: attempt,
		RetryAt: entry.RetryAt,
	})

	// Update phase to stalled
	o.State.UpdatePhase(issueID, types.PhaseStalled)

	// Remove from active state (will be retried via backoff)
	o.State.Remove(issueID)
}

// dispatchBackoff retries ready backoff entries.
func (o *Orchestrator) dispatchBackoff() {
	for _, entry := range o.Backoff.Ready() {
		// Skip if at capacity - re-add entry to backoff to preserve it
		if o.Config.MaxConcurrency > 0 && o.State.Len() >= o.Config.MaxConcurrency {
			// Re-add with same attempt to preserve retry position
			o.Backoff.Enqueue(entry.IssueID, entry.Attempt, entry.Error)
			return
		}

		// Re-claim the issue
		issue, err := o.Tracker.ClaimIssue(entry.IssueID)
		if err != nil {
			// Issue no longer available, remove from backoff
			o.Backoff.Remove(entry.IssueID)
			continue
		}

		// Start the run (reuse workspace if exists)
		o.startRun(issue, entry.Attempt)
	}
}

// dispatchReady claims and starts new issues.
func (o *Orchestrator) dispatchReady() {
	// Skip if at capacity
	if o.Config.MaxConcurrency > 0 && o.State.Len() >= o.Config.MaxConcurrency {
		return
	}

	// Fetch unclaimed issues
	issues, err := o.Tracker.FetchIssues()
	if err != nil {
		return
	}

	for _, issue := range issues {
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

// startRun starts a run for an issue with the given attempt number.
func (o *Orchestrator) startRun(issue types.Issue, attempt int) {
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

	// 1. Create workspace
	wsPath, err := o.Workspace.Create(runCtx, issue)
	if err != nil {
		runCancel()
		o.removeRunning(issueID)
		o.handleStartError(issue, attempt, err, "workspace creation failed")
		return
	}

	o.State.UpdatePhase(issueID, types.PhasePreparingWorkspace)
	o.emit(EventWorkspaceCreated, issueID, WorkspaceCreatedPayload{
		IssueID: issueID,
		Path:    wsPath,
	})

	// 2. Build prompt from template
	o.State.UpdatePhase(issueID, types.PhaseBuildingPrompt)
	prompt := o.buildPrompt(issue)
	o.emit(EventPromptBuilt, issueID, PromptBuiltPayload{
		IssueID: issueID,
		Length:  len(prompt),
	})

	// 3. Start agent
	o.State.UpdatePhase(issueID, types.PhaseLaunchingAgentProcess)
	proc, err := o.AgentRunner.Start(runCtx, issue, wsPath, prompt)
	if err != nil {
		runCancel()
		o.removeRunning(issueID)
		o.handleStartError(issue, attempt, err, "agent start failed")
		return
	}

	// 4. Add to state
	o.State.Add(issueID, issue, attempt, proc)
	o.State.UpdatePhase(issueID, types.PhaseInitializingSession)

	// 5. Emit agent started
	o.emit(EventAgentStarted, issueID, AgentStartedPayload{
		IssueID:   issueID,
		PID:       proc.PID,
		SessionID: proc.SessionID,
	})

	// 6. Spawn goroutine to monitor this agent
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		o.monitorAgent(issueID, proc, runCancel)
	}()
}

// removeRunning removes the cancel func for an issue from the running map.
func (o *Orchestrator) removeRunning(issueID string) {
	o.mu.Lock()
	delete(o.running, issueID)
	o.mu.Unlock()
}

// handleStartError handles errors during startRun.
func (o *Orchestrator) handleStartError(issue types.Issue, attempt int, err error, reason string) {
	o.State.SetError(issue.ID, err.Error())
	o.State.UpdatePhase(issue.ID, types.PhaseFailed)
	o.State.Remove(issue.ID)

	// Enqueue for retry
	entry := o.Backoff.Enqueue(issue.ID, attempt, fmt.Sprintf("%s: %v", reason, err))

	o.emit(EventAgentFinished, issue.ID, AgentFinishedPayload{
		IssueID: issue.ID,
		Success: false,
		Error:   err.Error(),
	})
	o.emit(EventIssueRetrying, issue.ID, IssueRetryingPayload{
		IssueID: issue.ID,
		Attempt: attempt,
		RetryAt: entry.RetryAt,
	})
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

// monitorAgent monitors an agent process and handles its events.
func (o *Orchestrator) monitorAgent(issueID string, proc *types.AgentProcess, runCancel context.CancelFunc) {
	defer func() {
		if r := recover(); r != nil {
			// Channel closed or panic
		}
		runCancel()
		o.removeRunning(issueID)
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

// handleAgentEvent processes an agent event.
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

	// Handle message content for output display
	if event.Type == agent.EventTypeMessageUpdated {
		if text := extractTextContent(event); text != "" {
			o.emit(EventAgentOutput, issueID, AgentOutputPayload{
				IssueID: issueID,
				Text:    text,
			})
		}
	}
}

// handleAgentDone handles when an agent completes or errors.
func (o *Orchestrator) handleAgentDone(issueID string, runErr error) {
	run, ok := o.State.Get(issueID)
	if !ok {
		return
	}

	// Update last event timestamp
	o.State.UpdateLastEvent(issueID)

	if runErr == nil {
		// Success
		o.State.UpdatePhase(issueID, types.PhaseSucceeded)
		o.State.Remove(issueID)

		// Mark issue as done
		_, _ = o.Tracker.UpdateIssueState(issueID, types.StateReleased)

		// Cleanup workspace
		_ = o.Workspace.Cleanup(o.ctx, issueID)

		// Remove from backoff
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
		o.State.UpdatePhase(issueID, types.PhaseFailed)
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

// emit sends an event to the Events channel (non-blocking).
func (o *Orchestrator) emit(eventType, issueID string, payload interface{}) {
	event := types.OrchestratorEvent{
		Type:      eventType,
		IssueID:   issueID,
		Timestamp: time.Now(),
		Payload:   payload,
	}

	select {
	case o.Events <- event:
	default:
		// Channel full, skip event
	}
}

// shutdown gracefully shuts down the orchestrator.
func (o *Orchestrator) shutdown() error {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return nil
	}
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

// Stop stops the orchestrator.
func (o *Orchestrator) Stop() {
	o.mu.Lock()
	if !o.closed {
		o.closed = true
		o.cancel()
	}
	o.mu.Unlock()
}

// extractTextContent extracts text content from an agent event.
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

	// Try "text" field at top level
	if text, ok := payload["text"].(string); ok {
		return text
	}

	return ""
}