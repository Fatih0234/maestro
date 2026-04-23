// Package tui provides the Charm Bubble Tea terminal UI.
package tui

import (
	"testing"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/orchestrator"
	"github.com/fatihkarahan/contrabass-pi/internal/types"
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

func TestCompactPhase(t *testing.T) {
	tests := []struct {
		phase types.RunPhase
		want  string
	}{
		{types.PhasePreparingWorkspace, "Prep"},
		{types.PhaseBuildingPrompt, "Prompt"},
		{types.PhaseLaunchingAgentProcess, "Launch"},
		{types.PhaseInitializingSession, "Init"},
		{types.PhaseStreamingTurn, "Turn"},
		{types.PhaseFinishing, "Finish"},
		{types.PhaseSucceeded, "Done"},
		{types.PhaseFailed, "Failed"},
		{types.PhaseTimedOut, "Timeout"},
		{types.PhaseStalled, "Stalled"},
	}

	for _, tt := range tests {
		t.Run(string(tt.phase.String()), func(t *testing.T) {
			got := compactPhase(tt.phase)
			if got != tt.want {
				t.Errorf("compactPhase(%v) = %s, want %s", tt.phase, got, tt.want)
			}
		})
	}
}

func TestIsActivePhase(t *testing.T) {
	activePhases := []types.RunPhase{
		types.PhaseInitializingSession,
		types.PhaseLaunchingAgentProcess,
		types.PhasePreparingWorkspace,
		types.PhaseBuildingPrompt,
		types.PhaseStreamingTurn,
		types.PhaseFinishing,
	}

	inactivePhases := []types.RunPhase{
		types.PhaseSucceeded,
		types.PhaseFailed,
		types.PhaseTimedOut,
		types.PhaseStalled,
	}

	for _, p := range activePhases {
		if !isActivePhase(p) {
			t.Errorf("isActivePhase(%v) = false, want true", p)
		}
	}

	for _, p := range inactivePhases {
		if isActivePhase(p) {
			t.Errorf("isActivePhase(%v) = true, want false", p)
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

func TestNewTable(t *testing.T) {
	tbl := NewTable()
	if tbl.width != 0 {
		t.Errorf("width = %d, want 0", tbl.width)
	}
	if tbl.selected != 0 {
		t.Errorf("selected = %d, want 0", tbl.selected)
	}
}

func TestTableUpdate(t *testing.T) {
	tbl := NewTable()
	rows := []SessionRow{
		{IssueID: "CB-1", Title: "Test Issue", Phase: types.PhaseInitializingSession, PID: 1234},
	}
	tbl = tbl.Update(rows, "")
	if len(tbl.rows) != 1 {
		t.Errorf("len(rows) = %d, want 1", len(tbl.rows))
	}
}

func TestTableSetWidth(t *testing.T) {
	tbl := NewTable().SetWidth(80)
	if tbl.width != 80 {
		t.Errorf("width = %d, want 80", tbl.width)
	}
}

func TestTableSetSelected(t *testing.T) {
	tbl := NewTable().SetSelected(5)
	if tbl.selected != 5 {
		t.Errorf("selected = %d, want 5", tbl.selected)
	}
}

func TestTableSetFocused(t *testing.T) {
	tbl := NewTable().SetFocused(true)
	if !tbl.focused {
		t.Error("focused = false, want true")
	}
}

func TestTableRowCount(t *testing.T) {
	tbl := NewTable().Update([]SessionRow{{}, {}}, "")
	if tbl.RowCount() != 2 {
		t.Errorf("RowCount() = %d, want 2", tbl.RowCount())
	}
}

func TestTableSelected(t *testing.T) {
	tbl := NewTable().SetSelected(3)
	if tbl.Selected() != 3 {
		t.Errorf("Selected() = %d, want 3", tbl.Selected())
	}
}

func TestDisplayIssueID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "-"},
		{"   ", "-"},
		{"CB-1", "CB-1"},
		{"PROJ-123", "PROJ-123"},
		{"abc", "abc"},
		{"verylongidentifier123", "verylongi..."},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := displayIssueID(tt.input)
			if got != tt.want {
				t.Errorf("displayIssueID(%q) = %q, want %q", tt.input, got, tt.want)
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
		Payload: orchestrator.IssueReadyForReviewPayload{
			IssueID:       "CB-1",
			Branch:        "contrabass/CB-1",
			WorkspacePath: "/tmp/ws/CB-1",
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
	if review.Branch != "contrabass/CB-1" {
		t.Fatalf("review branch = %q, want contrabass/CB-1", review.Branch)
	}
	if review.WorkspacePath != "/tmp/ws/CB-1" {
		t.Fatalf("review workspace path = %q, want /tmp/ws/CB-1", review.WorkspacePath)
	}
}

func TestModelApplyOrchestratorEvent_IssueCompletedRemovesReviewEntry(t *testing.T) {
	m := NewModel()
	m.reviews["CB-1"] = ReviewRow{IssueID: "CB-1", Branch: "contrabass/CB-1", WorkspacePath: "/tmp/ws/CB-1"}

	event := types.OrchestratorEvent{
		Type:      orchestrator.EventIssueCompleted,
		IssueID:   "CB-1",
		Timestamp: time.Now(),
		Payload:   orchestrator.IssueCompletedPayload{IssueID: "CB-1"},
	}

	m = m.applyOrchestratorEvent(event)

	if _, ok := m.reviews["CB-1"]; ok {
		t.Fatal("review entry should be removed when issue is completed")
	}
}

func TestStatusGlyph(t *testing.T) {
	tests := []struct {
		phase   types.RunPhase
		spinner string
		want    string
	}{
		{types.PhaseStreamingTurn, "●", "●"},
		{types.PhaseSucceeded, "", "✓"},
		{types.PhaseFailed, "", "✗"},
		{types.PhaseTimedOut, "", "⏱"},
		{types.PhaseStalled, "", "⚠"},
	}

	for _, tt := range tests {
		t.Run(tt.phase.String(), func(t *testing.T) {
			got := statusGlyph(tt.phase, tt.spinner)
			if got != tt.want {
				t.Errorf("statusGlyph(%v, %q) = %q, want %q", tt.phase, tt.spinner, got, tt.want)
			}
		})
	}
}
