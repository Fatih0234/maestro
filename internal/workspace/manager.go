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

	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

// Default paths
const (
	DefaultBaseDir      = "."
	DefaultBranchPrefix = "opencode/"
	DefaultWorktreeDir  = "workspaces"
)

// Manager handles git worktree-based workspaces for issues.
type Manager struct {
	baseDir      string // Root directory of the git repo
	worktreeDir  string // Subdirectory for worktrees (relative to baseDir)
	branchPrefix string // Prefix for worktree branch names

	gitBinary string // Git binary path

	mu     sync.RWMutex
	active map[string]string // issueID -> workspacePath
	locks  sync.Map           // Per-issue locks
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

	worktreeDir := cfg.WorktreeDir
	if strings.TrimSpace(worktreeDir) == "" {
		worktreeDir = DefaultWorktreeDir
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
		m.mu.Lock()
		m.active[issue.ID] = workspacePath
		m.mu.Unlock()
		return workspacePath, nil
	}

	// Create parent directory
	if err := os.MkdirAll(filepath.Dir(workspacePath), 0o755); err != nil {
		return "", fmt.Errorf("creating workspace parent directory: %w", err)
	}

	// Create git worktree
	branchName := m.branchPrefix + issue.ID
	_, err := m.runGit(ctx, "worktree", "add", workspacePath, "-b", branchName)
	if err != nil {
		// Fallback: try adding without creating a new branch
		_, fallbackErr := m.runGit(ctx, "worktree", "add", workspacePath, issue.ID)
		if fallbackErr != nil {
			return "", fmt.Errorf(
				"create git worktree for issue %s: worktree add -b %s failed: %v; fallback failed: %w",
				issue.ID, branchName, err, fallbackErr,
			)
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
		// Ignore if not a worktree
		if !strings.Contains(output, "is not a working tree") {
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
	return filepath.Join(m.baseDir, m.worktreeDir, issueID)
}

// lockIssue acquires an exclusive lock for the given issue.
func (m *Manager) lockIssue(issueID string) func() {
	lock, _ := m.locks.LoadOrStore(issueID, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
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
