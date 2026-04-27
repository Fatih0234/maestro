// Package main is the CLI entry point for Contrabass.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
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
	"github.com/fatihkarahan/contrabass-pi/internal/workspace"
)

var (
	configPath = flag.String("config", "WORKFLOW.md", "path to WORKFLOW.md")
	noTUI      = flag.Bool("no-tui", false, "run without TUI (headless mode)")
	dryRun     = flag.Bool("dry-run", false, "exit after first poll cycle")
	logLevel   = flag.String("log-level", "info", "log level (debug/info/warn/error)")
)

var version = "dev"

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
	// --version is handled globally before any other parsing.
	for _, arg := range os.Args[1:] {
		if arg == "--version" || arg == "-v" {
			fmt.Println("contrabass", version)
			return
		}
	}

	// Extract --config before any dispatching so board subcommands and the
	// orchestrator both respect it regardless of argument ordering.
	args := extractConfigFlag(os.Args[1:])

	// If the user just wants top-level help, let flag.Parse handle it.
	if hasHelpFlag(args) && len(args) > 0 && args[0] != "init" && args[0] != "board" {
		flag.CommandLine.SetOutput(os.Stdout)
		flag.CommandLine.Parse(args)
		return
	}

	// Dispatch to init subcommand before standard flag parsing.
	// init does not need config discovery.
	if len(args) > 0 && args[0] == "init" {
		if err := runInit(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	// Resolve config path for all non-init commands.
	resolved, err := resolveConfigPath(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	*configPath = resolved

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

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

func findGitRoot(startDir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = startDir
	out, err := cmd.Output()
	if err != nil {
		return "", errors.New("not a git repository")
	}
	return strings.TrimSpace(string(out)), nil
}

func findProjectRoot(startDir string) (string, error) {
	startDir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}

	gitRoot := ""
	if gr, err := findGitRoot(startDir); err == nil {
		gitRoot = gr
	}

	dir := startDir
	for {
		if _, err := os.Stat(filepath.Join(dir, "WORKFLOW.md")); err == nil {
			return dir, nil
		}
		if _, err := os.Stat(filepath.Join(dir, ".contrabass", "WORKFLOW.md")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		if gitRoot != "" && dir == gitRoot {
			break
		}
		dir = parent
	}

	if gitRoot != "" {
		return "", fmt.Errorf("no WORKFLOW.md found in %s or its parent directories (git root: %s)", startDir, gitRoot)
	}
	return "", fmt.Errorf("no WORKFLOW.md found in %s or its parent directories", startDir)
}

func resolveConfigPath(flagValue string) (string, error) {
	if flagValue != "WORKFLOW.md" {
		abs, err := filepath.Abs(flagValue)
		if err != nil {
			return "", fmt.Errorf("resolve config path %q: %w", flagValue, err)
		}
		return abs, nil
	}

	root, err := findProjectRoot(".")
	if err != nil {
		return "", fmt.Errorf("%w\nhint: run from a project root, or specify --config /path/to/WORKFLOW.md", err)
	}

	primary := filepath.Join(root, "WORKFLOW.md")
	if _, err := os.Stat(primary); err == nil {
		return primary, nil
	}
	return filepath.Join(root, ".contrabass", "WORKFLOW.md"), nil
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
	tr := tracker.New(tracker.Config{
		BoardDir:    boardDir,
		IssuePrefix: cfg.Tracker.IssuePrefix,
	})

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
	default:
		return nil, fmt.Errorf("agent.type=%s is not supported", cfg.Agent.Type)
	}
}

func runWithTUI(ctx context.Context, orch *orchestrator.Orchestrator, sigChan <-chan os.Signal, logger *cliLogger) error {
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
