package pipeline

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/config"
	"github.com/fatihkarahan/contrabass-pi/internal/diagnostics"
	"github.com/fatihkarahan/contrabass-pi/internal/types"
	"github.com/fatihkarahan/contrabass-pi/internal/workspace"
)

// mockAgentRunner is a minimal mock for testing pipeline.Runner in isolation.
type mockAgentRunner struct {
	shouldFail bool
	doneErr    error
	delay      time.Duration
}

func (m *mockAgentRunner) Start(ctx context.Context, issue types.Issue, workspace, prompt string) (*types.AgentProcess, error) {
	if m.shouldFail {
		return nil, errors.New("agent start failed")
	}
	events := make(chan types.AgentEvent, 4)
	done := make(chan error, 1)
	go func() {
		if m.delay > 0 {
			time.Sleep(m.delay)
		}
		close(events)
		done <- m.doneErr
		close(done)
	}()
	return &types.AgentProcess{
		PID:       1234,
		SessionID: "test-session",
		Events:    events,
		Done:      done,
	}, nil
}

func (m *mockAgentRunner) Stop(proc *types.AgentProcess) error { return nil }
func (m *mockAgentRunner) Close() error                         { return nil }

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := exec.Command("git", "init", dir).Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}
	if err := exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run(); err != nil {
		t.Fatalf("git config email failed: %v", err)
	}
	if err := exec.Command("git", "-C", dir, "config", "user.name", "Test").Run(); err != nil {
		t.Fatalf("git config name failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "readme.md"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	if err := exec.Command("git", "-C", dir, "add", ".").Run(); err != nil {
		t.Fatalf("git add failed: %v", err)
	}
	if err := exec.Command("git", "-C", dir, "commit", "-m", "init").Run(); err != nil {
		t.Fatalf("git commit failed: %v", err)
	}
}

func TestRunner_Run_Success(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)
	ws := workspace.New(workspace.Config{BaseDir: tmpDir, BranchPrefix: "test/"})
	runner := &Runner{
		Config: &config.Config{
			Content:   "Fix: {{ issue.title }}",
			Workspace: config.WorkspaceConfig{BranchPrefix: "test/"},
		},
		Workspace:   ws,
		AgentRunner: &mockAgentRunner{},
	}

	var emitted []types.OrchestratorEvent
	emit := func(e types.OrchestratorEvent) { emitted = append(emitted, e) }

	issue := types.Issue{ID: "CB-1", Title: "Test Issue", Description: "Do something"}
	result, err := runner.Run(context.Background(), issue, 1, types.StageExecute, emit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatal("expected success")
	}
	if result.IssueID != "CB-1" {
		t.Errorf("IssueID = %q, want CB-1", result.IssueID)
	}
	if result.Branch != "test/CB-1" {
		t.Errorf("Branch = %q, want test/CB-1", result.Branch)
	}
}

func TestRunner_Run_WorkspaceError(t *testing.T) {
	// Use a non-existent base dir to force workspace creation failure.
	runner := &Runner{
		Config: &config.Config{
			Content:   "Fix: {{ issue.title }}",
			Workspace: config.WorkspaceConfig{BranchPrefix: "test/"},
		},
		Workspace:   workspace.New(workspace.Config{BaseDir: "/nonexistent/path/12345", BranchPrefix: "test/"}),
		AgentRunner: &mockAgentRunner{},
	}

	issue := types.Issue{ID: "CB-1", Title: "Test Issue", Description: "Do something"}
	_, err := runner.Run(context.Background(), issue, 1, types.StageExecute, func(types.OrchestratorEvent) {})
	if err == nil {
		t.Fatal("expected workspace error")
	}
}

func TestRunner_Run_AgentError(t *testing.T) {
	ws := workspace.New(workspace.Config{BaseDir: t.TempDir(), BranchPrefix: "test/"})
	runner := &Runner{
		Config: &config.Config{
			Content:   "Fix: {{ issue.title }}",
			Workspace: config.WorkspaceConfig{BranchPrefix: "test/"},
		},
		Workspace:   ws,
		AgentRunner: &mockAgentRunner{shouldFail: true},
	}

	issue := types.Issue{ID: "CB-1", Title: "Test Issue", Description: "Do something"}
	_, err := runner.Run(context.Background(), issue, 1, types.StageExecute, func(types.OrchestratorEvent) {})
	if err == nil {
		t.Fatal("expected agent start error")
	}
}

func TestRunner_BuildPrompt(t *testing.T) {
	runner := &Runner{
		Config: &config.Config{
			Content: "ID={{ issue.id }} Title={{ issue.title }}",
		},
	}
	issue := types.Issue{ID: "CB-1", Title: "Hello", Description: "World"}
	prompt := runner.buildPrompt(issue)
	want := "ID=CB-1 Title=Hello"
	if prompt != want {
		t.Errorf("prompt = %q, want %q", prompt, want)
	}
}

