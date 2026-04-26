// Package tui provides the Charm Bubble Tea terminal UI.
package tui

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fatihkarahan/contrabass-pi/internal/orchestrator"
	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

// Refresh interval for updating derived fields.
const refreshInterval = time.Second

// OrchestratorEventMsg wraps orchestrator events for Bubble Tea.
type OrchestratorEventMsg struct {
	Event types.OrchestratorEvent
}

// tickMsg is sent periodically to refresh derived fields like ages.
type tickMsg time.Time

// Model is the main Bubble Tea model for the Contrabass TUI.
type Model struct {
	width  int
	height int

	// State maps
	agents   map[string]AgentRow
	reviews  map[string]ReviewRow
	backoffs map[string]BackoffRow
	stats    HeaderStats

	// Stage completion tracking (issueID -> set of completed stages)
	stageProgress map[string]map[types.Stage]bool

	// Session table
	table Table

	// Sorting caches
	agentKeys      []string
	reviewKeys     []string
	backoffKeys    []string
	agentSortDirty bool

	// Event log
	eventLog   []EventLogEntry
	maxLogSize int
	scrollPos  int

	// State
	quitting bool
}

// AgentRow holds display data for one running agent.
type AgentRow struct {
	IssueID   string
	Title     string
	Stage     types.Stage
	Status    string
	PID       int
	Age       string
	TokensIn  int64
	TokensOut int64
	SessionID string
	LastEvent string
	StartTime time.Time
	Attempt   int
}

// BackoffRow holds display data for one backoff entry.
type BackoffRow struct {
	IssueID     string
	Attempt     int
	Stage       types.Stage
	RetryIn     string
	RetryAt     time.Time
	Error       string
	FailureKind types.StageFailureKind
}

// ReviewRow holds display data for one review handoff entry.
type ReviewRow struct {
	IssueID         string
	Title           string
	Branch          string
	WorkspacePath   string
	ReadyAt         time.Time
	StagesCompleted map[types.Stage]bool
	FailureReason   string
}

// HeaderStats holds the header statistics.
type HeaderStats struct {
	RunningAgents int
	MaxAgents     int
	TokensIn      int64
	TokensOut     int64
	Runtime       string
}

// EventLogEntry represents one line in the event log.
type EventLogEntry struct {
	Timestamp time.Time
	IssueID   string
	Message   string
	Severity  string
}

// NewModel creates a new TUI model.
func NewModel() Model {
	return Model{
		agents:        make(map[string]AgentRow),
		reviews:       make(map[string]ReviewRow),
		backoffs:      make(map[string]BackoffRow),
		stageProgress: make(map[string]map[types.Stage]bool),
		maxLogSize:    100,
		eventLog:      make([]EventLogEntry, 0, 100),
		table:         NewTable(),
	}
}

// Init initializes the model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(doTick())
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "up":
			m.scrollPos--
			if m.scrollPos < 0 {
				m.scrollPos = 0
			}
			if m.scrollPos < len(m.eventLog) && m.table.RowCount() > 0 {
				m.table = m.table.SetSelected(m.table.Selected() - 1)
				if m.table.Selected() < 0 {
					m.table = m.table.SetSelected(0)
				}
			}
		case "down":
			m.scrollPos++
			maxScroll := len(m.eventLog) - 1
			if maxScroll < 0 {
				maxScroll = 0
			}
			if m.scrollPos > maxScroll {
				m.scrollPos = maxScroll
			}
			if m.table.RowCount() > 0 {
				m.table = m.table.SetSelected(m.table.Selected() + 1)
				if m.table.Selected() >= m.table.RowCount() {
					m.table = m.table.SetSelected(m.table.RowCount() - 1)
				}
			}
		case "r":
			// Force refresh - could trigger a manual poll in orchestrator
		case "esc":
			m.scrollPos = 0
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.table = m.table.SetWidth(msg.Width)

	case OrchestratorEventMsg:
		m = m.applyOrchestratorEvent(msg.Event)

	case tickMsg:
		m = m.refreshDerivedFields(time.Time(msg))
		cmd = doTick()
	}

	return m, cmd
}

