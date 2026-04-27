package diagnostics

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fatihkarahan/maestro/internal/types"
)

func TestRecorder_BeginAttemptCreatesIssueAndAttemptArtifacts(t *testing.T) {
	tmpDir := t.TempDir()
	boardDir := filepath.Join(tmpDir, ".maestro", "projects", "maestro-snake", "board")

	recorder, err := NewRecorder(boardDir)
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	defer recorder.Close()

	issue := types.Issue{
		ID:          "CB-1",
		Title:       "Add score display",
		Description: "Render a score counter",
		State:       types.StateRunning,
		Labels:      []string{"feature", "smoke-test"},
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	if err := recorder.EnsureIssue(issue); err != nil {
		t.Fatalf("EnsureIssue() error = %v", err)
	}

	attempt, err := recorder.BeginAttempt(issue, 1, "maestro/CB-1", "/tmp/worktree/CB-1", "prompt text", "status snapshot", "worktree snapshot")
	if err != nil {
		t.Fatalf("BeginAttempt() error = %v", err)
	}

	if err := attempt.SetStartCommit("abc123"); err != nil {
		t.Fatalf("SetStartCommit() error = %v", err)
	}
	if err := attempt.SetLaunchInfo(1234, "sess-1", "http://127.0.0.1:1234"); err != nil {
		t.Fatalf("SetLaunchInfo() error = %v", err)
	}
	if _, err := attempt.StdoutWriter().Write([]byte("server stdout\n")); err != nil {
		t.Fatalf("write stdout = %v", err)
	}
	if _, err := attempt.StderrWriter().Write([]byte("server stderr\n")); err != nil {
		t.Fatalf("write stderr = %v", err)
	}
	if err := recorder.RecordEvent(types.OrchestratorEvent{Type: "poll_started", IssueID: "", Timestamp: time.Now().UTC(), Payload: struct{}{}}); err != nil {
		t.Fatalf("RecordEvent() error = %v", err)
	}
	if err := recorder.RecordEvent(types.OrchestratorEvent{Type: "issue.claimed", IssueID: "CB-1", Timestamp: time.Now().UTC(), Payload: map[string]any{"issue": issue}}); err != nil {
		t.Fatalf("RecordEvent(issue) error = %v", err)
	}

	runsRoot := recorder.RunsRoot()
	issueDir := filepath.Join(runsRoot, "CB-1")
	attemptDir := filepath.Join(issueDir, "attempts", "001")

	if err := recorder.FinalizeAttempt("CB-1", 1, "succeeded", "def456", nil, nil, "post status", "post worktree"); err != nil {
		t.Fatalf("FinalizeAttempt() error = %v", err)
	}

	expectedFiles := []string{
		filepath.Join(issueDir, "issue.json"),
		filepath.Join(issueDir, "summary.json"),
		filepath.Join(attemptDir, "meta.json"),
		filepath.Join(attemptDir, "prompt.md"),
		filepath.Join(attemptDir, "events.jsonl"),
		filepath.Join(attemptDir, "stdout.log"),
		filepath.Join(attemptDir, "stderr.log"),
		filepath.Join(attemptDir, "preflight", "git-status.txt"),
		filepath.Join(attemptDir, "preflight", "git-worktree-list.txt"),
		filepath.Join(attemptDir, "postflight", "git-status.txt"),
		filepath.Join(attemptDir, "postflight", "git-worktree-list.txt"),
	}
	for _, path := range expectedFiles {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}

	metaBytes, err := os.ReadFile(filepath.Join(attemptDir, "meta.json"))
	if err != nil {
		t.Fatalf("read meta.json: %v", err)
	}
	if !strings.Contains(string(metaBytes), "abc123") {
		t.Fatalf("meta.json missing start commit: %s", string(metaBytes))
	}
	if !strings.Contains(string(metaBytes), "def456") {
		t.Fatalf("meta.json missing final commit: %s", string(metaBytes))
	}

	summaryBytes, err := os.ReadFile(filepath.Join(issueDir, "summary.json"))
	if err != nil {
		t.Fatalf("read summary.json: %v", err)
	}
	if !strings.Contains(string(summaryBytes), "succeeded") {
		t.Fatalf("summary.json missing succeeded outcome: %s", string(summaryBytes))
	}

	stdoutBytes, err := os.ReadFile(filepath.Join(attemptDir, "stdout.log"))
	if err != nil {
		t.Fatalf("read stdout.log: %v", err)
	}
	if !strings.Contains(string(stdoutBytes), "server stdout") {
		t.Fatalf("stdout.log missing content: %s", string(stdoutBytes))
	}

	stderrBytes, err := os.ReadFile(filepath.Join(attemptDir, "stderr.log"))
	if err != nil {
		t.Fatalf("read stderr.log: %v", err)
	}
	if !strings.Contains(string(stderrBytes), "server stderr") {
		t.Fatalf("stderr.log missing content: %s", string(stderrBytes))
	}

	eventsBytes, err := os.ReadFile(filepath.Join(attemptDir, "events.jsonl"))
	if err != nil {
		t.Fatalf("read events.jsonl: %v", err)
	}
	if lines := strings.Count(strings.TrimSpace(string(eventsBytes)), "\n") + 1; lines < 1 {
		t.Fatalf("events.jsonl should contain at least one line, got %d", lines)
	}
}

func TestRecorder_FinalizeAttempt_AwaitingReviewSetsReviewState(t *testing.T) {
	tmpDir := t.TempDir()
	boardDir := filepath.Join(tmpDir, ".maestro", "projects", "maestro-snake", "board")

	recorder, err := NewRecorder(boardDir)
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	defer recorder.Close()

	issue := types.Issue{
		ID:          "CB-9",
		Title:       "Review handoff",
		Description: "handoff to human review",
		State:       types.StateRunning,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	if err := recorder.EnsureIssue(issue); err != nil {
		t.Fatalf("EnsureIssue() error = %v", err)
	}

	if _, err := recorder.BeginAttempt(issue, 1, "maestro/CB-9", "/tmp/worktree/CB-9", "prompt", "status", "worktree"); err != nil {
		t.Fatalf("BeginAttempt() error = %v", err)
	}

	if err := recorder.FinalizeAttempt("CB-9", 1, "awaiting_review", "abc999", nil, nil, "post status", "post worktree"); err != nil {
		t.Fatalf("FinalizeAttempt() error = %v", err)
	}

	summaryPath := filepath.Join(recorder.RunsRoot(), "CB-9", "summary.json")
	summaryBytes, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("read summary.json: %v", err)
	}

	var summary IssueSummary
	if err := json.Unmarshal(summaryBytes, &summary); err != nil {
		t.Fatalf("unmarshal summary.json: %v", err)
	}

	if summary.Outcome != "awaiting_review" {
		t.Fatalf("summary outcome = %q, want awaiting_review", summary.Outcome)
	}
	if summary.IssueState != types.StateInReview.String() {
		t.Fatalf("summary issue_state = %q, want %q", summary.IssueState, types.StateInReview.String())
	}
	if summary.FinishedAt == nil {
		t.Fatal("summary finished_at should be set for awaiting_review")
	}
}

func TestRecorder_RetryCreatesNewAttemptDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	boardDir := filepath.Join(tmpDir, "board")

	recorder, err := NewRecorder(boardDir)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	defer recorder.Close()

	issue := types.Issue{
		ID:          "CB-1",
		Title:       "Retry Test",
		Description: "Test retry artifacts",
		State:       types.StateRunning,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	_ = recorder.EnsureIssue(issue)

	// First attempt fails
	_, err = recorder.BeginAttempt(issue, 1, "branch", "/ws", "prompt1", "pre-status", "pre-worktree")
	if err != nil {
		t.Fatalf("BeginAttempt(1): %v", err)
	}
	retryAt := time.Now().Add(time.Minute)
	if err := recorder.FinalizeAttempt("CB-1", 1, "retry_queued", "", &retryAt, errors.New("fail"), "post-status", "post-worktree"); err != nil {
		t.Fatalf("FinalizeAttempt(1): %v", err)
	}

	// Second attempt succeeds
	_, err = recorder.BeginAttempt(issue, 2, "branch", "/ws", "prompt2", "pre-status", "pre-worktree")
	if err != nil {
		t.Fatalf("BeginAttempt(2): %v", err)
	}
	if err := recorder.FinalizeAttempt("CB-1", 2, "awaiting_review", "abc123", nil, nil, "post-status", "post-worktree"); err != nil {
		t.Fatalf("FinalizeAttempt(2): %v", err)
	}

	// Both attempt dirs should exist
	for _, attempt := range []int{1, 2} {
		attemptDir := filepath.Join(recorder.RunsRoot(), "CB-1", "attempts", fmt.Sprintf("%03d", attempt))
		info, err := os.Stat(attemptDir)
		if err != nil {
			t.Errorf("attempt %d dir should exist: %v", attempt, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("attempt %d path should be a directory", attempt)
		}
		metaPath := filepath.Join(attemptDir, "meta.json")
		if _, err := os.Stat(metaPath); err != nil {
			t.Errorf("attempt %d meta.json should exist: %v", attempt, err)
		}
	}

	// Verify summary reflects the latest attempt
	summary, err := recorder.LoadIssueSummary("CB-1")
	if err != nil {
		t.Fatalf("LoadIssueSummary: %v", err)
	}
	if summary.CurrentAttempt != 2 {
		t.Errorf("current_attempt = %d, want 2", summary.CurrentAttempt)
	}
	if summary.Attempts != 2 {
		t.Errorf("attempts = %d, want 2", summary.Attempts)
	}
	if summary.Outcome != "awaiting_review" {
		t.Errorf("outcome = %q, want awaiting_review", summary.Outcome)
	}
}
