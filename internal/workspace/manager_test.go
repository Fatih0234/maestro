package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

func initGitRepo(t *testing.T, dir string) {
	t.Helper()

	// Initialize git repo
	if err := runCommand(dir, "git", "init"); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	// Configure git user for commits
	if err := runCommand(dir, "git", "config", "user.email", "test@example.com"); err != nil {
		t.Fatalf("git config email failed: %v", err)
	}
	if err := runCommand(dir, "git", "config", "user.name", "Test User"); err != nil {
		t.Fatalf("git config name failed: %v", err)
	}

	// Create initial commit
	if err := runCommand(dir, "touch", "README.md"); err != nil {
		t.Fatalf("touch README failed: %v", err)
	}
	if err := runCommand(dir, "git", "add", "README.md"); err != nil {
		t.Fatalf("git add failed: %v", err)
	}
	if err := runCommand(dir, "git", "commit", "-m", "Initial commit"); err != nil {
		t.Fatalf("git commit failed: %v", err)
	}
}

func runCommand(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func TestManager_Defaults(t *testing.T) {
	manager := New(Config{})

	if manager.BaseDir() != "." {
		t.Errorf("expected BaseDir '.', got %q", manager.BaseDir())
	}
}

func TestManager_Path(t *testing.T) {
	tmpDir := t.TempDir()

	manager := New(Config{
		BaseDir:     tmpDir,
		WorktreeDir: "workspaces",
	})

	path := manager.Path("CB-1")
	expected := filepath.Join(tmpDir, "workspaces", "CB-1")
	if path != expected {
		t.Errorf("expected path %q, got %q", expected, path)
	}
}

func TestManager_Exists_NotExists(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	manager := New(Config{
		BaseDir: tmpDir,
	})

	if manager.Exists("CB-1") {
		t.Error("expected Exists to return false for nonexistent workspace")
	}
}

func TestManager_List_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	manager := New(Config{
		BaseDir: tmpDir,
	})

	list := manager.List()
	if len(list) != 0 {
		t.Errorf("expected empty list, got %v", list)
	}
}

func TestManager_Create(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	manager := New(Config{
		BaseDir:      tmpDir,
		WorktreeDir:  "workspaces",
		BranchPrefix: "opencode/",
	})

	issue := types.Issue{
		ID:          "CB-1",
		Identifier:  "CB-1",
		Title:       "Test Issue",
		Description: "Test description",
	}

	workspacePath, err := manager.Create(context.Background(), issue)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	expected := filepath.Join(tmpDir, "workspaces", "CB-1")
	if workspacePath != expected {
		t.Errorf("expected workspace path %q, got %q", expected, workspacePath)
	}

	// Verify directory exists
	if _, err := os.Stat(workspacePath); err != nil {
		t.Fatalf("workspace directory not created: %v", err)
	}

	// Verify it's tracked
	if !manager.Exists("CB-1") {
		t.Error("expected Exists to return true after Create")
	}

	// Verify in List
	list := manager.List()
	if len(list) != 1 || list[0] != "CB-1" {
		t.Errorf("expected List [CB-1], got %v", list)
	}
}

func TestManager_Create_AlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	manager := New(Config{
		BaseDir: tmpDir,
	})

	issue := types.Issue{
		ID:          "CB-1",
		Identifier:  "CB-1",
		Title:       "Test Issue",
		Description: "Test description",
	}

	// Create twice
	_, err := manager.Create(context.Background(), issue)
	if err != nil {
		t.Fatalf("first Create failed: %v", err)
	}

	_, err = manager.Create(context.Background(), issue)
	if err != nil {
		t.Fatalf("second Create should not fail: %v", err)
	}

	// Should still only have one entry
	list := manager.List()
	if len(list) != 1 {
		t.Errorf("expected 1 workspace, got %d", len(list))
	}
}

func TestManager_Create_EmptyID(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	manager := New(Config{
		BaseDir: tmpDir,
	})

	_, err := manager.Create(context.Background(), types.Issue{})
	if err == nil {
		t.Error("expected error for empty issue ID")
	}
}

