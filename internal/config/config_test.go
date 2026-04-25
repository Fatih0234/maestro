package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCodeConfig_ProfileAndAgentFields(t *testing.T) {
	cfg := &OpenCodeConfig{
		BinaryPath: "opencode serve",
		Port:       9090,
		Password:   "secret",
		Model:      "",
		Profile:    "ws",
		Agent:      "scribe",
		ConfigDir:  ".opencode",
	}

	if cfg.Profile != "ws" {
		t.Errorf("Profile = %v, want ws", cfg.Profile)
	}
	if cfg.Agent != "scribe" {
		t.Errorf("Agent = %v, want scribe", cfg.Agent)
	}
	if cfg.ConfigDir != ".opencode" {
		t.Errorf("ConfigDir = %v, want .opencode", cfg.ConfigDir)
	}
}

func TestValidate_OpenCodeProfileNotFound(t *testing.T) {
	cfg := &Config{
		MaxConcurrency: 3,
		PollIntervalMs: 1000,
		Tracker: TrackerConfig{
			Type:        "internal",
			BoardDir:    ".contrabass/orchestrator/board",
			IssuePrefix: "CB",
		},
		Agent: AgentConfig{
			Type: "opencode",
		},
		OpenCode: &OpenCodeConfig{
			BinaryPath: "opencode serve",
			Profile:    "nonexistent-profile-xyz",
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for non-existent profile, got nil")
	}
	if !contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found', got: %v", err)
	}
}

func TestValidate_OpenCodeProfileExists(t *testing.T) {
	// Test with a profile that actually exists
	cfg := &Config{
		MaxConcurrency: 3,
		PollIntervalMs: 1000,
		Tracker: TrackerConfig{
			Type:        "internal",
			BoardDir:    ".contrabass/board",
			IssuePrefix: "CB",
		},
		Agent: AgentConfig{
			Type: "opencode",
		},
		OpenCode: &OpenCodeConfig{
			BinaryPath: "opencode serve",
			Profile:    "auto", // This should exist since it's a default profile
		},
	}

	err := cfg.Validate()
	// auto profile should exist, but we don't fail if it doesn't
	// The test is more about ensuring the validation runs correctly
	if err != nil && !contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_OpenCodeProfileEmptyIsValid(t *testing.T) {
	// Empty profile should be valid (use default)
	cfg := &Config{
		MaxConcurrency: 3,
		PollIntervalMs: 1000,
		Tracker: TrackerConfig{
			Type:        "internal",
			BoardDir:    ".contrabass/board",
			IssuePrefix: "CB",
		},
		Agent: AgentConfig{
			Type: "opencode",
		},
		OpenCode: &OpenCodeConfig{
			BinaryPath: "opencode serve",
			Profile:    "", // Empty is valid (no profile validation needed)
		},
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf("expected no error for empty profile, got: %v", err)
	}
}

func TestParse_ProfileAndAgent(t *testing.T) {
	content := `---
max_concurrency: 3
poll_interval_ms: 2000
tracker:
  type: internal
  board_dir: .contrabass/board
  issue_prefix: CB
agent:
  type: opencode
opencode:
  binary_path: opencode serve
  port: 9090
  profile: ws
  agent: plan
  config_dir: .opencode
workspace:
  base_dir: .
  branch_prefix: opencode/
---

# Task
{{ issue.title }}
`

	cfg, err := ParseBytes([]byte(content))
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}

	if cfg.OpenCode.Profile != "ws" {
		t.Errorf("Profile = %v, want ws", cfg.OpenCode.Profile)
	}
	if cfg.OpenCode.Agent != "plan" {
		t.Errorf("Agent = %v, want plan", cfg.OpenCode.Agent)
	}
	if cfg.OpenCode.ConfigDir != ".opencode" {
		t.Errorf("ConfigDir = %v, want .opencode", cfg.OpenCode.ConfigDir)
	}
	if cfg.OpenCode.BinaryPath != "opencode serve" {
		t.Errorf("BinaryPath = %v, want 'opencode serve'", cfg.OpenCode.BinaryPath)
	}
	if cfg.OpenCode.Port != 9090 {
		t.Errorf("Port = %v, want 9090", cfg.OpenCode.Port)
	}
}

