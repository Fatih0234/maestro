// Package tracker provides file-based issue tracking.
package tracker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fatihkarahan/maestro/internal/types"
)

// Board states (stored in JSON files)
const (
	StateTodo        = "todo"
	StateInProgress  = "in_progress"
	StateInReview    = "in_review"
	StateDone        = "done"
	StateFailed      = "failed"
	StateRetryQueued = "retry_queued" // Issue waiting for backoff retry
)

// Default paths
const (
	DefaultBoardDir    = ".maestro/projects/default/board"
	DefaultIssuePrefix = "CB"
	SchemaVersion      = "3" // v3: added in_review board state
)

// Manifest represents the board metadata file.
type Manifest struct {
	SchemaVersion   string    `json:"schema_version"`
	IssuePrefix     string    `json:"issue_prefix"`
	NextIssueNumber int       `json:"next_issue_number"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Issue represents a local board issue stored as JSON.
type Issue struct {
	ID           string     `json:"id"`
	Identifier   string     `json:"identifier"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	State        string     `json:"state"`
	Labels       []string   `json:"labels,omitempty"`
	URL          string     `json:"url,omitempty"`
	ClaimedBy    string     `json:"claimed_by,omitempty"`
	RetryAfter   *time.Time `json:"retry_after,omitempty"` // When to retry (for retry_queued state)
	RetryAttempt int        `json:"retry_attempt,omitempty"`
	RetryStage   string     `json:"retry_stage,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// Config holds configuration for the local tracker.
type Config struct {
	BoardDir    string // Board directory path (default: .maestro/projects/default/board)
	IssuePrefix string // Issue ID prefix (default: CB)
	Actor       string // Actor name for claiming (default: current user)
}

// LocalTracker implements types.IssueTracker for file-based storage.
type LocalTracker struct {
	boardDir    string
	issuePrefix string
	actor       string
	mu          sync.Mutex
}

// New creates a new LocalTracker with the given configuration.
func New(cfg Config) *LocalTracker {
	boardDir := cfg.BoardDir
	if strings.TrimSpace(boardDir) == "" {
		boardDir = DefaultBoardDir
	}

	prefix := cfg.IssuePrefix
	if strings.TrimSpace(prefix) == "" {
		prefix = DefaultIssuePrefix
	}

	actor := strings.TrimSpace(cfg.Actor)
	if actor == "" {
		actor = strings.TrimSpace(os.Getenv("USER"))
	}
	if actor == "" {
		actor = "maestro"
	}

	return &LocalTracker{
		boardDir:    filepath.Clean(boardDir),
		issuePrefix: strings.ToUpper(strings.TrimSpace(prefix)),
		actor:       actor,
	}
}

// BoardDir returns the board directory path.
func (t *LocalTracker) BoardDir() string {
	return t.boardDir
}

// EnsureBoard initializes the board directory and manifest if needed.
func (t *LocalTracker) EnsureBoard() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ensureBoardLocked()
}

// ensureBoardLocked creates board directories and manifest.
// Caller must hold t.mu.
func (t *LocalTracker) ensureBoardLocked() error {
	if err := os.MkdirAll(t.issuesDir(), 0o755); err != nil {
		return fmt.Errorf("creating issues directory: %w", err)
	}

	manifestPath := t.manifestPath()
	var manifest Manifest
	if err := readJSONFile(manifestPath, &manifest); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}

		// Create new manifest
		now := time.Now().UTC()
		manifest = Manifest{
			SchemaVersion:   SchemaVersion,
			IssuePrefix:     t.issuePrefix,
			NextIssueNumber: 1,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		return writeJSONAtomic(manifestPath, manifest)
	}

	// Ensure manifest has defaults
	if manifest.SchemaVersion != SchemaVersion {
		manifest.SchemaVersion = SchemaVersion
	}
	if manifest.IssuePrefix == "" {
		manifest.IssuePrefix = t.issuePrefix
	}
	if manifest.NextIssueNumber <= 0 {
		manifest.NextIssueNumber = 1
	}

	return writeJSONAtomic(manifestPath, manifest)
}

// FetchIssues returns all dispatchable issues that are ready to be processed.
// Issues in done or in_review are excluded.
// Issues in retry_queued state are only returned if their retry_after time has passed.
func (t *LocalTracker) FetchIssues() ([]types.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if err := t.ensureBoardLocked(); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(t.issuesDir())
	if err != nil {
		return nil, fmt.Errorf("reading issues directory: %w", err)
	}

	issues := make([]types.Issue, 0)
	now := time.Now()
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		var issue Issue
		if err := readJSONFile(filepath.Join(t.issuesDir(), entry.Name()), &issue); err != nil {
			return nil, err
		}

		// Skip terminal/handoff issues
		if issue.State == StateDone || issue.State == StateInReview || issue.State == StateFailed {
			continue
		}

		// Skip retry_queued issues that haven't reached their retry time
		if issue.State == StateRetryQueued {
			if issue.RetryAfter != nil && now.Before(*issue.RetryAfter) {
				continue
			}
		}

		issues = append(issues, t.toTypesIssue(issue))
	}

	// Sort by creation time, then ID
	slices.SortFunc(issues, func(a, b types.Issue) int {
		if a.CreatedAt.Equal(b.CreatedAt) {
			return strings.Compare(a.ID, b.ID)
		}
		if a.CreatedAt.Before(b.CreatedAt) {
			return -1
		}
		return 1
	})

	return issues, nil
}

// ClaimIssue marks an issue as in_progress and sets claimed_by.
func (t *LocalTracker) ClaimIssue(id string) (types.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if err := t.ensureBoardLocked(); err != nil {
		return types.Issue{}, err
	}

	issue, err := t.loadIssueLocked(id)
	if err != nil {
		return types.Issue{}, err
	}
	if issue.State == StateDone || issue.State == StateInReview || issue.State == StateFailed {
		return types.Issue{}, fmt.Errorf("issue %q cannot be claimed from state %q", id, issue.State)
	}

	issue.State = StateInProgress
	issue.ClaimedBy = t.actor
	issue.RetryAfter = nil
	issue.UpdatedAt = time.Now().UTC()

	if err := writeJSONAtomic(t.issuePath(id), issue); err != nil {
		return types.Issue{}, err
	}

	return t.toTypesIssue(issue), nil
}

// ReleaseIssue marks an issue as todo and clears claimed_by.
func (t *LocalTracker) ReleaseIssue(id string) (types.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if err := t.ensureBoardLocked(); err != nil {
		return types.Issue{}, err
	}

	issue, err := t.loadIssueLocked(id)
	if err != nil {
		return types.Issue{}, err
	}

	issue.State = StateTodo
	issue.ClaimedBy = ""
	issue.UpdatedAt = time.Now().UTC()

	if err := writeJSONAtomic(t.issuePath(id), issue); err != nil {
		return types.Issue{}, err
	}

	return t.toTypesIssue(issue), nil
}

// GetIssue fetches a single issue by ID.
func (t *LocalTracker) GetIssue(id string) (types.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if err := t.ensureBoardLocked(); err != nil {
		return types.Issue{}, err
	}

	issue, err := t.loadIssueLocked(id)
	if err != nil {
		return types.Issue{}, err
	}

	return t.toTypesIssue(issue), nil
}

// UpdateIssueState updates an issue's state.
func (t *LocalTracker) UpdateIssueState(id string, state types.IssueState) (types.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if err := t.ensureBoardLocked(); err != nil {
		return types.Issue{}, err
	}

	issue, err := t.loadIssueLocked(id)
	if err != nil {
		return types.Issue{}, err
	}

	issue.State = toBoardState(state)
	if issue.State == StateInProgress && issue.ClaimedBy == "" {
		issue.ClaimedBy = t.actor
	} else if issue.State != StateInProgress {
		issue.ClaimedBy = ""
	}
	// Clear retry_after when leaving retry_queued state
	if state != types.StateRetryQueued {
		issue.RetryAfter = nil
		issue.RetryAttempt = 0
		issue.RetryStage = ""
	}
	issue.UpdatedAt = time.Now().UTC()

	if err := writeJSONAtomic(t.issuePath(id), issue); err != nil {
		return types.Issue{}, err
	}

	return t.toTypesIssue(issue), nil
}

// CreateIssue creates a new issue and returns it.
func (t *LocalTracker) CreateIssue(title, description string, labels []string) (types.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if err := t.ensureBoardLocked(); err != nil {
		return types.Issue{}, err
	}

	// Read manifest to get next issue number
	manifestPath := t.manifestPath()
	var manifest Manifest
	if err := readJSONFile(manifestPath, &manifest); err != nil {
		return types.Issue{}, err
	}

	issueID := fmt.Sprintf("%s-%d", manifest.IssuePrefix, manifest.NextIssueNumber)
	manifest.NextIssueNumber++
	manifest.UpdatedAt = time.Now().UTC()

	if err := writeJSONAtomic(t.manifestPath(), manifest); err != nil {
		return types.Issue{}, err
	}

	now := time.Now().UTC()
	issue := Issue{
		ID:          issueID,
		Identifier:  issueID,
		Title:       title,
		Description: description,
		State:       StateTodo,
		Labels:      slices.Clone(labels),
		URL:         fmt.Sprintf("local://%s", issueID),
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := writeJSONAtomic(t.issuePath(issueID), issue); err != nil {
		return types.Issue{}, err
	}

	return t.toTypesIssue(issue), nil
}

// loadIssueLocked loads an issue file.
// Caller must hold t.mu.
func (t *LocalTracker) loadIssueLocked(id string) (Issue, error) {
	var issue Issue
	if err := readJSONFile(t.issuePath(id), &issue); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Issue{}, fmt.Errorf("issue %q not found", id)
		}
		return Issue{}, err
	}
	return issue, nil
}

// manifestPath returns the path to the manifest file.
func (t *LocalTracker) manifestPath() string {
	return filepath.Join(t.boardDir, "manifest.json")
}

// issuesDir returns the issues directory path.
func (t *LocalTracker) issuesDir() string {
	return filepath.Join(t.boardDir, "issues")
}

// issuePath returns the path to an issue JSON file.
func (t *LocalTracker) issuePath(id string) string {
	return filepath.Join(t.issuesDir(), id+".json")
}

// toTypesIssue converts a local Issue to types.Issue.
func (t *LocalTracker) toTypesIssue(issue Issue) types.Issue {
	return types.Issue{
		ID:           issue.ID,
		Identifier:   issue.Identifier,
		Title:        issue.Title,
		Description:  issue.Description,
		State:        toIssueState(issue.State),
		Labels:       slices.Clone(issue.Labels),
		URL:          issue.URL,
		RetryAfter:   issue.RetryAfter,
		RetryAttempt: issue.RetryAttempt,
		RetryStage:   types.Stage(issue.RetryStage),
		CreatedAt:    issue.CreatedAt,
		UpdatedAt:    issue.UpdatedAt,
	}
}

// ListAllIssues returns all known issues regardless of state.
func (t *LocalTracker) ListAllIssues() ([]types.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if err := t.ensureBoardLocked(); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(t.issuesDir())
	if err != nil {
		return nil, fmt.Errorf("reading issues directory: %w", err)
	}

	issues := make([]types.Issue, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		var issue Issue
		if err := readJSONFile(filepath.Join(t.issuesDir(), entry.Name()), &issue); err != nil {
			return nil, err
		}

		issues = append(issues, t.toTypesIssue(issue))
	}

	slices.SortFunc(issues, func(a, b types.Issue) int {
		if a.CreatedAt.Equal(b.CreatedAt) {
			return strings.Compare(a.ID, b.ID)
		}
		if a.CreatedAt.Before(b.CreatedAt) {
			return -1
		}
		return 1
	})

	return issues, nil
}

// SetRetryQueue marks an issue as retry_queued with a retry_after timestamp.
// This is the preferred way to queue an issue for retry instead of using UpdateIssueState.
func (t *LocalTracker) SetRetryQueue(id string, retryAt time.Time, attempt int, stage types.Stage) (types.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if err := t.ensureBoardLocked(); err != nil {
		return types.Issue{}, err
	}

	issue, err := t.loadIssueLocked(id)
	if err != nil {
		return types.Issue{}, err
	}

	issue.State = StateRetryQueued
	issue.RetryAfter = &retryAt
	issue.RetryAttempt = attempt
	issue.RetryStage = stage.String()
	issue.ClaimedBy = ""
	issue.UpdatedAt = time.Now().UTC()

	if err := writeJSONAtomic(t.issuePath(id), issue); err != nil {
		return types.Issue{}, err
	}

	return t.toTypesIssue(issue), nil
}

// toBoardState converts types.IssueState to board state string.
func toBoardState(state types.IssueState) string {
	switch state {
	case types.StateClaimed, types.StateRunning:
		return StateInProgress
	case types.StateRetryQueued:
		return StateRetryQueued // Preserve retry_queued state
	case types.StateInReview:
		return StateInReview
	case types.StateFailed:
		return StateFailed
	case types.StateReleased:
		return StateDone
	default:
		return StateTodo
	}
}

// toIssueState converts board state string to types.IssueState.
func toIssueState(state string) types.IssueState {
	switch state {
	case StateInProgress:
		return types.StateRunning
	case StateRetryQueued:
		return types.StateRetryQueued
	case StateInReview:
		return types.StateInReview
	case StateFailed:
		return types.StateFailed
	case StateDone:
		return types.StateReleased
	default:
		return types.StateUnclaimed
	}
}

// readJSONFile reads and unmarshals a JSON file.
func readJSONFile(path string, out interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decoding %s: %w", path, err)
	}
	return nil
}

// writeJSONAtomic writes JSON atomically using a temp file.
func writeJSONAtomic(path string, value interface{}) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating parent directory for %s: %w", path, err)
	}

	dir, base := filepath.Split(path)
	f, err := os.CreateTemp(dir, base+".*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file for %s: %w", path, err)
	}
	tempPath := f.Name()
	if err := f.Close(); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("closing temp file for %s: %w", path, err)
	}
	if err := os.WriteFile(tempPath, append(data, '\n'), 0o644); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("writing temp file for %s: %w", path, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("renaming temp file for %s: %w", path, err)
	}

	return nil
}
