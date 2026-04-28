// Package pipeline owns the per-issue stage lifecycle.
//
// Phase 2: Run is now stage-aware. The orchestrator calls Run once per stage
// (plan → execute → verify). Workspace creation is idempotent, so the same
// worktree is reused across stages.
package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fatihkarahan/maestro/internal/agent"
	"github.com/fatihkarahan/maestro/internal/config"
	"github.com/fatihkarahan/maestro/internal/diagnostics"
	"github.com/fatihkarahan/maestro/internal/types"
	"github.com/fatihkarahan/maestro/internal/util"
	"github.com/fatihkarahan/maestro/internal/workspace"
)

// Sentinel errors for the orchestrator to reliably classify failures without
// matching against error messages.
var (
	ErrWorkspace          = errors.New("workspace creation")
	ErrAgent              = errors.New("agent start")
	ErrVerificationFailed = errors.New("verification failed")
	ErrVerificationResult = errors.New("verification result")
)

type verificationResult struct {
	Passed  bool   `json:"passed"`
	Summary string `json:"summary"`
}

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
	StageOutput         string // Full agent response text (plan, implementation, verification result)
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
		return nil, fmt.Errorf("%w: %w", ErrWorkspace, err)
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
		err = fmt.Errorf("%w: %w", ErrAgent, err)
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
		return nil, err
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
			if event.Type == agent.EventTypeMessageUpdated || event.Type == agent.EventTypeMessageDelta {
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
			draining := true
			for draining {
				select {
				case event, ok := <-proc.Events:
					if !ok {
						draining = false
						continue
					}
					if ti, to := agent.ExtractTokens(event); ti > 0 || to > 0 {
						tokensIn += ti
						tokensOut += to
					}
					if event.Type == agent.EventTypeMessageUpdated || event.Type == agent.EventTypeMessageDelta {
						responseText.WriteString(agent.ExtractTextContent(event))
					}
					if stageRecorder != nil {
						_ = stageRecorder.AppendEvent(types.OrchestratorEvent{Type: event.Type, IssueID: issueID, Timestamp: time.Now().UTC(), Payload: event.Payload})
					}
				default:
					draining = false
				}
			}
			break monitor
		}
	}

	if runErr == nil && stage == types.StageVerify {
		if verifyErr := parseVerificationResult(responseText.String()); verifyErr != nil {
			runErr = verifyErr
		}
	}

	// 6. Capture postflight
	postflightStatus, postflightWorktrees := r.captureGitState(r.Workspace.BaseDir())
	finalCommit := r.captureCommit(ctx, workspacePath)
	diff := r.captureDiff(ctx, workspacePath)

	// Note: commits are intentionally left to the human reviewer.
	// The execute stage produces uncommitted changes so the reviewer can
	// inspect, amend, split, or re-commit with their own message.

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
		StageOutput:         responseText.String(),
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
	return util.ExpandPrompt(r.Config.Content, issue)
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
		prompt := fmt.Sprintf(`You are in EXECUTION mode. Implement the following task.

%s`, base)
		if issue.Plan != "" {
			prompt += fmt.Sprintf(`

Implementation plan to follow:

%s`, issue.Plan)
		}
		prompt += `

Make the necessary code changes to fulfill the requirements.

Do NOT run any git commands (do not commit, stage, or modify git state).`
		if issue.Feedback != "" {
			prompt += fmt.Sprintf(`

IMPORTANT: Your previous attempt was reviewed and the following issues were found:

%s

Address these specific issues in this attempt.`, issue.Feedback)
		}
		return prompt

	case types.StageVerify:
		prompt := fmt.Sprintf(`Check if the code changes satisfy this task.

Task:
%s`, base)
		if issue.Feedback != "" {
			prompt += fmt.Sprintf(`

The previous run was rejected with this feedback — verify these specific issues are fixed:

%s`, issue.Feedback)
		}
		prompt += `

Respond with ONLY this JSON object on its own line:
{"passed": true, "summary": "brief reason"}`
		return prompt

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

func parseVerificationResult(text string) error {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return fmt.Errorf("%w: missing JSON result", ErrVerificationResult)
	}

	// Collect positions of every '{' and try each one (first to last) as a
	// potential JSON result. Iterating first-to-last means the earliest
	// valid verification JSON wins, matching the old line-by-line behavior.
	var positions []int
	for i, ch := range trimmed {
		if ch == '{' {
			positions = append(positions, i)
		}
	}

	for _, pos := range positions {
		candidate := trimmed[pos:]

		// First pass: decode into a generic map so we can check for the
		// "passed" key without accidentally accepting unrelated JSON like
		// {"key": "val"} from code snippets in the response.
		// Using json.Decoder instead of json.Unmarshal so trailing text
		// after the JSON object is silently ignored.
		dec := json.NewDecoder(strings.NewReader(candidate))
		var raw map[string]interface{}
		if err := dec.Decode(&raw); err != nil {
			continue
		}
		if _, ok := raw["passed"]; !ok {
			continue
		}

		// Second pass: decode into our typed struct.
		dec2 := json.NewDecoder(strings.NewReader(candidate))
		var result verificationResult
		if err := dec2.Decode(&result); err != nil {
			return fmt.Errorf("%w: invalid JSON result: %w", ErrVerificationResult, err)
		}

		if !result.Passed {
			if strings.TrimSpace(result.Summary) == "" {
				return fmt.Errorf("%w: verification reported passed=false", ErrVerificationFailed)
			}
			return fmt.Errorf("%w: %s", ErrVerificationFailed, result.Summary)
		}
		return nil
	}

	return fmt.Errorf("%w: missing JSON result", ErrVerificationResult)
}

func (r *Runner) captureDiff(ctx context.Context, dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	tracked := r.gitOutput(ctx, dir, "diff", "HEAD")
	untracked := r.captureUntrackedDiff(ctx, dir)
	return strings.TrimSpace(tracked + "\n" + untracked)
}

func (r *Runner) captureUntrackedDiff(ctx context.Context, dir string) string {
	out := r.gitOutput(ctx, dir, "ls-files", "--others", "--exclude-standard")
	if strings.TrimSpace(out) == "" || strings.HasPrefix(out, "error:") {
		return ""
	}
	var diff strings.Builder
	for _, rel := range strings.Split(out, "\n") {
		rel = strings.TrimSpace(rel)
		if rel == "" {
			continue
		}
		path := filepath.Join(dir, rel)
		cmd := exec.CommandContext(ctx, "git", "diff", "--no-index", "--", os.DevNull, path)
		cmd.Dir = dir
		data, _ := cmd.CombinedOutput()
		if len(data) > 0 {
			diff.Write(data)
			if !strings.HasSuffix(diff.String(), "\n") {
				diff.WriteByte('\n')
			}
		}
	}
	return diff.String()
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
