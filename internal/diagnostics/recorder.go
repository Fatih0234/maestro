package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

type attemptContextKey struct{}

// WithAttemptRecorder attaches an attempt recorder to the context so
// lower-level components (like the agent runner) can capture run-specific
// logs without changing their public interfaces.
func WithAttemptRecorder(ctx context.Context, recorder *AttemptRecorder) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, attemptContextKey{}, recorder)
}

// AttemptFromContext returns the attempt recorder attached to the context, if any.
func AttemptFromContext(ctx context.Context) (*AttemptRecorder, bool) {
	if ctx == nil {
		return nil, false
	}
	recorder, ok := ctx.Value(attemptContextKey{}).(*AttemptRecorder)
	return recorder, ok && recorder != nil
}

// Recorder owns the run-log tree for a project.
//
// Layout:
//
//	<project>/runs/
//	  _orchestrator/events.jsonl
//	  CB-1/
//	    issue.json
//	    summary.json
//	    attempts/001/...
//	    attempts/002/...
//
// The root is derived from the board directory to keep logs beside the board
// and away from the remote project repository.
type Recorder struct {
	runsRoot         string
	globalDir        string
	globalEventsPath string
	globalEventsFile *os.File
	mu               sync.Mutex
	issues           map[string]*IssueRun
}

// IssueRun tracks the logs for a single issue.
type IssueRun struct {
	issueDir       string
	issuePath      string
	summaryPath    string
	attemptsDir    string
	issue          types.Issue
	summary        IssueSummary
	currentAttempt int
	attempts       map[int]*AttemptRecorder
	mu             sync.Mutex
}

// AttemptRecorder owns files for one attempt.
type AttemptRecorder struct {
	issueID       string
	attempt       int
	attemptDir    string
	metaPath      string
	promptPath    string
	eventsPath    string
	stdoutPath    string
	stderrPath    string
	stagesDir     string
	reviewDir     string
	preflightDir  string
	postflightDir string

	stdoutFile *os.File
	stderrFile *os.File
	eventsFile *os.File

	issueRun *IssueRun
	stages   map[types.Stage]*StageRecorder
	meta     AttemptMeta
	mu       sync.Mutex
}

// IssueSummary is the compact issue-level summary written to summary.json.
type IssueSummary struct {
	IssueID        string            `json:"issue_id"`
	Title          string            `json:"title"`
	Description    string            `json:"description,omitempty"`
	Labels         []string          `json:"labels,omitempty"`
	IssueState     string            `json:"issue_state,omitempty"`
	RunDir         string            `json:"run_dir"`
	IssueDir       string            `json:"issue_dir"`
	Attempts       int               `json:"attempts"`
	CurrentAttempt int               `json:"current_attempt,omitempty"`
	CurrentStage   types.Stage       `json:"current_stage"`
	ReviewState    types.ReviewState `json:"review_state"`
	Outcome        string            `json:"outcome,omitempty"`
	Branch         string            `json:"branch,omitempty"`
	WorkspacePath  string            `json:"workspace_path,omitempty"`
	StartCommit    string            `json:"start_commit,omitempty"`
	FinalCommit    string            `json:"final_commit,omitempty"`
	StartedAt      time.Time         `json:"started_at"`
	FinishedAt     *time.Time        `json:"finished_at,omitempty"`
	ReviewedAt     *time.Time        `json:"reviewed_at,omitempty"`
	ReviewedBy     string            `json:"reviewed_by,omitempty"`
	UpdatedAt      time.Time         `json:"updated_at"`
	LastError      string            `json:"last_error,omitempty"`
}

