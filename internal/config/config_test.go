package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseBytes_FullConfig(t *testing.T) {
	content := `---
max_concurrency: 5
poll_interval_ms: 5000
tracker:
  type: internal
  board_dir: .board
  issue_prefix: CB
agent:
  type: opencode
opencode:
  binary_path: opencode serve
  port: 9090
workspace:
  base_dir: workspaces
  branch_prefix: feature/
---

# Task

{{ issue.title }}
`

	cfg, err := ParseBytes([]byte(content))
	if err != nil {
		t.Fatalf("ParseBytes failed: %v", err)
	}

	// Verify parsed values
	if cfg.MaxConcurrency != 5 {
		t.Errorf("MaxConcurrency = %d, want 5", cfg.MaxConcurrency)
	}
	if cfg.PollIntervalMs != 5000 {
		t.Errorf("PollIntervalMs = %d, want 5000", cfg.PollIntervalMs)
	}
	if cfg.Tracker.Type != "internal" {
		t.Errorf("Tracker.Type = %q, want 'internal'", cfg.Tracker.Type)
	}
	if cfg.Tracker.BoardDir != ".board" {
		t.Errorf("Tracker.BoardDir = %q, want '.board'", cfg.Tracker.BoardDir)
	}
	if cfg.Tracker.IssuePrefix != "CB" {
		t.Errorf("Tracker.IssuePrefix = %q, want 'CB'", cfg.Tracker.IssuePrefix)
	}
	if cfg.Agent.Type != "opencode" {
		t.Errorf("Agent.Type = %q, want 'opencode'", cfg.Agent.Type)
	}
	if cfg.OpenCode == nil {
		t.Fatal("OpenCode config is nil")
	}
	if cfg.OpenCode.BinaryPath != "opencode serve" {
		t.Errorf("OpenCode.BinaryPath = %q, want 'opencode serve'", cfg.OpenCode.BinaryPath)
	}
	if cfg.OpenCode.Port != 9090 {
		t.Errorf("OpenCode.Port = %d, want 9090", cfg.OpenCode.Port)
	}
	if cfg.Workspace.BaseDir != "workspaces" {
		t.Errorf("Workspace.BaseDir = %q, want 'workspaces'", cfg.Workspace.BaseDir)
	}
	if cfg.Workspace.BranchPrefix != "feature/" {
		t.Errorf("Workspace.BranchPrefix = %q, want 'feature/'", cfg.Workspace.BranchPrefix)
	}
	if cfg.Content != "# Task\n\n{{ issue.title }}" {
		t.Errorf("Content = %q, unexpected", cfg.Content)
	}
}

func TestParseBytes_NoFrontMatter(t *testing.T) {
	content := `# Just Markdown

No YAML front matter here.
`

	cfg, err := ParseBytes([]byte(content))
	if err != nil {
		t.Fatalf("ParseBytes failed: %v", err)
	}

	// Should use defaults
	if cfg.MaxConcurrency != 3 {
		t.Errorf("MaxConcurrency = %d, want default 3", cfg.MaxConcurrency)
	}
	if cfg.Tracker.Type != "internal" {
		t.Errorf("Tracker.Type = %q, want default 'internal'", cfg.Tracker.Type)
	}
	// TrimSpace is applied to content
	if cfg.Content != "# Just Markdown\n\nNo YAML front matter here." {
		t.Errorf("Content = %q, unexpected", cfg.Content)
	}
}

func TestParseBytes_EmptyContent(t *testing.T) {
	cfg, err := ParseBytes([]byte(""))
	if err != nil {
		t.Fatalf("ParseBytes failed: %v", err)
	}

	if cfg.Content != "" {
		t.Errorf("Content = %q, want empty", cfg.Content)
	}
}

func TestParseBytes_PartialYAML(t *testing.T) {
	// Only partial config, rest should be defaults
	content := `---
max_concurrency: 10
---
# Task
`
	cfg, err := ParseBytes([]byte(content))
	if err != nil {
		t.Fatalf("ParseBytes failed: %v", err)
	}

	if cfg.MaxConcurrency != 10 {
		t.Errorf("MaxConcurrency = %d, want 10", cfg.MaxConcurrency)
	}
	// Default should still apply for unspecified fields
	if cfg.PollIntervalMs != 30000 {
		t.Errorf("PollIntervalMs = %d, want default 30000", cfg.PollIntervalMs)
	}
}