// View renders the TUI.
func (m Model) View() string {
	// Render all sections
	header := m.renderHeader()
	table := m.renderTable()
	review := m.renderReviewQueue()
	backoff := m.renderBackoffQueue()
	events := m.renderEventLog()

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		lipgloss.NewStyle().PaddingBottom(1).Render(table),
		lipgloss.NewStyle().PaddingBottom(1).Render(review),
		lipgloss.NewStyle().PaddingBottom(1).Render(backoff),
		lipgloss.NewStyle().PaddingBottom(1).Render(events),
	)
}

// renderHeader renders the top header bar.
func (m Model) renderHeader() string {
	running := len(m.agents)
	maxAgents := m.stats.MaxAgents
	if maxAgents == 0 {
		maxAgents = m.stats.RunningAgents
	}
	if maxAgents == 0 {
		maxAgents = running
	}
	tokensIn := formatTokens(m.stats.TokensIn)
	tokensOut := formatTokens(m.stats.TokensOut)

	header := fmt.Sprintf("Contrabass    Running: %d/%d    Tokens: %s/%s\n",
		running, maxAgents, tokensIn, tokensOut)

	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color("42")).
		Bold(true)

	return style.Render(header)
}

// renderTable renders the session table.
func (m Model) renderTable() string {
	if len(m.agents) == 0 {
		return lipgloss.NewStyle().Faint(true).Render("  No agents running")
	}

	rows := m.sortedAgentRows()
	sessionRows := make([]SessionRow, 0, len(rows))
	for _, row := range rows {
		sessionRows = append(sessionRows, SessionRow{
			IssueID:   row.IssueID,
			Title:     row.Title,
			Stage:     row.Stage,
			Status:    row.Status,
			PID:       row.PID,
			Age:       row.Age,
			TokensIn:  row.TokensIn,
			TokensOut: row.TokensOut,
			SessionID: row.SessionID,
			LastEvent: row.LastEvent,
			Attempt:   row.Attempt,
		})
	}

	m.table = m.table.Update(sessionRows, "")
	return m.table.View()
}

// renderReviewQueue renders issues waiting for human review.
func (m Model) renderReviewQueue() string {
	if len(m.reviews) == 0 {
		return ""
	}

	boxStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("42")).
		Padding(0, 1)

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	header := headerStyle.Render(fmt.Sprintf("Ready for Human Review [%d]", len(m.reviews)))

	rows := ""
	for _, issueID := range m.sortedReviewKeys() {
		row := m.reviews[issueID]
		branch := row.Branch
		if branch == "" {
			branch = "-"
		}
		workspacePath := row.WorkspacePath
		if workspacePath == "" {
			workspacePath = "(workspace path unavailable)"
		}

		waitTime := "just now"
		if !row.ReadyAt.IsZero() {
			waitTime = durationString(time.Since(row.ReadyAt)) + " ago"
		}

		stagesStr := m.formatStageCompletion(row.StagesCompleted)

		rows += fmt.Sprintf("  %s  %s\n", issueID, row.Title)
		rows += fmt.Sprintf("    branch:  %s\n", branch)
		rows += fmt.Sprintf("    workspace: %s\n", workspacePath)
		rows += fmt.Sprintf("    ready:   %s\n", waitTime)
		if stagesStr != "" {
			rows += fmt.Sprintf("    stages:  %s\n", stagesStr)
		}
		if row.FailureReason != "" {
			rows += fmt.Sprintf("    reason:  %s\n", row.FailureReason)
		}
	}

	content := header + "\n" + rows
	return boxStyle.Render(content)
}

// renderBackoffQueue renders the backoff queue section.
func (m Model) renderBackoffQueue() string {
	if len(m.backoffs) == 0 {
		return ""
	}

	boxStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("208")).
		Padding(0, 1)

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("208"))
	header := headerStyle.Render(fmt.Sprintf("Backoff Queue [%d]", len(m.backoffs)))

	rows := ""
	sortedKeys := m.sortedBackoffKeys()
	for _, issueID := range sortedKeys {
		row := m.backoffs[issueID]
		retryIn := row.RetryIn
		if retryIn == "" {
			retryIn = "pending"
		}

		stageStr := compactStage(row.Stage)
		if stageStr == "" {
			stageStr = "unknown"
		}

		failureStr := ""
		if row.FailureKind != "" {
			failureStr = string(row.FailureKind)
		} else if row.Error != "" {
			failureStr = truncate(row.Error, 40)
		}

		rows += fmt.Sprintf("  %s  retry in %s  (attempt #%d)\n", issueID, retryIn, row.Attempt)
		rows += fmt.Sprintf("    stage:  %s failed\n", stageStr)
		if failureStr != "" {
			rows += fmt.Sprintf("    reason: %s\n", failureStr)
		}
	}

	content := header + "\n" + rows
	return boxStyle.Render(content)
}

