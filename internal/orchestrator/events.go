// Package orchestrator provides the main orchestrator that ties together
// tracker, workspace, and agent components.
package orchestrator

import (
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

// OrchestratorEvent type constants for high-level orchestrator events.
// These are separate from low-level AgentEvent constants in the agent package.
const (
	EventPollStarted         = "poll_started"
	EventIssueClaimed        = "issue.claimed"
	EventWorkspaceCreated    = "workspace.created"
	EventPromptBuilt         = "prompt.built"
	EventAgentStarted        = "agent.started"
	EventTokensUpdated       = "tokens.updated"
	EventAgentOutput         = "agent.output"
	EventAgentFinished       = "agent.finished"
	EventStageStarted        = "stage.started"
	EventStageCompleted      = "stage.completed"
	EventStageFailed         = "stage.failed"
	EventIssueReadyForReview = "issue.ready_for_review"
	EventIssueCompleted      = "issue.completed"
	EventIssueRetrying       = "issue.retrying"
	EventBackoffQueued       = "backoff.queued"
	EventStallDetected       = "stall.detected"
	EventTimeoutDetected     = "timeout.detected"
	EventPollCompleted       = "poll.completed"
	EventMergeFailed         = "merge.failed"
)

// Event payloads

// IssueClaimedPayload is the payload for EventIssueClaimed.
type IssueClaimedPayload struct {
	Issue interface{} // types.Issue
}

// WorkspaceCreatedPayload is the payload for EventWorkspaceCreated.
type WorkspaceCreatedPayload struct {
	IssueID string
	Path    string
}

// PromptBuiltPayload is the payload for EventPromptBuilt.
type PromptBuiltPayload struct {
	IssueID string
	Length  int
}

// AgentStartedPayload is the payload for EventAgentStarted.
type AgentStartedPayload struct {
	IssueID   string
	Stage     types.Stage
	Attempt   int
	PID       int
	SessionID string
}

// TokensUpdatedPayload is the payload for EventTokensUpdated.
type TokensUpdatedPayload struct {
	IssueID   string
	TokensIn  int64
	TokensOut int64
}

// AgentOutputPayload is the payload for EventAgentOutput.
type AgentOutputPayload struct {
	IssueID string
	Text    string
}

// AgentFinishedPayload is the payload for EventAgentFinished.
type AgentFinishedPayload struct {
	IssueID string
	Success bool
	Error   string
}

// StageStartedPayload is the payload for EventStageStarted.
type StageStartedPayload struct {
	IssueID string
	Stage   types.Stage
	Attempt int
	Agent   string
}

// StageCompletedPayload is the payload for EventStageCompleted.
type StageCompletedPayload struct {
	IssueID string
	Stage   types.Stage
	Summary string
}

// StageFailedPayload is the payload for EventStageFailed.
type StageFailedPayload struct {
	IssueID     string
	Stage       types.Stage
	FailureKind types.StageFailureKind
	Error       string
	Retryable   bool
}

// IssueReadyForReviewPayload is the payload for EventIssueReadyForReview.
type IssueReadyForReviewPayload struct {
	IssueID       string
	Title         string
	Branch        string
	WorkspacePath string
	SummaryPath   string
}

// IssueCompletedPayload is the payload for EventIssueCompleted.
type IssueCompletedPayload struct {
	IssueID string
}

// IssueRetryingPayload is the payload for EventIssueRetrying.
type IssueRetryingPayload struct {
	IssueID     string
	Attempt     int
	Stage       types.Stage
	RetryAt     time.Time
	Error       string
	FailureKind types.StageFailureKind
}

// BackoffQueuedPayload is the payload for EventBackoffQueued.
type BackoffQueuedPayload struct {
	IssueID     string
	Attempt     int
	Stage       types.Stage
	RetryAt     time.Time
	Error       string
	FailureKind types.StageFailureKind
}

// StallDetectedPayload is the payload for EventStallDetected.
type StallDetectedPayload struct {
	IssueID      string
	Reason       string
	Detail       string
	LastEventAge time.Duration
}

// TimeoutDetectedPayload is the payload for EventTimeoutDetected.
type TimeoutDetectedPayload struct {
	IssueID string
	Elapsed time.Duration
}

// MergeFailedPayload is the payload for EventMergeFailed.
type MergeFailedPayload struct {
	IssueID string
	Error   string
}
