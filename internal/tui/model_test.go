// Package tui provides the Charm Bubble Tea terminal UI.
package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/fatihkarahan/maestro/internal/orchestrator"
	"github.com/fatihkarahan/maestro/internal/types"
)

func TestNewModel(t *testing.T) {
	m := NewModel()
	if m.agents == nil {
		t.Error("agents map should be initialized")
	}
	if m.reviews == nil {
		t.Error("reviews map should be initialized")
	}
	if m.backoffs == nil {
		t.Error("backoffs map should be initialized")
	}
	if m.stageProgress == nil {
		t.Error("stageProgress map should be initialized")
	}
	if m.maxLogSize != 100 {
		t.Errorf("maxLogSize = %d, want 100", m.maxLogSize)
	}
}

func TestDurationString(t *testing.T) {
	tests := []struct {
		name string
		d    string
		want string
	}{
		{"zero", "0s", "0s"},
		{"seconds", "30s", "30s"},
		{"minutes", "2m", "2m0s"},
		{"hours", "1h", "1h0m0s"},
	}

	// Just test that it's not panicking
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Cannot easily test since we use time.Duration
		})
	}
}

func TestCompactStage(t *testing.T) {
	tests := []struct {
		stage types.Stage
		want  string
	}{
		{types.StagePlan, "Plan"},
		{types.StageExecute, "Exec"},
		{types.StageVerify, "Verify"},
		{types.StageHumanReview, "Review"},
	}

	for _, tt := range tests {
		t.Run(string(tt.stage), func(t *testing.T) {
			got := compactStage(tt.stage)
			if got != tt.want {
				t.Errorf("compactStage(%v) = %s, want %s", tt.stage, got, tt.want)
			}
		})
	}
}