// renderEventLog renders the scrolling event log.
func (m Model) renderEventLog() string {
	headerStyle := lipgloss.NewStyle().Bold(true).Faint(true)
	header := headerStyle.Render("  [Events]")

	if len(m.eventLog) == 0 {
		return header + "\n" + lipgloss.NewStyle().Faint(true).Render("  No events yet")
	}

	// Calculate visible range
	start := 0
	end := len(m.eventLog)
	maxVisible := 15 // rough estimate
	if end-start > maxVisible {
		start = end - maxVisible
		if start < 0 {
			start = 0
		}
	}

	entries := ""
	for i := start; i < end && i < len(m.eventLog); i++ {
		entry := m.eventLog[i]
		timeStr := entry.Timestamp.Format("15:04:05")
		issueStr := entry.IssueID
		if issueStr == "" {
			issueStr = "-"
		}

		msgStyle := lipgloss.NewStyle()
		switch entry.Severity {
		case "error":
			msgStyle = msgStyle.Foreground(lipgloss.Color("1"))
		case "warn":
			msgStyle = msgStyle.Foreground(lipgloss.Color("208"))
		case "success":
			msgStyle = msgStyle.Foreground(lipgloss.Color("42"))
		default:
			msgStyle = msgStyle.Faint(true)
		}

		msg := fmt.Sprintf("  %s %-8s %s", timeStr, issueStr, entry.Message)
		entries += msgStyle.Render(msg) + "\n"
	}

	return header + "\n" + entries
}

// StartEventBridge starts the goroutine that bridges orchestrator events to the TUI.
func StartEventBridge(ctx context.Context, p *tea.Program, events <-chan types.OrchestratorEvent) {
	if p == nil || events == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				p.Send(OrchestratorEventMsg{Event: event})
			}
		}
	}()
}

