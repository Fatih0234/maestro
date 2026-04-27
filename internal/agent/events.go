// Package agent provides the OpenCode agent runner implementation.
package agent

import (
	"github.com/fatihkarahan/maestro/internal/types"
)

// Event type constants for agent events
const (
	EventTypeSessionStatus  = "session.status"
	EventTypeSessionError   = "session.error"
	EventTypeSessionOutput  = "session.output"
	EventTypeTokensUpdated  = "tokens.updated"
	EventTypeHeartbeat      = "server.heartbeat"
	EventTypeConnected      = "server.connected"
	EventTypeProtocolError  = "protocol/error"
	EventTypeMessageUpdated = "message.part.updated"
)

// ExtractTextContent extracts text content from an agent event.
func ExtractTextContent(event types.AgentEvent) string {
	payload, ok := event.Payload.(map[string]interface{})
	if !ok {
		return ""
	}

	if part, ok := payload["part"].(map[string]interface{}); ok {
		if text, ok := part["text"].(string); ok {
			return text
		}
	}

	if text, ok := payload["text"].(string); ok {
		return text
	}

	return ""
}

// ExtractTokens extracts token counts from a session.status event.
func ExtractTokens(event types.AgentEvent) (tokensIn, tokensOut int64) {
	payload, ok := event.Payload.(map[string]interface{})
	if !ok {
		return 0, 0
	}
	properties, ok := payload["properties"].(map[string]interface{})
	if !ok {
		return 0, 0
	}
	status, ok := properties["status"].(map[string]interface{})
	if !ok {
		return 0, 0
	}

	if ti, ok := status["tokens_in"].(float64); ok {
		tokensIn = int64(ti)
	}
	if to, ok := status["tokens_out"].(float64); ok {
		tokensOut = int64(to)
	}
	return tokensIn, tokensOut
}
