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
	EventFetchError          = "fetch.error"
)

// ProcessPayload carries agent process and token information.
// Used by EventAgentStarted and EventTokensUpdated.
type ProcessPayload struct {
	IssueID   string
	Title     string
	Stage     types.Stage
	Attempt   int
	PID       int
	SessionID string
	TokensIn  int64
	TokensOut int64
}

// StagePayload carries stage lifecycle information.
// Used by EventStageStarted, EventStageCompleted, and EventStageFailed.
type StagePayload struct {
	IssueID     string
	Stage       types.Stage
	Attempt     int
	Agent       string
	Summary     string
	FailureKind types.StageFailureKind
	Error       string
	Retryable   bool
}

// BackoffPayload carries retry/backoff information.
// Used by EventBackoffQueued and EventIssueRetrying.
type BackoffPayload struct {
	IssueID     string
	Stage       types.Stage
	Attempt     int
	RetryAt     time.Time
	Error       string
	FailureKind types.StageFailureKind
}

// AgentResultPayload carries agent completion status.
// Used by EventAgentFinished.
type AgentResultPayload struct {
	IssueID string
	Success bool
	Error   string
}