// AttemptMeta is the attempt-level metadata written to meta.json.
type AttemptMeta struct {
	IssueID       string      `json:"issue_id"`
	IssueTitle    string      `json:"issue_title,omitempty"`
	Attempt       int         `json:"attempt"`
	CurrentStage  types.Stage `json:"current_stage"`
	Outcome       string      `json:"outcome,omitempty"`
	Branch        string      `json:"branch,omitempty"`
	WorkspacePath string      `json:"workspace_path,omitempty"`
	StartedAt     time.Time   `json:"started_at"`
	EndedAt       *time.Time  `json:"ended_at,omitempty"`
	UpdatedAt     time.Time   `json:"updated_at"`
	PID           int         `json:"pid,omitempty"`
	SessionID     string      `json:"session_id,omitempty"`
	ServerURL     string      `json:"server_url,omitempty"`
	StartCommit   string      `json:"start_commit,omitempty"`
	FinalCommit   string      `json:"final_commit,omitempty"`
	Error         string      `json:"error,omitempty"`
	RetryAt       *time.Time  `json:"retry_at,omitempty"`
	PromptPath    string      `json:"prompt_path,omitempty"`
	EventsPath    string      `json:"events_path,omitempty"`
	StdoutPath    string      `json:"stdout_path,omitempty"`
	StderrPath    string      `json:"stderr_path,omitempty"`
}

// NewRecorder creates a recorder rooted at <board-dir-parent>/runs.
func NewRecorder(boardDir string) (*Recorder, error) {
	boardDir = filepath.Clean(boardDir)
	if boardDir == "." || boardDir == "" {
		return nil, errors.New("board directory is required")
	}

	absBoardDir, err := filepath.Abs(boardDir)
	if err != nil {
		return nil, fmt.Errorf("resolve board directory: %w", err)
	}

	runsRoot := filepath.Join(filepath.Dir(absBoardDir), "runs")
	globalDir := filepath.Join(runsRoot, "_orchestrator")
	globalEventsPath := filepath.Join(globalDir, "events.jsonl")

	if err := os.MkdirAll(runsRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create runs root: %w", err)
	}
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		return nil, fmt.Errorf("create global diagnostics dir: %w", err)
	}

	return &Recorder{
		runsRoot:         runsRoot,
		globalDir:        globalDir,
		globalEventsPath: globalEventsPath,
		issues:           make(map[string]*IssueRun),
	}, nil
}

// RunsRoot returns the root directory used for issue logs.
func (r *Recorder) RunsRoot() string {
	if r == nil {
		return ""
	}
	return r.runsRoot
}

// EnsureIssue creates or refreshes the issue-level run directory and snapshots.
func (r *Recorder) EnsureIssue(issue types.Issue) error {
	if r == nil {
		return nil
	}
	if issue.ID == "" {
		return errors.New("issue ID is required")
	}

	r.mu.Lock()
	ir, ok := r.issues[issue.ID]
	if !ok {
		ir = &IssueRun{
			issueDir:    filepath.Join(r.runsRoot, issue.ID),
			issuePath:   filepath.Join(r.runsRoot, issue.ID, "issue.json"),
			summaryPath: filepath.Join(r.runsRoot, issue.ID, "summary.json"),
			attemptsDir: filepath.Join(r.runsRoot, issue.ID, "attempts"),
			attempts:    make(map[int]*AttemptRecorder),
		}
		r.issues[issue.ID] = ir
	}
	r.mu.Unlock()

	return ir.refreshIssue(issue)
}

// BeginAttempt creates the attempt directory and initial files before the agent is launched.
func (r *Recorder) BeginAttempt(
	issue types.Issue,
	attempt int,
	branch,
	workspacePath,
	prompt,
	preflightGitStatus,
	preflightGitWorktreeList string,
) (*AttemptRecorder, error) {
	if r == nil {
		return nil, nil
	}
	if issue.ID == "" {
		return nil, errors.New("issue ID is required")
	}
	if attempt < 1 {
		return nil, fmt.Errorf("attempt must be >= 1, got %d", attempt)
	}

	if err := r.EnsureIssue(issue); err != nil {
		return nil, err
	}

	r.mu.Lock()
	ir := r.issues[issue.ID]
	r.mu.Unlock()
	if ir == nil {
		return nil, fmt.Errorf("issue run %q not initialized", issue.ID)
	}

	return ir.beginAttempt(issue, attempt, branch, workspacePath, prompt, preflightGitStatus, preflightGitWorktreeList)
}

