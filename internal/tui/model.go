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

// Model is the main Bubble Tea model for the Contrabass TUI.
type Model struct {
	width  int
	height int

	// State maps
	agents   map[string]AgentRow
	backoffs map[string]BackoffRow
	stats    HeaderStats

	// Session table
	table Table

	// Sorting caches
	agentKeys      []string
	backoffKeys    []string
	agentSortDirty bool

	// Event log
	eventLog    []EventLogEntry
	maxLogSize  int
	scrollPos   int

	// State
	quitting bool
}

// AgentRow holds display data for one running agent.
type AgentRow struct {
	IssueID   string
	Title     string
	Phase     types.RunPhase
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
	IssueID string
	Attempt int
	RetryIn string
	RetryAt time.Time
	Error   string
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
}

// NewModel creates a new TUI model.
func NewModel() Model {
	return Model{
		agents:     make(map[string]AgentRow),
		backoffs:   make(map[string]BackoffRow),
		maxLogSize: 100,
		eventLog:   make([]EventLogEntry, 0, 100),
		table:      NewTable(),
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
	backoff := m.renderBackoffQueue()
	events := m.renderEventLog()

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		lipgloss.NewStyle().PaddingBottom(1).Render(table),
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
			Phase:     row.Phase,
			PID:       row.PID,
			Age:       row.Age,
			TokensIn:  row.TokensIn,
			TokensOut: row.TokensOut,
			SessionID: row.SessionID,
			LastEvent: row.LastEvent,
		})
	}

	m.table = m.table.Update(sessionRows, "")
	return m.table.View()
}

// renderBackoffQueue renders the backoff queue section.
func (m Model) renderBackoffQueue() string {
	if len(m.backoffs) == 0 {
		return ""
	}

	headerStyle := lipgloss.NewStyle().Faint(true)
	header := headerStyle.Render(fmt.Sprintf("Backoff Queue                                      [%d]\n", len(m.backoffs)))

	rows := ""
	sortedKeys := m.sortedBackoffKeys()
	for _, issueID := range sortedKeys {
		row := m.backoffs[issueID]
		retryIn := row.RetryIn
		if retryIn == "" {
			retryIn = "pending"
		}
		rows += fmt.Sprintf("  %s  retry in %s (attempt %d)\n", issueID, retryIn, row.Attempt)
	}

	return header + rows
}

// renderEventLog renders the scrolling event log.
func (m Model) renderEventLog() string {
	headerStyle := lipgloss.NewStyle().Bold(true).Faint(true)
	header := headerStyle.Render("  [Events]\n")

	if len(m.eventLog) == 0 {
		return header + lipgloss.NewStyle().Faint(true).Render("  No events yet")
	}

	// Calculate visible range
	start := 0
	end := len(m.eventLog)
	maxVisible := 15 // rough estimate
	if end-start > maxVisible {
		start = end - maxVisible
	}

	entries := ""
	for i := start; i < end && i < len(m.eventLog); i++ {
		entry := m.eventLog[i]
		timeStr := entry.Timestamp.Format("15:04:05")
		issueStr := entry.IssueID
		if issueStr == "" {
			issueStr = "-"
		}
		msg := fmt.Sprintf("  %s %s %s\n", timeStr, issueStr, entry.Message)
		entries += msg
	}

	return header + entries
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

	// Log the event
	m.pushEvent(event.IssueID, fmt.Sprintf("%s", event.Type))

	switch event.Type {
	case orchestrator.EventAgentStarted:
		if payload, ok := event.Payload.(orchestrator.AgentStartedPayload); ok {
			issueID := event.IssueID
			if issueID == "" {
				issueID = "-"
			}
			m.agents[event.IssueID] = AgentRow{
				IssueID:   issueID,
				Phase:     types.PhaseInitializingSession,
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
				row.LastEvent = "tokens updated"
				m.agents[event.IssueID] = row
			}
		}

	case orchestrator.EventAgentFinished:
		if payload, ok := event.Payload.(orchestrator.AgentFinishedPayload); ok {
			if !payload.Success {
				// Move to backoff or failed state
				if row, exists := m.agents[event.IssueID]; exists {
					row.Phase = types.PhaseFailed
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
				IssueID: event.IssueID,
				Attempt: payload.Attempt,
				RetryAt: payload.RetryAt,
			}
		}

	case orchestrator.EventIssueCompleted:
		delete(m.agents, event.IssueID)
		delete(m.backoffs, event.IssueID)
		m.agentSortDirty = true

	case orchestrator.EventTimeoutDetected:
		if row, exists := m.agents[event.IssueID]; exists {
			row.Phase = types.PhaseTimedOut
			row.LastEvent = "timeout"
			m.agents[event.IssueID] = row
		}

	case orchestrator.EventStallDetected:
		if row, exists := m.agents[event.IssueID]; exists {
			row.Phase = types.PhaseStalled
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

	return m
}

// pushEvent adds an event to the log.
func (m *Model) pushEvent(issueID, message string) {
	entry := EventLogEntry{
		Timestamp: time.Now(),
		IssueID:   issueID,
		Message:   message,
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

func compactPhase(phase types.RunPhase) string {
	switch phase {
	case types.PhasePreparingWorkspace:
		return "Prep"
	case types.PhaseBuildingPrompt:
		return "Prompt"
	case types.PhaseLaunchingAgentProcess:
		return "Launch"
	case types.PhaseInitializingSession:
		return "Init"
	case types.PhaseStreamingTurn:
		return "Turn"
	case types.PhaseFinishing:
		return "Finish"
	case types.PhaseSucceeded:
		return "Done"
	case types.PhaseFailed:
		return "Failed"
	case types.PhaseTimedOut:
		return "Timeout"
	case types.PhaseStalled:
		return "Stalled"
	default:
		return phase.String()
	}
}

func isActivePhase(phase types.RunPhase) bool {
	switch phase {
	case types.PhaseInitializingSession,
		types.PhaseLaunchingAgentProcess,
		types.PhasePreparingWorkspace,
		types.PhaseBuildingPrompt,
		types.PhaseStreamingTurn,
		types.PhaseFinishing:
		return true
	default:
		return false
	}
}