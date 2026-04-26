package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fatihkarahan/contrabass-pi/internal/config"
)

// runDoctor validates the local environment and project setup.
// It always runs all checks and reports pass/fail for each.
func runDoctor(args []string) error {
	var checks []checkResult

	// 1. Git repository
	gitDir, gitErr := exec.Command("git", "rev-parse", "--git-dir").CombinedOutput()
	checks = append(checks, checkResult{
		name:    "Git repository",
		passed:  gitErr == nil,
		detail:  strings.TrimSpace(string(gitDir)),
		hint:    "not a git repository (contrabass requires git for worktrees)",
	})

	// 2. Git user.name
	name, nameErr := exec.Command("git", "config", "user.name").CombinedOutput()
	checks = append(checks, checkResult{
		name:    "Git user.name",
		passed:  nameErr == nil && strings.TrimSpace(string(name)) != "",
		detail:  strings.TrimSpace(string(name)),
		hint:    "run: git config --global user.name \"Your Name\"",
	})

	// 3. Git user.email
	email, emailErr := exec.Command("git", "config", "user.email").CombinedOutput()
	checks = append(checks, checkResult{
		name:    "Git user.email",
		passed:  emailErr == nil && strings.TrimSpace(string(email)) != "",
		detail:  strings.TrimSpace(string(email)),
		hint:    "run: git config --global user.email \"you@example.com\"",
	})

	// 4. WORKFLOW.md exists and is parseable
	var cfg *config.Config
	var cfgPath string
	explicitConfig := *configPath != "WORKFLOW.md"
	if explicitConfig {
		cfgPath = *configPath
	} else {
		if projectRoot, err := findProjectRoot("."); err == nil {
			cfgPath = filepath.Join(projectRoot, "WORKFLOW.md")
			if _, err := os.Stat(cfgPath); err != nil {
				cfgPath = filepath.Join(projectRoot, ".contrabass", "WORKFLOW.md")
			}
		}
	}

	cfgFound := false
	if cfgPath != "" {
		if _, statErr := os.Stat(cfgPath); statErr == nil {
			cfgFound = true
			checks = append(checks, checkResult{
				name:   "WORKFLOW.md exists",
				passed: true,
				detail: cfgPath,
			})
			loaded, loadErr := config.Load(cfgPath)
			if loadErr == nil {
				cfg = loaded
				checks = append(checks, checkResult{
					name:   "WORKFLOW.md parseable",
					passed: true,
					detail: "valid configuration",
				})
			} else {
				checks = append(checks, checkResult{
					name:   "WORKFLOW.md parseable",
					passed: false,
					detail: loadErr.Error(),
					hint:   "fix syntax errors in WORKFLOW.md",
				})
			}
		}
	}
	if !cfgFound {
		checks = append(checks, checkResult{
			name:   "WORKFLOW.md exists",
			passed: false,
			detail: "not found",
			hint:   "run `contrabass init` to create a project",
		})
		checks = append(checks, checkResult{
			name:   "WORKFLOW.md parseable",
			passed: false,
			detail: "file missing",
			hint:   "run `contrabass init` to create a project",
		})
	}

	// 5. OpenCode binary on PATH
	binary := "opencode"
	if cfg != nil && cfg.OpenCode != nil && cfg.OpenCode.BinaryPath != "" {
		binary = cfg.OpenCode.BinaryPath
	}
	binaryName := binary
	if strings.Contains(binary, " ") {
		binaryName = strings.Fields(binary)[0]
	}
	binaryPath, lookErr := exec.LookPath(binaryName)
	checks = append(checks, checkResult{
		name:    "OpenCode binary",
		passed:  lookErr == nil,
		detail:  binaryPath,
		hint:    fmt.Sprintf("%q not found in PATH; install opencode or update opencode.binary_path in WORKFLOW.md", binaryName),
	})

	// 6. Board directory writable
	boardDir := ".contrabass/board"
	if cfg != nil && cfg.Tracker.BoardDir != "" {
		boardDir = cfg.Tracker.BoardDir
	}
	if !filepath.IsAbs(boardDir) && cfgPath != "" {
		boardDir = filepath.Join(filepath.Dir(cfgPath), boardDir)
	}
	writable := false
	if statErr := os.MkdirAll(boardDir, 0o755); statErr == nil {
		tmpFile := filepath.Join(boardDir, ".doctor-write-test")
		if f, createErr := os.Create(tmpFile); createErr == nil {
			f.Close()
			os.Remove(tmpFile)
			writable = true
		}
	}
	checks = append(checks, checkResult{
		name:    "Board directory writable",
		passed:  writable,
		detail:  boardDir,
		hint:    fmt.Sprintf("cannot write to %s; check permissions", boardDir),
	})

	// Print report
	fmt.Println("Contrabass environment check")
	fmt.Println()
	allPassed := true
	for _, c := range checks {
		marker := "✓"
		if !c.passed {
			marker = "✗"
			allPassed = false
		}
		fmt.Printf("  %s %-28s %s\n", marker, c.name, c.detail)
		if !c.passed && c.hint != "" {
			fmt.Printf("    → %s\n", c.hint)
		}
	}
	fmt.Println()
	if allPassed {
		fmt.Println("All checks passed. You're ready to run contrabass.")
	} else {
		fmt.Println("Some checks failed. Fix the issues above and run `contrabass doctor` again.")
	}
	return nil
}

type checkResult struct {
	name   string
	passed bool
	detail string
	hint   string
}
