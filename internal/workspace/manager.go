// Package workspace provides git worktree-based workspace management.
package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/fatihkarahan/maestro/internal/types"
	"github.com/fatihkarahan/maestro/internal/util"
)

// WorkspaceManager interface defines the operations needed by the orchestrator.
type WorkspaceManager interface {
	Create(ctx context.Context, issue types.Issue) (string, error)
	Cleanup(ctx context.Context, issueID string) error
	MergeToMain(ctx context.Context, issueID string) error
	Path(issueID string) string
	BaseDir() string
}

// Default paths
const (
	DefaultBaseDir      = "."
	DefaultBranchPrefix = "opencode/"
	DefaultWorktreeDir  = "" // default is derived from the base repo name and placed outside the repo tree
)

// Manager handles git worktree-based workspaces for issues.
type Manager struct {
	baseDir      string // Root directory of the git repo
	worktreeDir  string // Subdirectory for worktrees (relative to baseDir)
	branchPrefix string // Prefix for worktree branch names

	gitBinary string // Git binary path

	mu     sync.RWMutex
	active map[string]string // issueID -> workspacePath
	locks  sync.Map          // Per-issue locks
}

// Config holds configuration for the workspace manager.
type Config struct {
	BaseDir      string // Root directory of the git repo (default: .)
	WorktreeDir  string // Subdirectory for worktrees (default: workspaces)
	BranchPrefix string // Prefix for branch names (default: opencode/)
}

// New creates a new workspace Manager with the given configuration.
func New(cfg Config) *Manager {
	baseDir := cfg.BaseDir
	if strings.TrimSpace(baseDir) == "" {
		baseDir = DefaultBaseDir
	}

	worktreeDir := strings.TrimSpace(cfg.WorktreeDir)
	if worktreeDir == "" {
		worktreeDir = defaultWorktreeDir(baseDir)
	}

	branchPrefix := cfg.BranchPrefix
	if strings.TrimSpace(branchPrefix) == "" {
		branchPrefix = DefaultBranchPrefix
	}

	return &Manager{
		baseDir:      filepath.Clean(baseDir),
		worktreeDir:  worktreeDir,
		branchPrefix: branchPrefix,
		gitBinary:    "git",
		active:       make(map[string]string),
	}
}

// Create creates a new workspace for the given issue using git worktree.
func (m *Manager) Create(ctx context.Context, issue types.Issue) (string, error) {
	if issue.ID == "" {
		return "", errors.New("issue ID is required")
	}

	unlock := m.lockIssue(issue.ID)
	defer unlock()

	workspacePath := m.workspacePath(issue.ID)

	// Check if already tracked
	m.mu.RLock()
	trackedPath, tracked := m.active[issue.ID]
	m.mu.RUnlock()
	if tracked {
		// Verify it still exists
		if info, err := os.Stat(trackedPath); err == nil && info.IsDir() {
			return trackedPath, nil
		}
	}

	// Check if directory exists but not tracked
	if info, err := os.Stat(workspacePath); err == nil && info.IsDir() {
		// Verify it is a valid git worktree, not an orphaned/prunable one.
		if m.isValidWorktree(ctx, workspacePath) {
			m.mu.Lock()
			m.active[issue.ID] = workspacePath
			m.mu.Unlock()
			return workspacePath, nil
		}
		return "", fmt.Errorf("workspace path %s already exists but is not a valid git worktree; move or remove it before retrying", workspacePath)
	}

	// Create parent directory
	if err := os.MkdirAll(filepath.Dir(workspacePath), 0o755); err != nil {
		return "", fmt.Errorf("creating workspace parent directory: %w", err)
	}

	// Create git worktree
	branchName := m.branchPrefix + util.SanitizeBranchName(issue.ID)
	output, err := m.runGit(ctx, "worktree", "add", workspacePath, "-b", branchName)
	if err != nil {
		// If the branch already exists, reuse it instead of falling back to an
		// invalid ref. This makes reruns idempotent after cleanup removed the
		// worktree but preserved the branch.
		if strings.Contains(output, "a branch named") && strings.Contains(output, "already exists") {
			if _, fallbackErr := m.runGit(ctx, "worktree", "add", workspacePath, branchName); fallbackErr != nil {
				// A stale worktree registration (directory missing but still registered in
				// git) prevents reusing the branch. Prune the stale entry and retry.
				combined := fallbackErr.Error()
				if strings.Contains(combined, "already registered worktree") || strings.Contains(combined, "missing but already registered") {
					_, _ = m.runGit(ctx, "worktree", "prune", "--expire=now")
					if _, retryErr := m.runGit(ctx, "worktree", "add", workspacePath, branchName); retryErr != nil {
						return "", fmt.Errorf(
							"create git worktree for issue %s: worktree add -b %s failed: %v; fallback to existing branch failed after prune: %w",
							issue.ID, branchName, err, retryErr,
						)
					}
				} else {
					return "", fmt.Errorf(
						"create git worktree for issue %s: worktree add -b %s failed: %v; fallback to existing branch failed: %w",
						issue.ID, branchName, err, fallbackErr,
					)
				}
			}
		} else {
			return "", fmt.Errorf("create git worktree for issue %s: worktree add -b %s failed: %w", issue.ID, branchName, err)
		}
	}

	m.mu.Lock()
	m.active[issue.ID] = workspacePath
	m.mu.Unlock()

	return workspacePath, nil
}

