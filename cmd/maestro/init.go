package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// defaultWorkflowTemplate is the WORKFLOW.md written by `maestro init`.
// The %s placeholder is filled with the project name (directory basename).
const defaultWorkflowTemplate = `---
# Maestro project configuration.
# This file lives at the project root and configures how the orchestrator
# runs on this repository.
#
# How to create issues:
#   maestro board create "Fix the login bug" --description "Details..."
#
# How to list all issues:
#   maestro board list --all
#
# How to start the orchestrator:
#   maestro

max_concurrency: 1
poll_interval_ms: 3000
max_retry_backoff_ms: 60000
agent_timeout_ms: 300000
stall_timeout_ms: 120000
tracker:
  type: internal
  board_dir: .maestro/projects/%s/board
  issue_prefix: CB
agent:
  type: opencode
opencode:
  binary_path: opencode serve
  port: 0
  profile: ""    # optional: your OpenCode profile name (~/.config/opencode/profiles/)
  agent: ""      # optional: your default agent name (e.g. build, coder, plan)
  # agents:       # optional: per-stage agent mapping
  #   plan: plan
  #   execute: execute
  #   verify: verify
workspace:
  base_dir: .
  branch_prefix: maestro/
---

# Task

{{ issue.title }}

{{ issue.description }}
`

// runInit sets up a project for maestro orchestration.
// It creates .maestro/projects/<name>/board, writes a default WORKFLOW.md, and
// ensures run directories are gitignored.
func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	force := fs.Bool("force", false, "overwrite existing .maestro/ directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Find the git repository root so we always initialize at the project root,
	// even when the command is run from a subdirectory.
	gitRoot, err := findGitRoot(".")
	if err != nil {
		return err
	}

	cwd, _ := os.Getwd()
	if cwd != gitRoot {
		fmt.Printf("Found git root at %s. Initializing there.\n\n", gitRoot)
	}

	projectName := filepath.Base(gitRoot)

	// Prevent overwriting an existing setup unless --force is given.
	maestroDir := filepath.Join(gitRoot, ".maestro")
	if _, err := os.Stat(maestroDir); err == nil && !*force {
		return errors.New(".maestro/ already exists — run `maestro init --force` to overwrite")
	}

	// When forcing, remove the old directory so we start fresh.
	if *force {
		if err := os.RemoveAll(maestroDir); err != nil {
			return fmt.Errorf("remove existing .maestro/: %w", err)
		}
	}

	// Create .maestro/projects/<name>/board/ and manifest.json.
	boardDir := filepath.Join(maestroDir, "projects", projectName, "board")
	if err := os.MkdirAll(boardDir, 0o755); err != nil {
		return fmt.Errorf("create board directory: %w", err)
	}
	if err := writeBoardManifest(boardDir); err != nil {
		return fmt.Errorf("write board manifest: %w", err)
	}

	// Write WORKFLOW.md at project root if it does not already exist.
	workflowPath := filepath.Join(gitRoot, "WORKFLOW.md")
	if _, err := os.Stat(workflowPath); os.IsNotExist(err) {
		workflowContent := fmt.Sprintf(defaultWorkflowTemplate, projectName)
		if err := os.WriteFile(workflowPath, []byte(workflowContent), 0o644); err != nil {
			return fmt.Errorf("write WORKFLOW.md: %w", err)
		}
		fmt.Printf("Created WORKFLOW.md\n")
	} else {
		fmt.Printf("WORKFLOW.md already exists — leaving it untouched\n")
	}

	// Ensure run directories are gitignored without hiding the board.
	if err := ensureGitignore(gitRoot); err != nil {
		return fmt.Errorf("update .gitignore: %w", err)
	}

	fmt.Printf("\nInitialized maestro in %s\n", gitRoot)
	fmt.Printf("  board:        %s\n", boardDir)
	fmt.Printf("  workflow:     %s\n", workflowPath)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  1. Edit WORKFLOW.md to set your OpenCode profile and agent\n")
	fmt.Printf("  2. Run `maestro board create \"<title>\"` to add an issue\n")
	fmt.Printf("  3. Run `maestro` to start the orchestrator\n")
	return nil
}

// writeBoardManifest creates the initial manifest.json in the board directory.
func writeBoardManifest(boardDir string) error {
	manifestPath := filepath.Join(boardDir, "manifest.json")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	manifest := fmt.Sprintf(`{
  "schema_version": "3",
  "issue_prefix": "CB",
  "next_issue_number": 1,
  "created_at": "%s",
  "updated_at": "%s"
}
`, now, now)
	return os.WriteFile(manifestPath, []byte(manifest), 0o644)
}

// ensureGitignore adds run-directory patterns to .gitignore so ephemeral
// diagnostics are hidden while the board files remain tracked.
func ensureGitignore(cwd string) error {
	gitignorePath := filepath.Join(cwd, ".gitignore")

	existing := ""
	if data, err := os.ReadFile(gitignorePath); err == nil {
		existing = string(data)
	}

	entries := []string{
		".maestro/projects/*/runs/",
		".maestro/projects/*/runs.old.*",
	}

	var missing []string
	for _, entry := range entries {
		if !strings.Contains(existing, entry) {
			missing = append(missing, entry)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	// If the file exists but does not end with a newline, prepend one.
	prefix := ""
	if len(existing) > 0 && !strings.HasSuffix(existing, "\n") {
		prefix = "\n"
	}

	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(prefix + "# Maestro run diagnostics (ephemeral)\n")
	if err != nil {
		return err
	}
	for _, entry := range missing {
		if _, err := f.WriteString(entry + "\n"); err != nil {
			return err
		}
	}
	return nil
}