// UpdateAttemptStartCommit records the workspace start commit after the worktree has been created.
func (r *Recorder) UpdateAttemptStartCommit(issueID string, attempt int, commit string) error {
	if r == nil || issueID == "" || attempt < 1 {
		return nil
	}
	ar, err := r.getAttempt(issueID, attempt)
	if err != nil {
		return err
	}
	if err := ar.updateMeta(func(meta *AttemptMeta) {
		meta.StartCommit = commit
	}); err != nil {
		return err
	}

	ir, err := r.getIssueRun(issueID)
	if err != nil {
		return err
	}
	ir.mu.Lock()
	ir.summary.StartCommit = commit
	ir.summary.UpdatedAt = time.Now().UTC()
	summary := ir.summary
	ir.mu.Unlock()

	return writeJSONAtomic(ir.summaryPath, summary)
}

// UpdateAttemptLaunchInfo records the PID/session/server URL once the agent starts.
func (r *Recorder) UpdateAttemptLaunchInfo(issueID string, attempt int, pid int, sessionID, serverURL string) error {
	if r == nil || issueID == "" || attempt < 1 {
		return nil
	}
	ar, err := r.getAttempt(issueID, attempt)
	if err != nil {
		return err
	}
	return ar.updateMeta(func(meta *AttemptMeta) {
		meta.PID = pid
		meta.SessionID = sessionID
		meta.ServerURL = serverURL
	})
}

// RecordEvent appends an orchestrator event to the global log and the current issue attempt log.
func (r *Recorder) RecordEvent(event types.OrchestratorEvent) error {
	if r == nil {
		return nil
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal orchestrator event: %w", err)
	}

	if err := r.appendGlobalLine(payload); err != nil {
		return err
	}

	if event.IssueID == "" {
		return nil
	}

	ar, err := r.currentAttemptRecorder(event.IssueID)
	if err != nil || ar == nil {
		return err
	}

	return ar.AppendEvent(event)
}

// FinalizeAttempt writes the final metadata and postflight files for an attempt.
func (r *Recorder) FinalizeAttempt(
	issueID string,
	attempt int,
	outcome string,
	finalCommit string,
	retryAt *time.Time,
	runErr error,
	postflightGitStatus,
	postflightGitWorktreeList string,
) error {
	if r == nil || issueID == "" || attempt < 1 {
		return nil
	}

	ir, err := r.getIssueRun(issueID)
	if err != nil {
		return err
	}

	ir.mu.Lock()
	ar := ir.attempts[attempt]
	ir.mu.Unlock()
	if ar == nil {
		return fmt.Errorf("attempt %03d for issue %q not found", attempt, issueID)
	}

	if finalCommit == "" {
		finalCommit = ar.meta.StartCommit
	}

	now := time.Now().UTC()
	ar.mu.Lock()
	ar.meta.Outcome = outcome
	ar.meta.FinalCommit = finalCommit
	if runErr != nil {
		ar.meta.Error = runErr.Error()
	} else {
		ar.meta.Error = ""
		ar.meta.CurrentStage = types.StageHumanReview
	}
	ar.meta.RetryAt = retryAt
	ar.meta.EndedAt = &now
	ar.meta.UpdatedAt = now
	meta := ar.meta
	ar.mu.Unlock()

	if err := writeTextFile(filepath.Join(ar.postflightDir, "git-status.txt"), postflightGitStatus); err != nil {
		return err
	}
	if err := writeTextFile(filepath.Join(ar.postflightDir, "git-worktree-list.txt"), postflightGitWorktreeList); err != nil {
		return err
	}

	if err := writeJSONAtomic(ar.metaPath, meta); err != nil {
		return err
	}

	ir.mu.Lock()
	ir.summary.Attempts = max(ir.summary.Attempts, attempt)
	ir.summary.CurrentAttempt = attempt
	if ir.summary.StartCommit == "" {
		ir.summary.StartCommit = meta.StartCommit
	}
	ir.summary.Outcome = outcome
	ir.summary.FinalCommit = finalCommit
	ir.summary.WorkspacePath = meta.WorkspacePath
	ir.summary.Branch = meta.Branch
	ir.summary.UpdatedAt = now
	ir.summary.LastError = meta.Error
	if runErr == nil {
		finishedAt := now
		ir.summary.FinishedAt = &finishedAt
		ir.summary.CurrentStage = types.StageHumanReview
		ir.summary.ReviewState = types.ReviewStateReady
		ir.summary.ReviewedAt = nil
		ir.summary.ReviewedBy = ""
		ir.summary.IssueState = types.StateInReview.BoardState()
	} else {
		ir.summary.FinishedAt = nil
		ir.summary.IssueState = types.StateRetryQueued.BoardState()
	}
	summary := ir.summary
	ir.mu.Unlock()

	return writeJSONAtomic(ir.summaryPath, summary)
}

