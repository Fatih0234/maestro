//go:build ignore

// Integration test: End-to-end flow with real data
// Tests: config parsing, local board, workspace creation, prompt rendering
// Requires: opencode binary, git repo

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/agent"
	"github.com/fatihkarahan/contrabass-pi/internal/config"
	"github.com/fatihkarahan/contrabass-pi/internal/orchestrator"
	"github.com/fatihkarahan/contrabass-pi/internal/tracker"
	"github.com/fatihkarahan/contrabass-pi/internal/types"
	"github.com/fatihkarahan/contrabass-pi/internal/workspace"
)

// dummyRunner is a no-op agent runner for testing prompt rendering
type dummyRunner struct{}

func (d *dummyRunner) Start(ctx context.Context, issue types.Issue, workspace, prompt string) (*types.AgentProcess, error) {
	return nil, errors.New("not used - dummy runner")
}
func (d *dummyRunner) Stop(proc *types.AgentProcess) error { return nil }
func (d *dummyRunner) Close() error                        { return nil }

func main() {
	fmt.Println("═══════════════════════════════════════════════════════")
	fmt.Println("   CONTRABASS INTEGRATION TEST")
	fmt.Println("═══════════════════════════════════════════════════════")
	fmt.Println()

	// Handle Ctrl+C gracefully
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\n⚠️  Received signal, shutting down...")
		cancel()
	}()

	// ─────────────────────────────────────────────────────────────
	// STEP 1: Load Config
	// ─────────────────────────────────────────────────────────────
	fmt.Println("📋 STEP 1: Loading Configuration")
	fmt.Println("───────────────────────────────────")

	configPath := ".contrabass/WORKFLOW.md"
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Printf("❌ Failed to load config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Config loaded from %s\n", configPath)
	fmt.Printf("   MaxConcurrency: %d\n", cfg.MaxConcurrency)
	fmt.Printf("   PollIntervalMs: %d\n", cfg.PollIntervalMs)
	fmt.Printf("   Tracker: %s (board_dir: %s)\n", cfg.Tracker.Type, cfg.Tracker.BoardDir)
	fmt.Printf("   Agent: %s (binary: %s)\n", cfg.Agent.Type, cfg.OpenCode.BinaryPath)
	fmt.Printf("   Profile: %s, Agent: %s\n", cfg.OpenCode.Profile, cfg.OpenCode.Agent)
	fmt.Printf("   AgentTimeout: %dms, StallTimeout: %dms\n", cfg.AgentTimeoutMs, cfg.StallTimeoutMs)
	fmt.Println()

	// ─────────────────────────────────────────────────────────────
	// STEP 2: Initialize Components
	// ─────────────────────────────────────────────────────────────
	fmt.Println("📋 STEP 2: Initializing Components")
	fmt.Println("───────────────────────────────────")

	// Create tracker
	trackerCfg := tracker.Config{
		BoardDir:    cfg.Tracker.BoardDir,
		IssuePrefix: cfg.Tracker.IssuePrefix,
	}
	localTracker := tracker.New(trackerCfg)

	if err := localTracker.EnsureBoard(); err != nil {
		fmt.Printf("❌ Failed to initialize board: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Local tracker initialized (board: %s)\n", localTracker.BoardDir())

	// Create workspace manager
	wsCfg := workspace.Config{
		BaseDir:      cfg.Workspace.BaseDir,
		WorktreeDir:  "workspaces",
		BranchPrefix: cfg.Workspace.BranchPrefix,
	}
	wsManager := workspace.New(wsCfg)
	fmt.Printf("✅ Workspace manager initialized (base: %s)\n", wsManager.BaseDir())
	fmt.Println()

	// ─────────────────────────────────────────────────────────────
	// STEP 3: Fetch Issues from Local Board
	// ─────────────────────────────────────────────────────────────
	fmt.Println("📋 STEP 3: Fetching Issues from Local Board")
	fmt.Println("───────────────────────────────────")

	issues, err := localTracker.FetchIssues()
	if err != nil {
		fmt.Printf("❌ Failed to fetch issues: %v\n", err)
		os.Exit(1)
	}

	if len(issues) == 0 {
		fmt.Println("⚠️  No issues found in board. Creating test issue...")
		newIssue, err := localTracker.CreateIssue(
			"Write hello world",
			"Create a file called hello.txt with content: Hello, Contrabass!",
			[]string{"test", "good-first-issue"},
		)
		if err != nil {
			fmt.Printf("❌ Failed to create issue: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Created issue: %s - %s\n", newIssue.ID, newIssue.Title)
		issues = append(issues, newIssue)
	} else {
		fmt.Printf("✅ Found %d issue(s)\n", len(issues))
	}

	for _, issue := range issues {
		stateStr := issue.State.String()
		if issue.State == types.StateUnclaimed {
			stateStr = "todo"
		}
		fmt.Printf("   • %s: %s [%s] %v\n", issue.ID, issue.Title, stateStr, issue.Labels)
	}
	fmt.Println()

	// ─────────────────────────────────────────────────────────────
	// STEP 4: Test Workspace Creation
	// ─────────────────────────────────────────────────────────────
	fmt.Println("📋 STEP 4: Testing Workspace Creation")
	fmt.Println("───────────────────────────────────")

	testIssue := issues[0]
	wsPath, err := wsManager.Create(ctx, testIssue)
	if err != nil {
		fmt.Printf("❌ Failed to create workspace: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Workspace created: %s\n", wsPath)

	// Check git worktree
	fmt.Println()
	fmt.Println("   Git worktree verification:")
	stdout, _, _ := runGit("worktree", "list")
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for _, line := range lines {
		fmt.Printf("   %s\n", line)
	}
	fmt.Println()

	// ─────────────────────────────────────────────────────────────
	// STEP 5: Manual Prompt Rendering (same logic as orchestrator.buildPrompt)
	// ─────────────────────────────────────────────────────────────
	fmt.Println("📋 STEP 5: Testing Prompt Rendering (Manual)")
	fmt.Println("───────────────────────────────────")

	// Replicate the template substitution logic from orchestrator.buildPrompt
	template := cfg.Content
	template = strings.ReplaceAll(template, "{{ issue.id }}", testIssue.ID)
	template = strings.ReplaceAll(template, "{{ issue.identifier }}", testIssue.Identifier)
	template = strings.ReplaceAll(template, "{{ issue.title }}", testIssue.Title)
	template = strings.ReplaceAll(template, "{{ issue.description }}", testIssue.Description)

	// Handle labels - comma-separated
	labels := ""
	if len(testIssue.Labels) > 0 {
		labels = strings.Join(testIssue.Labels, ", ")
	}
	template = strings.ReplaceAll(template, "{{ issue.labels }}", labels)

	renderedPrompt := strings.TrimSpace(template)

	fmt.Println("Rendered prompt:")
	fmt.Println("───────────────────────────────────")
	for _, line := range strings.Split(renderedPrompt, "\n") {
		fmt.Printf("   %s\n", line)
	}
	fmt.Println("───────────────────────────────────")
	fmt.Println()

	// ─────────────────────────────────────────────────────────────
	// STEP 6: Start Orchestrator with Real OpenCode Agent
	// ─────────────────────────────────────────────────────────────
	fmt.Println("📋 STEP 6: Starting Orchestrator (End-to-End Test)")
	fmt.Println("───────────────────────────────────")
	fmt.Println("⏳ Starting... Press Ctrl+C to stop gracefully.")
	fmt.Println()
	fmt.Println("Events:")
	fmt.Println("───────────────────────────────────")

	// Create real agent runner
	agentRunner := agent.NewOpenCodeRunner(
		cfg.OpenCode.BinaryPath,
		cfg.OpenCode.Port,
		cfg.OpenCode.Password,
		"", // username
		30*time.Second,
		cfg.OpenCode.Profile,
		cfg.OpenCode.Agent,
		cfg.OpenCode.ConfigDir,
	)

	// Create orchestrator with real components
	orch := orchestrator.New(cfg, localTracker, wsManager, agentRunner)

	// Event counter
	eventCount := 0

	// Start event collector in background
	eventCh := orch.Events
	eventDone := make(chan struct{})
	go func() {
		for event := range eventCh {
			eventCount++
			ts := event.Timestamp.Format("15:04:05.000")
			fmt.Printf("   [%s] %s", ts, event.Type)
			if event.IssueID != "" {
				fmt.Printf(" (issue: %s)", event.IssueID)
			}
			if event.Payload != nil {
				switch p := event.Payload.(type) {
				case types.Issue:
					fmt.Printf(" - %s", p.Title)
					fmt.Printf(" - %v", p)
					fmt.Printf(" [pid=%d, session=%s]", p.PID, p.SessionID)
					fmt.Printf(" [in=%d, out=%d]", p.TokensIn, p.TokensOut)
				}
			}
			fmt.Println()
		}
		close(eventDone)
	}()

	// Run orchestrator in background
	orchDone := make(chan error, 1)
	go func() {
		orchDone <- orch.Run()
	}()

	// Wait for orchestrator to do some work (poll at least once)
	time.Sleep(5 * time.Second)

	// Check board state
	issues, _ = localTracker.FetchIssues()
	fmt.Println()
	fmt.Println("───────────────────────────────────")
	fmt.Println("📊 Board State After 5 seconds:")
	for _, issue := range issues {
		stateStr := issue.State.String()
		if issue.State == types.StateUnclaimed {
			stateStr = "todo"
		}
		fmt.Printf("   • %s: %s [%s]\n", issue.ID, issue.Title, stateStr)
	}
	fmt.Println()

	// Check workspaces
	fmt.Println("📁 Workspaces Created:")
	wsList := wsManager.List()
	for _, id := range wsList {
		fmt.Printf("   • %s\n", id)
	}
	fmt.Println()

	// Graceful shutdown
	fmt.Println("🛑 Shutting down orchestrator...")
	orch.Stop()

	// Wait for orchestrator to stop
	select {
	case <-orchDone:
		fmt.Println("✅ Orchestrator stopped cleanly")
	case <-time.After(5 * time.Second):
		fmt.Println("⚠️  Orchestrator shutdown timed out")
	}

	// Cleanup workspaces
	fmt.Println()
	fmt.Println("🧹 Cleanup:")
	for _, id := range wsList {
		if err := wsManager.Cleanup(ctx, id); err != nil {
			fmt.Printf("   ❌ Failed to cleanup %s: %v\n", id, err)
		} else {
			fmt.Printf("   ✅ Cleaned up %s\n", id)
		}
	}

	// Close agent runner
	agentRunner.Close()

	// Verify cleanup
	fmt.Println()
	fmt.Println("✅ Integration test complete!")
	fmt.Printf("   Total events: %d\n", eventCount)
}

func runGit(args ...string) (string, string, int) {
	cmd := exec.Command("git", args...)
	cmd.Dir = "."
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", string(output), 1
	}
	return string(output), "", 0
}
