// Package tui provides the Charm Bubble Tea terminal UI.
package tui

import (
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

// OrchestratorEventMsg wraps orchestrator events for Bubble Tea.
type OrchestratorEventMsg struct {
	Event types.OrchestratorEvent
}

// tickMsg is sent periodically to refresh derived fields like ages.
type tickMsg time.Time