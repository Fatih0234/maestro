package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fatihkarahan/maestro/internal/config"
	"github.com/fatihkarahan/maestro/internal/diagnostics"
	"github.com/fatihkarahan/maestro/internal/tracker"
	"github.com/fatihkarahan/maestro/internal/types"
)

// setupTestBoard creates a temporary board with a config file and returns
// the temp directory, tracker, and recorder.
func setupTestBoard(t *testing.T) (string, *tracker.LocalTracker, *diagnostics.Recorder) {
	t.Helper()
	tmpDir := t.TempDir()
	boardDir := filepath.Join(tmpDir, "board")

	tr := tracker.New(tracker.Config{BoardDir: boardDir, IssuePrefix: "CB"})
	if err := tr.EnsureBoard(); err != nil {
		t.Fatalf("EnsureBoard: %v", err)
	}

	recorder, err := diagnostics.NewRecorder(boardDir)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	// Write a minimal WORKFLOW.md config file.
	cfg := config.DefaultConfig()
	cfg.Tracker.BoardDir = boardDir
	cfg.Workspace.BaseDir = tmpDir
	cfgContent := fmt.Sprintf(`---
max_concurrency: 1
poll_interval_ms: 1000
agent:
  type: opencode
opencode:
  binary_path: opencode
workspace:
  base_dir: %s
  branch_prefix: opencode/
tracker:
  type: internal
  board_dir: %s
  issue_prefix: CB
---
`, tmpDir, boardDir)
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	if err := os.WriteFile(workflowPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Override global configPath for the duration of the test.
	*configPath = workflowPath

	return tmpDir, tr, recorder
}

func TestBoardList_AllStates(t *testing.T) {
	tmpDir, tr, recorder := setupTestBoard(t)
	defer recorder.Close()

	// Create issues in different states.
	_, _ = tr.CreateIssue("Issue A", "Desc A", nil)
	issueB, _ := tr.CreateIssue("Issue B", "Desc B", nil)
	tr.UpdateIssueState(issueB.ID, types.StateInReview)
	issueC, _ := tr.CreateIssue("Issue C", "Desc C", nil)
	tr.UpdateIssueState(issueC.ID, types.StateReleased)

	// Set the global config path for the command.
	oldConfigPath := *configPath
	*configPath = filepath.Join(tmpDir, "WORKFLOW.md")
	defer func() { *configPath = oldConfigPath }()

	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := boardList([]string{"--all"})
	w.Close()
	os.Stdout = oldStdout
	if err != nil {
		t.Fatalf("boardList: %v", err)
	}
	buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "CB-1") {
		t.Errorf("expected CB-1 in output, got:\n%s", out)
	}
	if !strings.Contains(out, "CB-2") {
		t.Errorf("expected CB-2 in output, got:\n%s", out)
	}
	if !strings.Contains(out, "CB-3") {
		t.Errorf("expected CB-3 in output, got:\n%s", out)
	}
}

func TestBoardList_FilterByState(t *testing.T) {
	tmpDir, tr, recorder := setupTestBoard(t)
	defer recorder.Close()

	_, _ = tr.CreateIssue("Issue A", "Desc A", nil)
	issueB, _ := tr.CreateIssue("Issue B", "Desc B", nil)
	tr.UpdateIssueState(issueB.ID, types.StateInReview)

	oldConfigPath := *configPath
	*configPath = filepath.Join(tmpDir, "WORKFLOW.md")
	defer func() { *configPath = oldConfigPath }()

	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := boardList([]string{"--state", "in_review"})
	w.Close()
	os.Stdout = oldStdout
	if err != nil {
		t.Fatalf("boardList: %v", err)
	}
	buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "CB-2") {
		t.Errorf("expected CB-2 in output, got:\n%s", out)
	}
	if strings.Contains(out, "CB-1") {
		t.Errorf("did not expect CB-1 in filtered output, got:\n%s", out)
	}
}