func TestParse_File(t *testing.T) {
	// Create a temp file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "WORKFLOW.md")

	content := `---
max_concurrency: 2
agent:
  type: codex
codex:
  binary_path: codex app-server
  approval_policy: auto-edit
---

# Task
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	cfg, err := Parse(configPath)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.MaxConcurrency != 2 {
		t.Errorf("MaxConcurrency = %d, want 2", cfg.MaxConcurrency)
	}
	if cfg.Agent.Type != "codex" {
		t.Errorf("Agent.Type = %q, want 'codex'", cfg.Agent.Type)
	}
	if cfg.Codex == nil {
		t.Fatal("Codex config is nil")
	}
	if cfg.Codex.ApprovalPolicy != "auto-edit" {
		t.Errorf("Codex.ApprovalPolicy = %q, want 'auto-edit'", cfg.Codex.ApprovalPolicy)
	}
}

func TestParse_FileNotFound(t *testing.T) {
	_, err := Parse("/nonexistent/path/WORKFLOW.md")
	if err == nil {
		t.Error("Expected error for nonexistent file, got nil")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.MaxConcurrency != 3 {
		t.Errorf("MaxConcurrency = %d, want 3", cfg.MaxConcurrency)
	}
	if cfg.PollIntervalMs != 30000 {
		t.Errorf("PollIntervalMs = %d, want 30000", cfg.PollIntervalMs)
	}
	if cfg.Tracker.Type != "internal" {
		t.Errorf("Tracker.Type = %q, want 'internal'", cfg.Tracker.Type)
	}
	if cfg.Agent.Type != "opencode" {
		t.Errorf("Agent.Type = %q, want 'opencode'", cfg.Agent.Type)
	}
	if cfg.OpenCode == nil {
		t.Fatal("OpenCode config should not be nil")
	}
	if cfg.Workspace.BaseDir != "." {
		t.Errorf("Workspace.BaseDir = %q, want '.'", cfg.Workspace.BaseDir)
	}
}

func TestValidate_Valid(t *testing.T) {
	cfg := &Config{
		MaxConcurrency: 3,
		PollIntervalMs: 30000,
		Tracker: TrackerConfig{Type: "internal"},
		Agent:   AgentConfig{Type: "opencode"},
		OpenCode: &OpenCodeConfig{BinaryPath: "opencode serve"},
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate failed for valid config: %v", err)
	}
}

func TestValidate_InvalidConcurrency(t *testing.T) {
	cfg := &Config{
		MaxConcurrency: 0, // Invalid
		PollIntervalMs: 30000,
		Tracker:        TrackerConfig{Type: "internal"},
		Agent:          AgentConfig{Type: "opencode"},
		OpenCode:       &OpenCodeConfig{BinaryPath: "opencode serve"},
	}

	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for invalid concurrency, got nil")
	}
}

func TestValidate_MissingTrackerType(t *testing.T) {
	cfg := &Config{
		MaxConcurrency: 3,
		PollIntervalMs: 30000,
		Tracker:        TrackerConfig{Type: ""}, // Missing
		Agent:          AgentConfig{Type: "opencode"},
		OpenCode:       &OpenCodeConfig{BinaryPath: "opencode serve"},
	}

	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for missing tracker type, got nil")
	}
}

func TestValidate_MissingOpenCodeConfig(t *testing.T) {
	cfg := &Config{
		MaxConcurrency: 3,
		PollIntervalMs: 30000,
		Tracker:        TrackerConfig{Type: "internal"},
		Agent:          AgentConfig{Type: "opencode"},
		OpenCode:       nil, // Missing
	}

	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for missing opencode config, got nil")
	}
}

func TestValidate_EmptyOpenCodeBinaryPath(t *testing.T) {
	cfg := &Config{
		MaxConcurrency: 3,
		PollIntervalMs: 30000,
		Tracker:        TrackerConfig{Type: "internal"},
		Agent:          AgentConfig{Type: "opencode"},
		OpenCode:       &OpenCodeConfig{BinaryPath: ""}, // Empty path
	}

	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for empty opencode binary_path, got nil")
	}
}

func TestValidate_EmptyCodexBinaryPath(t *testing.T) {
	cfg := &Config{
		MaxConcurrency: 3,
		PollIntervalMs: 30000,
		Tracker:        TrackerConfig{Type: "internal"},
		Agent:          AgentConfig{Type: "codex"},
		Codex:          &CodexConfig{BinaryPath: ""}, // Empty path
	}

	if err := cfg.Validate(); err == nil {
		t.Error("Expected error for empty codex binary_path, got nil")
	}
}

func TestResolvePaths(t *testing.T) {
	cfg := &Config{
		Workspace: WorkspaceConfig{
			BaseDir: "workspaces",
		},
		Tracker: TrackerConfig{
			BoardDir: ".board",
		},
	}

	baseDir := "/project"
	cfg.ResolvePaths(baseDir)

	if cfg.Workspace.BaseDir != "/project/workspaces" {
		t.Errorf("Workspace.BaseDir = %q, want '/project/workspaces'", cfg.Workspace.BaseDir)
	}
	if cfg.Tracker.BoardDir != "/project/.board" {
		t.Errorf("Tracker.BoardDir = %q, want '/project/.board'", cfg.Tracker.BoardDir)
	}
}

func TestExtractFrontMatter(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantFront   string
		wantBody    string
		wantFound   bool
	}{
		{
			name:      "standard front matter",
			content:   "---\nkey: value\n---\nbody",
			wantFront: "key: value",
			wantBody:  "\nbody", // Body includes leading newline after closing ---
			wantFound: true,
		},
		{
			name:      "front matter with newlines",
			content:   "---\nkey1: val1\nkey2: val2\n---\n\n# Title\n\nbody",
			wantFront: "key1: val1\nkey2: val2",
			wantBody:  "\n\n# Title\n\nbody", // Body includes newlines after closing ---
			wantFound: true,
		},
		{
			name:      "no front matter",
			content:   "# Just markdown\n\nno yaml here",
			wantFront: "",
			wantBody:  "# Just markdown\n\nno yaml here",
			wantFound: false,
		},
		{
			name:      "empty front matter marker only", // Not valid YAML, so treated as no front matter
			content:   "---\n---\nbody",
			wantFront: "",
			wantBody:  "---\n---\nbody",
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			front, body, found := extractFrontMatter(tt.content)
			if found != tt.wantFound {
				t.Errorf("extractFrontMatter found = %v, want %v", found, tt.wantFound)
			}
			if front != tt.wantFront {
				t.Errorf("extractFrontMatter front = %q, want %q", front, tt.wantFront)
			}
			if body != tt.wantBody {
				t.Errorf("extractFrontMatter body = %q, want %q", body, tt.wantBody)
			}
		})
	}
}
