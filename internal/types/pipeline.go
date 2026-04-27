package types

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Stage identifies one step in the orchestrator-owned pipeline.
type Stage string

const (
	StagePlan        Stage = "plan"
	StageExecute     Stage = "execute"
	StageVerify      Stage = "verify"
	StageHumanReview Stage = "human_review"
)

func (s Stage) String() string {
	return string(s)
}

// Valid reports whether the stage is one of the known pipeline stages.
func (s Stage) Valid() bool {
	switch s {
	case StagePlan, StageExecute, StageVerify, StageHumanReview:
		return true
	default:
		return false
	}
}

// NextAction returns the default next_action value after a successful stage.
// The human review gate is represented as "review" in stage results while the
// current-stage summary uses "human_review".
func (s Stage) NextAction() string {
	switch s {
	case StagePlan:
		return StageExecute.String()
	case StageExecute:
		return StageVerify.String()
	case StageVerify:
		return "review"
	default:
		return ""
	}
}

// ParseStage parses a string into a Stage value. It accepts "review" as an
// alias for the human review gate.
func ParseStage(value string) (Stage, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case StagePlan.String():
		return StagePlan, true
	case StageExecute.String():
		return StageExecute, true
	case StageVerify.String():
		return StageVerify, true
	case StageHumanReview.String(), "review":
		return StageHumanReview, true
	default:
		return "", false
	}
}

// StageState describes the status of a stage manifest or stage result.
type StageState string

const (
	StageStateRunning  StageState = "running"
	StageStatePassed   StageState = "passed"
	StageStateFailed   StageState = "failed"
	StageStateBlocked  StageState = "blocked"
	StageStateRetrying StageState = "retrying"
	StageStateSkipped  StageState = "skipped"
)

func (s StageState) String() string {
	return string(s)
}

func (s StageState) Valid() bool {
	switch s {
	case StageStateRunning, StageStatePassed, StageStateFailed, StageStateBlocked, StageStateRetrying, StageStateSkipped:
		return true
	default:
		return false
	}
}

// StageFailureKind classifies a stage failure in a replay-friendly way.
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

func (k StageFailureKind) String() string {
	return string(k)
}

func (k StageFailureKind) Valid() bool {
	switch k {
	case StageFailurePromptError,
		StageFailureSessionStartError,
		StageFailureTimeout,
		StageFailureStall,
		StageFailureModelFailure,
		StageFailureWorkspaceError,
		StageFailureToolError,
		StageFailureVerification,
		StageFailureHandoffError,
		StageFailureDecisionMissing:
		return true
	default:
		return false
	}
}

// ReviewState describes the current review gate status in the issue summary.
type ReviewState string

const (
	ReviewStatePending      ReviewState = "pending"
	ReviewStateReady        ReviewState = "ready"
	ReviewStateApproved     ReviewState = "approved"
	ReviewStateRejected     ReviewState = "rejected"
	ReviewStateNeedsChanges ReviewState = "needs_changes"
)

func (s ReviewState) String() string {
	return string(s)
}

func (s ReviewState) Valid() bool {
	switch s {
	case ReviewStatePending, ReviewStateReady, ReviewStateApproved, ReviewStateRejected, ReviewStateNeedsChanges:
		return true
	default:
		return false
	}
}

// ReviewDecisionKind is the explicit decision stored in review/decision.json.
type ReviewDecisionKind string

const (
	ReviewDecisionApproved     ReviewDecisionKind = "approved"
	ReviewDecisionRejected     ReviewDecisionKind = "rejected"
	ReviewDecisionNeedsChanges ReviewDecisionKind = "needs_changes"
)

func (k ReviewDecisionKind) String() string {
	return string(k)
}

func (k ReviewDecisionKind) Valid() bool {
	switch k {
	case ReviewDecisionApproved, ReviewDecisionRejected, ReviewDecisionNeedsChanges:
		return true
	default:
		return false
	}
}

// ReviewFollowUpState records the board state that should follow a review decision.
type ReviewFollowUpState string

const (
	ReviewFollowUpDone        ReviewFollowUpState = "done"
	ReviewFollowUpRetryQueued ReviewFollowUpState = "retry_queued"
	ReviewFollowUpTodo        ReviewFollowUpState = "todo"
	ReviewFollowUpInProgress  ReviewFollowUpState = "in_progress"
)