// Close closes all open files managed by the recorder.
func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	issues := make([]*IssueRun, 0, len(r.issues))
	for _, ir := range r.issues {
		issues = append(issues, ir)
	}
	if r.globalEventsFile != nil {
		_ = r.globalEventsFile.Close()
		r.globalEventsFile = nil
	}
	r.mu.Unlock()

	var errs []error
	for _, ir := range issues {
		if err := ir.close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (r *Recorder) appendGlobalLine(line []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := os.MkdirAll(r.globalDir, 0o755); err != nil {
		return fmt.Errorf("create global diagnostics dir: %w", err)
	}
	if r.globalEventsFile == nil {
		f, err := os.OpenFile(r.globalEventsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("open global events log: %w", err)
		}
		r.globalEventsFile = f
	}
	if _, err := r.globalEventsFile.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("append global event: %w", err)
	}
	return nil
}

func (r *Recorder) currentAttemptRecorder(issueID string) (*AttemptRecorder, error) {
	r.mu.Lock()
	ir := r.issues[issueID]
	r.mu.Unlock()
	if ir == nil {
		return nil, nil
	}

	ir.mu.Lock()
	defer ir.mu.Unlock()
	if ir.currentAttempt == 0 {
		return nil, nil
	}
	ar := ir.attempts[ir.currentAttempt]
	return ar, nil
}

func (r *Recorder) getAttempt(issueID string, attempt int) (*AttemptRecorder, error) {
	r.mu.Lock()
	ir := r.issues[issueID]
	r.mu.Unlock()
	if ir == nil {
		return nil, fmt.Errorf("issue run %q not found", issueID)
	}

	ir.mu.Lock()
	defer ir.mu.Unlock()
	ar := ir.attempts[attempt]
	if ar == nil {
		return nil, fmt.Errorf("attempt %03d for issue %q not found", attempt, issueID)
	}
	return ar, nil
}

func (ir *IssueRun) refreshIssue(issue types.Issue) error {
	if issue.ID == "" {
		return errors.New("issue ID is required")
	}

	if err := os.MkdirAll(ir.attemptsDir, 0o755); err != nil {
		return fmt.Errorf("create issue attempts dir: %w", err)
	}

	now := time.Now().UTC()
	ir.mu.Lock()
	if ir.summary.IssueID == "" {
		var stored IssueSummary
		if err := readJSONFile(ir.summaryPath, &stored); err != nil && !errors.Is(err, os.ErrNotExist) {
			ir.mu.Unlock()
			return err
		}
		if stored.IssueID != "" {
			ir.summary = stored
		}
	}
	if ir.summary.StartedAt.IsZero() {
		ir.summary.StartedAt = now
	}
	ir.issue = issue
	ir.summary.IssueID = issue.ID
	ir.summary.Title = issue.Title
	ir.summary.Description = issue.Description
	ir.summary.Labels = cloneStrings(issue.Labels)
	ir.summary.IssueState = issue.State.BoardState()
	ir.summary.RunDir = ir.issueDir
	ir.summary.IssueDir = ir.issueDir
	if ir.summary.ReviewState == "" {
		ir.summary.ReviewState = types.ReviewStatePending
	}
	if ir.summary.CurrentStage == "" && ir.summary.CurrentAttempt == 0 && ir.summary.Attempts == 0 && ir.summary.Outcome == "" {
		ir.summary.CurrentStage = types.StagePlan
		ir.summary.Outcome = issue.State.BoardState()
	}
	if ir.summary.Outcome == "" {
		ir.summary.Outcome = issue.State.BoardState()
	}
	ir.summary.UpdatedAt = now
	summary := ir.summary
	ir.mu.Unlock()

	if err := writeJSONAtomic(ir.issuePath, issue); err != nil {
		return err
	}
	if err := writeJSONAtomic(ir.summaryPath, summary); err != nil {
		return err
	}
	return nil
}

