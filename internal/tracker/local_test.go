package tracker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

func TestLocalTracker_Defaults(t *testing.T) {
	tmpDir := t.TempDir()

	tracker := New(Config{
		BoardDir: tmpDir,
	})

	if tracker.BoardDir() != tmpDir {
		t.Errorf("expected BoardDir %q, got %q", tmpDir, tracker.BoardDir())
	}
}

func TestLocalTracker_EnsureBoard(t *testing.T) {
	tmpDir := t.TempDir()

	tracker := New(Config{
		BoardDir: tmpDir,
	})

	if err := tracker.EnsureBoard(); err != nil {
		t.Fatalf("EnsureBoard failed: %v", err)
	}

	// Check manifest exists
	manifestPath := filepath.Join(tmpDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("manifest.json not created: %v", err)
	}

	// Verify manifest content
	var manifest Manifest
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("failed to read manifest: %v", err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("failed to parse manifest: %v", err)
	}

	if manifest.SchemaVersion != SchemaVersion {
		t.Errorf("expected schema version %q, got %q", SchemaVersion, manifest.SchemaVersion)
	}
	if manifest.IssuePrefix != "CB" {
		t.Errorf("expected default prefix CB, got %q", manifest.IssuePrefix)
	}
	if manifest.NextIssueNumber != 1 {
		t.Errorf("expected next issue number 1, got %d", manifest.NextIssueNumber)
	}
}

func TestLocalTracker_CreateIssue(t *testing.T) {
	tmpDir := t.TempDir()

	tracker := New(Config{
		BoardDir:    tmpDir,
		IssuePrefix: "TEST",
	})

	issue, err := tracker.CreateIssue("Test Issue", "Description here", []string{"bug"})
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}

	if issue.ID != "TEST-1" {
		t.Errorf("expected ID TEST-1, got %q", issue.ID)
	}
	if issue.Title != "Test Issue" {
		t.Errorf("expected title 'Test Issue', got %q", issue.Title)
	}
	if issue.Description != "Description here" {
		t.Errorf("expected description 'Description here', got %q", issue.Description)
	}
	if len(issue.Labels) != 1 || issue.Labels[0] != "bug" {
		t.Errorf("expected labels [bug], got %v", issue.Labels)
	}
	if issue.State != types.StateUnclaimed {
		t.Errorf("expected state unclaimed, got %v", issue.State)
	}
	if issue.URL != "local://TEST-1" {
		t.Errorf("expected URL local://TEST-1, got %q", issue.URL)
	}

	// Verify file was created
	issuePath := filepath.Join(tmpDir, "issues", "TEST-1.json")
	if _, err := os.Stat(issuePath); err != nil {
		t.Fatalf("issue file not created: %v", err)
	}
}

func TestLocalTracker_GetIssue(t *testing.T) {
	tmpDir := t.TempDir()

	tracker := New(Config{
		BoardDir: tmpDir,
	})

	// Create an issue
	created, err := tracker.CreateIssue("Get Test", "Get description", nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}

	// Get it back
	fetched, err := tracker.GetIssue(created.ID)
	if err != nil {
		t.Fatalf("GetIssue failed: %v", err)
	}

	if fetched.ID != created.ID {
		t.Errorf("ID mismatch: expected %q, got %q", created.ID, fetched.ID)
	}
	if fetched.Title != created.Title {
		t.Errorf("Title mismatch: expected %q, got %q", created.Title, fetched.Title)
	}
}

func TestLocalTracker_GetIssue_NotFound(t *testing.T) {
	tmpDir := t.TempDir()

	tracker := New(Config{
		BoardDir: tmpDir,
	})

	_, err := tracker.GetIssue("NONEXISTENT-1")
	if err == nil {
		t.Error("expected error for nonexistent issue")
	}
}

func TestLocalTracker_ClaimIssue(t *testing.T) {
	tmpDir := t.TempDir()

	tracker := New(Config{
		BoardDir: tmpDir,
		Actor:    "tester",
	})

	// Create an issue
	issue, err := tracker.CreateIssue("Claim Test", "Description", nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}

	// Claim it
	claimed, err := tracker.ClaimIssue(issue.ID)
	if err != nil {
		t.Fatalf("ClaimIssue failed: %v", err)
	}

	if claimed.State != types.StateRunning {
		t.Errorf("expected state running, got %v", claimed.State)
	}

	// Verify file was updated
	issuePath := filepath.Join(tmpDir, "issues", issue.ID+".json")
	data, err := os.ReadFile(issuePath)
	if err != nil {
		t.Fatalf("failed to read issue file: %v", err)
	}

	var stored Issue
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("failed to parse issue file: %v", err)
	}

	if stored.State != StateInProgress {
		t.Errorf("expected stored state in_progress, got %q", stored.State)
	}
	if stored.ClaimedBy != "tester" {
		t.Errorf("expected claimed_by tester, got %q", stored.ClaimedBy)
	}
}

