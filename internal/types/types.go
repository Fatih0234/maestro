package types

import (
	"context"
	"time"
)

type IssueState int

const (
	StateUnclaimed IssueState = iota
	StateClaimed
	StateRunning
	StateRetryQueued
	StateInReview
	StateFailed
	StateReleased
)

func (s IssueState) String() string {
	switch s {
	case StateUnclaimed:
		return "unclaimed"
	case StateClaimed:
		return "claimed"
	case StateRunning:
		return "running"
	case StateRetryQueued:
		return "retry_queued"
	case StateInReview:
		return "in_review"
	case StateFailed:
		return "failed"
	case StateReleased:
		return "released"
	default:
		return "unknown"
	}
}

type RunPhase int

const (
	PhasePreparingWorkspace RunPhase = iota
	PhaseBuildingPrompt
	PhaseLaunchingAgentProcess
	PhaseInitializingSession
	PhaseStreamingTurn
	PhaseFinishing
	PhaseSucceeded
	PhaseFailed
	PhaseTimedOut
	PhaseStalled
)

// String returns a human-readable string for RunPhase
func (p RunPhase) String() string {
	switch p {
	case PhasePreparingWorkspace:
		return "preparing_workspace"
	case PhaseBuildingPrompt:
		return "building_prompt"
	case PhaseLaunchingAgentProcess:
		return "launching_agent_process"
	case PhaseInitializingSession:
		return "initializing_session"
	case PhaseStreamingTurn:
		return "streaming_turn"
	case PhaseFinishing:
		return "finishing"
	case PhaseSucceeded:
		return "succeeded"
	case PhaseFailed:
		return "failed"
	case PhaseTimedOut:
		return "timed_out"
	case PhaseStalled:
		return "stalled"
	default:
		return "unknown"
	}
}

// Issue represents a task to be processed by an agent.

type Issue struct {
	ID           string     `json:"id"`                    // Unique identifier (e.g., "CB-1")
	Identifier   string     `json:"identifier,omitempty"`  // Display identifier (e.g., "CB-1")
	Title        string     `json:"title"`                 // Brief description
	Description  string     `json:"description"`           // Full details
	State        IssueState `json:"state"`                 // Current state
	Labels       []string   `json:"labels,omitempty"`      // Tags for categorization
	URL          string     `json:"url,omitempty"`         // Link to the issue (empty for local tracker)
	RetryAfter   *time.Time `json:"retry_after,omitempty"` // When to retry (for retry_queued state)
	RetryAttempt int        `json:"retry_attempt,omitempty"`
	RetryStage   Stage      `json:"retry_stage,omitempty"`
	Feedback     string     `json:"feedback,omitempty"` // Failure context from previous stage for retry
	Plan         string     `json:"plan,omitempty"`     // Plan from the plan stage, fed to execute and verify
	CreatedAt    time.Time  `json:"created_at"`         // When issue was created
	UpdatedAt    time.Time  `json:"updated_at"`         // Last modification
}

// BackoffEntry represents a queued retry for a failed issue.
type BackoffEntry struct {
	IssueID  string    // Which issue to retry
	Attempt  int       // Which attempt number this retry represents
	Stage    Stage     // Which stage to resume from
	RetryAt  time.Time // When to retry (calculated from backoff strategy)
	Error    string    // What went wrong last time
	Feedback string    // Context to pass into the next execution attempt
	Plan     string    // Latest implementation plan to preserve across retries
}

// AgentRunner is the interface for starting/stopping agent processes.
type AgentRunner interface {
	// Start launches an agent for the given issue in the workspace.
	Start(ctx context.Context, issue Issue, workspace, prompt string) (*AgentProcess, error)
	// Stop terminates a running agent process.
	Stop(proc *AgentProcess) error
	// Close releases any resources held by the runner.
	Close() error
}

// AgentProcess represents a running agent instance.
type AgentProcess struct {
	PID       int             // Process ID of the agent process
	SessionID string          // Agent session identifier
	Events    chan AgentEvent // Streams events from the agent
	Done      chan error      // Closed when the agent finishes

	// ServerURL is used internally by OpenCodeRunner to route requests.
	// This is set by the runner when creating the process.
	ServerURL string
}

// AgentEvent represents something that happened during execution
type AgentEvent struct {
	Type    string      // Event type (e.g., "token_update", "session_status")
	Payload interface{} // Event data (varies by type)
}

// IssueTracker defines the interface for issue tracking systems.
type IssueTracker interface {
	// FetchIssues returns all issues that need processing.
	// For trackers that support retry, this filters out issues that are
	// waiting for their retry_after time to pass.
	FetchIssues() ([]Issue, error)
	// ClaimIssue marks an issue as in progress
	ClaimIssue(id string) (Issue, error)
	// ReleaseIssue marks an issue as available again.
	ReleaseIssue(id string) (Issue, error)
	// GetIssue fetches a single issue by ID
	GetIssue(id string) (Issue, error)
	// UpdateIssueState updates an issue's state
	UpdateIssueState(id string, state IssueState) (Issue, error)
	// SetFeedback updates the human review feedback for an issue.
	SetFeedback(id string, feedback string) (Issue, error)
	// SetPlan persists the implementation plan on the issue for the next retry.
	SetPlan(id string, plan string) (Issue, error)
	// SetRetryQueue marks an issue as waiting for retry and persists retry
	// context needed by resumed retries (feedback + plan). Not all trackers
	// may support this; those that don't should return an error.
	SetRetryQueue(id string, retryAt time.Time, attempt int, stage Stage, feedback, plan string) (Issue, error)
	// ListAllIssues returns all known issues regardless of state.
	// For remote trackers this may be limited to open/non-archived issues.
	ListAllIssues() ([]Issue, error)
	// CreateIssue creates a new issue with the given title, description, and labels.
	CreateIssue(title, description string, labels []string) (Issue, error)
}

// OrchestratorEvent represents a high-level event in the orchestrator lifecycle.
// This is distinct from AgentEvent which represents low-level agent events.
type OrchestratorEvent struct {
	Type      string
	IssueID   string
	Timestamp time.Time
	Payload   interface{}
}