func (ir *IssueRun) beginAttempt(
	issue types.Issue,
	attempt int,
	branch,
	workspacePath,
	prompt,
	preflightGitStatus,
	preflightGitWorktreeList string,
) (*AttemptRecorder, error) {
	if err := os.MkdirAll(ir.attemptsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create attempts dir: %w", err)
	}

	attemptDir := filepath.Join(ir.attemptsDir, fmt.Sprintf("%03d", attempt))
	preflightDir := filepath.Join(attemptDir, "preflight")
	postflightDir := filepath.Join(attemptDir, "postflight")
	stagesDir := filepath.Join(attemptDir, "stages")
	reviewDir := filepath.Join(attemptDir, "review")
	if err := os.MkdirAll(preflightDir, 0o755); err != nil {
		return nil, fmt.Errorf("create preflight dir: %w", err)
	}
	if err := os.MkdirAll(postflightDir, 0o755); err != nil {
		return nil, fmt.Errorf("create postflight dir: %w", err)
	}
	if err := os.MkdirAll(stagesDir, 0o755); err != nil {
		return nil, fmt.Errorf("create stages dir: %w", err)
	}
	if err := os.MkdirAll(reviewDir, 0o755); err != nil {
		return nil, fmt.Errorf("create review dir: %w", err)
	}

	stdoutPath := filepath.Join(attemptDir, "stdout.log")
	stderrPath := filepath.Join(attemptDir, "stderr.log")
	eventsPath := filepath.Join(attemptDir, "events.jsonl")
	metaPath := filepath.Join(attemptDir, "meta.json")
	promptPath := filepath.Join(attemptDir, "prompt.md")

	stdoutFile, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open stdout log: %w", err)
	}
	stderrFile, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		_ = stdoutFile.Close()
		return nil, fmt.Errorf("open stderr log: %w", err)
	}
	eventsFile, err := os.OpenFile(eventsPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		_ = stdoutFile.Close()
		_ = stderrFile.Close()
		return nil, fmt.Errorf("open events log: %w", err)
	}

	now := time.Now().UTC()
	meta := AttemptMeta{
		IssueID:       issue.ID,
		IssueTitle:    issue.Title,
		Attempt:       attempt,
		CurrentStage:  types.StagePlan,
		Outcome:       "running",
		Branch:        branch,
		WorkspacePath: workspacePath,
		StartedAt:     now,
		UpdatedAt:     now,
		PromptPath:    promptPath,
		EventsPath:    eventsPath,
		StdoutPath:    stdoutPath,
		StderrPath:    stderrPath,
	}

	ar := &AttemptRecorder{
		issueID:       issue.ID,
		attempt:       attempt,
		attemptDir:    attemptDir,
		metaPath:      metaPath,
		promptPath:    promptPath,
		eventsPath:    eventsPath,
		stdoutPath:    stdoutPath,
		stderrPath:    stderrPath,
		stagesDir:     stagesDir,
		reviewDir:     reviewDir,
		preflightDir:  preflightDir,
		postflightDir: postflightDir,
		stdoutFile:    stdoutFile,
		stderrFile:    stderrFile,
		eventsFile:    eventsFile,
		issueRun:      ir,
		stages:        make(map[types.Stage]*StageRecorder),
		meta:          meta,
	}

	if err := writeTextFile(promptPath, prompt); err != nil {
		_ = ar.close()
		return nil, err
	}
	if err := writeTextFile(filepath.Join(preflightDir, "git-status.txt"), preflightGitStatus); err != nil {
		_ = ar.close()
		return nil, err
	}
	if err := writeTextFile(filepath.Join(preflightDir, "git-worktree-list.txt"), preflightGitWorktreeList); err != nil {
		_ = ar.close()
		return nil, err
	}
	if err := writeJSONAtomic(metaPath, meta); err != nil {
		_ = ar.close()
		return nil, err
	}

	ir.mu.Lock()
	ir.attempts[attempt] = ar
	ir.currentAttempt = attempt
	ir.summary.Attempts = max(ir.summary.Attempts, attempt)
	ir.summary.CurrentAttempt = attempt
	ir.summary.CurrentStage = types.StagePlan
	ir.summary.ReviewState = types.ReviewStatePending
	ir.summary.Outcome = "running"
	ir.summary.Branch = branch
	ir.summary.WorkspacePath = workspacePath
	ir.summary.StartCommit = ""
	ir.summary.FinalCommit = ""
	ir.summary.FinishedAt = nil
	ir.summary.ReviewedAt = nil
	ir.summary.ReviewedBy = ""
	ir.summary.LastError = ""
	ir.summary.UpdatedAt = now
	ir.mu.Unlock()

	if err := ir.writeSummary(); err != nil {
		_ = ar.close()
		return nil, err
	}

	return ar, nil
}

