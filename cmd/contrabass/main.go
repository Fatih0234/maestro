// Package main is the CLI entry point for Contrabass.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/fatihkarahan/contrabass-pi/internal/agent"
	"github.com/fatihkarahan/contrabass-pi/internal/config"
	"github.com/fatihkarahan/contrabass-pi/internal/diagnostics"
	"github.com/fatihkarahan/contrabass-pi/internal/orchestrator"
	"github.com/fatihkarahan/contrabass-pi/internal/tui"
	"github.com/fatihkarahan/contrabass-pi/internal/tracker"
	"github.com/fatihkarahan/contrabass-pi/internal/workspace"
)

var (
	configPath = flag.String("config", "WORKFLOW.md", "path to WORKFLOW.md")
	noTUI      = flag.Bool("no-tui", false, "run without TUI (headless mode)")
	dryRun     = flag.Bool("dry-run", false, "exit after first poll cycle")
	logLevel   = flag.String("log-level", "info", "log level (debug/info/warn/error)")
)

func main() {
	flag.Parse()

	// Load config
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	log.Printf("Starting Contrabass with config: %s", *configPath)
	log.Printf("  max_concurrency: %d", cfg.MaxConcurrency)
	log.Printf("  poll_interval: %dms", cfg.PollIntervalMs)

	// Create tracker
	boardDir := cfg.Tracker.BoardDir
	if boardDir == "" {
		boardDir = ".contrabass/board"
	}
	tr := tracker.New(tracker.Config{
		BoardDir:    boardDir,
		IssuePrefix: cfg.Tracker.IssuePrefix,
	})

	// Create workspace manager
	wsMgr := workspace.New(workspace.Config{
		BaseDir:      cfg.Workspace.BaseDir,
		BranchPrefix: cfg.Workspace.BranchPrefix,
	})

	// Create agent runner (OpenCode)
	agentRunner := agent.NewOpenCodeRunner(
		cfg.OpenCode.BinaryPath,
		cfg.OpenCode.Port,
		cfg.OpenCode.Password,
		"", // username - not used currently
		30*time.Second,
		cfg.OpenCode.Profile,
		cfg.OpenCode.Agent,
		cfg.OpenCode.ConfigDir,
	)

	// Create persistent diagnostics recorder
	recorder, err := diagnostics.NewRecorder(boardDir)
	if err != nil {
		log.Fatalf("failed to initialize diagnostics recorder: %v", err)
	}
	defer recorder.Close()

	// Create orchestrator
	orch := orchestrator.New(cfg, tr, wsMgr, agentRunner)
	orch.SetRecorder(recorder)

	// Context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start the TUI if enabled
	if !*noTUI {
		runWithTUI(ctx, orch, sigChan)
	} else {
		runHeadless(ctx, orch, sigChan)
	}
}

// runWithTUI starts the orchestrator with a Bubble Tea TUI.
func runWithTUI(ctx context.Context, orch *orchestrator.Orchestrator, sigChan <-chan os.Signal) {
	// Create TUI model
	tuiModel := tui.NewModel()

	// Create and start the Bubble Tea program
	p := tea.NewProgram(tuiModel, tea.WithAltScreen())

	// Start event bridge - send orchestrator events to TUI
	tui.StartEventBridge(ctx, p, orch.Events)

	// Start orchestrator in background
	errChan := make(chan error, 1)
	go func() {
		if err := orch.Run(); err != nil {
			errChan <- err
		}
	}()

	// Run TUI in main goroutine
	go func() {
		if err := p.Start(); err != nil {
			errChan <- err
		}
	}()

	// Wait for signal or error
	select {
	case sig := <-sigChan:
		log.Printf("Received signal: %v", sig)
		orch.Stop()
		p.Quit()
	case err := <-errChan:
		if err != nil {
			log.Printf("Error: %v", err)
		}
	}
}

// runHeadless runs the orchestrator without TUI, logging to stdout.
func runHeadless(ctx context.Context, orch *orchestrator.Orchestrator, sigChan <-chan os.Signal) {
	// Start event log goroutine
	go func() {
		for event := range orch.Events {
			timestamp := event.Timestamp.Format("15:04:05")
			if event.IssueID != "" {
				fmt.Printf("[%s] %s %s\n", timestamp, event.IssueID, event.Type)
			} else {
				fmt.Printf("[%s] %s\n", timestamp, event.Type)
			}
		}
	}()

	// Handle signals
	go func() {
		<-sigChan
		log.Println("Shutting down...")
		orch.Stop()
	}()

	// Run orchestrator
	if err := orch.Run(); err != nil {
		log.Printf("Orchestrator error: %v", err)
	}
}