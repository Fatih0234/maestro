// Package tui provides the Charm Bubble Tea terminal UI.
package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

// SessionRow holds display data for one row in the session table.
type SessionRow struct {
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
	Attempt   int
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
	header := headerStyle.Render(fmt.Sprintf("%-8s %-24s %-7s %-8s %-10s %-6s %s\n",
		"Issue", "Title", "Stage", "PID", "Tokens", "Age", "Att"))

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
		if len(title) > 24 {
			title = title[:21] + "..."
		}
		title = fmt.Sprintf("%-24s", title)

		stageStr := fmt.Sprintf("%-7s", compactStage(row.Stage))

		glyph := statusGlyph(row.Status, t.spinner)
		if t.focused && i == t.selected {
			glyph = "▶"
		}

		attemptStr := fmt.Sprintf("#%d", row.Attempt)
		if row.Attempt <= 0 {
			attemptStr = "#1"
		}

		rowStr := fmt.Sprintf("%s %-8s %-24s %-7s %8d %-10s %-6s %-3s\n",
			glyph, row.IssueID, title, stageStr, row.PID, tokens, ageStr, attemptStr)

		// Apply coloring
		if t.focused && i == t.selected {
			rowStr = lipgloss.NewStyle().Bold(true).Background(lipgloss.Color("238")).Render(rowStr)
		} else if isActiveStatus(row.Status) {
			rowStr = lipgloss.NewStyle().Bold(true).Render(rowStr)
		} else if row.Status == "done" {
			rowStr = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render(rowStr)
		} else if row.Status == "failed" || row.Status == "timeout" || row.Status == "stalled" {
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

// statusGlyph returns a status indicator based on run status.
func statusGlyph(status string, spinner string) string {
	if isActiveStatus(status) {
		if spinner != "" {
			return spinner
		}
		return "●"
	}

	switch status {
	case "done":
		return "✓"
	case "failed":
		return "✗"
	case "timeout":
		return "⏱"
	case "stalled":
		return "⚠"
	default:
		return "○"
	}
}