func TestManager_Cleanup(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	manager := New(Config{
		BaseDir: tmpDir,
	})

	issue := types.Issue{
		ID:          "CB-1",
		Identifier:  "CB-1",
		Title:       "Test Issue",
		Description: "Test description",
	}

	// Create workspace
	workspacePath, err := manager.Create(context.Background(), issue)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Cleanup
	if err := manager.Cleanup(context.Background(), "CB-1"); err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}

	// Verify directory is gone
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Error("expected workspace directory to be removed")
	}

	// Verify not tracked
	if manager.Exists("CB-1") {
		t.Error("expected Exists to return false after Cleanup")
	}

	// Verify not in List
	list := manager.List()
	if len(list) != 0 {
		t.Errorf("expected empty list after cleanup, got %v", list)
	}
}

func TestManager_Cleanup_NotExists(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	manager := New(Config{
		BaseDir: tmpDir,
	})

	// Cleanup should not fail for nonexistent workspace
	if err := manager.Cleanup(context.Background(), "NONEXISTENT"); err != nil {
		t.Errorf("Cleanup should not fail for nonexistent workspace: %v", err)
	}
}

func TestManager_Cleanup_EmptyID(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	manager := New(Config{
		BaseDir: tmpDir,
	})

	// Cleanup should silently succeed for empty ID
	if err := manager.Cleanup(context.Background(), ""); err != nil {
		t.Errorf("Cleanup with empty ID should not fail: %v", err)
	}
}

func TestManager_CleanupAll(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	manager := New(Config{
		BaseDir: tmpDir,
	})

	// Create multiple workspaces
	for i := 1; i <= 3; i++ {
		issue := types.Issue{
			ID:          "CB-" + string(rune('0'+i)),
			Identifier:  "CB-" + string(rune('0'+i)),
			Title:       "Test Issue",
			Description: "Test description",
		}
		if _, err := manager.Create(context.Background(), issue); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	// Cleanup all
	if err := manager.CleanupAll(context.Background()); err != nil {
		t.Fatalf("CleanupAll failed: %v", err)
	}

	// Verify all gone
	list := manager.List()
	if len(list) != 0 {
		t.Errorf("expected empty list after CleanupAll, got %v", list)
	}
}

func TestManager_MultipleWorkspaces(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	manager := New(Config{
		BaseDir: tmpDir,
	})

	issues := []types.Issue{
		{ID: "CB-1", Title: "Issue 1"},
		{ID: "CB-2", Title: "Issue 2"},
		{ID: "CB-3", Title: "Issue 3"},
	}

	for _, issue := range issues {
		if _, err := manager.Create(context.Background(), issue); err != nil {
			t.Fatalf("Create failed for %s: %v", issue.ID, err)
		}
	}

	list := manager.List()
	if len(list) != 3 {
		t.Errorf("expected 3 workspaces, got %d", len(list))
	}

	// Verify all exist
	for _, issue := range issues {
		if !manager.Exists(issue.ID) {
			t.Errorf("expected workspace %s to exist", issue.ID)
		}
	}
}

func TestManager_BranchNaming(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	manager := New(Config{
		BaseDir:      tmpDir,
		BranchPrefix: "feature/",
	})

	issue := types.Issue{
		ID:          "CB-1",
		Identifier:  "CB-1",
		Title:       "Test Issue",
		Description: "Test description",
	}

	workspacePath, err := manager.Create(context.Background(), issue)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Verify branch was created with correct name
	branchName, err := getCurrentBranch(workspacePath)
	if err != nil {
		t.Fatalf("getCurrentBranch failed: %v", err)
	}

	if branchName != "feature/CB-1" {
		t.Errorf("expected branch name 'feature/CB-1', got %q", branchName)
	}
}

func getCurrentBranch(dir string) (string, error) {
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	// Trim trailing newline
	return trimNewline(string(output)), nil
}

func trimNewline(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\n' {
		return s[:len(s)-1]
	}
	return s
}
