package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/fatihkarahan/maestro/internal/diagnostics"
	"github.com/fatihkarahan/maestro/internal/types"
)

func runBoardCommand(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: maestro board <command> [args]\n\nCommands:\n  create  Create a new issue\n  list    List issues by state\n  show    Show issue details\n  approve Approve an issue\n  reject  Reject an issue\n  retry   Retry an issue")
	}

	cmd := args[0]
	cmdArgs := args[1:]

	switch cmd {
	case "create":
		return boardCreate(cmdArgs)
	case "list":
		return boardList(cmdArgs)
	case "show":
		return boardShow(cmdArgs)
	case "approve":
		return boardApprove(cmdArgs)
	case "reject":
		return boardReject(cmdArgs)
	case "retry":
		return boardRetry(cmdArgs)
	default:
		return fmt.Errorf("unknown board command: %q", cmd)
	}
}

// boardCreate creates a new issue on the local board.
func boardCreate(args []string) error {
	fs := flag.NewFlagSet("board create", flag.ExitOnError)
	description := fs.String("description", "", "issue description")
	labelsStr := fs.String("labels", "", "comma-separated labels")
	if err := fs.Parse(flagsBeforePositionals(args)); err != nil {
		return err
	}

	if len(fs.Args()) < 1 {
		return errors.New("usage: maestro board create <title> [--description <desc>] [--labels <labels>]")
	}
	title := fs.Args()[0]

	var labels []string
	if *labelsStr != "" {
		for _, l := range strings.Split(*labelsStr, ",") {
			labels = append(labels, strings.TrimSpace(l))
		}
	}

	_, tr, _, recorder, err := buildDeps(*configPath)
	if err != nil {
		return err
	}
	defer recorder.Close()

	issue, err := tr.CreateIssue(title, *description, labels)
	if err != nil {
		return fmt.Errorf("creating issue: %w", err)
	}

	fmt.Printf("Created issue %s: %s\n", issue.ID, issue.Title)
	return nil
}

// boardList lists issues by state.
func boardList(args []string) error {
	fs := flag.NewFlagSet("board list", flag.ExitOnError)
	stateFilter := fs.String("state", "", "filter by state (todo/in_progress/in_review/done/retry_queued)")
	showAll := fs.Bool("all", false, "show all states")
	if err := fs.Parse(args); err != nil {
		return err
	}

	_, tr, _, recorder, err := buildDeps(*configPath)
	if err != nil {
		return err
	}
	defer recorder.Close()

	allIssues, err := tr.ListAllIssues()
	if err != nil {
		return fmt.Errorf("listing issues: %w", err)
	}

	var issues []types.Issue
	for _, issue := range allIssues {
		if *showAll {
			issues = append(issues, issue)
			continue
		}
		if *stateFilter == "" || issue.State.BoardState() == *stateFilter {
			issues = append(issues, issue)
		}
	}

	// Build tabular output.
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATE\tTITLE\tBRANCH\tATTEMPTS\tREADY")
	for _, issue := range issues {
		branch := "-"
		attempts := "-"
		ready := "-"

		summary, err := recorder.LoadIssueSummary(issue.ID)
		if err == nil {
			if summary.Branch != "" {
				branch = summary.Branch
			}
			if summary.Attempts > 0 {
				attempts = fmt.Sprintf("%d", summary.Attempts)
			}
			if summary.FinishedAt != nil {
				ready = humanDuration(time.Since(*summary.FinishedAt)) + " ago"
			}
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			issue.ID,
			issue.State.BoardState(),
			truncate(issue.Title, 30),
			branch,
			attempts,
			ready,
		)
	}
	return w.Flush()
}