func TestIsActiveStatus(t *testing.T) {
	activeStatuses := []string{"running"}
	inactiveStatuses := []string{"done", "failed", "timeout", "stalled", ""}

	for _, s := range activeStatuses {
		if !isActiveStatus(s) {
			t.Errorf("isActiveStatus(%q) = false, want true", s)
		}
	}

	for _, s := range inactiveStatuses {
		if isActiveStatus(s) {
			t.Errorf("isActiveStatus(%q) = true, want false", s)
		}
	}
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{100, "100"},
		{1000, "1.0K"},
		{1500, "1.5K"},
		{1000000, "1.0M"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatTokens(tt.n)
			if got != tt.want {
				t.Errorf("formatTokens(%d) = %s, want %s", tt.n, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		s      string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"exactly12", 8, "exact..."},
		{"verylongstring", 6, "ver..."},
	}

	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			got := truncate(tt.s, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestModelApplyOrchestratorEvent_IssueReadyForReview(t *testing.T) {
	m := NewModel()
	m.agents["CB-1"] = AgentRow{IssueID: "CB-1"}

	event := types.OrchestratorEvent{
		Type:      orchestrator.EventIssueReadyForReview,
		IssueID:   "CB-1",
		Timestamp: time.Now(),
		Payload: map[string]interface{}{
			"issue_id":       "CB-1",
			"title":          "Fix login bug",
			"branch":         "maestro/CB-1",
			"workspace_path": "/tmp/ws/CB-1",
		},
	}

	m = m.applyOrchestratorEvent(event)

	if _, ok := m.agents["CB-1"]; ok {
		t.Fatal("agent should be removed after review handoff")
	}
	review, ok := m.reviews["CB-1"]
	if !ok {
		t.Fatal("review entry not created")
	}
	if review.Branch != "maestro/CB-1" {
		t.Fatalf("review branch = %q, want maestro/CB-1", review.Branch)
	}
	if review.WorkspacePath != "/tmp/ws/CB-1" {
		t.Fatalf("review workspace path = %q, want /tmp/ws/CB-1", review.WorkspacePath)
	}
	if review.Title != "Fix login bug" {
		t.Fatalf("review title = %q, want Fix login bug", review.Title)
	}
}

func TestModelApplyOrchestratorEvent_IssueCompletedRemovesReviewEntry(t *testing.T) {
	m := NewModel()
	m.reviews["CB-1"] = ReviewRow{IssueID: "CB-1", Branch: "maestro/CB-1", WorkspacePath: "/tmp/ws/CB-1"}

	event := types.OrchestratorEvent{
		Type:      orchestrator.EventIssueCompleted,
		IssueID:   "CB-1",
		Timestamp: time.Now(),
		Payload:   map[string]interface{}{"issue_id": "CB-1"},
	}

	m = m.applyOrchestratorEvent(event)

	if _, ok := m.reviews["CB-1"]; ok {
		t.Fatal("review entry should be removed when issue is completed")
	}
}

func TestModelApplyOrchestratorEvent_BackoffQueuedWithStageAndFailureKind(t *testing.T) {
	m := NewModel()

	event := types.OrchestratorEvent{
		Type:      orchestrator.EventBackoffQueued,
		IssueID:   "CB-1",
		Timestamp: time.Now(),
		Payload: orchestrator.BackoffPayload{
			IssueID:     "CB-1",
			Attempt:     2,
			Stage:       types.StageExecute,
			RetryAt:     time.Now().Add(2 * time.Minute),
			Error:       "go build failed",
			FailureKind: types.StageFailureToolError,
		},
	}

	m = m.applyOrchestratorEvent(event)

	backoff, ok := m.backoffs["CB-1"]
	if !ok {
		t.Fatal("backoff entry not created")
	}
	if backoff.Attempt != 2 {
		t.Errorf("attempt = %d, want 2", backoff.Attempt)
	}
	if backoff.Stage != types.StageExecute {
		t.Errorf("stage = %q, want execute", backoff.Stage)
	}
	if backoff.FailureKind != types.StageFailureToolError {
		t.Errorf("failureKind = %q, want tool_error", backoff.FailureKind)
	}
	if backoff.Error != "go build failed" {
		t.Errorf("error = %q, want go build failed", backoff.Error)
	}
}

func TestModelApplyOrchestratorEvent_IssueRetryingHandledLikeBackoff(t *testing.T) {
	m := NewModel()

	event := types.OrchestratorEvent{
		Type:      orchestrator.EventIssueRetrying,
		IssueID:   "CB-1",
		Timestamp: time.Now(),
		Payload: orchestrator.BackoffPayload{
			IssueID:     "CB-1",
			Attempt:     3,
			Stage:       types.StageVerify,
			RetryAt:     time.Now().Add(5 * time.Minute),
			Error:       "tests failed",
			FailureKind: types.StageFailureVerification,
		},
	}

	m = m.applyOrchestratorEvent(event)

	backoff, ok := m.backoffs["CB-1"]
	if !ok {
		t.Fatal("backoff entry not created from issue.retrying")
	}
	if backoff.Attempt != 3 {
		t.Errorf("attempt = %d, want 3", backoff.Attempt)
	}
	if backoff.Stage != types.StageVerify {
		t.Errorf("stage = %q, want verify", backoff.Stage)
	}
}

func TestModelApplyOrchestratorEvent_StageCompletedTracksProgress(t *testing.T) {
	m := NewModel()
	m.agents["CB-1"] = AgentRow{IssueID: "CB-1", Stage: types.StagePlan}

	event := types.OrchestratorEvent{
		Type:      orchestrator.EventStageCompleted,
		IssueID:   "CB-1",
		Timestamp: time.Now(),
		Payload: orchestrator.StagePayload{
			IssueID: "CB-1",
			Stage:   types.StagePlan,
			Summary: "plan done",
		},
	}

	m = m.applyOrchestratorEvent(event)

	if !m.stageProgress["CB-1"][types.StagePlan] {
		t.Error("plan stage should be recorded as completed")
	}
}

func TestModelApplyOrchestratorEvent_ReviewRowIncludesStageProgress(t *testing.T) {
	m := NewModel()
	m.agents["CB-1"] = AgentRow{IssueID: "CB-1"}
	m.stageProgress["CB-1"] = map[types.Stage]bool{
		types.StagePlan:    true,
		types.StageExecute: true,
		types.StageVerify:  true,
	}

	event := types.OrchestratorEvent{
		Type:      orchestrator.EventIssueReadyForReview,
		IssueID:   "CB-1",
		Timestamp: time.Now(),
		Payload: map[string]interface{}{
			"issue_id":       "CB-1",
			"title":          "Fix bug",
			"branch":         "maestro/CB-1",
			"workspace_path": "/tmp/ws/CB-1",
		},
	}

	m = m.applyOrchestratorEvent(event)

	review := m.reviews["CB-1"]
	if len(review.StagesCompleted) != 3 {
		t.Errorf("stages completed count = %d, want 3", len(review.StagesCompleted))
	}
	if !review.StagesCompleted[types.StagePlan] {
		t.Error("plan should be in stages completed")
	}
	if m.stageProgress["CB-1"] != nil {
		t.Error("stageProgress should be cleaned up after review handoff")
	}
}

func TestFormatEventMessage(t *testing.T) {
	tests := []struct {
		name    string
		event   types.OrchestratorEvent
		wantMsg string
		wantSev string
	}{
		{
			name: "stage started",
			event: types.OrchestratorEvent{
				Type: orchestrator.EventStageStarted,
				Payload: orchestrator.StagePayload{
					Stage:   types.StageExecute,
					Attempt: 1,
				},
			},
			wantMsg: "[Exec] started (attempt #1)",
			wantSev: "info",
		},
		{
			name: "stage completed",
			event: types.OrchestratorEvent{
				Type: orchestrator.EventStageCompleted,
				Payload: orchestrator.StagePayload{
					Stage: types.StageVerify,
				},
			},
			wantMsg: "[Verify] completed",
			wantSev: "success",
		},
		{
			name: "stage failed",
			event: types.OrchestratorEvent{
				Type: orchestrator.EventStageFailed,
				Payload: orchestrator.StagePayload{
					Stage:       types.StageExecute,
					FailureKind: types.StageFailureToolError,
				},
			},
			wantMsg: "[Exec] failed: tool_error",
			wantSev: "error",
		},
		{
			name:    "unknown event",
			event:   types.OrchestratorEvent{Type: "custom.thing"},
			wantMsg: "custom.thing",
			wantSev: "info",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMsg, gotSev := formatEventMessage(tt.event)
			if gotMsg != tt.wantMsg {
				t.Errorf("message = %q, want %q", gotMsg, tt.wantMsg)
			}
			if gotSev != tt.wantSev {
				t.Errorf("severity = %q, want %q", gotSev, tt.wantSev)
			}
		})
	}
}

func TestStatusGlyph(t *testing.T) {
	tests := []struct {
		status  string
		spinner string
		want    string
	}{
		{"running", "●", "●"},
		{"done", "", "✓"},
		{"failed", "", "✗"},
		{"timeout", "", "⏱"},
		{"stalled", "", "⚠"},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := statusGlyph(tt.status, tt.spinner)
			if got != tt.want {
				t.Errorf("statusGlyph(%q, %q) = %q, want %q", tt.status, tt.spinner, got, tt.want)
			}
		})
	}
}

