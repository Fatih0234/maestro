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
	ID          string     // Unique identifier (e.g., "CB-1")
	Identifier  string     // Display identifier (e.g., "CB-1")
	Title       string     // Brief description
	Description string     // Full details
	State       IssueState // Current state
	Labels      []string   // Tags for categorization
	URL         string     // Link to the issue (empty for local tracker)
	CreatedAt   time.Time  // When issue was created
	UpdatedAt   time.Time  // Last modification
}

// RunAttempt tracks an active or completed run for an issue.

type RunAttempt struct {
	IssueID       string    // Which issue this belongs to
	Phase         RunPhase  // Current execution phase
	Attempt       int       // Which attempt this is (1, 2, 3, ...)
	PID           int       // Process ID of the agent process
	StartTime     time.Time // When the run started
	TokensIn      int64     // Tokens sent to agent
	TokensOut     int64     // Tokens received from agent
	SessionID     string    // Agent session identifier
	WorkspacePath string    // Path to the workspace directory
}

// BackoffEntry represents a queued retry for a failed issue.
type BackoffEntry struct {
	IssueID string    // Which issue to retry
	Attempt int       // Which attempt number this retry represents
	RetryAt time.Time // When to retry (calculated from backoff strategy)
	Error   string    // What went wrong last time
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
}

// AgentEvent represents something that happened during execution
type AgentEvent struct {
	Type    string      // Event type (e.g., "token_update", "session_status")
	Payload interface{} // Event data (varies by type)
}

// IssueTracker defines the interface for issue tracking systems.
type IssueTracker interface {
	// FetchIssues returns all issues that need processing.
	FetchIssues() ([]Issue, error)
	// ClaimIssue marks an issue as in progress
	ClaimIssue(id string) (Issue, error)
	// ReleaseIssue marks an issue as available again.
	ReleaseIssue(id string) (Issue, error)
	// GetIssue fetches a single issue by ID
	GetIssue(id string) (Issue, error)
	// UpdateIssueState updates an issue's state
	UpdateIssueState(id string, state IssueState) (Issue, error)
}
