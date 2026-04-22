// Package tui provides the Charm Bubble Tea terminal UI.
package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

// SessionRow holds display data for one row in the session table.
type SessionRow struct {
	IssueID   string
	Title     string
	Phase     types.RunPhase
	PID       int
	Age       string
	TokensIn  int64
	TokensOut int64
	SessionID string
	LastEvent string
}

// Table holds the session table state.
type Table struct {
	width     int
	rows      []SessionRow
	selected  int
	focused   bool
	spinner   string
}

// NewTable creates a new Table.
func NewTable() Table {
	return Table{
		rows:     make([]SessionRow, 0),
		selected: 0,
	}
}

// Update updates the table with new rows and a spinner view.
func (t Table) Update(rows []SessionRow, spinner string) Table {
	t.rows = rows
	t.spinner = spinner
	return t
}

// SetWidth sets the table width.
func (t Table) SetWidth(w int) Table {
	t.width = w
	return t
}

// SetSelected sets the selected row index.
func (t Table) SetSelected(i int) Table {
	t.selected = i
	return t
}

// SetFocused sets whether the table is focused.
func (t Table) SetFocused(f bool) Table {
	t.focused = f
	return t
}

// RowCount returns the number of rows.
func (t Table) RowCount() int {
	return len(t.rows)
}

// Selected returns the selected row index.
func (t Table) Selected() int {
	return t.selected
}

// View renders the table.
func (t Table) View() string {
	if len(t.rows) == 0 {
		return lipgloss.NewStyle().Faint(true).Render("  No sessions running")
	}

	// Build header
	headerStyle := lipgloss.NewStyle().Bold(true).Faint(true)
	header := headerStyle.Render(fmt.Sprintf("%-8s %-26s %-8s %-8s %-10s %s\n",
		"Issue", "Title", "Phase", "PID", "Tokens", "Last"))

	// Build rows
	var rowStrs []string
	for i, row := range t.rows {
		ageStr := row.Age
		if ageStr == "" {
			ageStr = "0s"
		}

		tokens := fmt.Sprintf("%s/%s", formatTokensShort(row.TokensIn), formatTokensShort(row.TokensOut))

		// Truncate title
		title := row.Title
		if len(title) > 26 {
			title = title[:23] + "..."
		}
		title = fmt.Sprintf("%-26s", title)

		phaseStr := fmt.Sprintf("%-8s", compactPhase(row.Phase))

		glyph := statusGlyph(row.Phase, t.spinner)
		if t.focused && i == t.selected {
			glyph = "▶"
		}

		rowStr := fmt.Sprintf("%s %-8s %-26s %-8s %8d %-10s %s\n",
			glyph, row.IssueID, title, phaseStr, row.PID, tokens, ageStr)

		// Apply coloring
		if t.focused && i == t.selected {
			rowStr = lipgloss.NewStyle().Bold(true).Background(lipgloss.Color("238")).Render(rowStr)
		} else if isActivePhase(row.Phase) {
			rowStr = lipgloss.NewStyle().Bold(true).Render(rowStr)
		} else if row.Phase == types.PhaseSucceeded {
			rowStr = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render(rowStr)
		} else if row.Phase == types.PhaseFailed || row.Phase == types.PhaseTimedOut || row.Phase == types.PhaseStalled {
			rowStr = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(rowStr)
		}

		rowStrs = append(rowStrs, rowStr)
	}

	// Join rows
	result := header
	for _, r := range rowStrs {
		result += r
	}

	return result
}

// statusGlyph returns a status indicator based on phase.
func statusGlyph(phase types.RunPhase, spinner string) string {
	if isActivePhase(phase) {
		if spinner != "" {
			return spinner
		}
		return "●"
	}

	switch phase {
	case types.PhaseSucceeded:
		return "✓"
	case types.PhaseFailed:
		return "✗"
	case types.PhaseTimedOut:
		return "⏱"
	case types.PhaseStalled:
		return "⚠"
	default:
		return "○"
	}
}

var issueKeyPattern = regexp.MustCompile(`^[A-Z]+-[0-9]+$`)

func displayIssueID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "-"
	}
	if issueKeyPattern.MatchString(id) {
		return id
	}
	if len(id) > 12 {
		return id[:9] + "..."
	}
	return id
}