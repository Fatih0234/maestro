// Package pipeline owns the per-issue stage lifecycle.
//
// Currently this is a single-stage execute scaffold. Phase 2 will expand the
// Run method to orchestrate plan → execute → verify transitions.
package pipeline

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/agent"
	"github.com/fatihkarahan/contrabass-pi/internal/config"
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

// Result captures the outcome of a single pipeline run.
type Result struct {
	IssueID             string
	Attempt             int
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

// Run executes the full pipeline for one issue.
// It creates the workspace, starts the agent, monitors events, and returns
// when the agent completes or the context is cancelled.
func (r *Runner) Run(ctx context.Context, issue types.Issue, attempt int, emit func(types.OrchestratorEvent)) (*Result, error) {
	issueID := issue.ID
	branchName := r.Config.Workspace.BranchPrefix + util.SanitizeBranchName(issueID)
	workspacePath := r.Workspace.Path(issueID)
	prompt := r.buildPrompt(issue)

	// 1. Create workspace
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
		Timestamp: time.Now().UTC(),
		Payload:   map[string]string{"path": workspacePath},
	})

	// 2. Build prompt
	emit(types.OrchestratorEvent{
		Type:      EventPromptBuilt,
		IssueID:   issueID,
		Timestamp: time.Now().UTC(),
		Payload:   map[string]int{"length": len(prompt)},
	})

	// 3. Start agent
	proc, err := r.AgentRunner.Start(ctx, issue, workspacePath, prompt)
	if err != nil {
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

	// 4. Monitor agent
	var tokensIn, tokensOut int64
	var runErr error

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
					emit(types.OrchestratorEvent{
						Type:      EventAgentOutput,
						IssueID:   issueID,
						Timestamp: time.Now().UTC(),
						Payload:   map[string]string{"text": text},
					})
				}
			}
		case err := <-proc.Done:
			runErr = err
			break monitor
		}
	}

	// 5. Capture postflight
	postflightStatus, postflightWorktrees := r.captureGitState(r.Workspace.BaseDir())
	finalCommit := r.captureCommit(ctx, workspacePath)

	return &Result{
		IssueID:             issueID,
		Attempt:             attempt,
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