// Cleanup removes a workspace for the given issue.
func (m *Manager) Cleanup(ctx context.Context, issueID string) error {
	if issueID == "" {
		return nil
	}

	unlock := m.lockIssue(issueID)
	defer unlock()

	workspacePath := m.workspacePath(issueID)

	// Check if directory exists
	if _, err := os.Stat(workspacePath); errors.Is(err, os.ErrNotExist) {
		// Already gone, clean up tracking
		m.mu.Lock()
		delete(m.active, issueID)
		m.mu.Unlock()
		m.locks.Delete(issueID)
		return nil
	}

	// Remove git worktree
	output, err := m.runGit(ctx, "worktree", "remove", workspacePath, "--force")
	if err != nil {
		// Check if it's "not a working tree" - this happens for orphaned worktrees
		// that exist on disk but aren't tracked by git
		if strings.Contains(output, "is not a working tree") {
			// Best-effort directory removal for orphaned worktrees
			if rmErr := os.RemoveAll(workspacePath); rmErr != nil {
				return fmt.Errorf(
					"remove orphaned worktree for issue %s: git failed: %w; directory removal failed: %v",
					issueID, err, rmErr,
				)
			}
			// Directory removed successfully
		} else {
			return fmt.Errorf("remove git worktree for issue %s: %w", issueID, err)
		}
	}

	m.mu.Lock()
	delete(m.active, issueID)
	m.mu.Unlock()
	m.locks.Delete(issueID)

	return nil
}