func TestLocalTracker_ReleaseIssue(t *testing.T) {
	tmpDir := t.TempDir()

	tracker := New(Config{
		BoardDir: tmpDir,
		Actor:    "tester",
	})

	// Create and claim an issue
	issue, err := tracker.CreateIssue("Release Test", "Description", nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}

	if _, err := tracker.ClaimIssue(issue.ID); err != nil {
		t.Fatalf("ClaimIssue failed: %v", err)
	}

	// Release it
	released, err := tracker.ReleaseIssue(issue.ID)
	if err != nil {
		t.Fatalf("ReleaseIssue failed: %v", err)
	}

	if released.State != types.StateUnclaimed {
		t.Errorf("expected state unclaimed, got %v", released.State)
	}

	// Verify file was updated
	issuePath := filepath.Join(tmpDir, "issues", issue.ID+".json")
	data, err := os.ReadFile(issuePath)
	if err != nil {
		t.Fatalf("failed to read issue file: %v", err)
	}

	var stored Issue
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("failed to parse issue file: %v", err)
	}

	if stored.State != StateTodo {
		t.Errorf("expected stored state todo, got %q", stored.State)
	}
	if stored.ClaimedBy != "" {
		t.Errorf("expected claimed_by empty, got %q", stored.ClaimedBy)
	}
}

func TestLocalTracker_UpdateIssueState(t *testing.T) {
	tmpDir := t.TempDir()

	tracker := New(Config{
		BoardDir: tmpDir,
	})

	// Create an issue
	issue, err := tracker.CreateIssue("Update State Test", "Description", nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}

	// Update to done
	updated, err := tracker.UpdateIssueState(issue.ID, types.StateReleased)
	if err != nil {
		t.Fatalf("UpdateIssueState failed: %v", err)
	}

	if updated.State != types.StateReleased {
		t.Errorf("expected state released, got %v", updated.State)
	}
}

func TestLocalTracker_FetchIssues(t *testing.T) {
	tmpDir := t.TempDir()

	tracker := New(Config{
		BoardDir: tmpDir,
	})

	// Create multiple issues
	_, err := tracker.CreateIssue("Issue 1", "Desc 1", nil)
	if err != nil {
		t.Fatalf("CreateIssue 1 failed: %v", err)
	}

	issue2, err := tracker.CreateIssue("Issue 2", "Desc 2", nil)
	if err != nil {
		t.Fatalf("CreateIssue 2 failed: %v", err)
	}

	issue3, err := tracker.CreateIssue("Issue 3", "Desc 3", nil)
	if err != nil {
		t.Fatalf("CreateIssue 3 failed: %v", err)
	}

	// Claim one
	if _, err := tracker.ClaimIssue(issue2.ID); err != nil {
		t.Fatalf("ClaimIssue failed: %v", err)
	}

	// Mark one as done
	if _, err := tracker.UpdateIssueState(issue3.ID, types.StateReleased); err != nil {
		t.Fatalf("UpdateIssueState failed: %v", err)
	}

	// Fetch should return only non-done issues
	issues, err := tracker.FetchIssues()
	if err != nil {
		t.Fatalf("FetchIssues failed: %v", err)
	}

	if len(issues) != 2 {
		t.Errorf("expected 2 issues, got %d", len(issues))
	}

	// Verify done issue is not in the list
	for _, issue := range issues {
		if issue.ID == issue3.ID {
			t.Error("done issue should not be in FetchIssues result")
		}
	}
}

func TestLocalTracker_CreateMultipleIssues(t *testing.T) {
	tmpDir := t.TempDir()

	tracker := New(Config{
		BoardDir:    tmpDir,
		IssuePrefix: "CB",
	})

	// Create issues
	issue1, err := tracker.CreateIssue("First", "Desc 1", nil)
	if err != nil {
		t.Fatalf("CreateIssue 1 failed: %v", err)
	}

	issue2, err := tracker.CreateIssue("Second", "Desc 2", nil)
	if err != nil {
		t.Fatalf("CreateIssue 2 failed: %v", err)
	}

	issue3, err := tracker.CreateIssue("Third", "Desc 3", nil)
	if err != nil {
		t.Fatalf("CreateIssue 3 failed: %v", err)
	}

	if issue1.ID != "CB-1" {
		t.Errorf("expected ID CB-1, got %q", issue1.ID)
	}
	if issue2.ID != "CB-2" {
		t.Errorf("expected ID CB-2, got %q", issue2.ID)
	}
	if issue3.ID != "CB-3" {
		t.Errorf("expected ID CB-3, got %q", issue3.ID)
	}
}

func TestLocalTracker_IssueStateConversions(t *testing.T) {
	tests := []struct {
		boardState    string
		expectedState types.IssueState
	}{
		{StateTodo, types.StateUnclaimed},
		{StateInProgress, types.StateRunning},
		{StateDone, types.StateReleased},
	}

	for _, tc := range tests {
		t.Run(tc.boardState, func(t *testing.T) {
			result := toIssueState(tc.boardState)
			if result != tc.expectedState {
				t.Errorf("toIssueState(%q) = %v, want %v", tc.boardState, result, tc.expectedState)
			}
		})
	}
}

func TestLocalTracker_BoardStateConversions(t *testing.T) {
	tests := []struct {
		issueState    types.IssueState
		expectedBoard string
	}{
		{types.StateUnclaimed, StateTodo},
		{types.StateClaimed, StateInProgress},
		{types.StateRunning, StateInProgress},
		{types.StateRetryQueued, StateTodo},
		{types.StateReleased, StateDone},
	}

	for _, tc := range tests {
		t.Run(tc.issueState.String(), func(t *testing.T) {
			result := toBoardState(tc.issueState)
			if result != tc.expectedBoard {
				t.Errorf("toBoardState(%v) = %q, want %q", tc.issueState, result, tc.expectedBoard)
			}
		})
	}
}
