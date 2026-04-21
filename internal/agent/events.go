// Package agent provides the OpenCode agent runner implementation.
package agent

import (
	"github.com/fatihkarahan/contrabass-pi/internal/types"
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

// IsIdle checks if the event indicates the session has completed (status.type == "idle").
func IsIdle(event types.AgentEvent) bool {
	payload, ok := event.Payload.(map[string]interface{})
	if !ok {
		return false
	}
	properties, ok := payload["properties"].(map[string]interface{})
	if !ok {
		return false
	}
	status, ok := properties["status"].(map[string]interface{})
	if !ok {
		return false
	}
	statusType, _ := status["type"].(string)
	return statusType == "idle"
}

// IsError checks if the event indicates the session encountered an error.
func IsError(event types.AgentEvent) bool {
	return event.Type == EventTypeSessionError
}

// IsHeartbeat checks if the event is a server heartbeat (can be ignored).
func IsHeartbeat(event types.AgentEvent) bool {
	return event.Type == EventTypeHeartbeat || event.Type == EventTypeConnected
}

// ExtractSessionID extracts the session ID from an event's payload.
func ExtractSessionID(event types.AgentEvent) string {
	payload, ok := event.Payload.(map[string]interface{})
	if !ok {
		return ""
	}
	properties, ok := payload["properties"].(map[string]interface{})
	if !ok {
		return ""
	}
	sessionID, _ := properties["sessionID"].(string)
	return sessionID
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