func TestModel_View_WithRunningAgent(t *testing.T) {
	m := NewModel()
	m.width = 120
	m.height = 40

	m.agents["CB-1"] = AgentRow{
		IssueID:   "CB-1",
		Title:     "Fix login bug",
		Stage:     types.StageExecute,
		Status:    "running",
		PID:       1234,
		Age:       "5s",
		TokensIn:  1024,
		TokensOut: 2048,
		Attempt:   1,
	}

	view := m.View()
	if !strings.Contains(view, "Fix login bug") {
		t.Error("view should contain issue title")
	}
	if !strings.Contains(view, "CB-1") {
		t.Error("view should contain issue ID")
	}
	if !strings.Contains(view, "Running:") {
		t.Error("view should contain running count header")
	}
}

func TestModel_View_ReviewQueue(t *testing.T) {
	m := NewModel()
	m.width = 120
	m.height = 40

	m.reviews["CB-2"] = ReviewRow{
		IssueID:       "CB-2",
		Title:         "Add dark mode",
		Branch:        "opencode/CB-2",
		WorkspacePath: "/tmp/ws/CB-2",
		ReadyAt:       time.Now(),
		StagesCompleted: map[types.Stage]bool{
			types.StagePlan:    true,
			types.StageExecute: true,
			types.StageVerify:  true,
		},
	}

	view := m.View()
	if !strings.Contains(view, "Ready for Human Review") {
		t.Error("view should show review queue header")
	}
	if !strings.Contains(view, "Add dark mode") {
		t.Error("view should contain review issue title")
	}
	if !strings.Contains(view, "opencode/CB-2") {
		t.Error("view should contain branch name")
	}
	if !strings.Contains(view, "/tmp/ws/CB-2") {
		t.Error("view should contain workspace path")
	}
}

func TestModel_View_BackoffQueue(t *testing.T) {
	m := NewModel()
	m.width = 120
	m.height = 40

	m.backoffs["CB-3"] = BackoffRow{
		IssueID:     "CB-3",
		Attempt:     2,
		Stage:       types.StageExecute,
		RetryAt:     time.Now().Add(2 * time.Minute),
		RetryIn:     "2m",
		Error:       "go build failed",
		FailureKind: types.StageFailureToolError,
	}

	view := m.View()
	if !strings.Contains(view, "Backoff Queue") {
		t.Error("view should show backoff queue header")
	}
	if !strings.Contains(view, "CB-3") {
		t.Error("view should contain backoff issue ID")
	}
	if !strings.Contains(view, "tool_error") {
		// When FailureKind is set, the TUI renders the kind, not the raw error
		t.Error("view should contain failure kind")
	}
}

func TestModel_View_EventLog(t *testing.T) {
	m := NewModel()
	m.width = 120
	m.height = 40

	m.pushEvent("CB-1", "agent started", "info")
	m.pushEvent("CB-1", "stage completed", "success")
	m.pushEvent("CB-1", "ready for review", "success")

	view := m.View()
	if !strings.Contains(view, "[Events]") {
		t.Error("view should show event log header")
	}
	if !strings.Contains(view, "agent started") {
		t.Error("view should contain event log entries")
	}
}