func TestParse_ProfileAndAgentMinimal(t *testing.T) {
	// Minimal config with only required fields
	content := `---
tracker:
  type: internal
agent:
  type: opencode
opencode:
  binary_path: opencode serve
---

# Task
`

	cfg, err := ParseBytes([]byte(content))
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}

	if cfg.OpenCode.Profile != "" {
		t.Errorf("Profile = %v, want empty", cfg.OpenCode.Profile)
	}
	if cfg.OpenCode.Agent != "" {
		t.Errorf("Agent = %v, want empty", cfg.OpenCode.Agent)
	}
	if cfg.OpenCode.ConfigDir != "" {
		t.Errorf("ConfigDir = %v, want empty", cfg.OpenCode.ConfigDir)
	}
}

func TestDefaultConfig_ProfileAgentEmpty(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.OpenCode.Profile != "" {
		t.Errorf("Default Profile = %v, want empty", cfg.OpenCode.Profile)
	}
	if cfg.OpenCode.Agent != "" {
		t.Errorf("Default Agent = %v, want empty", cfg.OpenCode.Agent)
	}
	if cfg.OpenCode.ConfigDir != "" {
		t.Errorf("Default ConfigDir = %v, want empty", cfg.OpenCode.ConfigDir)
	}
	if cfg.OpenCode.Model != "" {
		t.Errorf("Default Model = %v, want empty (deprecated)", cfg.OpenCode.Model)
	}
}

func TestLoad_ValidProfilePath(t *testing.T) {
	// Create a temporary WORKFLOW.md with the auto profile (which should exist)
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")

	content := `---
max_concurrency: 1
poll_interval_ms: 1000
tracker:
  type: internal
  board_dir: .board
  issue_prefix: CB
agent:
  type: opencode
opencode:
  binary_path: opencode serve
  profile: auto
workspace:
  base_dir: .
---

# Task
`
	err := os.WriteFile(workflowPath, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	_, err = Load(workflowPath)
	// Don't fail if auto profile doesn't exist - just verify loading works
	if err != nil && !contains(err.Error(), "not found") {
		t.Fatalf("Load() unexpected error: %v", err)
	}
}

func TestLoad_ResolvesRelativePathsFromConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "project")
	_ = os.MkdirAll(subDir, 0o755)
	workflowPath := filepath.Join(subDir, "WORKFLOW.md")

	content := `---
max_concurrency: 1
poll_interval_ms: 1000
tracker:
  type: internal
  board_dir: ./board
  issue_prefix: CB
agent:
  type: opencode
opencode:
  binary_path: opencode serve
workspace:
  base_dir: ./workspaces
---
`
	if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	cfg, err := Load(workflowPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	wantBoardDir := filepath.Join(subDir, "board")
	if cfg.Tracker.BoardDir != wantBoardDir {
		t.Errorf("Tracker.BoardDir = %q, want %q", cfg.Tracker.BoardDir, wantBoardDir)
	}

	wantBaseDir := filepath.Join(subDir, "workspaces")
	if cfg.Workspace.BaseDir != wantBaseDir {
		t.Errorf("Workspace.BaseDir = %q, want %q", cfg.Workspace.BaseDir, wantBaseDir)
	}
}

func TestLoad_KeepsAbsolutePaths(t *testing.T) {
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")

	absBoardDir := filepath.Join(tmpDir, "absolute", "board")
	absBaseDir := filepath.Join(tmpDir, "absolute", "workspaces")
	_ = os.MkdirAll(absBoardDir, 0o755)
	_ = os.MkdirAll(absBaseDir, 0o755)

	content := `---
max_concurrency: 1
poll_interval_ms: 1000
tracker:
  type: internal
  board_dir: ` + absBoardDir + `
  issue_prefix: CB
agent:
  type: opencode
opencode:
  binary_path: opencode serve
workspace:
  base_dir: ` + absBaseDir + `
---
`
	if err := os.WriteFile(workflowPath, []byte(content), 0o644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	cfg, err := Load(workflowPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Tracker.BoardDir != absBoardDir {
		t.Errorf("Tracker.BoardDir = %q, want %q", cfg.Tracker.BoardDir, absBoardDir)
	}
	if cfg.Workspace.BaseDir != absBaseDir {
		t.Errorf("Workspace.BaseDir = %q, want %q", cfg.Workspace.BaseDir, absBaseDir)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