// boardShow displays the full review package for an issue.
func boardShow(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: maestro board show <issue-id>")
	}
	issueID := args[0]

	_, tr, wsMgr, recorder, err := buildDeps(*configPath)
	if err != nil {
		return err
	}
	defer recorder.Close()

	issue, err := tr.GetIssue(issueID)
	if err != nil {
		return err
	}

	fmt.Printf("Issue: %s\n", issue.ID)
	fmt.Printf("Title: %s\n", issue.Title)
	fmt.Printf("State: %s\n", issue.State.BoardState())

	summary, summaryErr := recorder.LoadIssueSummary(issue.ID)
	if summaryErr == nil {
		if summary.Branch != "" {
			fmt.Printf("Branch: %s\n", summary.Branch)
		}
		if summary.WorkspacePath != "" {
			fmt.Printf("Workspace: %s\n", summary.WorkspacePath)
		}
		if summary.Attempts > 0 {
			fmt.Printf("Attempts: %d\n", summary.Attempts)
		}
		if summary.FinishedAt != nil {
			fmt.Printf("Ready: %s ago\n", humanDuration(time.Since(*summary.FinishedAt)))
		}
	} else {
		fmt.Printf("Note: diagnostic data unavailable: %v\n", summaryErr)
	}

	fmt.Println()

	// Show stage completion from summary.
	if summaryErr == nil && summary.Attempts > 0 {
		fmt.Println("Stages:")
		for attempt := 1; attempt <= summary.Attempts; attempt++ {
			meta, err := recorder.LoadAttemptMeta(issue.ID, attempt)
			if err != nil {
				continue
			}
			duration := "-"
			if meta.StartedAt.IsZero() {
				continue
			}
			if meta.EndedAt != nil {
				duration = humanDuration(meta.EndedAt.Sub(meta.StartedAt))
			} else {
				duration = humanDuration(time.Since(meta.StartedAt))
			}
			commit := ""
			if meta.FinalCommit != "" {
				commit = fmt.Sprintf(", commit: %s", meta.FinalCommit[:min(7, len(meta.FinalCommit))])
			}
			fmt.Printf("  attempt #%d: %s (%s%s)\n", attempt, meta.CurrentStage, duration, commit)
		}
		fmt.Println()
	}

	// Show review handoff if present.
	if summaryErr == nil && summary.Attempts > 0 {
		handoffPath := filepath.Join(recorder.RunsRoot(), issue.ID, "attempts", fmt.Sprintf("%03d", summary.Attempts), "review", "handoff.md")
		if data, err := os.ReadFile(handoffPath); err == nil && len(data) > 0 {
			fmt.Println("Review handoff:")
			fmt.Println(string(data))
			fmt.Println()
		}
	}

	// Show run records path.
	if summaryErr == nil {
		fmt.Printf("Run records:\n  %s\n", summary.RunDir)
	} else {
		fmt.Printf("Run records:\n  %s\n", filepath.Join(recorder.RunsRoot(), issue.ID))
	}

	// Show workspace path from manager as fallback.
	wsPath := wsMgr.Path(issue.ID)
	if wsPath != "" {
		fmt.Printf("Workspace:\n  %s\n", wsPath)
	}

	return nil
}

// boardApprove approves an issue and moves it to done.
func boardApprove(args []string) error {
	fs := flag.NewFlagSet("board approve", flag.ExitOnError)
	message := fs.String("message", "", "approval note")
	if err := fs.Parse(flagsBeforePositionals(args)); err != nil {
		return err
	}
	remaining := fs.Args()
	if len(remaining) < 1 {
		return errors.New("usage: maestro board approve <issue-id> [--message \"...\"]")
	}
	issueID := remaining[0]

	_, tr, _, recorder, err := buildDeps(*configPath)
	if err != nil {
		return err
	}
	defer recorder.Close()

	issue, err := tr.GetIssue(issueID)
	if err != nil {
		return err
	}
	if issue.State != types.StateInReview {
		return fmt.Errorf("%s is in state %q, expected %q", issueID, issue.State.BoardState(), types.StateInReview.BoardState())
	}

	summary, err := recorder.LoadIssueSummary(issue.ID)
	if err != nil {
		return fmt.Errorf("loading issue summary: %w", err)
	}
	attempt := summary.Attempts
	if attempt == 0 {
		attempt = summary.CurrentAttempt
	}
	if attempt == 0 {
		attempt = 1
	}

	ar, err := recorder.LoadAttemptRecorder(issue.ID, attempt)
	if err != nil {
		return fmt.Errorf("loading attempt recorder: %w", err)
	}

	// Update tracker state first so we never write a decision artifact
	// without the tracker reflecting the transition.
	if _, err := tr.UpdateIssueState(issue.ID, types.StateReleased); err != nil {
		return fmt.Errorf("updating issue state: %w", err)
	}

	decision := types.ReviewDecision{
		Decision:      types.ReviewDecisionApproved,
		ReviewedBy:    diagnostics.DefaultReviewer(),
		ReviewedAt:    time.Now().UTC(),
		Notes:         *message,
		FollowUpState: types.ReviewFollowUpDone,
	}
	if err := ar.RecordReviewDecision(decision); err != nil {
		return fmt.Errorf("recording review decision: %w", err)
	}

	fmt.Printf("Approved %s\n", issueID)
	fmt.Printf("  Workspace: %s\n", summary.WorkspacePath)
	fmt.Printf("  Branch: %s\n", summary.Branch)
	return nil
}

