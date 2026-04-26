// Package main is the CLI entry point for Contrabass.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fatihkarahan/contrabass-pi/internal/agent"
	"github.com/fatihkarahan/contrabass-pi/internal/config"
	"github.com/fatihkarahan/contrabass-pi/internal/diagnostics"
	"github.com/fatihkarahan/contrabass-pi/internal/orchestrator"
	"github.com/fatihkarahan/contrabass-pi/internal/tracker"
	"github.com/fatihkarahan/contrabass-pi/internal/tui"
	"github.com/fatihkarahan/contrabass-pi/internal/types"
	"github.com/fatihkarahan/contrabass-pi/internal/web"
	"github.com/fatihkarahan/contrabass-pi/internal/workspace"
)

var (
	configPath = flag.String("config", "WORKFLOW.md", "path to WORKFLOW.md")
	noTUI      = flag.Bool("no-tui", false, "run without TUI (headless mode)")
	dryRun     = flag.Bool("dry-run", false, "exit after first poll cycle")
	logLevel   = flag.String("log-level", "info", "log level (debug/info/warn/error)")
	port       = flag.Int("port", 0, "HTTP API port (0 = disabled)")
)

const tuiShutdownTimeout = 5 * time.Second

type logSeverity int

const (
	severityDebug logSeverity = iota
	severityInfo
	severityWarn
	severityError
)

type cliLogger struct {
	level logSeverity
	mu    sync.Mutex
}

func newCLILogger(level logSeverity) *cliLogger {
	return &cliLogger{level: level}
}

func parseLogLevel(value string) (logSeverity, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return severityDebug, nil
	case "info":
		return severityInfo, nil
	case "warn":
		return severityWarn, nil
	case "error":
		return severityError, nil
	default:
		return severityInfo, fmt.Errorf("invalid log level %q (expected debug/info/warn/error)", value)
	}
}

func (l *cliLogger) logf(w io.Writer, level logSeverity, format string, args ...any) {
	if l == nil || level < l.level {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	fmt.Fprintf(w, format+"\n", args...)
}

func (l *cliLogger) Debugf(format string, args ...any) {
	l.logf(os.Stdout, severityDebug, format, args...)
}

func (l *cliLogger) Infof(format string, args ...any) {
	l.logf(os.Stdout, severityInfo, format, args...)
}

func (l *cliLogger) Warnf(format string, args ...any) {
	l.logf(os.Stderr, severityWarn, format, args...)
}

func (l *cliLogger) Errorf(format string, args ...any) {
	l.logf(os.Stderr, severityError, format, args...)
}

func main() {
	// Extract --config before any dispatching so board subcommands and the
	// orchestrator both respect it regardless of argument ordering.
	args := extractConfigFlag(os.Args[1:])

	// Dispatch to board subcommands before standard flag parsing so board
	// commands can define their own flags.
	if len(args) > 0 && args[0] == "board" {
		if err := runBoardCommand(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	// Re-assemble remaining args for standard flag parsing.
	flag.CommandLine.Parse(args)
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// extractConfigFlag scans os.Args for --config, sets the global configPath
// pointer, and returns the remaining args with --config removed.
func extractConfigFlag(args []string) []string {
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--config=") {
			*configPath = strings.TrimPrefix(args[i], "--config=")
			return append(args[:i], args[i+1:]...)
		}
		if args[i] == "--config" && i+1 < len(args) {
			*configPath = args[i+1]
			return append(args[:i], args[i+2:]...)
		}
	}
	return args
}

// buildDeps loads config and creates all core dependencies.
func buildDeps(configPath string) (*config.Config, types.IssueTracker, workspace.WorkspaceManager, *diagnostics.Recorder, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to load config: %w", err)
	}

	boardDir := cfg.Tracker.BoardDir
	if boardDir == "" {
		boardDir = ".contrabass/board"
	}
	var tr types.IssueTracker
	switch cfg.Tracker.Type {
	case "github":
		tr = tracker.NewGitHub(
			cfg.Tracker.Owner,
			cfg.Tracker.Repo,
			cfg.Tracker.Token,
			cfg.Tracker.LabelPrefix,
			cfg.Tracker.AssigneeBot,
		)
	default:
		tr = tracker.New(tracker.Config{
			BoardDir:    boardDir,
			IssuePrefix: cfg.Tracker.IssuePrefix,
		})
	}

	wsMgr := workspace.New(workspace.Config{
		BaseDir:      cfg.Workspace.BaseDir,
		BranchPrefix: cfg.Workspace.BranchPrefix,
	})

	recorder, err := diagnostics.NewRecorder(boardDir)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to initialize diagnostics recorder: %w", err)
	}

	return cfg, tr, wsMgr, recorder, nil
}