// doTick returns a command that will send a tickMsg after refreshInterval.
func doTick() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// applyOrchestratorEvent processes an orchestrator event and updates the model.
func (m Model) applyOrchestratorEvent(event types.OrchestratorEvent) Model {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// Log the event with a human-readable message
	msg, severity := formatEventMessage(event)
	m.pushEvent(event.IssueID, msg, severity)

	switch event.Type {
	case orchestrator.EventAgentStarted:
		if payload, ok := event.Payload.(orchestrator.AgentStartedPayload); ok {
			issueID := event.IssueID
			if issueID == "" {
				issueID = "-"
			}
			m.agents[event.IssueID] = AgentRow{
				IssueID:   issueID,
				Stage:     payload.Stage,
				Attempt:   payload.Attempt,
				Status:    "running",
				PID:       payload.PID,
				SessionID: payload.SessionID,
				StartTime: event.Timestamp,
				TokensIn:  0,
				TokensOut: 0,
			}
			m.agentSortDirty = true
		}

	case orchestrator.EventTokensUpdated:
		if payload, ok := event.Payload.(orchestrator.TokensUpdatedPayload); ok {
			if row, exists := m.agents[event.IssueID]; exists {
				row.TokensIn = payload.TokensIn
				row.TokensOut = payload.TokensOut
				row.LastEvent = fmt.Sprintf("[%s] tokens %s/%s", compactStage(row.Stage), formatTokensShort(payload.TokensIn), formatTokensShort(payload.TokensOut))
				m.agents[event.IssueID] = row
			}
		}

	case orchestrator.EventStageStarted:
		if payload, ok := event.Payload.(orchestrator.StageStartedPayload); ok {
			if row, exists := m.agents[event.IssueID]; exists {
				row.Stage = payload.Stage
				row.Status = "running"
				row.Attempt = payload.Attempt
				row.LastEvent = fmt.Sprintf("[%s] started", compactStage(payload.Stage))
				m.agents[event.IssueID] = row
			}
		}

	case orchestrator.EventStageCompleted:
		if payload, ok := event.Payload.(orchestrator.StageCompletedPayload); ok {
			if row, exists := m.agents[event.IssueID]; exists {
				row.Stage = payload.Stage
				row.LastEvent = fmt.Sprintf("[%s] completed", compactStage(payload.Stage))
				m.agents[event.IssueID] = row
			}
			m.recordStageCompletion(event.IssueID, payload.Stage)
		}

	case orchestrator.EventStageFailed:
		if payload, ok := event.Payload.(orchestrator.StageFailedPayload); ok {
			if row, exists := m.agents[event.IssueID]; exists {
				row.Stage = payload.Stage
				row.Status = "failed"
				row.LastEvent = fmt.Sprintf("[%s] failed: %s", compactStage(payload.Stage), payload.FailureKind)
				m.agents[event.IssueID] = row
			}
		}

	case orchestrator.EventAgentFinished:
		if payload, ok := event.Payload.(orchestrator.AgentFinishedPayload); ok {
			if !payload.Success {
				// Move to backoff or failed state
				if row, exists := m.agents[event.IssueID]; exists {
					row.Status = "failed"
					row.LastEvent = "failed"
					m.agents[event.IssueID] = row
				}
			} else {
				// Remove from agents
				delete(m.agents, event.IssueID)
			}
			m.agentSortDirty = true
		}

	case orchestrator.EventBackoffQueued:
		if payload, ok := event.Payload.(orchestrator.BackoffQueuedPayload); ok {
			m.backoffs[event.IssueID] = BackoffRow{
				IssueID:     event.IssueID,
				Attempt:     payload.Attempt,
				Stage:       payload.Stage,
				RetryAt:     payload.RetryAt,
				Error:       payload.Error,
				FailureKind: payload.FailureKind,
			}
		}

	case orchestrator.EventIssueRetrying:
		if payload, ok := event.Payload.(orchestrator.IssueRetryingPayload); ok {
			m.backoffs[event.IssueID] = BackoffRow{
				IssueID:     event.IssueID,
				Attempt:     payload.Attempt,
				Stage:       payload.Stage,
				RetryAt:     payload.RetryAt,
				Error:       payload.Error,
				FailureKind: payload.FailureKind,
			}
		}

	case orchestrator.EventIssueReadyForReview:
		if payload, ok := event.Payload.(orchestrator.IssueReadyForReviewPayload); ok {
			issueID := payload.IssueID
			if issueID == "" {
				issueID = event.IssueID
			}
			if issueID != "" {
				stagesCompleted := m.stageProgress[issueID]
				if stagesCompleted == nil {
					stagesCompleted = make(map[types.Stage]bool)
				}
				m.reviews[issueID] = ReviewRow{
					IssueID:         issueID,
					Title:           payload.Title,
					Branch:          payload.Branch,
					WorkspacePath:   payload.WorkspacePath,
					ReadyAt:         event.Timestamp,
					StagesCompleted: stagesCompleted,
				}
				m.reviewKeys = nil
				delete(m.agents, issueID)
				delete(m.backoffs, issueID)
				delete(m.stageProgress, issueID)
				m.agentSortDirty = true
			}
		}

	case orchestrator.EventIssueCompleted:
		delete(m.agents, event.IssueID)
		delete(m.reviews, event.IssueID)
		m.reviewKeys = nil
		delete(m.backoffs, event.IssueID)
		delete(m.stageProgress, event.IssueID)
		m.agentSortDirty = true

	case orchestrator.EventTimeoutDetected:
		if row, exists := m.agents[event.IssueID]; exists {
			row.Status = "timeout"
			row.LastEvent = "timeout"
			m.agents[event.IssueID] = row
		}

	case orchestrator.EventStallDetected:
		if row, exists := m.agents[event.IssueID]; exists {
			row.Status = "stalled"
			row.LastEvent = "stalled"
			m.agents[event.IssueID] = row
		}

	case orchestrator.EventPollCompleted:
		// Update stats if available
		if payload, ok := event.Payload.(map[string]interface{}); ok {
			if running, ok := payload["RunningAgents"].(int); ok {
				m.stats.RunningAgents = running
			}
		}
	}

	return m
}