func (ir *IssueRun) writeSummary() error {
	ir.mu.Lock()
	summary := ir.summary
	ir.mu.Unlock()
	return writeJSONAtomic(ir.summaryPath, summary)
}

func (ar *AttemptRecorder) StdoutWriter() io.Writer {
	return ar.stdoutFile
}

func (ar *AttemptRecorder) StderrWriter() io.Writer {
	return ar.stderrFile
}

func (ar *AttemptRecorder) AppendStdoutLine(line string) error {
	if ar == nil || ar.stdoutFile == nil {
		return nil
	}
	ar.mu.Lock()
	defer ar.mu.Unlock()
	_, err := fmt.Fprintln(ar.stdoutFile, line)
	return err
}

func (ar *AttemptRecorder) AppendEvent(event types.OrchestratorEvent) error {
	if ar == nil || ar.eventsFile == nil {
		return nil
	}
	return ar.appendJSONLine(ar.eventsFile, event)
}

func (ar *AttemptRecorder) SetStartCommit(commit string) error {
	if ar == nil {
		return nil
	}
	if err := ar.updateMeta(func(meta *AttemptMeta) {
		meta.StartCommit = commit
	}); err != nil {
		return err
	}
	if ar.issueRun == nil {
		return nil
	}
	ar.issueRun.mu.Lock()
	ar.issueRun.summary.StartCommit = commit
	ar.issueRun.summary.UpdatedAt = time.Now().UTC()
	summary := ar.issueRun.summary
	ar.issueRun.mu.Unlock()
	return writeJSONAtomic(ar.issueRun.summaryPath, summary)
}

func (ar *AttemptRecorder) SetLaunchInfo(pid int, sessionID, serverURL string) error {
	return ar.updateMeta(func(meta *AttemptMeta) {
		meta.PID = pid
		meta.SessionID = sessionID
		meta.ServerURL = serverURL
	})
}

func (ar *AttemptRecorder) updateMeta(mutator func(*AttemptMeta)) error {
	if ar == nil {
		return nil
	}
	ar.mu.Lock()
	mutator(&ar.meta)
	ar.meta.UpdatedAt = time.Now().UTC()
	meta := ar.meta
	ar.mu.Unlock()
	return writeJSONAtomic(ar.metaPath, meta)
}

func (ar *AttemptRecorder) appendJSONLine(file *os.File, value any) error {
	if ar == nil || file == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal json line: %w", err)
	}
	ar.mu.Lock()
	defer ar.mu.Unlock()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write json line: %w", err)
	}
	return nil
}