func TestRunner_BuildStagePrompt(t *testing.T) {
	runner := &Runner{
		Config: &config.Config{
			Content: "Task: {{ issue.title }}",
		},
	}
	issue := types.Issue{ID: "CB-1", Title: "Hello", Description: "World"}

	plan := runner.buildStagePrompt(issue, types.StagePlan)
	if !strings.Contains(plan, "PLANNING mode") {
		t.Errorf("plan prompt missing PLANNING mode header")
	}
	if !strings.Contains(plan, "Do NOT make any code changes") {
		t.Errorf("plan prompt missing read-only instruction")
	}
	if !strings.Contains(plan, "Task: Hello") {
		t.Errorf("plan prompt missing base prompt")
	}

	execute := runner.buildStagePrompt(issue, types.StageExecute)
	if !strings.Contains(execute, "EXECUTION mode") {
		t.Errorf("execute prompt missing EXECUTION mode header")
	}
	if !strings.Contains(execute, "Task: Hello") {
		t.Errorf("execute prompt missing base prompt")
	}

	verify := runner.buildStagePrompt(issue, types.StageVerify)
	if !strings.Contains(verify, "VERIFICATION mode") {
		t.Errorf("verify prompt missing VERIFICATION mode header")
	}
	if !strings.Contains(verify, "pass/fail assessment") {
		t.Errorf("verify prompt missing reviewer instruction")
	}

	// Unknown stage falls back to base prompt
	unknown := runner.buildStagePrompt(issue, types.Stage("unknown"))
	if unknown != "Task: Hello" {
		t.Errorf("unknown stage prompt = %q, want base prompt", unknown)
	}
}

func TestRunner_Run_StageRecording(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	// Set up recorder rooted beside a fake board dir
	recorder, err := diagnostics.NewRecorder(filepath.Join(tmpDir, "board"))
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	issue := types.Issue{ID: "CB-1", Title: "Test Issue", Description: "Do something"}
	if err := recorder.EnsureIssue(issue); err != nil {
		t.Fatalf("EnsureIssue: %v", err)
	}
	attemptRecorder, err := recorder.BeginAttempt(issue, 1, "test/CB-1", tmpDir, "prompt", "", "")
	if err != nil {
		t.Fatalf("BeginAttempt: %v", err)
	}
	ctx := diagnostics.WithAttemptRecorder(context.Background(), attemptRecorder)

	ws := workspace.New(workspace.Config{BaseDir: tmpDir, BranchPrefix: "test/"})
	runner := &Runner{
		Config: &config.Config{
			Content:   "Fix: {{ issue.title }}",
			Workspace: config.WorkspaceConfig{BranchPrefix: "test/"},
		},
		Workspace:   ws,
		AgentRunner: &mockAgentRunner{},
	}

	_, err = runner.Run(ctx, issue, 1, types.StagePlan, func(types.OrchestratorEvent) {})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	// Verify stage manifest was written
	manifest, err := recorder.LoadStageManifest("CB-1", 1, types.StagePlan)
	if err != nil {
		t.Fatalf("LoadStageManifest: %v", err)
	}
	if manifest.Stage != types.StagePlan {
		t.Errorf("Stage = %v, want plan", manifest.Stage)
	}
	if manifest.Status != types.StageStatePassed {
		t.Errorf("Status = %v, want passed", manifest.Status)
	}

	// Verify stage result was written
	result, err := recorder.LoadStageResult("CB-1", 1, types.StagePlan)
	if err != nil {
		t.Fatalf("LoadStageResult: %v", err)
	}
	if result.Status != types.StageStatePassed {
		t.Errorf("Result.Status = %v, want passed", result.Status)
	}
	if result.NextAction != types.StageExecute.String() {
		t.Errorf("NextAction = %v, want execute", result.NextAction)
	}
}

func TestRunner_Run_AgentStartFailureWritesStageResult(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	recorder, err := diagnostics.NewRecorder(filepath.Join(tmpDir, "board"))
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	issue := types.Issue{ID: "CB-1", Title: "Test Issue", Description: "Do something"}
	_ = recorder.EnsureIssue(issue)
	attemptRecorder, _ := recorder.BeginAttempt(issue, 1, "test/CB-1", tmpDir, "prompt", "", "")
	ctx := diagnostics.WithAttemptRecorder(context.Background(), attemptRecorder)

	ws := workspace.New(workspace.Config{BaseDir: tmpDir, BranchPrefix: "test/"})
	runner := &Runner{
		Config: &config.Config{
			Content:   "Fix: {{ issue.title }}",
			Workspace: config.WorkspaceConfig{BranchPrefix: "test/"},
		},
		Workspace:   ws,
		AgentRunner: &mockAgentRunner{shouldFail: true},
	}

	_, err = runner.Run(ctx, issue, 1, types.StageExecute, func(types.OrchestratorEvent) {})
	if err == nil {
		t.Fatal("expected agent start error")
	}

	result, err := recorder.LoadStageResult("CB-1", 1, types.StageExecute)
	if err != nil {
		t.Fatalf("LoadStageResult: %v", err)
	}
	if result.Status != types.StageStateFailed {
		t.Errorf("Result.Status = %v, want failed", result.Status)
	}
	if result.FailureKind != types.StageFailureSessionStartError {
		t.Errorf("FailureKind = %v, want session_start_error", result.FailureKind)
	}
	if !result.Retryable {
		t.Error("expected retryable = true")
	}
}