func TestBoardCreate_AcceptsFlagsAfterTitle(t *testing.T) {
	tmpDir, tr, recorder := setupTestBoard(t)
	defer recorder.Close()

	oldConfigPath := *configPath
	*configPath = filepath.Join(tmpDir, "WORKFLOW.md")
	defer func() { *configPath = oldConfigPath }()

	if err := boardCreate([]string{"Flag Order", "--description", "Details", "--labels", "bug,urgent"}); err != nil {
		t.Fatalf("boardCreate: %v", err)
	}

	issues, err := tr.ListAllIssues()
	if err != nil {
		t.Fatalf("ListAllIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("issue count = %d, want 1", len(issues))
	}
	if issues[0].Description != "Details" {
		t.Fatalf("description = %q, want Details", issues[0].Description)
	}
	if got := strings.Join(issues[0].Labels, ","); got != "bug,urgent" {
		t.Fatalf("labels = %q, want bug,urgent", got)
	}
}

func TestBoardShow_DisplaysIssue(t *testing.T) {
	tmpDir, tr, recorder := setupTestBoard(t)
	defer recorder.Close()

	issue, _ := tr.CreateIssue("Show Test", "Show Desc", nil)

	oldConfigPath := *configPath
	*configPath = filepath.Join(tmpDir, "WORKFLOW.md")
	defer func() { *configPath = oldConfigPath }()

	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := boardShow([]string{issue.ID})
	w.Close()
	os.Stdout = oldStdout
	if err != nil {
		t.Fatalf("boardShow: %v", err)
	}
	buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, issue.ID) {
		t.Errorf("expected issue ID in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Show Test") {
		t.Errorf("expected title in output, got:\n%s", out)
	}
}

func TestBoardApprove_UpdatesStateAndWritesDecision(t *testing.T) {
	tmpDir, tr, recorder := setupTestBoard(t)
	defer recorder.Close()

	issue, _ := tr.CreateIssue("Approve Test", "Desc", nil)
	if _, err := tr.UpdateIssueState(issue.ID, types.StateInReview); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	// Simulate a run by creating attempt directory and summary.
	if err := recorder.EnsureIssue(issue); err != nil {
		t.Fatalf("EnsureIssue: %v", err)
	}
	_, err := recorder.BeginAttempt(issue, 1, "opencode/CB-1", "/tmp/ws", "prompt", "", "")
	if err != nil {
		t.Fatalf("BeginAttempt: %v", err)
	}

	oldConfigPath := *configPath
	*configPath = filepath.Join(tmpDir, "WORKFLOW.md")
	defer func() { *configPath = oldConfigPath }()

	err = boardApprove([]string{issue.ID, "--message", "LGTM"})
	if err != nil {
		t.Fatalf("boardApprove: %v", err)
	}

	// Verify tracker state.
	updated, err := tr.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if updated.State != types.StateReleased {
		t.Errorf("state = %v, want released", updated.State)
	}

	// Verify decision artifact.
	decision, err := recorder.LoadReviewDecision(issue.ID, 1)
	if err != nil {
		t.Fatalf("LoadReviewDecision: %v", err)
	}
	if decision.Decision != types.ReviewDecisionApproved {
		t.Errorf("decision = %v, want approved", decision.Decision)
	}
	if decision.Notes != "LGTM" {
		t.Errorf("notes = %q, want LGTM", decision.Notes)
	}
	if decision.FollowUpState != types.ReviewFollowUpDone {
		t.Errorf("follow_up_state = %v, want done", decision.FollowUpState)
	}
}

func TestBoardApprove_FailsIfNotInReview(t *testing.T) {
	tmpDir, tr, recorder := setupTestBoard(t)
	defer recorder.Close()

	issue, _ := tr.CreateIssue("Approve Fail", "Desc", nil)
	// Leave in todo state.

	oldConfigPath := *configPath
	*configPath = filepath.Join(tmpDir, "WORKFLOW.md")
	defer func() { *configPath = oldConfigPath }()

	err := boardApprove([]string{issue.ID})
	if err == nil {
		t.Fatal("expected error for issue not in_review")
	}
	if !strings.Contains(err.Error(), "expected \"in_review\"") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestBoardReject_UpdatesStateAndWritesDecision(t *testing.T) {
	tmpDir, tr, recorder := setupTestBoard(t)
	defer recorder.Close()

	issue, _ := tr.CreateIssue("Reject Test", "Desc", nil)
	if _, err := tr.UpdateIssueState(issue.ID, types.StateInReview); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	if err := recorder.EnsureIssue(issue); err != nil {
		t.Fatalf("EnsureIssue: %v", err)
	}
	_, err := recorder.BeginAttempt(issue, 1, "opencode/CB-1", "/tmp/ws", "prompt", "", "")
	if err != nil {
		t.Fatalf("BeginAttempt: %v", err)
	}

	oldConfigPath := *configPath
	*configPath = filepath.Join(tmpDir, "WORKFLOW.md")
	defer func() { *configPath = oldConfigPath }()

	err = boardReject([]string{issue.ID, "--message", "Needs tests"})
	if err != nil {
		t.Fatalf("boardReject: %v", err)
	}

	updated, err := tr.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if updated.State != types.StateUnclaimed {
		t.Errorf("state = %v, want unclaimed", updated.State)
	}

	decision, err := recorder.LoadReviewDecision(issue.ID, 1)
	if err != nil {
		t.Fatalf("LoadReviewDecision: %v", err)
	}
	if decision.Decision != types.ReviewDecisionRejected {
		t.Errorf("decision = %v, want rejected", decision.Decision)
	}
	if decision.FollowUpState != types.ReviewFollowUpTodo {
		t.Errorf("follow_up_state = %v, want todo", decision.FollowUpState)
	}
}

func TestBoardRetry_MovesRetryQueuedToTodo(t *testing.T) {
	tmpDir, tr, recorder := setupTestBoard(t)
	defer recorder.Close()

	issue, _ := tr.CreateIssue("Retry Test", "Desc", nil)
	if _, err := tr.UpdateIssueState(issue.ID, types.StateRetryQueued); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}
	// Set retry_after in the past so it's eligible.
	tr.SetRetryQueue(issue.ID, time.Now().Add(-time.Hour), 2, types.StageExecute, "", "")

	oldConfigPath := *configPath
	*configPath = filepath.Join(tmpDir, "WORKFLOW.md")
	defer func() { *configPath = oldConfigPath }()

	err := boardRetry([]string{issue.ID})
	if err != nil {
		t.Fatalf("boardRetry: %v", err)
	}

	updated, err := tr.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if updated.State != types.StateUnclaimed {
		t.Errorf("state = %v, want unclaimed", updated.State)
	}
}

func TestBoardRetry_FailsIfInProgress(t *testing.T) {
	tmpDir, tr, recorder := setupTestBoard(t)
	defer recorder.Close()

	issue, _ := tr.CreateIssue("Retry Fail", "Desc", nil)
	if _, err := tr.UpdateIssueState(issue.ID, types.StateRunning); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	oldConfigPath := *configPath
	*configPath = filepath.Join(tmpDir, "WORKFLOW.md")
	defer func() { *configPath = oldConfigPath }()

	err := boardRetry([]string{issue.ID})
	if err == nil {
		t.Fatal("expected error for issue in_progress")
	}
	if !strings.Contains(err.Error(), "currently in_progress") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestBoardApprove_FailsWithNoAttempts(t *testing.T) {
	tmpDir, tr, recorder := setupTestBoard(t)
	defer recorder.Close()

	issue, _ := tr.CreateIssue("No Attempts", "Desc", nil)
	if _, err := tr.UpdateIssueState(issue.ID, types.StateInReview); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	// Ensure issue exists in recorder so summary.json is present,
	// but do not create any attempt.
	if err := recorder.EnsureIssue(issue); err != nil {
		t.Fatalf("EnsureIssue: %v", err)
	}

	oldConfigPath := *configPath
	*configPath = filepath.Join(tmpDir, "WORKFLOW.md")
	defer func() { *configPath = oldConfigPath }()

	err := boardApprove([]string{issue.ID})
	if err == nil {
		t.Fatal("expected error when no attempt exists")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestBoardReject_NotesPreserved(t *testing.T) {
	tmpDir, tr, recorder := setupTestBoard(t)
	defer recorder.Close()

	issue, _ := tr.CreateIssue("Reject Notes", "Desc", nil)
	if _, err := tr.UpdateIssueState(issue.ID, types.StateInReview); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	if err := recorder.EnsureIssue(issue); err != nil {
		t.Fatalf("EnsureIssue: %v", err)
	}
	_, err := recorder.BeginAttempt(issue, 1, "opencode/CB-1", "/tmp/ws", "prompt", "", "")
	if err != nil {
		t.Fatalf("BeginAttempt: %v", err)
	}

	oldConfigPath := *configPath
	*configPath = filepath.Join(tmpDir, "WORKFLOW.md")
	defer func() { *configPath = oldConfigPath }()

	err = boardReject([]string{issue.ID, "--message", "Add more tests"})
	if err != nil {
		t.Fatalf("boardReject: %v", err)
	}

	decision, err := recorder.LoadReviewDecision(issue.ID, 1)
	if err != nil {
		t.Fatalf("LoadReviewDecision: %v", err)
	}
	if decision.Notes != "Add more tests" {
		t.Errorf("notes = %q, want %q", decision.Notes, "Add more tests")
	}
}

func TestBoardList_DefaultShowsAll(t *testing.T) {
	tmpDir, tr, recorder := setupTestBoard(t)
	defer recorder.Close()

	_, _ = tr.CreateIssue("Issue A", "Desc A", nil)
	issueB, _ := tr.CreateIssue("Issue B", "Desc B", nil)
	tr.UpdateIssueState(issueB.ID, types.StateInReview)

	oldConfigPath := *configPath
	*configPath = filepath.Join(tmpDir, "WORKFLOW.md")
	defer func() { *configPath = oldConfigPath }()

	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// No flags at all — should show all issues.
	err := boardList([]string{})
	w.Close()
	os.Stdout = oldStdout
	if err != nil {
		t.Fatalf("boardList: %v", err)
	}
	buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "CB-1") {
		t.Errorf("expected CB-1 in default output, got:\n%s", out)
	}
	if !strings.Contains(out, "CB-2") {
		t.Errorf("expected CB-2 in default output, got:\n%s", out)
	}
}

func TestHumanDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{90 * time.Second, "1m"},
		{45 * time.Minute, "45m"},
		{2 * time.Hour, "2h"},
	}
	for _, tt := range tests {
		got := humanDuration(tt.d)
		if got != tt.want {
			t.Errorf("humanDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate(\"short\", 10) = %q, want %q", got, "short")
	}
	if got := truncate("this is a very long string", 10); got != "this is..." {
		t.Errorf("truncate long = %q, want %q", got, "this is...")
	}
}
