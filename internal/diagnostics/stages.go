package diagnostics

import (
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

// readJSONFile reads and unmarshals a JSON file from disk.
func readJSONFile(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// StageRecorder owns the files for one pipeline stage inside an attempt.
type StageRecorder struct {
	attempt      *AttemptRecorder
	manifest     types.StageManifest
	stageDir     string
	manifestPath string
	promptPath   string
	responsePath string
	resultPath   string
	diffPath     string
	eventsPath   string
	stdoutPath   string
	stderrPath   string

	stdoutFile *os.File
	stderrFile *os.File
	eventsFile *os.File

	closed bool
	mu     sync.Mutex
}

// LoadIssueSummary reads summary.json for an issue from disk.
func (r *Recorder) LoadIssueSummary(issueID string) (IssueSummary, error) {
	if r == nil {
		return IssueSummary{}, errors.New("recorder is nil")
	}
	if strings.TrimSpace(issueID) == "" {
		return IssueSummary{}, errors.New("issue ID is required")
	}
	var summary IssueSummary
	if err := readJSONFile(filepath.Join(r.runsRoot, issueID, "summary.json"), &summary); err != nil {
		return IssueSummary{}, err
	}
	return summary, nil
}

// LoadAttemptMeta reads attempts/<NNN>/meta.json for an issue from disk.
func (r *Recorder) LoadAttemptMeta(issueID string, attempt int) (AttemptMeta, error) {
	if r == nil {
		return AttemptMeta{}, errors.New("recorder is nil")
	}
	if strings.TrimSpace(issueID) == "" {
		return AttemptMeta{}, errors.New("issue ID is required")
	}
	if attempt < 1 {
		return AttemptMeta{}, fmt.Errorf("attempt must be >= 1, got %d", attempt)
	}
	var meta AttemptMeta
	if err := readJSONFile(filepath.Join(r.runsRoot, issueID, "attempts", fmt.Sprintf("%03d", attempt), "meta.json"), &meta); err != nil {
		return AttemptMeta{}, err
	}
	return meta, nil
}

// LoadStageManifest reads attempts/<NNN>/stages/<stage>/manifest.json from disk.
func (r *Recorder) LoadStageManifest(issueID string, attempt int, stage types.Stage) (types.StageManifest, error) {
	if r == nil {
		return types.StageManifest{}, errors.New("recorder is nil")
	}
	if strings.TrimSpace(issueID) == "" {
		return types.StageManifest{}, errors.New("issue ID is required")
	}
	if attempt < 1 {
		return types.StageManifest{}, fmt.Errorf("attempt must be >= 1, got %d", attempt)
	}
	if !stage.Valid() {
		return types.StageManifest{}, fmt.Errorf("invalid stage %q", stage)
	}
	var manifest types.StageManifest
	if err := readJSONFile(filepath.Join(r.runsRoot, issueID, "attempts", fmt.Sprintf("%03d", attempt), "stages", stage.String(), "manifest.json"), &manifest); err != nil {
		return types.StageManifest{}, err
	}
	return manifest, nil
}

// LoadStageResult reads attempts/<NNN>/stages/<stage>/result.json from disk.
func (r *Recorder) LoadStageResult(issueID string, attempt int, stage types.Stage) (types.StageResult, error) {
	if r == nil {
		return types.StageResult{}, errors.New("recorder is nil")
	}
	if strings.TrimSpace(issueID) == "" {
		return types.StageResult{}, errors.New("issue ID is required")
	}
	if attempt < 1 {
		return types.StageResult{}, fmt.Errorf("attempt must be >= 1, got %d", attempt)
	}
	if !stage.Valid() {
		return types.StageResult{}, fmt.Errorf("invalid stage %q", stage)
	}
	var result types.StageResult
	if err := readJSONFile(filepath.Join(r.runsRoot, issueID, "attempts", fmt.Sprintf("%03d", attempt), "stages", stage.String(), "result.json"), &result); err != nil {
		return types.StageResult{}, err
	}
	return result, nil
}

// LoadReviewDecision reads attempts/<NNN>/review/decision.json from disk.
func (r *Recorder) LoadReviewDecision(issueID string, attempt int) (types.ReviewDecision, error) {
	if r == nil {
		return types.ReviewDecision{}, errors.New("recorder is nil")
	}
	if strings.TrimSpace(issueID) == "" {
		return types.ReviewDecision{}, errors.New("issue ID is required")
	}
	if attempt < 1 {
		return types.ReviewDecision{}, fmt.Errorf("attempt must be >= 1, got %d", attempt)
	}
	var decision types.ReviewDecision
	if err := readJSONFile(filepath.Join(r.runsRoot, issueID, "attempts", fmt.Sprintf("%03d", attempt), "review", "decision.json"), &decision); err != nil {
		return types.ReviewDecision{}, err
	}
	return decision, nil
}

// updateSnapshot mutates the attempt metadata and issue summary together and
// persists both files atomically with respect to the in-memory state.
func (ar *AttemptRecorder) updateSnapshot(mutator func(meta *AttemptMeta, summary *IssueSummary, now time.Time)) error {
	if ar == nil {
		return nil
	}
	if ar.issueRun == nil {
		return errors.New("attempt recorder is not attached to an issue run")
	}

	now := time.Now().UTC()
	ar.mu.Lock()
	ar.issueRun.mu.Lock()
	mutator(&ar.meta, &ar.issueRun.summary, now)
	ar.meta.UpdatedAt = now
	ar.issueRun.summary.UpdatedAt = now
	meta := ar.meta
	summary := ar.issueRun.summary
	ar.issueRun.mu.Unlock()
	ar.mu.Unlock()

	if err := writeJSONAtomic(ar.metaPath, meta); err != nil {
		return err
	}
	if err := writeJSONAtomic(ar.issueRun.summaryPath, summary); err != nil {
		return err
	}
	return nil
}

func (ar *AttemptRecorder) stageRecorder(stage types.Stage) *StageRecorder {
	if ar == nil {
		return nil
	}
	ar.mu.Lock()
	defer ar.mu.Unlock()
	if ar.stages == nil {
		ar.stages = make(map[types.Stage]*StageRecorder)
	}
	return ar.stages[stage]
}

func (ar *AttemptRecorder) registerStageRecorder(recorder *StageRecorder) {
	if ar == nil || recorder == nil {
		return
	}
	ar.mu.Lock()
	if ar.stages == nil {
		ar.stages = make(map[types.Stage]*StageRecorder)
	}
	ar.stages[recorder.manifest.Stage] = recorder
	ar.mu.Unlock()
}

func (ar *AttemptRecorder) unregisterStageRecorder(stage types.Stage, recorder *StageRecorder) {
	if ar == nil {
		return
	}
	ar.mu.Lock()
	if current, ok := ar.stages[stage]; ok && current == recorder {
		delete(ar.stages, stage)
	}
	ar.mu.Unlock()
}

// BeginStage creates the stage tree for one pipeline stage and returns a
// recorder for the stage artifacts.
func (ar *AttemptRecorder) BeginStage(manifest types.StageManifest, prompt string) (*StageRecorder, error) {
	if ar == nil {
		return nil, nil
	}
	if ar.issueRun == nil {
		return nil, errors.New("attempt recorder is not attached to an issue run")
	}
	if !manifest.Stage.Valid() {
		return nil, fmt.Errorf("invalid stage %q", manifest.Stage)
	}
	if manifest.Attempt == 0 {
		manifest.Attempt = ar.attempt
	}
	if manifest.Attempt != ar.attempt {
		return nil, fmt.Errorf("stage attempt %d does not match attempt recorder %d", manifest.Attempt, ar.attempt)
	}
	if manifest.Status == "" {
		manifest.Status = types.StageStateRunning
	}
	if !manifest.Status.Valid() {
		return nil, fmt.Errorf("invalid stage status %q", manifest.Status)
	}
	if manifest.WorkspacePath == "" {
		manifest.WorkspacePath = ar.meta.WorkspacePath
	}
	if manifest.SessionID == "" {
		manifest.SessionID = ar.meta.SessionID
	}
	if manifest.StartedAt.IsZero() {
		manifest.StartedAt = time.Now().UTC()
	}

	stageDir := filepath.Join(ar.stagesDir, manifest.Stage.String())
	manifestPath := filepath.Join(stageDir, "manifest.json")
	promptPath := manifest.PromptPath
	if promptPath == "" {
		promptPath = filepath.Join(stageDir, "prompt.md")
	}
	responsePath := manifest.ResponsePath
	if responsePath == "" {
		responsePath = filepath.Join(stageDir, "response.md")
	}
	resultPath := manifest.ResultPath
	if resultPath == "" {
		resultPath = filepath.Join(stageDir, "result.json")
	}
	eventsPath := manifest.EventsPath
	if eventsPath == "" {
		eventsPath = filepath.Join(stageDir, "events.jsonl")
	}
	stdoutPath := manifest.StdoutPath
	if stdoutPath == "" {
		stdoutPath = filepath.Join(stageDir, "stdout.log")
	}
	stderrPath := manifest.StderrPath
	if stderrPath == "" {
		stderrPath = filepath.Join(stageDir, "stderr.log")
	}
	diffPath := manifest.DiffPath
	if diffPath == "" && manifest.Stage == types.StageExecute {
		diffPath = filepath.Join(stageDir, "diff.patch")
	}
	manifest.PromptPath = promptPath
	manifest.ResponsePath = responsePath
	manifest.ResultPath = resultPath
	manifest.EventsPath = eventsPath
	manifest.StdoutPath = stdoutPath
	manifest.StderrPath = stderrPath
	manifest.DiffPath = diffPath

	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return nil, fmt.Errorf("create stage dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
		return nil, fmt.Errorf("create stage prompt dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(responsePath), 0o755); err != nil {
		return nil, fmt.Errorf("create stage response dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(resultPath), 0o755); err != nil {
		return nil, fmt.Errorf("create stage result dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(eventsPath), 0o755); err != nil {
		return nil, fmt.Errorf("create stage events dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(stdoutPath), 0o755); err != nil {
		return nil, fmt.Errorf("create stage stdout dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(stderrPath), 0o755); err != nil {
		return nil, fmt.Errorf("create stage stderr dir: %w", err)
	}
	if diffPath != "" {
		if err := os.MkdirAll(filepath.Dir(diffPath), 0o755); err != nil {
			return nil, fmt.Errorf("create stage diff dir: %w", err)
		}
	}

	stdoutFile, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open stage stdout log: %w", err)
	}
	stderrFile, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		_ = stdoutFile.Close()
		return nil, fmt.Errorf("open stage stderr log: %w", err)
	}
	eventsFile, err := os.OpenFile(eventsPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		_ = stdoutFile.Close()
		_ = stderrFile.Close()
		return nil, fmt.Errorf("open stage events log: %w", err)
	}

	recorder := &StageRecorder{
		attempt:      ar,
		manifest:     manifest,
		stageDir:     stageDir,
		manifestPath: manifestPath,
		promptPath:   promptPath,
		responsePath: responsePath,
		resultPath:   resultPath,
		diffPath:     diffPath,
		eventsPath:   eventsPath,
		stdoutPath:   stdoutPath,
		stderrPath:   stderrPath,
		stdoutFile:   stdoutFile,
		stderrFile:   stderrFile,
		eventsFile:   eventsFile,
	}

	if err := writeTextFile(promptPath, prompt); err != nil {
		_ = recorder.Close()
		return nil, err
	}
	if err := writeJSONAtomic(manifestPath, manifest); err != nil {
		_ = recorder.Close()
		return nil, err
	}

	ar.registerStageRecorder(recorder)
	if err := ar.updateSnapshot(func(meta *AttemptMeta, summary *IssueSummary, now time.Time) {
		meta.CurrentStage = manifest.Stage
		summary.CurrentStage = manifest.Stage
		if summary.ReviewState == "" {
			summary.ReviewState = types.ReviewStatePending
		}
		meta.Outcome = "running"
		summary.Outcome = "running"
		summary.LastError = ""
		meta.Error = ""
	}); err != nil {
		_ = recorder.Close()
		return nil, err
	}

	return recorder, nil
}

// RecordReviewHandoff writes the durable handoff package for the human review gate.
func (ar *AttemptRecorder) RecordReviewHandoff(body, notes string) error {
	if ar == nil {
		return nil
	}
	if ar.issueRun == nil {
		return errors.New("attempt recorder is not attached to an issue run")
	}
	if err := os.MkdirAll(ar.reviewDir, 0o755); err != nil {
		return fmt.Errorf("create review dir: %w", err)
	}
	if err := writeTextFile(filepath.Join(ar.reviewDir, "handoff.md"), body); err != nil {
		return err
	}
	notesPath := filepath.Join(ar.reviewDir, "notes.md")
	if strings.TrimSpace(notes) != "" {
		if err := writeTextFile(notesPath, notes); err != nil {
			return err
		}
	} else if _, err := os.Stat(notesPath); errors.Is(err, os.ErrNotExist) {
		if err := writeTextFile(notesPath, ""); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	return ar.updateSnapshot(func(meta *AttemptMeta, summary *IssueSummary, now time.Time) {
		meta.CurrentStage = types.StageHumanReview
		meta.Outcome = "awaiting_review"
		if meta.EndedAt == nil {
			ended := now
			meta.EndedAt = &ended
		}
		summary.CurrentStage = types.StageHumanReview
		summary.ReviewState = types.ReviewStateReady
		summary.Outcome = "awaiting_review"
		summary.IssueState = types.StateInReview.BoardState()
		if summary.FinishedAt == nil {
			finished := now
			summary.FinishedAt = &finished
		}
		summary.ReviewedAt = nil
		summary.ReviewedBy = ""
		summary.LastError = ""
		meta.Error = ""
	})
}

// RecordReviewDecision writes review/decision.json and updates the summary state.
func (ar *AttemptRecorder) RecordReviewDecision(decision types.ReviewDecision) error {
	if ar == nil {
		return nil
	}
	if ar.issueRun == nil {
		return errors.New("attempt recorder is not attached to an issue run")
	}
	if !decision.Decision.Valid() {
		return fmt.Errorf("invalid review decision %q", decision.Decision)
	}

	if decision.ReviewedAt.IsZero() {
		decision.ReviewedAt = time.Now().UTC()
	}
	if strings.TrimSpace(decision.ReviewedBy) == "" {
		decision.ReviewedBy = defaultReviewer()
	}
	if decision.FollowUpState == "" {
		switch decision.Decision {
		case types.ReviewDecisionApproved:
			decision.FollowUpState = types.ReviewFollowUpDone
		case types.ReviewDecisionRejected:
			decision.FollowUpState = types.ReviewFollowUpRetryQueued
		case types.ReviewDecisionNeedsChanges:
			decision.FollowUpState = types.ReviewFollowUpTodo
		}
	}
	if !decision.FollowUpState.Valid() {
		return fmt.Errorf("invalid review follow-up state %q", decision.FollowUpState)
	}

	if err := os.MkdirAll(ar.reviewDir, 0o755); err != nil {
		return fmt.Errorf("create review dir: %w", err)
	}
	handoffPath := filepath.Join(ar.reviewDir, "handoff.md")
	if _, err := os.Stat(handoffPath); errors.Is(err, os.ErrNotExist) {
		if err := writeTextFile(handoffPath, ""); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	notesPath := filepath.Join(ar.reviewDir, "notes.md")
	if strings.TrimSpace(decision.Notes) != "" {
		if err := writeTextFile(notesPath, decision.Notes); err != nil {
			return err
		}
	} else if _, err := os.Stat(notesPath); errors.Is(err, os.ErrNotExist) {
		if err := writeTextFile(notesPath, ""); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := writeJSONAtomic(filepath.Join(ar.reviewDir, "decision.json"), decision); err != nil {
		return err
	}

	return ar.updateSnapshot(func(meta *AttemptMeta, summary *IssueSummary, now time.Time) {
		meta.CurrentStage = types.StageHumanReview
		meta.Outcome = decision.FollowUpState.String()
		if meta.EndedAt == nil {
			ended := decision.ReviewedAt
			meta.EndedAt = &ended
		}
		meta.Error = ""
		summary.CurrentStage = types.StageHumanReview
		summary.ReviewState = reviewStateForDecision(decision.Decision)
		summary.ReviewedAt = &decision.ReviewedAt
		summary.ReviewedBy = decision.ReviewedBy
		summary.Outcome = decision.FollowUpState.String()
		summary.IssueState = decision.FollowUpState.String()
		if summary.FinishedAt == nil {
			finished := decision.ReviewedAt
			summary.FinishedAt = &finished
		}
		summary.LastError = ""
	})
}

// Close releases the stage recorder's file handles.
func (sr *StageRecorder) Close() error {
	if sr == nil {
		return nil
	}

	sr.mu.Lock()
	if sr.closed {
		sr.mu.Unlock()
		return nil
	}
	sr.closed = true
	stdoutFile := sr.stdoutFile
	stderrFile := sr.stderrFile
	eventsFile := sr.eventsFile
	sr.stdoutFile = nil
	sr.stderrFile = nil
	sr.eventsFile = nil
	attempt := sr.attempt
	stage := sr.manifest.Stage
	sr.mu.Unlock()

	if attempt != nil {
		attempt.unregisterStageRecorder(stage, sr)
	}

	var errs []error
	if stdoutFile != nil {
		if err := stdoutFile.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if stderrFile != nil {
		if err := stderrFile.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if eventsFile != nil {
		if err := eventsFile.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// StdoutWriter exposes the stage stdout log writer.
func (sr *StageRecorder) StdoutWriter() io.Writer {
	if sr == nil {
		return nil
	}
	return sr.stdoutFile
}

// StderrWriter exposes the stage stderr log writer.
func (sr *StageRecorder) StderrWriter() io.Writer {
	if sr == nil {
		return nil
	}
	return sr.stderrFile
}

// AppendStdoutLine appends one line to the stage stdout log.
func (sr *StageRecorder) AppendStdoutLine(line string) error {
	if sr == nil || sr.stdoutFile == nil {
		return nil
	}
	sr.mu.Lock()
	defer sr.mu.Unlock()
	_, err := fmt.Fprintln(sr.stdoutFile, line)
	return err
}

// AppendStderrLine appends one line to the stage stderr log.
func (sr *StageRecorder) AppendStderrLine(line string) error {
	if sr == nil || sr.stderrFile == nil {
		return nil
	}
	sr.mu.Lock()
	defer sr.mu.Unlock()
	_, err := fmt.Fprintln(sr.stderrFile, line)
	return err
}

// AppendEvent appends an event to the stage event log.
func (sr *StageRecorder) AppendEvent(event types.OrchestratorEvent) error {
	if sr == nil || sr.eventsFile == nil {
		return nil
	}
	return sr.appendJSONLine(sr.eventsFile, event)
}

// Finish persists the final response, result, and manifest for a stage.
func (sr *StageRecorder) Finish(result types.StageResult, response, diff string) (err error) {
	if sr == nil {
		return nil
	}
	defer func() {
		if closeErr := sr.Close(); closeErr != nil {
			if err == nil {
				err = closeErr
			} else {
				err = errors.Join(err, closeErr)
			}
		}
	}()

	if !sr.manifest.Stage.Valid() {
		return fmt.Errorf("invalid stage %q", sr.manifest.Stage)
	}
	if result.Stage == "" {
		result.Stage = sr.manifest.Stage
	}
	if !result.Stage.Valid() {
		return fmt.Errorf("invalid result stage %q", result.Stage)
	}
	if result.Status == "" {
		return errors.New("stage result status is required")
	}
	if !result.Status.Valid() {
		return fmt.Errorf("invalid stage status %q", result.Status)
	}
	if result.Status == types.StageStateRunning {
		return errors.New("stage result status cannot be running")
	}
	if result.StartedAt.IsZero() {
		result.StartedAt = sr.manifest.StartedAt
	}
	if result.FinishedAt.IsZero() {
		result.FinishedAt = time.Now().UTC()
	}
	if result.Status == types.StageStatePassed && result.NextAction == "" {
		result.NextAction = sr.manifest.Stage.NextAction()
	}
	if result.Status != types.StageStatePassed && result.FailureKind == "" && sr.manifest.ErrorKind != "" {
		result.FailureKind = sr.manifest.ErrorKind
	}
	if result.Status != types.StageStatePassed && result.Status != types.StageStateSkipped && result.Status != types.StageStateBlocked && result.Status != types.StageStateRetrying && result.Status != types.StageStateFailed {
		return fmt.Errorf("unexpected stage status %q", result.Status)
	}
	if result.Status == types.StageStatePassed && result.NextAction != "" {
		if _, ok := resolveStageAction(result.NextAction); !ok {
			return fmt.Errorf("invalid next_action %q", result.NextAction)
		}
	}

	if err := writeTextFile(sr.responsePath, response); err != nil {
		return err
	}
	if sr.diffPath != "" {
		if err := writeTextFile(sr.diffPath, diff); err != nil {
			return err
		}
	}
	if err := writeJSONAtomic(sr.resultPath, result); err != nil {
		return err
	}

	now := result.FinishedAt
	sr.manifest.Status = result.Status
	sr.manifest.FinishedAt = &now
	if result.Status == types.StageStatePassed {
		sr.manifest.ErrorKind = ""
	} else {
		sr.manifest.ErrorKind = result.FailureKind
	}
	sr.manifest.Retryable = result.Retryable
	if err := writeJSONAtomic(sr.manifestPath, sr.manifest); err != nil {
		return err
	}

	if err := sr.attempt.updateSnapshot(func(meta *AttemptMeta, summary *IssueSummary, now time.Time) {
		if result.Status == types.StageStatePassed {
			if nextStage, ok := resolveStageAction(result.NextAction); ok {
				meta.CurrentStage = nextStage
				summary.CurrentStage = nextStage
				if nextStage == types.StageHumanReview {
					meta.Outcome = "awaiting_review"
					summary.Outcome = "awaiting_review"
					summary.ReviewState = types.ReviewStateReady
					summary.IssueState = types.StateInReview.BoardState()
					if meta.EndedAt == nil {
						ended := now
						meta.EndedAt = &ended
					}
					if summary.FinishedAt == nil {
						finished := now
						summary.FinishedAt = &finished
					}
				} else {
					meta.Outcome = "running"
					summary.Outcome = "running"
				}
			} else {
				meta.Outcome = "running"
				summary.Outcome = "running"
			}
			summary.LastError = ""
			meta.Error = ""
			return
		}

		meta.CurrentStage = sr.manifest.Stage
		meta.Outcome = result.Status.String()
		summary.CurrentStage = sr.manifest.Stage
		summary.Outcome = result.Status.String()
		if result.Retryable || result.Status == types.StageStateRetrying {
			summary.IssueState = types.StateRetryQueued.BoardState()
		} else {
			summary.IssueState = types.StateRunning.BoardState()
		}
		meta.Error = stageErrorSummary(result)
		summary.LastError = meta.Error
		if meta.EndedAt == nil {
			ended := now
			meta.EndedAt = &ended
		}
		if summary.FinishedAt == nil {
			finished := now
			summary.FinishedAt = &finished
		}
	}); err != nil {
		return err
	}

	return nil
}

func (sr *StageRecorder) appendJSONLine(file *os.File, value any) error {
	if sr == nil || file == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal json line: %w", err)
	}
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if sr.closed {
		return nil
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write json line: %w", err)
	}
	return nil
}

func defaultReviewer() string {
	if reviewer := strings.TrimSpace(os.Getenv("USER")); reviewer != "" {
		return reviewer
	}
	if reviewer := strings.TrimSpace(os.Getenv("USERNAME")); reviewer != "" {
		return reviewer
	}
	return "contrabass"
}

func reviewStateForDecision(decision types.ReviewDecisionKind) types.ReviewState {
	switch decision {
	case types.ReviewDecisionApproved:
		return types.ReviewStateApproved
	case types.ReviewDecisionRejected:
		return types.ReviewStateRejected
	case types.ReviewDecisionNeedsChanges:
		return types.ReviewStateNeedsChanges
	default:
		return types.ReviewStatePending
	}
}

func resolveStageAction(action string) (types.Stage, bool) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case types.StageExecute.String():
		return types.StageExecute, true
	case types.StageVerify.String():
		return types.StageVerify, true
	case "review", types.StageHumanReview.String():
		return types.StageHumanReview, true
	default:
		return "", false
	}
}

func stageErrorSummary(result types.StageResult) string {
	summary := strings.TrimSpace(result.Summary)
	if summary != "" {
		return summary
	}
	if result.FailureKind != "" {
		return result.FailureKind.String()
	}
	return result.Status.String()
}
