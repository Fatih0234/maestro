// Package web provides a lightweight HTTP API for the orchestrator.
package web

import (
	"encoding/json"
	"time"
)

// WebEvent is a JSON-friendly wrapper for orchestrator events.
type WebEvent struct {
	Type      string          `json:"type"`
	IssueID   string          `json:"issue_id,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}