// refreshDerivedFields updates age strings and backoff countdowns.
func (m Model) refreshDerivedFields(now time.Time) Model {
	// Update agent ages
	for issueID, row := range m.agents {
		if !row.StartTime.IsZero() {
			age := now.Sub(row.StartTime)
			row.Age = durationString(age)
			m.agents[issueID] = row
		}
	}

	// Update backoff retry times
	for issueID, row := range m.backoffs {
		remaining := row.RetryAt.Sub(now)
		if remaining < 0 {
			remaining = 0
		}
		row.RetryIn = durationString(remaining)
		m.backoffs[issueID] = row
	}

	// Update review wait times (indirectly via ReadyAt)
	for issueID, row := range m.reviews {
		if row.ReadyAt.IsZero() {
			row.ReadyAt = now
			m.reviews[issueID] = row
		}
	}

	return m
}

// pushEvent adds an event to the log.
func (m *Model) pushEvent(issueID, message, severity string) {
	entry := EventLogEntry{
		Timestamp: time.Now(),
		IssueID:   issueID,
		Message:   message,
		Severity:  severity,
	}

	m.eventLog = append(m.eventLog, entry)

	// Trim to max size
	if len(m.eventLog) > m.maxLogSize {
		m.eventLog = m.eventLog[len(m.eventLog)-m.maxLogSize:]
	}

	// Auto-scroll to bottom
	m.scrollPos = len(m.eventLog) - 1
	if m.scrollPos < 0 {
		m.scrollPos = 0
	}
}

// recordStageCompletion marks a stage as completed for an issue.
func (m *Model) recordStageCompletion(issueID string, stage types.Stage) {
	if m.stageProgress[issueID] == nil {
		m.stageProgress[issueID] = make(map[types.Stage]bool)
	}
	m.stageProgress[issueID][stage] = true
}

// formatStageCompletion renders completed stages as a compact string.
func (m Model) formatStageCompletion(stages map[types.Stage]bool) string {
	if len(stages) == 0 {
		return ""
	}
	var parts []string
	for _, s := range []types.Stage{types.StagePlan, types.StageExecute, types.StageVerify} {
		if stages[s] {
			parts = append(parts, "✓ "+s.String())
		}
	}
	if len(parts) == 0 {
		return ""
	}
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += "  "
		}
		result += p
	}
	return " " + result
}