func (s ReviewFollowUpState) String() string {
	return string(s)
}

func (s ReviewFollowUpState) Valid() bool {
	switch s {
	case ReviewFollowUpDone, ReviewFollowUpRetryQueued, ReviewFollowUpTodo, ReviewFollowUpInProgress:
		return true
	default:
		return false
	}
}

// StageManifest captures the durable control data for a stage.
type StageManifest struct {
	Stage         Stage            `json:"stage"`
	Attempt       int              `json:"attempt"`
	Status        StageState       `json:"status"`
	Agent         string           `json:"agent,omitempty"`
	SessionID     string           `json:"session_id,omitempty"`
	WorkspacePath string           `json:"workspace_path"`
	PromptPath    string           `json:"prompt_path"`
	ResponsePath  string           `json:"response_path"`
	ResultPath    string           `json:"result_path"`
	EventsPath    string           `json:"events_path"`
	StdoutPath    string           `json:"stdout_path"`
	StderrPath    string           `json:"stderr_path"`
	DiffPath      string           `json:"diff_path,omitempty"`
	StartedAt     time.Time        `json:"started_at"`
	FinishedAt    *time.Time       `json:"finished_at,omitempty"`
	ErrorKind     StageFailureKind `json:"error_kind,omitempty"`
	Retryable     bool             `json:"retryable"`
}

// StageResult captures the machine-readable outcome for a stage.
type StageResult struct {
	Stage       Stage            `json:"stage"`
	Status      StageState       `json:"status"`
	Summary     string           `json:"summary"`
	FailureKind StageFailureKind `json:"failure_kind,omitempty"`
	Retryable   bool             `json:"retryable"`
	Evidence    []string         `json:"evidence,omitempty"`
	NextAction  string           `json:"next_action,omitempty"`
	StartedAt   time.Time        `json:"started_at"`
	FinishedAt  time.Time        `json:"finished_at"`
}

// ReviewDecision is written to review/decision.json once a human makes a call.
type ReviewDecision struct {
	Decision      ReviewDecisionKind  `json:"decision"`
	ReviewedBy    string              `json:"reviewed_by"`
	ReviewedAt    time.Time           `json:"reviewed_at"`
	Notes         string              `json:"notes,omitempty"`
	FollowUpState ReviewFollowUpState `json:"follow_up_state"`
}

// BoardState returns the canonical local-board state string for the issue state.
func (s IssueState) BoardState() string {
	switch s {
	case StateUnclaimed:
		return "todo"
	case StateClaimed, StateRunning:
		return "in_progress"
	case StateRetryQueued:
		return "retry_queued"
	case StateInReview:
		return "in_review"
	case StateFailed:
		return "failed"
	case StateReleased:
		return "done"
	default:
		return "unknown"
	}
}

// MarshalJSON ensures issue states persist as board-state strings instead of
// the legacy integer representation.
func (s IssueState) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.BoardState())
}

// UnmarshalJSON accepts both the board-state strings and the older legacy
// labels used by previous versions of the project.
func (s *IssueState) UnmarshalJSON(data []byte) error {
	var label string
	if err := json.Unmarshal(data, &label); err == nil {
		state, err := parseIssueStateLabel(label)
		if err != nil {
			return err
		}
		*s = state
		return nil
	}

	var raw int
	if err := json.Unmarshal(data, &raw); err == nil {
		*s = IssueState(raw)
		return nil
	}

	return fmt.Errorf("invalid issue state JSON: %s", string(data))
}

func parseIssueStateLabel(label string) (IssueState, error) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "todo", "unclaimed":
		return StateUnclaimed, nil
	case "in_progress":
		return StateRunning, nil
	case "claimed":
		return StateClaimed, nil
	case "running":
		return StateRunning, nil
	case "retry_queued":
		return StateRetryQueued, nil
	case "in_review":
		return StateInReview, nil
	case "failed":
		return StateFailed, nil
	case "done", "released":
		return StateReleased, nil
	default:
		return 0, fmt.Errorf("unknown issue state %q", label)
	}
}
