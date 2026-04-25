package types

import (
	"testing"
	"time"
)

func TestStage_Valid(t *testing.T) {
	tests := []struct {
		stage Stage
		want  bool
	}{
		{StagePlan, true},
		{StageExecute, true},
		{StageVerify, true},
		{StageHumanReview, true},
		{Stage("unknown"), false},
		{Stage(""), false},
	}
	for _, tt := range tests {
		if got := tt.stage.Valid(); got != tt.want {
			t.Errorf("Stage(%q).Valid() = %v, want %v", tt.stage, got, tt.want)
		}
	}
}

func TestStage_NextAction(t *testing.T) {
	tests := []struct {
		stage Stage
		want  string
	}{
		{StagePlan, "execute"},
		{StageExecute, "verify"},
		{StageVerify, "review"},
		{StageHumanReview, ""},
	}
	for _, tt := range tests {
		if got := tt.stage.NextAction(); got != tt.want {
			t.Errorf("Stage(%q).NextAction() = %q, want %q", tt.stage, got, tt.want)
		}
	}
}

func TestParseStage(t *testing.T) {
	tests := []struct {
		input string
		want  Stage
		ok    bool
	}{
		{"plan", StagePlan, true},
		{"execute", StageExecute, true},
		{"verify", StageVerify, true},
		{"human_review", StageHumanReview, true},
		{"review", StageHumanReview, true},
		{"unknown", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := ParseStage(tt.input)
		if ok != tt.ok {
			t.Errorf("ParseStage(%q) ok = %v, want %v", tt.input, ok, tt.ok)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseStage(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestStageState_Valid(t *testing.T) {
	tests := []struct {
		state StageState
		want  bool
	}{
		{StageStateRunning, true},
		{StageStatePassed, true},
		{StageStateFailed, true},
		{StageStateBlocked, true},
		{StageStateRetrying, true},
		{StageStateSkipped, true},
		{StageState("unknown"), false},
	}
	for _, tt := range tests {
		if got := tt.state.Valid(); got != tt.want {
			t.Errorf("StageState(%q).Valid() = %v, want %v", tt.state, got, tt.want)
		}
	}
}

func TestStageFailureKind_Valid(t *testing.T) {
	tests := []struct {
		kind StageFailureKind
		want bool
	}{
		{StageFailurePromptError, true},
		{StageFailureSessionStartError, true},
		{StageFailureTimeout, true},
		{StageFailureStall, true},
		{StageFailureModelFailure, true},
		{StageFailureWorkspaceError, true},
		{StageFailureToolError, true},
		{StageFailureVerification, true},
		{StageFailureHandoffError, true},
		{StageFailureDecisionMissing, true},
		{StageFailureKind("unknown"), false},
	}
	for _, tt := range tests {
		if got := tt.kind.Valid(); got != tt.want {
			t.Errorf("StageFailureKind(%q).Valid() = %v, want %v", tt.kind, got, tt.want)
		}
	}
}

func TestReviewState_Valid(t *testing.T) {
	tests := []struct {
		state ReviewState
		want  bool
	}{
		{ReviewStatePending, true},
		{ReviewStateReady, true},
		{ReviewStateApproved, true},
		{ReviewStateRejected, true},
		{ReviewStateNeedsChanges, true},
		{ReviewState("unknown"), false},
	}
	for _, tt := range tests {
		if got := tt.state.Valid(); got != tt.want {
			t.Errorf("ReviewState(%q).Valid() = %v, want %v", tt.state, got, tt.want)
		}
	}
}

func TestReviewDecisionKind_Valid(t *testing.T) {
	tests := []struct {
		kind ReviewDecisionKind
		want bool
	}{
		{ReviewDecisionApproved, true},
		{ReviewDecisionRejected, true},
		{ReviewDecisionNeedsChanges, true},
		{ReviewDecisionKind("unknown"), false},
	}
	for _, tt := range tests {
		if got := tt.kind.Valid(); got != tt.want {
			t.Errorf("ReviewDecisionKind(%q).Valid() = %v, want %v", tt.kind, got, tt.want)
		}
	}
}

func TestReviewFollowUpState_Valid(t *testing.T) {
	tests := []struct {
		state ReviewFollowUpState
		want  bool
	}{
		{ReviewFollowUpDone, true},
		{ReviewFollowUpRetryQueued, true},
		{ReviewFollowUpTodo, true},
		{ReviewFollowUpInProgress, true},
		{ReviewFollowUpState("unknown"), false},
	}
	for _, tt := range tests {
		if got := tt.state.Valid(); got != tt.want {
			t.Errorf("ReviewFollowUpState(%q).Valid() = %v, want %v", tt.state, got, tt.want)
		}
	}
}

func TestIssueState_BoardState(t *testing.T) {
	tests := []struct {
		state IssueState
		want  string
	}{
		{StateUnclaimed, "todo"},
		{StateClaimed, "in_progress"},
		{StateRunning, "in_progress"},
		{StateRetryQueued, "retry_queued"},
		{StateInReview, "in_review"},
		{StateReleased, "done"},
	}
	for _, tt := range tests {
		if got := tt.state.BoardState(); got != tt.want {
			t.Errorf("IssueState(%v).BoardState() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestIssueState_MarshalJSON(t *testing.T) {
	data, err := StateRunning.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}
	if string(data) != `"in_progress"` {
		t.Errorf("MarshalJSON = %s, want \"in_progress\"", string(data))
	}
}

func TestIssueState_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		input string
		want  IssueState
	}{
		{`"todo"`, StateUnclaimed},
		{`"in_progress"`, StateRunning},
		{`"retry_queued"`, StateRetryQueued},
		{`"in_review"`, StateInReview},
		{`"done"`, StateReleased},
		// Legacy integer support
		{"1", StateClaimed},
		{"2", StateRunning},
	}
	for _, tt := range tests {
		var s IssueState
		if err := s.UnmarshalJSON([]byte(tt.input)); err != nil {
			t.Errorf("UnmarshalJSON(%s) error: %v", tt.input, err)
			continue
		}
		if s != tt.want {
			t.Errorf("UnmarshalJSON(%s) = %v, want %v", tt.input, s, tt.want)
		}
	}
}

func TestStageManifest_RoundTrip(t *testing.T) {
	now := time.Now().UTC()
	manifest := StageManifest{
		Stage:         StageExecute,
		Attempt:       1,
		Status:        StageStatePassed,
		Agent:         "coder",
		SessionID:     "sess-1",
		WorkspacePath: "/tmp/ws",
		PromptPath:    "/tmp/ws/prompt.md",
		ResponsePath:  "/tmp/ws/response.md",
		ResultPath:    "/tmp/ws/result.json",
		StartedAt:     now,
	}
	if !manifest.Stage.Valid() {
		t.Error("execute stage should be valid")
	}
	if !manifest.Status.Valid() {
		t.Error("passed status should be valid")
	}
}

func TestStageResult_NextActionMapping(t *testing.T) {
	if got := StagePlan.NextAction(); got != StageExecute.String() {
		t.Errorf("plan next action = %q, want execute", got)
	}
	if got := StageExecute.NextAction(); got != StageVerify.String() {
		t.Errorf("execute next action = %q, want verify", got)
	}
	if got := StageVerify.NextAction(); got != "review" {
		t.Errorf("verify next action = %q, want review", got)
	}
}