// CleanupAll removes all tracked workspaces.
func (m *Manager) CleanupAll(ctx context.Context) error {
	issueIDs := m.List()
	var errs []error

	for _, issueID := range issueIDs {
		if err := m.Cleanup(ctx, issueID); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// MergeToMain merges the issue's worktree branch into main and cleans up the branch.
// Returns error if merge fails (e.g., conflicts). Does NOT remove the worktree on error.
// The caller is responsible for calling Cleanup after successful merge.
func (m *Manager) MergeToMain(ctx context.Context, issueID string) error {
	if issueID == "" {
		return errors.New("issue ID is required")
	}

	unlock := m.lockIssue(issueID)
	defer unlock()

	branchName := m.branchPrefix + util.SanitizeBranchName(issueID)

	// 1. Verify worktree exists
	wsPath := m.workspacePath(issueID)
	if _, err := os.Stat(wsPath); err != nil {
		return fmt.Errorf("worktree for %s does not exist: %w", issueID, err)
	}

	// 2. Check if branch has commits (skip merge if empty - shouldn't happen with worktrees)
	if hasCommits, err := m.branchHasCommits(ctx, branchName); err != nil {
		return err
	} else if !hasCommits {
		// No commits, nothing to merge
		return nil
	}

	// 3. Checkout main in the base directory
	if _, err := m.runGit(ctx, "checkout", "main"); err != nil {
		return fmt.Errorf("checkout main failed: %w", err)
	}

	// 4. Attempt merge of the feature branch into main with --no-ff to preserve history
	output, err := m.runGit(ctx, "merge", "--no-ff", branchName, "-m", fmt.Sprintf("Merge %s into main", branchName))
	if err != nil {
		// Check for conflicts
		if strings.Contains(output, "CONFLICT") {
			return fmt.Errorf("merge conflict in %s: %s", issueID, output)
		}
		return fmt.Errorf("merge failed for %s: %w; output: %s", issueID, err, output)
	}

	// 5. Push to origin if it exists (non-fatal if fails - local main has the changes)
	if _, err := m.runGit(ctx, "push", "origin", "main"); err != nil {
		// Non-fatal: local main has the changes even if push fails
	}

	return nil
}

// branchHasCommits checks if a branch has any commits.
func (m *Manager) branchHasCommits(ctx context.Context, branchName string) (bool, error) {
	output, err := m.runGit(ctx, "rev-list", "--count", branchName)
	if err != nil {
		return false, err
	}
	count := strings.TrimSpace(output)
	return count != "0", nil
}

// Exists checks if a workspace exists for the given issue.
func (m *Manager) Exists(issueID string) bool {
	m.mu.RLock()
	workspacePath, ok := m.active[issueID]
	m.mu.RUnlock()
	if !ok {
		return false
	}

	info, err := os.Stat(workspacePath)
	return err == nil && info.IsDir()
}

// Path returns the workspace path for an issue without creating it.
func (m *Manager) Path(issueID string) string {
	return m.workspacePath(issueID)
}

// List returns all tracked issue IDs with active workspaces.
func (m *Manager) List() []string {
	m.mu.RLock()
	issueIDs := make([]string, 0, len(m.active))
	for issueID := range m.active {
		issueIDs = append(issueIDs, issueID)
	}
	m.mu.RUnlock()

	sort.Strings(issueIDs)
	return issueIDs
}

// BaseDir returns the base directory path.
func (m *Manager) BaseDir() string {
	return m.baseDir
}

// workspacePath returns the full path to a workspace directory.
func (m *Manager) workspacePath(issueID string) string {
	return filepath.Join(m.worktreeRoot(), util.SanitizeFileName(issueID))
}

// worktreeRoot returns the base directory where worktrees live.
func (m *Manager) worktreeRoot() string {
	if filepath.IsAbs(m.worktreeDir) {
		return filepath.Clean(m.worktreeDir)
	}
	return filepath.Join(m.baseDir, m.worktreeDir)
}

// defaultWorktreeDir derives a sibling directory outside the repo tree.
func defaultWorktreeDir(baseDir string) string {
	cleanBase := filepath.Clean(baseDir)
	baseName := filepath.Base(cleanBase)
	if baseName == "." || baseName == string(filepath.Separator) || baseName == "" {
		return "../workspaces"
	}
	return "../" + baseName + ".worktrees"
}

// lockIssue acquires an exclusive lock for the given issue.
func (m *Manager) lockIssue(issueID string) func() {
	lock, _ := m.locks.LoadOrStore(issueID, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// isValidWorktree checks whether a directory is a healthy git worktree.
// It returns false for orphaned/prunable worktrees (missing .git file,
// broken gitdir link, or worktree marked prunable by git).
func (m *Manager) isValidWorktree(ctx context.Context, path string) bool {
	// Must have a .git file (worktrees use a gitfile, not a .git directory)
	gitFile := filepath.Join(path, ".git")
	info, err := os.Stat(gitFile)
	if err != nil || info.IsDir() {
		return false
	}

	// The gitfile must contain a valid gitdir: line pointing to an existing path.
	// Only the first line is checked — .git files may contain additional lines.
	data, err := os.ReadFile(gitFile)
	if err != nil {
		return false
	}
	const prefix = "gitdir: "
	firstLine := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)[0]
	if !strings.HasPrefix(firstLine, prefix) {
		return false
	}
	gitDir := strings.TrimSpace(firstLine[len(prefix):])
	if _, err := os.Stat(gitDir); err != nil {
		return false
	}

	// Ask git whether this path is known as a valid worktree.
	// Parse porcelain output line-by-line to avoid substring false positives
	// (e.g., /worktrees/CB-1 matching /worktrees/CB-10).
	output, err := m.runGit(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			if strings.TrimSpace(line[len("worktree "):]) == path {
				return true
			}
		}
	}
	return false
}

// runGit executes a git command in the base directory.
func (m *Manager) runGit(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, m.gitBinary, args...)
	cmd.Dir = m.baseDir
	output, err := cmd.CombinedOutput()
	if err == nil {
		return string(output), nil
	}

	var execErr *exec.Error
	if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
		return string(output), fmt.Errorf("git executable not found: %w", err)
	}

	return string(output), fmt.Errorf(
		"git %s failed: %w; output: %s",
		strings.Join(args, " "),
		err,
		strings.TrimSpace(string(output)),
	)
}