func run() error {
	level, err := parseLogLevel(*logLevel)
	if err != nil {
		return err
	}

	logger := newCLILogger(level)

	cfg, tr, wsMgr, recorder, err := buildDeps(*configPath)
	if err != nil {
		return err
	}
	defer recorder.Close()

	logger.Infof("Starting Contrabass with config: %s", *configPath)
	logger.Infof("  max_concurrency: %d", cfg.MaxConcurrency)
	logger.Infof("  poll_interval: %dms", cfg.PollIntervalMs)

	// Create agent runner based on config
	agentRunner, err := newAgentRunnerFromConfig(cfg)
	if err != nil {
		return fmt.Errorf("failed to create agent runner: %w", err)
	}

	// Create orchestrator
	orch := orchestrator.New(cfg, tr, wsMgr, agentRunner)
	orch.SetRecorder(recorder)

	// Context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	if *dryRun {
		return runDryRun(ctx, orch, sigChan, logger)
	}

	if !*noTUI {
		return runWithTUI(ctx, orch, sigChan, logger)
	}

	return runHeadless(ctx, orch, sigChan, logger)
}

// snapshotAdapter adapts orchestrator state to the web.SnapshotProvider interface.
type snapshotAdapter struct {
	orch *orchestrator.Orchestrator
}

func (a *snapshotAdapter) Snapshot() web.Snapshot {
	runs := a.orch.State.GetAll()
	running := make([]web.RunSnapshot, 0, len(runs))
	var totalTokensIn, totalTokensOut int64

	for _, run := range runs {
		pid := 0
		if run.Process != nil {
			pid = run.Process.PID
		}
		running = append(running, web.RunSnapshot{
			IssueID:     run.Issue.ID,
			Title:       run.Issue.Title,
			Stage:       run.Stage.String(),
			Attempt:     run.Attempt,
			PID:         pid,
			TokensIn:    run.TokensIn,
			TokensOut:   run.TokensOut,
			StartedAt:   run.StartedAt,
			LastEventAt: run.LastEventAt,
			Error:       run.Error,
		})
		totalTokensIn += run.TokensIn
		totalTokensOut += run.TokensOut
	}

	backoffEntries := a.orch.Backoff.GetAll()
	backoff := make([]web.BackoffSnapshot, 0, len(backoffEntries))
	for i := range backoffEntries {
		entry := &backoffEntries[i]
		backoff = append(backoff, web.BackoffSnapshot{
			IssueID: entry.IssueID,
			Stage:   entry.Stage.String(),
			Attempt: entry.Attempt,
			RetryAt: entry.RetryAt,
			Error:   entry.Error,
		})
	}

	review := make([]web.ReviewSnapshot, 0)
	if a.orch.Tracker != nil {
		issues, err := a.orch.Tracker.IssuesInReview()
		if err != nil {
			log.Printf("[web] failed to get issues in review: %v", err)
		} else {
			for _, issue := range issues {
				review = append(review, web.ReviewSnapshot{
					IssueID: issue.ID,
					Title:   issue.Title,
					ReadyAt: issue.UpdatedAt,
				})
			}
		}
	}

	return web.Snapshot{
		Running: running,
		Backoff: backoff,
		Review:  review,
		Stats: web.StatsSnapshot{
			RunningCount:   len(running),
			TotalTokensIn:  totalTokensIn,
			TotalTokensOut: totalTokensOut,
		},
	}
}

// maybeStartWebServer starts the HTTP API server if --port is set.
func maybeStartWebServer(ctx context.Context, orch *orchestrator.Orchestrator) *web.Server {
	if *port <= 0 {
		return nil
	}

	hub := web.NewHub()
	provider := &snapshotAdapter{orch: orch}
	srv := web.NewServer(fmt.Sprintf("127.0.0.1:%d", *port), provider, hub, orch.Events, orch.Config.MaxConcurrency)
	srv.StartEventBridge(ctx)

	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "HTTP server error: %v\n", err)
		}
	}()

	return srv
}

