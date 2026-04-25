package diagnostics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

func TestRecorder_StageArtifactsAdvanceSummary(t *testing.T) {
	tmpDir := t.TempDir()
	boardDir := filepath.Join(tmpDir, ".contrabass", "projects", "contrabass-snake", "board")

	recorder, err := NewRecorder(boardDir)
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	defer recorder.Close()

	now := time.Now().UTC()
	issue := types.Issue{
		ID:          "CB-1",
		Title:       "Add score display",
		Description: "Render a score counter",
		State:       types.StateRunning,
		Labels:      []string{"feature", "smoke-test"},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := recorder.EnsureIssue(issue); err != nil {
		t.Fatalf("EnsureIssue() error = %v", err)
	}

	issueSnapshotPath := filepath.Join(recorder.RunsRoot(), "CB-1", "issue.json")
	issueBytes, err := os.ReadFile(issueSnapshotPath)
	if err != nil {
		t.Fatalf("read issue.json: %v", err)
	}
	if !strings.Contains(string(issueBytes), `"state": "in_progress"`) && !strings.Contains(string(issueBytes), `"state":"in_progress"`) {
		t.Fatalf("issue.json should store board state strings, got: %s", string(issueBytes))
	}

	attempt, err := recorder.BeginAttempt(issue, 1, "opencode/CB-1", "/tmp/worktree/CB-1", "prompt body", "git status", "git worktree list")
	if err != nil {
		t.Fatalf("BeginAttempt() error = %v", err)
	}

	summary, err := recorder.LoadIssueSummary("CB-1")
	if err != nil {
		t.Fatalf("LoadIssueSummary() after BeginAttempt error = %v", err)
	}
	if summary.CurrentStage != types.StagePlan {
		t.Fatalf("summary.current_stage = %q, want %q", summary.CurrentStage, types.StagePlan)
	}
	if summary.ReviewState != types.ReviewStatePending {
		t.Fatalf("summary.review_state = %q, want %q", summary.ReviewState, types.ReviewStatePending)
	}
	if summary.Outcome != "running" {
		t.Fatalf("summary.outcome = %q, want running", summary.Outcome)
	}

	stage, err := attempt.BeginStage(types.StageManifest{
		Stage:         types.StagePlan,
		Agent:         "opencode",
		WorkspacePath: "/tmp/worktree/CB-1",
	}, "plan prompt text")
	if err != nil {
		t.Fatalf("BeginStage(plan) error = %v", err)
	}

	if err := stage.AppendStdoutLine("planning stdout"); err != nil {
		t.Fatalf("AppendStdoutLine() error = %v", err)
	}
	if err := stage.AppendStderrLine("planning stderr"); err != nil {
		t.Fatalf("AppendStderrLine() error = %v", err)
	}
	if err := stage.AppendEvent(types.OrchestratorEvent{Type: "stage.started", IssueID: "CB-1", Timestamp: time.Now().UTC(), Payload: map[string]any{"stage": "plan"}}); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	if err := stage.Finish(types.StageResult{
		Status:   types.StageStatePassed,
		Summary:  "plan complete",
		Evidence: []string{"read issue", "draft plan"},
	}, "plan response text", ""); err != nil {
		t.Fatalf("Finish(plan) error = %v", err)
	}

	stageDir := filepath.Join(recorder.RunsRoot(), "CB-1", "attempts", "001", "stages", "plan")
	expectedFiles := []string{
		filepath.Join(stageDir, "manifest.json"),
		filepath.Join(stageDir, "prompt.md"),
		filepath.Join(stageDir, "response.md"),
		filepath.Join(stageDir, "result.json"),
		filepath.Join(stageDir, "events.jsonl"),
		filepath.Join(stageDir, "stdout.log"),
		filepath.Join(stageDir, "stderr.log"),
	}
	for _, path := range expectedFiles {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(stageDir, "diff.patch")); !os.IsNotExist(err) {
		t.Fatalf("plan stage should not create diff.patch, got err=%v", err)
	}

	loadedManifest, err := recorder.LoadStageManifest("CB-1", 1, types.StagePlan)
	if err != nil {
		t.Fatalf("LoadStageManifest() error = %v", err)
	}
	if loadedManifest.Status != types.StageStatePassed {
		t.Fatalf("manifest.status = %q, want %q", loadedManifest.Status, types.StageStatePassed)
	}
	if loadedManifest.ResultPath == "" || loadedManifest.PromptPath == "" || loadedManifest.ResponsePath == "" {
		t.Fatalf("manifest should record stage file paths: %+v", loadedManifest)
	}

	loadedResult, err := recorder.LoadStageResult("CB-1", 1, types.StagePlan)
	if err != nil {
		t.Fatalf("LoadStageResult() error = %v", err)
	}
	if loadedResult.Status != types.StageStatePassed {
		t.Fatalf("result.status = %q, want %q", loadedResult.Status, types.StageStatePassed)
	}
	if loadedResult.NextAction != types.StageExecute.String() {
		t.Fatalf("result.next_action = %q, want %q", loadedResult.NextAction, types.StageExecute.String())
	}

	loadedMeta, err := recorder.LoadAttemptMeta("CB-1", 1)
	if err != nil {
		t.Fatalf("LoadAttemptMeta() error = %v", err)
	}
	if loadedMeta.CurrentStage != types.StageExecute {
		t.Fatalf("meta.current_stage = %q, want %q", loadedMeta.CurrentStage, types.StageExecute)
	}
	if loadedMeta.Outcome != "running" {
		t.Fatalf("meta.outcome = %q, want running", loadedMeta.Outcome)
	}

	summary, err = recorder.LoadIssueSummary("CB-1")
	if err != nil {
		t.Fatalf("LoadIssueSummary() after stage finish error = %v", err)
	}
	if summary.CurrentStage != types.StageExecute {
		t.Fatalf("summary.current_stage = %q, want %q", summary.CurrentStage, types.StageExecute)
	}
	if summary.ReviewState != types.ReviewStatePending {
		t.Fatalf("summary.review_state = %q, want %q", summary.ReviewState, types.ReviewStatePending)
	}
	if summary.FinishedAt != nil {
		t.Fatalf("summary.finished_at should stay nil until review handoff")
	}
	if summary.LastError != "" {
		t.Fatalf("summary.last_error = %q, want empty", summary.LastError)
	}
}

func TestRecorder_ReviewArtifactsPersistAcrossRecorderRestart(t *testing.T) {
	tmpDir := t.TempDir()
	boardDir := filepath.Join(tmpDir, ".contrabass", "projects", "contrabass-snake", "board")

	recorder, err := NewRecorder(boardDir)
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}

	now := time.Now().UTC()
	issue := types.Issue{
		ID:          "CB-9",
		Title:       "Review handoff",
		Description: "handoff to human review",
		State:       types.StateRunning,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := recorder.EnsureIssue(issue); err != nil {
		t.Fatalf("EnsureIssue() error = %v", err)
	}

	attempt, err := recorder.BeginAttempt(issue, 1, "opencode/CB-9", "/tmp/worktree/CB-9", "prompt", "status", "worktree")
	if err != nil {
		t.Fatalf("BeginAttempt() error = %v", err)
	}

	if err := attempt.RecordReviewHandoff("handoff body", "handoff notes"); err != nil {
		t.Fatalf("RecordReviewHandoff() error = %v", err)
	}

	summary, err := recorder.LoadIssueSummary("CB-9")
	if err != nil {
		t.Fatalf("LoadIssueSummary() after handoff error = %v", err)
	}
	if summary.CurrentStage != types.StageHumanReview {
		t.Fatalf("summary.current_stage = %q, want %q", summary.CurrentStage, types.StageHumanReview)
	}
	if summary.ReviewState != types.ReviewStateReady {
		t.Fatalf("summary.review_state = %q, want %q", summary.ReviewState, types.ReviewStateReady)
	}
	if summary.IssueState != types.StateInReview.BoardState() {
		t.Fatalf("summary.issue_state = %q, want %q", summary.IssueState, types.StateInReview.BoardState())
	}
	if summary.FinishedAt == nil {
		t.Fatal("summary.finished_at should be set once the run is handed off")
	}

	decision := types.ReviewDecision{
		Decision:   types.ReviewDecisionApproved,
		ReviewedBy: "alice",
		Notes:      "looks good",
	}
	if err := attempt.RecordReviewDecision(decision); err != nil {
		t.Fatalf("RecordReviewDecision() error = %v", err)
	}

	expectedReviewFiles := []string{
		filepath.Join(recorder.RunsRoot(), "CB-9", "attempts", "001", "review", "handoff.md"),
		filepath.Join(recorder.RunsRoot(), "CB-9", "attempts", "001", "review", "notes.md"),
		filepath.Join(recorder.RunsRoot(), "CB-9", "attempts", "001", "review", "decision.json"),
	}
	for _, path := range expectedReviewFiles {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}

	loadedDecision, err := recorder.LoadReviewDecision("CB-9", 1)
	if err != nil {
		t.Fatalf("LoadReviewDecision() error = %v", err)
	}
	if loadedDecision.Decision != types.ReviewDecisionApproved {
		t.Fatalf("decision.decision = %q, want %q", loadedDecision.Decision, types.ReviewDecisionApproved)
	}
	if loadedDecision.FollowUpState != types.ReviewFollowUpDone {
		t.Fatalf("decision.follow_up_state = %q, want %q", loadedDecision.FollowUpState, types.ReviewFollowUpDone)
	}
	if loadedDecision.ReviewedBy != "alice" {
		t.Fatalf("decision.reviewed_by = %q, want alice", loadedDecision.ReviewedBy)
	}

	summary, err = recorder.LoadIssueSummary("CB-9")
	if err != nil {
		t.Fatalf("LoadIssueSummary() after decision error = %v", err)
	}
	if summary.CurrentStage != types.StageHumanReview {
		t.Fatalf("summary.current_stage = %q, want %q", summary.CurrentStage, types.StageHumanReview)
	}
	if summary.ReviewState != types.ReviewStateApproved {
		t.Fatalf("summary.review_state = %q, want %q", summary.ReviewState, types.ReviewStateApproved)
	}
	if summary.IssueState != types.ReviewFollowUpDone.String() {
		t.Fatalf("summary.issue_state = %q, want %q", summary.IssueState, types.ReviewFollowUpDone.String())
	}
	if summary.Outcome != types.ReviewFollowUpDone.String() {
		t.Fatalf("summary.outcome = %q, want %q", summary.Outcome, types.ReviewFollowUpDone.String())
	}
	if summary.ReviewedBy != "alice" {
		t.Fatalf("summary.reviewed_by = %q, want alice", summary.ReviewedBy)
	}
	if summary.ReviewedAt == nil || summary.ReviewedAt.IsZero() {
		t.Fatal("summary.reviewed_at should be set after decision")
	}
	if summary.FinishedAt == nil {
		t.Fatal("summary.finished_at should remain set after decision")
	}

	if err := recorder.Close(); err != nil {
		t.Fatalf("recorder.Close() error = %v", err)
	}

	reopened, err := NewRecorder(boardDir)
	if err != nil {
		t.Fatalf("NewRecorder(reopened) error = %v", err)
	}
	defer reopened.Close()

	finalIssue := issue
	finalIssue.State = types.StateReleased
	if err := reopened.EnsureIssue(finalIssue); err != nil {
		t.Fatalf("EnsureIssue(reopened) error = %v", err)
	}

	reloadedSummary, err := reopened.LoadIssueSummary("CB-9")
	if err != nil {
		t.Fatalf("LoadIssueSummary(reopened) error = %v", err)
	}
	if reloadedSummary.CurrentStage != types.StageHumanReview {
		t.Fatalf("reloaded summary.current_stage = %q, want %q", reloadedSummary.CurrentStage, types.StageHumanReview)
	}
	if reloadedSummary.ReviewState != types.ReviewStateApproved {
		t.Fatalf("reloaded summary.review_state = %q, want %q", reloadedSummary.ReviewState, types.ReviewStateApproved)
	}
	if reloadedSummary.IssueState != types.StateReleased.BoardState() {
		t.Fatalf("reloaded summary.issue_state = %q, want %q", reloadedSummary.IssueState, types.StateReleased.BoardState())
	}
	if reloadedSummary.Outcome != types.ReviewFollowUpDone.String() {
		t.Fatalf("reloaded summary.outcome = %q, want %q", reloadedSummary.Outcome, types.ReviewFollowUpDone.String())
	}
}
