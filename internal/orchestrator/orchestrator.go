// Package orchestrator provides the main orchestrator that ties together
// tracker, workspace, and agent components.
package orchestrator

import (
	"context"
	"fmt"
	"strings"
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
	Workspace   workspace.Manager
	AgentRunner types.AgentRunner
	Events      chan types.OrchestratorEvent
	State       *StateManager
	Backoff     *BackoffManager

	// Internal
	ctx        context.Context
	cancel     context.CancelFunc
	promptTmpl string
}

// New creates a new Orchestrator.
func New(cfg *config.Config, tr types.IssueTracker, ws workspace.Manager, runner types.AgentRunner) *Orchestrator {
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
	// Stop agent process
	if run.Process != nil {
		_ = o.AgentRunner.Stop(run.Process)
	}

	// Emit timeout event
	o.emit(EventTimeoutDetected, run.Issue.ID, TimeoutDetectedPayload{
		IssueID: run.Issue.ID,
		Elapsed: elapsed,
	})

	// Enqueue backoff
	attempt := run.Attempt + 1
	entry := o.Backoff.Enqueue(run.Issue.ID, attempt, fmt.Sprintf("timeout after %v", elapsed))

	o.emit(EventBackoffQueued, run.Issue.ID, BackoffQueuedPayload{
		IssueID: run.Issue.ID,
		Attempt: attempt,
		RetryAt: entry.RetryAt,
	})

	// Update phase to timed out
	o.State.UpdatePhase(run.Issue.ID, types.PhaseTimedOut)

	// Remove from active state (will be retried via backoff)
	o.State.Remove(run.Issue.ID)
}

// handleStall handles a run that has stalled (no recent events).
func (o *Orchestrator) handleStall(run *RunState, lastEventAge time.Duration) {
	// Stop agent process
	if run.Process != nil {
		_ = o.AgentRunner.Stop(run.Process)
	}

	// Emit stall event
	o.emit(EventStallDetected, run.Issue.ID, StallDetectedPayload{
		IssueID:     run.Issue.ID,
		Reason:      "stall",
		Detail:      fmt.Sprintf("no event received for %v", lastEventAge),
		LastEventAge: lastEventAge,
	})

	// Enqueue backoff
	attempt := run.Attempt + 1
	entry := o.Backoff.Enqueue(run.Issue.ID, attempt, fmt.Sprintf("stall: no event for %v", lastEventAge))

	o.emit(EventBackoffQueued, run.Issue.ID, BackoffQueuedPayload{
		IssueID: run.Issue.ID,
		Attempt: attempt,
		RetryAt: entry.RetryAt,
	})

	// Update phase to stalled
	o.State.UpdatePhase(run.Issue.ID, types.PhaseStalled)

	// Remove from active state (will be retried via backoff)
	o.State.Remove(run.Issue.ID)
}

// dispatchBackoff retries ready backoff entries.
func (o *Orchestrator) dispatchBackoff() {
	for _, entry := range o.Backoff.Ready() {
		// Skip if at capacity
		if o.Config.MaxConcurrency > 0 && o.State.Len() >= o.Config.MaxConcurrency {
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
	// 1. Create workspace
	ctx := o.ctx
	wsPath, err := o.Workspace.Create(ctx, issue)
	if err != nil {
		o.handleStartError(issue, attempt, err, "workspace creation failed")
		return
	}

	o.State.UpdatePhase(issue.ID, types.PhasePreparingWorkspace)
	o.emit(EventWorkspaceCreated, issue.ID, WorkspaceCreatedPayload{
		IssueID: issue.ID,
		Path:    wsPath,
	})

	// 2. Build prompt from template
	o.State.UpdatePhase(issue.ID, types.PhaseBuildingPrompt)
	prompt := o.buildPrompt(issue)
	o.emit(EventPromptBuilt, issue.ID, PromptBuiltPayload{
		IssueID: issue.ID,
		Length:  len(prompt),
	})

	// 3. Start agent
	o.State.UpdatePhase(issue.ID, types.PhaseLaunchingAgentProcess)
	proc, err := o.AgentRunner.Start(ctx, issue, wsPath, prompt)
	if err != nil {
		o.handleStartError(issue, attempt, err, "agent start failed")
		return
	}

	// 4. Add to state
	o.State.Add(issue.ID, issue, attempt, proc)
	o.State.UpdatePhase(issue.ID, types.PhaseInitializingSession)

	// 5. Emit agent started
	o.emit(EventAgentStarted, issue.ID, AgentStartedPayload{
		IssueID:   issue.ID,
		PID:       proc.PID,
		SessionID: proc.SessionID,
	})

	// 6. Spawn goroutine to monitor this agent
	go o.monitorAgent(issue.ID, proc)
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

// Stop stops the orchestrator.
func (o *Orchestrator) Stop() {
	o.cancel()
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