// boardReject rejects an issue and returns it to todo.
func boardReject(args []string) error {
	fs := flag.NewFlagSet("board reject", flag.ExitOnError)
	message := fs.String("message", "", "rejection note")
	if err := fs.Parse(flagsBeforePositionals(args)); err != nil {
		return err
	}
	remaining := fs.Args()
	if len(remaining) < 1 {
		return errors.New("usage: maestro board reject <issue-id> [--message \"...\"]")
	}
	issueID := remaining[0]

	_, tr, _, recorder, err := buildDeps(*configPath)
	if err != nil {
		return err
	}
	defer recorder.Close()

	issue, err := tr.GetIssue(issueID)
	if err != nil {
		return err
	}
	if issue.State != types.StateInReview {
		return fmt.Errorf("%s is in state %q, expected %q", issueID, issue.State.BoardState(), types.StateInReview.BoardState())
	}

	if *message == "" {
		fmt.Fprintln(os.Stderr, "Warning: No rejection note provided. Consider adding --message.")
	}

	summary, err := recorder.LoadIssueSummary(issue.ID)
	if err != nil {
		return fmt.Errorf("loading issue summary: %w", err)
	}
	attempt := summary.Attempts
	if attempt == 0 {
		attempt = summary.CurrentAttempt
	}
	if attempt == 0 {
		attempt = 1
	}

	ar, err := recorder.LoadAttemptRecorder(issue.ID, attempt)
	if err != nil {
		return fmt.Errorf("loading attempt recorder: %w", err)
	}

	// Update tracker state first so we never write a decision artifact
	// without the tracker reflecting the transition.
	if _, err := tr.UpdateIssueState(issue.ID, types.StateUnclaimed); err != nil {
		return fmt.Errorf("updating issue state: %w", err)
	}

	decision := types.ReviewDecision{
		Decision:      types.ReviewDecisionRejected,
		ReviewedBy:    diagnostics.DefaultReviewer(),
		ReviewedAt:    time.Now().UTC(),
		Notes:         *message,
		FollowUpState: types.ReviewFollowUpTodo,
	}
	if err := ar.RecordReviewDecision(decision); err != nil {
		return fmt.Errorf("recording review decision: %w", err)
	}

	fmt.Printf("Rejected %s\n", issueID)
	fmt.Printf("  Workspace: %s\n", summary.WorkspacePath)
	return nil
}

func flagsBeforePositionals(args []string) []string {
	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if !strings.Contains(arg, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	return append(flags, positionals...)
}

// boardRetry moves a retry_queued issue back to todo.
func boardRetry(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: maestro board retry <issue-id>")
	}
	issueID := args[0]

	_, tr, _, recorder, err := buildDeps(*configPath)
	if err != nil {
		return err
	}
	defer recorder.Close()

	issue, err := tr.GetIssue(issueID)
	if err != nil {
		return err
	}
	if issue.State != types.StateRetryQueued && issue.State != types.StateUnclaimed {
		return fmt.Errorf("Cannot retry %s: currently %s", issueID, issue.State.BoardState())
	}

	if _, err := tr.UpdateIssueState(issue.ID, types.StateUnclaimed); err != nil {
		return fmt.Errorf("updating issue state: %w", err)
	}

	fmt.Printf("Retry queued %s -> todo\n", issueID)
	return nil
}

// humanDuration formats a duration in a human-readable way.
func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

// truncate truncates a string to maxLen, adding "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// min returns the minimum of two ints.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