func (ar *AttemptRecorder) close() error {
	if ar == nil {
		return nil
	}
	var errs []error
	ar.mu.Lock()
	stages := make([]*StageRecorder, 0, len(ar.stages))
	for _, stage := range ar.stages {
		stages = append(stages, stage)
	}
	ar.mu.Unlock()

	for _, stage := range stages {
		if stage == nil {
			continue
		}
		if err := stage.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if ar.stdoutFile != nil {
		if err := ar.stdoutFile.Close(); err != nil {
			errs = append(errs, err)
		}
		ar.stdoutFile = nil
	}
	if ar.stderrFile != nil {
		if err := ar.stderrFile.Close(); err != nil {
			errs = append(errs, err)
		}
		ar.stderrFile = nil
	}
	if ar.eventsFile != nil {
		if err := ar.eventsFile.Close(); err != nil {
			errs = append(errs, err)
		}
		ar.eventsFile = nil
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (r *Recorder) updateSummary(issueID string, attempt int, mutator func(*IssueSummary)) error {
	r.mu.Lock()
	ir := r.issues[issueID]
	r.mu.Unlock()
	if ir == nil {
		return fmt.Errorf("issue run %q not found", issueID)
	}

	ir.mu.Lock()
	mutator(&ir.summary)
	ir.summary.CurrentAttempt = attempt
	ir.summary.UpdatedAt = time.Now().UTC()
	summary := ir.summary
	ir.mu.Unlock()

	return writeJSONAtomic(ir.summaryPath, summary)
}

func (r *Recorder) getIssueRun(issueID string) (*IssueRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ir := r.issues[issueID]
	if ir == nil {
		return nil, fmt.Errorf("issue run %q not found", issueID)
	}
	return ir, nil
}

func (r *Recorder) currentIssueAttempt(issueID string) (*AttemptRecorder, error) {
	ir, err := r.getIssueRun(issueID)
	if err != nil {
		return nil, err
	}
	ir.mu.Lock()
	defer ir.mu.Unlock()
	if ir.currentAttempt == 0 {
		return nil, nil
	}
	return ir.attempts[ir.currentAttempt], nil
}

func (r *Recorder) CloseIssue(issueID string) error {
	if r == nil || issueID == "" {
		return nil
	}
	ir, err := r.getIssueRun(issueID)
	if err != nil {
		return err
	}
	return ir.close()
}

// LoadAttemptRecorder creates an AttemptRecorder for an existing attempt directory.
// It is used by board commands to write review decisions on completed attempts.
func (r *Recorder) LoadAttemptRecorder(issueID string, attempt int) (*AttemptRecorder, error) {
	if r == nil {
		return nil, errors.New("recorder is nil")
	}
	if strings.TrimSpace(issueID) == "" {
		return nil, errors.New("issue ID is required")
	}
	if attempt < 1 {
		return nil, fmt.Errorf("attempt must be >= 1, got %d", attempt)
	}

	// Ensure IssueRun exists
	if err := r.EnsureIssue(types.Issue{ID: issueID}); err != nil {
		return nil, err
	}

	r.mu.Lock()
	ir := r.issues[issueID]
	r.mu.Unlock()
	if ir == nil {
		return nil, fmt.Errorf("issue run %q not initialized", issueID)
	}

	ir.mu.Lock()
	if ar, ok := ir.attempts[attempt]; ok {
		ir.mu.Unlock()
		return ar, nil
	}
	ir.mu.Unlock()

	attemptDir := filepath.Join(ir.attemptsDir, fmt.Sprintf("%03d", attempt))
	metaPath := filepath.Join(attemptDir, "meta.json")

	var meta AttemptMeta
	if err := readJSONFile(metaPath, &meta); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	ar := &AttemptRecorder{
		issueID:       issueID,
		attempt:       attempt,
		attemptDir:    attemptDir,
		metaPath:      metaPath,
		promptPath:    filepath.Join(attemptDir, "prompt.md"),
		eventsPath:    filepath.Join(attemptDir, "events.jsonl"),
		stdoutPath:    filepath.Join(attemptDir, "stdout.log"),
		stderrPath:    filepath.Join(attemptDir, "stderr.log"),
		stagesDir:     filepath.Join(attemptDir, "stages"),
		reviewDir:     filepath.Join(attemptDir, "review"),
		preflightDir:  filepath.Join(attemptDir, "preflight"),
		postflightDir: filepath.Join(attemptDir, "postflight"),
		issueRun:      ir,
		stages:        make(map[types.Stage]*StageRecorder),
		meta:          meta,
	}

	ir.mu.Lock()
	ir.attempts[attempt] = ar
	if ir.currentAttempt < attempt {
		ir.currentAttempt = attempt
	}
	ir.mu.Unlock()

	return ar, nil
}

func (ir *IssueRun) close() error {
	ir.mu.Lock()
	attempts := make([]*AttemptRecorder, 0, len(ir.attempts))
	for _, ar := range ir.attempts {
		attempts = append(attempts, ar)
	}
	ir.mu.Unlock()

	var errs []error
	for _, ar := range attempts {
		if err := ar.close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent for %s: %w", path, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write temp %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename temp %s: %w", path, err)
	}
	return nil
}

func writeTextFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent for %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
