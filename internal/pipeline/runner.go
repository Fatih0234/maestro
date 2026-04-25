// Package pipeline owns the per-issue stage lifecycle.
//
// Phase 2: Run is now stage-aware. The orchestrator calls Run once per stage
// (plan → execute → verify). Workspace creation is idempotent, so the same
// worktree is reused across stages.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/agent"
	"github.com/fatihkarahan/contrabass-pi/internal/config"
	"github.com/fatihkarahan/contrabass-pi/internal/diagnostics"
	"github.com/fatihkarahan/contrabass-pi/internal/types"
	"github.com/fatihkarahan/contrabass-pi/internal/util"
	"github.com/fatihkarahan/contrabass-pi/internal/workspace"
)

// Event type strings emitted by the runner. These mirror the orchestrator
// constants so consumers can treat them interchangeably.
const (
	EventWorkspaceCreated = "workspace.created"
	EventPromptBuilt      = "prompt.built"
	EventAgentStarted     = "agent.started"
	EventTokensUpdated    = "tokens.updated"
	EventAgentOutput      = "agent.output"
)

// Runner owns the lifecycle of one issue from workspace creation through
// agent completion.
type Runner struct {
	Config      *config.Config
	Workspace   workspace.WorkspaceManager
	AgentRunner types.AgentRunner
}

// Result captures the outcome of a single pipeline stage run.
type Result struct {
	IssueID             string
	Attempt             int
	Stage               types.Stage
	Success             bool
	Error               error
	WorkspacePath       string
	Branch              string
	FinalCommit         string
	PostflightStatus    string
	PostflightWorktrees string
	TokensIn            int64
	TokensOut           int64
}

// Run executes a single pipeline stage for one issue.
// It creates (or reuses) the workspace, builds a stage-specific prompt, starts
// the agent, monitors events, writes stage artifacts, and returns when the
// agent completes or the context is cancelled.
func (r *Runner) Run(ctx context.Context, issue types.Issue, attempt int, stage types.Stage, emit func(types.OrchestratorEvent)) (*Result, error) {
	issueID := issue.ID
	branchName := r.Config.Workspace.BranchPrefix + util.SanitizeBranchName(issueID)
	workspacePath := r.Workspace.Path(issueID)
	prompt := r.buildStagePrompt(issue, stage)
	startedAt := time.Now().UTC()

	// 1. Create workspace (idempotent — reused across stages)
	wsPath, err := r.Workspace.Create(ctx, issue)
	if err != nil {
		return nil, fmt.Errorf("workspace creation: %w", err)
	}
	if wsPath != "" {
		workspacePath = wsPath
	}

	emit(types.OrchestratorEvent{
		Type:      EventWorkspaceCreated,
		IssueID:   issueID,
		Timestamp: startedAt,
		Payload:   map[string]string{"path": workspacePath},
	})

	// 2. Build prompt
	emit(types.OrchestratorEvent{
		Type:      EventPromptBuilt,
		IssueID:   issueID,
		Timestamp: startedAt,
		Payload:   map[string]int{"length": len(prompt)},
	})

	// 3. Begin stage recording
	var stageRecorder *diagnostics.StageRecorder
	attemptRecorder, _ := diagnostics.AttemptFromContext(ctx)
	if attemptRecorder != nil {
		manifest := types.StageManifest{
			Stage:         stage,
			Attempt:       attempt,
			Status:        types.StageStateRunning,
			WorkspacePath: workspacePath,
			StartedAt:     startedAt,
		}
		if sr, err := attemptRecorder.BeginStage(manifest, prompt); err == nil {
			stageRecorder = sr
		}
	}

	// 4. Start agent
	proc, err := r.AgentRunner.Start(ctx, issue, workspacePath, prompt)
	if err != nil {
		if stageRecorder != nil {
			result := types.StageResult{
				Stage:       stage,
				Status:      types.StageStateFailed,
				Summary:     fmt.Sprintf("agent start failed: %v", err),
				FailureKind: types.StageFailureSessionStartError,
				Retryable:   true,
				StartedAt:   startedAt,
				FinishedAt:  time.Now().UTC(),
			}
			_ = stageRecorder.Finish(result, "", "")
		}
		return nil, fmt.Errorf("agent start: %w", err)
	}

	emit(types.OrchestratorEvent{
		Type:      EventAgentStarted,
		IssueID:   issueID,
		Timestamp: time.Now().UTC(),
		Payload: map[string]interface{}{
			"pid":        proc.PID,
			"session_id": proc.SessionID,
		},
	})

	// 5. Monitor agent
	var tokensIn, tokensOut int64
	var runErr error
	var responseText strings.Builder

monitor:
	for {
		select {
		case <-ctx.Done():
			_ = r.AgentRunner.Stop(proc)
			runErr = ctx.Err()
			break monitor
		case event, ok := <-proc.Events:
			if !ok {
				select {
				case runErr = <-proc.Done:
				default:
				}
				break monitor
			}
			if ti, to := agent.ExtractTokens(event); ti > 0 || to > 0 {
				tokensIn += ti
				tokensOut += to
				emit(types.OrchestratorEvent{
					Type:      EventTokensUpdated,
					IssueID:   issueID,
					Timestamp: time.Now().UTC(),
					Payload:   map[string]int64{"tokens_in": ti, "tokens_out": to},
				})
			}
			if event.Type == agent.EventTypeMessageUpdated {
				if text := agent.ExtractTextContent(event); text != "" {
					responseText.WriteString(text)
					emit(types.OrchestratorEvent{
						Type:      EventAgentOutput,
						IssueID:   issueID,
						Timestamp: time.Now().UTC(),
						Payload:   map[string]string{"text": text},
					})
				}
			}
			if stageRecorder != nil {
				_ = stageRecorder.AppendEvent(types.OrchestratorEvent{
					Type:      event.Type,
					IssueID:   issueID,
					Timestamp: time.Now().UTC(),
					Payload:   event.Payload,
				})
			}
		case err := <-proc.Done:
			runErr = err
			break monitor
		}
	}

	// 6. Capture postflight
	postflightStatus, postflightWorktrees := r.captureGitState(r.Workspace.BaseDir())
	finalCommit := r.captureCommit(ctx, workspacePath)
	diff := r.captureDiff(ctx, workspacePath)

	// 7. Finalize stage recording
	if stageRecorder != nil {
		var result types.StageResult
		if runErr == nil {
			result = types.StageResult{
				Stage:      stage,
				Status:     types.StageStatePassed,
				Summary:    fmt.Sprintf("%s stage completed successfully", stage),
				Retryable:  false,
				NextAction: stage.NextAction(),
				StartedAt:  startedAt,
				FinishedAt: time.Now().UTC(),
			}
		} else {
			result = types.StageResult{
				Stage:       stage,
				Status:      types.StageStateFailed,
				Summary:     runErr.Error(),
				FailureKind: r.classifyFailure(runErr, stage),
				Retryable:   true,
				StartedAt:   startedAt,
				FinishedAt:  time.Now().UTC(),
			}
		}
		_ = stageRecorder.Finish(result, responseText.String(), diff)
	}

	return &Result{
		IssueID:             issueID,
		Attempt:             attempt,
		Stage:               stage,
		Success:             runErr == nil,
		Error:               runErr,
		WorkspacePath:       workspacePath,
		Branch:              branchName,
		FinalCommit:         finalCommit,
		PostflightStatus:    postflightStatus,
		PostflightWorktrees: postflightWorktrees,
		TokensIn:            tokensIn,
		TokensOut:           tokensOut,
	}, nil
}