// formatEventMessage returns a human-readable message and severity for an orchestrator event.
func formatEventMessage(event types.OrchestratorEvent) (string, string) {
	switch event.Type {
	case orchestrator.EventPollStarted:
		return "poll started", "info"
	case orchestrator.EventPollCompleted:
		return "poll completed", "info"
	case orchestrator.EventIssueClaimed:
		return "issue claimed", "info"
	case orchestrator.EventWorkspaceCreated:
		return "workspace created", "info"
	case orchestrator.EventPromptBuilt:
		return "prompt built", "info"
	case orchestrator.EventAgentStarted:
		if p, ok := event.Payload.(orchestrator.AgentStartedPayload); ok {
			return fmt.Sprintf("[%s] agent started (pid: %d)", compactStage(p.Stage), p.PID), "info"
		}
		return "agent started", "info"
	case orchestrator.EventTokensUpdated:
		if p, ok := event.Payload.(orchestrator.TokensUpdatedPayload); ok {
			return fmt.Sprintf("tokens: %s/%s", formatTokensShort(p.TokensIn), formatTokensShort(p.TokensOut)), "info"
		}
		return "tokens updated", "info"
	case orchestrator.EventAgentOutput:
		return "agent output", "info"
	case orchestrator.EventStageStarted:
		if p, ok := event.Payload.(orchestrator.StageStartedPayload); ok {
			return fmt.Sprintf("[%s] started (attempt #%d)", compactStage(p.Stage), p.Attempt), "info"
		}
		return "stage started", "info"
	case orchestrator.EventStageCompleted:
		if p, ok := event.Payload.(orchestrator.StageCompletedPayload); ok {
			return fmt.Sprintf("[%s] completed", compactStage(p.Stage)), "success"
		}
		return "stage completed", "success"
	case orchestrator.EventStageFailed:
		if p, ok := event.Payload.(orchestrator.StageFailedPayload); ok {
			return fmt.Sprintf("[%s] failed: %s", compactStage(p.Stage), p.FailureKind), "error"
		}
		return "stage failed", "error"
	case orchestrator.EventAgentFinished:
		if p, ok := event.Payload.(orchestrator.AgentFinishedPayload); ok {
			if p.Success {
				return "agent finished", "success"
			}
			return fmt.Sprintf("agent finished: %s", truncate(p.Error, 30)), "error"
		}
		return "agent finished", "info"
	case orchestrator.EventIssueReadyForReview:
		return "ready for review", "success"
	case orchestrator.EventIssueCompleted:
		return "issue completed", "success"
	case orchestrator.EventBackoffQueued, orchestrator.EventIssueRetrying:
		if p, ok := event.Payload.(orchestrator.BackoffQueuedPayload); ok {
			return fmt.Sprintf("retry queued in %s (%s failed)", durationString(time.Until(p.RetryAt)), compactStage(p.Stage)), "warn"
		}
		if p, ok := event.Payload.(orchestrator.IssueRetryingPayload); ok {
			return fmt.Sprintf("retry queued in %s (%s failed)", durationString(time.Until(p.RetryAt)), compactStage(p.Stage)), "warn"
		}
		return "retry queued", "warn"
	case orchestrator.EventTimeoutDetected:
		return "timeout detected", "error"
	case orchestrator.EventStallDetected:
		return "stall detected", "warn"
	case orchestrator.EventMergeFailed:
		return "merge failed", "error"
	case orchestrator.EventFetchError:
		if p, ok := event.Payload.(orchestrator.FetchErrorPayload); ok {
			return fmt.Sprintf("fetch error (%s): %s", p.Operation, truncate(p.Error, 40)), "error"
		}
		return "fetch error", "error"
	default:
		return event.Type, "info"
	}
}

// sortedAgentKeys returns sorted agent keys.
func (m *Model) sortedAgentKeys() []string {
	if m.agentSortDirty || len(m.agentKeys) != len(m.agents) {
		m.agentKeys = m.agentKeys[:0]
		for issueID := range m.agents {
			m.agentKeys = append(m.agentKeys, issueID)
		}
		sort.Strings(m.agentKeys)
		m.agentSortDirty = false
	}
	return m.agentKeys
}

// sortedReviewKeys returns sorted review keys.
func (m *Model) sortedReviewKeys() []string {
	if len(m.reviewKeys) != len(m.reviews) {
		m.reviewKeys = m.reviewKeys[:0]
		for issueID := range m.reviews {
			m.reviewKeys = append(m.reviewKeys, issueID)
		}
		sort.Strings(m.reviewKeys)
	}
	return m.reviewKeys
}

// sortedBackoffKeys returns sorted backoff keys.
func (m *Model) sortedBackoffKeys() []string {
	if len(m.backoffKeys) != len(m.backoffs) {
		m.backoffKeys = m.backoffKeys[:0]
		for issueID := range m.backoffs {
			m.backoffKeys = append(m.backoffKeys, issueID)
		}
		sort.Strings(m.backoffKeys)
	}
	return m.backoffKeys
}

// sortedAgentRows returns sorted agent rows.
func (m *Model) sortedAgentRows() []AgentRow {
	sortedKeys := m.sortedAgentKeys()
	rows := make([]AgentRow, 0, len(sortedKeys))
	for _, issueID := range sortedKeys {
		rows = append(rows, m.agents[issueID])
	}
	return rows
}

// Helper functions

func durationString(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	seconds := int(d.Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	return fmt.Sprintf("%dh", hours)
}

func formatTokens(n int64) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func formatTokensShort(n int64) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func compactStage(stage types.Stage) string {
	switch stage {
	case types.StagePlan:
		return "Plan"
	case types.StageExecute:
		return "Exec"
	case types.StageVerify:
		return "Verify"
	case types.StageHumanReview:
		return "Review"
	default:
		return stage.String()
	}
}

func isActiveStatus(status string) bool {
	return status == "running"
}
