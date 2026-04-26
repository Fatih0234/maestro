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

// defaultWorkflowTemplate is the WORKFLOW.md written by `contrabass init`.
const defaultWorkflowTemplate = `---
# Contrabass project configuration.
# This file lives at the project root and configures how the orchestrator
# runs on this repository.
#
# How to create issues:
#   contrabass board create "Fix the login bug" --description "Details..."
#
# How to list all issues:
#   contrabass board list --all
#
# How to start the orchestrator:
#   contrabass

max_concurrency: 1
poll_interval_ms: 3000
max_retry_backoff_ms: 60000
agent_timeout_ms: 300000
stall_timeout_ms: 120000
tracker:
  type: internal
  board_dir: .contrabass/board
  issue_prefix: CB
agent:
  type: opencode
opencode:
  binary_path: opencode serve
  port: 0
  profile: ""    # optional: your OpenCode profile name (~/.config/opencode/profiles/)
  agent: ""      # optional: your default agent name (e.g. build, coder, plan)
workspace:
  base_dir: .
  branch_prefix: contrabass/
---

# Task

{{ issue.title }}

{{ issue.description }}
`

// runInit sets up a project for contrabass orchestration.
// It creates .contrabass/board, writes a default WORKFLOW.md, and
// adds .contrabass/ to .gitignore.
func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	force := fs.Bool("force", false, "overwrite existing .contrabass/ directory")
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

	// Prevent overwriting an existing setup unless --force is given.
	contrabassDir := filepath.Join(gitRoot, ".contrabass")
	if _, err := os.Stat(contrabassDir); err == nil && !*force {
		return errors.New(".contrabass/ already exists — run `contrabass init --force` to overwrite")
	}

	// When forcing, remove the old directory so we start fresh.
	if *force {
		if err := os.RemoveAll(contrabassDir); err != nil {
			return fmt.Errorf("remove existing .contrabass/: %w", err)
		}
	}

	// Create .contrabass/board/ and manifest.json.
	boardDir := filepath.Join(contrabassDir, "board")
	if err := os.MkdirAll(boardDir, 0o755); err != nil {
		return fmt.Errorf("create board directory: %w", err)
	}
	if err := writeBoardManifest(boardDir); err != nil {
		return fmt.Errorf("write board manifest: %w", err)
	}

	// Write WORKFLOW.md at project root if it does not already exist.
	workflowPath := filepath.Join(gitRoot, "WORKFLOW.md")
	if _, err := os.Stat(workflowPath); os.IsNotExist(err) {
		if err := os.WriteFile(workflowPath, []byte(defaultWorkflowTemplate), 0o644); err != nil {
			return fmt.Errorf("write WORKFLOW.md: %w", err)
		}
		fmt.Printf("Created WORKFLOW.md\n")
	} else {
		fmt.Printf("WORKFLOW.md already exists — leaving it untouched\n")
	}

	// Ensure .contrabass/ is in .gitignore.
	if err := ensureGitignore(gitRoot); err != nil {
		return fmt.Errorf("update .gitignore: %w", err)
	}

	fmt.Printf("\nInitialized contrabass in %s\n", gitRoot)
	fmt.Printf("  board:        %s\n", boardDir)
	fmt.Printf("  workflow:     %s\n", workflowPath)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  1. Edit WORKFLOW.md to set your OpenCode profile and agent\n")
	fmt.Printf("  2. Run `contrabass board create \"<title>\"` to add an issue\n")
	fmt.Printf("  3. Run `contrabass` to start the orchestrator\n")
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

// ensureGitignore adds .contrabass/ to .gitignore if it is not already present.
func ensureGitignore(cwd string) error {
	gitignorePath := filepath.Join(cwd, ".gitignore")

	existing := ""
	if data, err := os.ReadFile(gitignorePath); err == nil {
		existing = string(data)
	}

	if strings.Contains(existing, ".contrabass/") {
		return nil // already ignored
	}

	// If the file exists but does not end with a newline, prepend one.
	entry := ".contrabass/\n"
	if len(existing) > 0 && !strings.HasSuffix(existing, "\n") {
		entry = "\n" + entry
	}

	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(entry)
	return err
}
