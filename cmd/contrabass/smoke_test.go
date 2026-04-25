package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/agent"
	"github.com/fatihkarahan/contrabass-pi/internal/config"
	"github.com/fatihkarahan/contrabass-pi/internal/diagnostics"
	"github.com/fatihkarahan/contrabass-pi/internal/orchestrator"
	"github.com/fatihkarahan/contrabass-pi/internal/tracker"
	"github.com/fatihkarahan/contrabass-pi/internal/types"
	"github.com/fatihkarahan/contrabass-pi/internal/workspace"
)

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := exec.Command("git", "init", dir).Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := exec.Command("git", "-C", dir, "config", "user.email", "smoke@test.com").Run(); err != nil {
		t.Fatalf("git config email: %v", err)
	}
	if err := exec.Command("git", "-C", dir, "config", "user.name", "Smoke Test").Run(); err != nil {
		t.Fatalf("git config name: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := exec.Command("git", "-C", dir, "add", ".").Run(); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := exec.Command("git", "-C", dir, "commit", "-m", "init").Run(); err != nil {
		t.Fatalf("git commit: %v", err)
	}
}

// TestSmoke_OneIssue_ReachesInReview runs the full orchestration stack with a
// fake agent runner and verifies the issue reaches in_review with all stage
// artifacts present.
func TestSmoke_OneIssue_ReachesInReview(t *testing.T) {
	tmpDir := t.TempDir()
	boardDir := filepath.Join(tmpDir, "board")

	// Create tracker and issue
	tr := tracker.New(tracker.Config{BoardDir: boardDir, IssuePrefix: "CB"})
	if err := tr.EnsureBoard(); err != nil {
		t.Fatalf("EnsureBoard: %v", err)
	}
	issue, err := tr.CreateIssue("Smoke Test", "Verify the smoke path reaches in_review", nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	// Create workspace manager in a temp git repo
	repoDir := filepath.Join(tmpDir, "repo")
	initGitRepo(t, repoDir)

	ws := workspace.New(workspace.Config{BaseDir: repoDir, BranchPrefix: "smoke/"})

	// Fake runner: every stage succeeds quickly
	fakeRunner := agent.NewFakeRunner()
	fakeRunner.DefaultScript = &agent.StageScript{
		Delay: 10 * time.Millisecond,
		Events: []types.AgentEvent{
			{Type: "session.status", Payload: map[string]interface{}{
				"properties": map[string]interface{}{
					"sessionID": "sess-1",
					"status":    map[string]interface{}{"type": "idle"},
				},
			}},
		},
	}

	cfg := &config.Config{
		MaxConcurrency: 1,
		PollIntervalMs: 100,
		Content:        "Task: {{ issue.title }}",
		Workspace:      config.WorkspaceConfig{BaseDir: repoDir, BranchPrefix: "smoke/"},
		Agent:          config.AgentConfig{Type: "opencode"},
		OpenCode:       &config.OpenCodeConfig{BinaryPath: "opencode"},
		AgentTimeoutMs: 60000,
		StallTimeoutMs: 30000,
	}

	recorder, err := diagnostics.NewRecorder(boardDir)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	defer recorder.Close()

	orch := orchestrator.New(cfg, tr, ws, fakeRunner)
	orch.SetRecorder(recorder)

	// Run orchestrator long enough for the pipeline to finish, then stop
	go func() {
		time.Sleep(500 * time.Millisecond)
		orch.Stop()
	}()
	if err := orch.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify issue state on tracker
	updated, err := tr.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if updated.State != types.StateInReview {
		t.Errorf("tracker issue state = %v, want in_review", updated.State)
	}

	// Verify summary
	summary, err := recorder.LoadIssueSummary(issue.ID)
	if err != nil {
		t.Fatalf("LoadIssueSummary: %v", err)
	}
	if summary.ReviewState != types.ReviewStateReady {
		t.Errorf("summary review_state = %v, want ready", summary.ReviewState)
	}
	if summary.Outcome != "awaiting_review" {
		t.Errorf("summary outcome = %q, want awaiting_review", summary.Outcome)
	}

	// Verify all stage manifests and results exist
	for _, stage := range []types.Stage{types.StagePlan, types.StageExecute, types.StageVerify} {
		manifest, err := recorder.LoadStageManifest(issue.ID, 1, stage)
		if err != nil {
			t.Fatalf("LoadStageManifest(%s): %v", stage, err)
		}
		if manifest.Status != types.StageStatePassed {
			t.Errorf("%s manifest status = %v, want passed", stage, manifest.Status)
		}

		result, err := recorder.LoadStageResult(issue.ID, 1, stage)
		if err != nil {
			t.Fatalf("LoadStageResult(%s): %v", stage, err)
		}
		if result.Status != types.StageStatePassed {
			t.Errorf("%s result status = %v, want passed", stage, result.Status)
		}
	}

	// Verify review handoff exists
	handoffPath := filepath.Join(recorder.RunsRoot(), issue.ID, "attempts", "001", "review", "handoff.md")
	if _, err := os.Stat(handoffPath); err != nil {
		t.Errorf("review handoff missing: %v", err)
	}

	// Verify workspace is preserved after in_review
	if _, err := os.Stat(ws.Path(issue.ID)); err != nil {
		t.Errorf("workspace should be preserved after in_review: %v", err)
	}
}

// TestSmoke_OneIssue_ApprovalToDone runs the full stack to in_review, then
// simulates human approval and verifies the issue can reach done without
// destroying evidence.
func TestSmoke_OneIssue_ApprovalToDone(t *testing.T) {
	tmpDir := t.TempDir()
	boardDir := filepath.Join(tmpDir, "board")

	tr := tracker.New(tracker.Config{BoardDir: boardDir, IssuePrefix: "CB"})
	_ = tr.EnsureBoard()
	issue, err := tr.CreateIssue("Approval Test", "Test approval path to done", nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	repoDir := filepath.Join(tmpDir, "repo")
	initGitRepo(t, repoDir)

	ws := workspace.New(workspace.Config{BaseDir: repoDir, BranchPrefix: "smoke/"})

	fakeRunner := agent.NewFakeRunner()
	fakeRunner.DefaultScript = &agent.StageScript{
		Delay: 10 * time.Millisecond,
		Events: []types.AgentEvent{
			{Type: "session.status", Payload: map[string]interface{}{
				"properties": map[string]interface{}{
					"sessionID": "sess-1",
					"status":    map[string]interface{}{"type": "idle"},
				},
			}},
		},
	}

	cfg := &config.Config{
		MaxConcurrency: 1,
		PollIntervalMs: 100,
		Content:        "Task: {{ issue.title }}",
		Workspace:      config.WorkspaceConfig{BaseDir: repoDir, BranchPrefix: "smoke/"},
		Agent:          config.AgentConfig{Type: "opencode"},
		OpenCode:       &config.OpenCodeConfig{BinaryPath: "opencode"},
		AgentTimeoutMs: 60000,
		StallTimeoutMs: 30000,
	}

	recorder, err := diagnostics.NewRecorder(boardDir)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	defer recorder.Close()

	orch := orchestrator.New(cfg, tr, ws, fakeRunner)
	orch.SetRecorder(recorder)

	// Run to in_review
	go func() {
		time.Sleep(500 * time.Millisecond)
		orch.Stop()
	}()
	if err := orch.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Simulate human approval by writing the decision artifact directly
	attemptDir := filepath.Join(recorder.RunsRoot(), issue.ID, "attempts", "001")
	decision := types.ReviewDecision{
		Decision:      types.ReviewDecisionApproved,
		ReviewedBy:    "smoke-tester",
		ReviewedAt:    time.Now().UTC(),
		Notes:         "approved during smoke test",
		FollowUpState: types.ReviewFollowUpDone,
	}
	decisionPath := filepath.Join(attemptDir, "review", "decision.json")
	if err := os.MkdirAll(filepath.Dir(decisionPath), 0o755); err != nil {
		t.Fatalf("mkdir decision dir: %v", err)
	}
	decisionData, _ := json.MarshalIndent(decision, "", "  ")
	if err := os.WriteFile(decisionPath, append(decisionData, '\n'), 0o644); err != nil {
		t.Fatalf("write decision: %v", err)
	}

	// Update tracker to done
	if _, err := tr.UpdateIssueState(issue.ID, types.StateReleased); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	// Verify final tracker state
	final, err := tr.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if final.State != types.StateReleased {
		t.Errorf("final tracker state = %v, want released", final.State)
	}

	// Verify decision artifact can be loaded
	loaded, err := recorder.LoadReviewDecision(issue.ID, 1)
	if err != nil {
		t.Fatalf("LoadReviewDecision: %v", err)
	}
	if loaded.Decision != types.ReviewDecisionApproved {
		t.Errorf("decision = %v, want approved", loaded.Decision)
	}
	if loaded.ReviewedBy != "smoke-tester" {
		t.Errorf("reviewed_by = %q, want smoke-tester", loaded.ReviewedBy)
	}
	if loaded.FollowUpState != types.ReviewFollowUpDone {
		t.Errorf("follow_up_state = %v, want done", loaded.FollowUpState)
	}

	// Verify stage artifacts are still present (not deleted by approval)
	for _, stage := range []types.Stage{types.StagePlan, types.StageExecute, types.StageVerify} {
		if _, err := recorder.LoadStageManifest(issue.ID, 1, stage); err != nil {
			t.Errorf("stage %s manifest should still exist: %v", stage, err)
		}
		if _, err := recorder.LoadStageResult(issue.ID, 1, stage); err != nil {
			t.Errorf("stage %s result should still exist: %v", stage, err)
		}
	}
}