func (r *Runner) buildPrompt(issue types.Issue) string {
	template := r.Config.Content
	if template == "" {
		return issue.Description
	}
	template = strings.ReplaceAll(template, "{{ issue.id }}", issue.ID)
	template = strings.ReplaceAll(template, "{{ issue.identifier }}", issue.Identifier)
	template = strings.ReplaceAll(template, "{{ issue.title }}", issue.Title)
	template = strings.ReplaceAll(template, "{{ issue.description }}", issue.Description)
	labels := ""
	if len(issue.Labels) > 0 {
		labels = strings.Join(issue.Labels, ", ")
	}
	template = strings.ReplaceAll(template, "{{ issue.labels }}", labels)
	return strings.TrimSpace(template)
}

// buildStagePrompt wraps the base prompt with stage-specific intent so the
// agent knows whether it should plan, implement, or verify.
func (r *Runner) buildStagePrompt(issue types.Issue, stage types.Stage) string {
	base := r.buildPrompt(issue)

	switch stage {
	case types.StagePlan:
		return fmt.Sprintf(`You are in PLANNING mode. Analyze the following issue and produce a concrete implementation plan.

%s

Your plan should include:
- What files need to change
- What the changes should do
- How to verify the changes work
- Any risks or edge cases

Do NOT make any code changes yet.`, base)

	case types.StageExecute:
		return fmt.Sprintf(`You are in EXECUTION mode. Implement the following task.

%s

Make the necessary code changes to fulfill the requirements.`, base)

	case types.StageVerify:
		return fmt.Sprintf(`You are in VERIFICATION mode. Review the implementation against the requirements.

%s

Verify that:
- The changes satisfy the issue
- Tests pass (if applicable)
- No unintended side effects were introduced

Provide a pass/fail assessment with evidence.`, base)

	default:
		return base
	}
}

// classifyFailure maps a runtime error to a typed stage failure kind.
func (r *Runner) classifyFailure(err error, stage types.Stage) types.StageFailureKind {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return types.StageFailureTimeout
	}
	switch stage {
	case types.StagePlan:
		return types.StageFailureModelFailure
	case types.StageExecute:
		return types.StageFailureToolError
	case types.StageVerify:
		return types.StageFailureVerification
	default:
		return types.StageFailureToolError
	}
}

func (r *Runner) captureDiff(ctx context.Context, dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	return r.gitOutput(ctx, dir, "diff", "HEAD")
}

func (r *Runner) captureGitState(baseDir string) (string, string) {
	if strings.TrimSpace(baseDir) == "" {
		return "", ""
	}
	return r.gitOutput(context.Background(), baseDir, "status", "--short"),
		r.gitOutput(context.Background(), baseDir, "worktree", "list")
}

func (r *Runner) captureCommit(ctx context.Context, dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (r *Runner) gitOutput(ctx context.Context, dir string, args ...string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text != "" {
			return text + "\nerror: " + err.Error()
		}
		return "error: " + err.Error()
	}
	return text
}