// newAgentRunnerFromConfig creates the appropriate agent runner based on cfg.Agent.Type.
func newAgentRunnerFromConfig(cfg *config.Config) (types.AgentRunner, error) {
	switch cfg.Agent.Type {
	case "opencode":
		if cfg.OpenCode == nil {
			return nil, fmt.Errorf("agent.type=%s requires opencode configuration block", cfg.Agent.Type)
		}
		return agent.NewOpenCodeRunner(
			cfg.OpenCode.BinaryPath,
			cfg.OpenCode.Port,
			cfg.OpenCode.Password,
			"", // username - not used currently
			30*time.Second,
			cfg.OpenCode.Profile,
			cfg.OpenCode.Agent,
			cfg.OpenCode.ConfigDir,
		), nil
	case "codex":
		return nil, fmt.Errorf("agent.type=%s is not supported yet", cfg.Agent.Type)
	default:
		return nil, fmt.Errorf("agent.type=%s is not supported", cfg.Agent.Type)
	}
}

func runWithTUI(ctx context.Context, orch *orchestrator.Orchestrator, sigChan <-chan os.Signal, logger *cliLogger) error {
	// Start web server if requested
	if srv := maybeStartWebServer(ctx, orch); srv != nil {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
		}()
	}

	// Create TUI model
	tuiModel := tui.NewModel()

	// Create and start the Bubble Tea program
	p := tea.NewProgram(tuiModel, tea.WithAltScreen())

	// Start event bridge - send orchestrator events to TUI
	tui.StartEventBridge(ctx, p, orch.Events)

	orchDone := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				orchDone <- fmt.Errorf("orchestrator panic: %v", r)
				p.Quit()
			}
		}()

		orchDone <- orch.Run()
		p.Quit()
	}()

	go func() {
		select {
		case <-ctx.Done():
			return
		case sig := <-sigChan:
			logger.Warnf("Received signal: %v", sig)
			orch.Stop()
			p.Quit()
		}
	}()

	// Run TUI in the main goroutine
	_, tuiErr := p.Run()

	// TUI exited — cancel orchestrator and wait for graceful shutdown
	orch.Stop()

	var orchErr error
	select {
	case orchErr = <-orchDone:
	case <-time.After(tuiShutdownTimeout):
		orchErr = errors.New("timed out waiting for orchestrator shutdown")
	}

	if tuiErr != nil || orchErr != nil {
		return errors.Join(tuiErr, orchErr)
	}
	return nil
}

// runDryRun runs a single orchestrator poll cycle and exits.
func runDryRun(ctx context.Context, orch *orchestrator.Orchestrator, sigChan <-chan os.Signal, logger *cliLogger) error {
	if srv := maybeStartWebServer(ctx, orch); srv != nil {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
		}()
	}

	eventDone := make(chan struct{})
	go func() {
		defer close(eventDone)
		for event := range orch.Events {
			logger.Infof("%s", formatEvent(event))
		}
	}()

	go func() {
		select {
		case <-ctx.Done():
			return
		case sig := <-sigChan:
			logger.Warnf("Received signal: %v", sig)
			orch.Stop()
		}
	}()

	err := orch.RunOnce()
	<-eventDone
	return err
}

// runHeadless runs the orchestrator without TUI, logging to stdout.
func runHeadless(ctx context.Context, orch *orchestrator.Orchestrator, sigChan <-chan os.Signal, logger *cliLogger) error {
	if srv := maybeStartWebServer(ctx, orch); srv != nil {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
		}()
	}

	// Start event log goroutine
	eventDone := make(chan struct{})
	go func() {
		defer close(eventDone)
		for event := range orch.Events {
			logger.Infof("%s", formatEvent(event))
		}
	}()

	// Handle signals
	go func() {
		select {
		case <-ctx.Done():
			return
		case sig := <-sigChan:
			logger.Warnf("Shutting down due to signal: %v", sig)
			orch.Stop()
		}
	}()

	// Run orchestrator
	err := orch.Run()
	<-eventDone
	return err
}

func formatEvent(event types.OrchestratorEvent) string {
	timestamp := event.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}

	stamp := timestamp.Format("15:04:05")
	if event.IssueID != "" {
		return fmt.Sprintf("[%s] %s %s", stamp, event.IssueID, event.Type)
	}
	return fmt.Sprintf("[%s] %s", stamp, event.Type)
}
