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
	EventTypeMessageDelta   = "message.part.delta"
)

// ExtractTextContent extracts text content from an agent event.
// Handles both message.part.updated (completed text parts) and
// message.part.delta (streaming text fragments).
func ExtractTextContent(event types.AgentEvent) string {
	payload, ok := event.Payload.(map[string]interface{})
	if !ok {
		return ""
	}

	// Current OpenCode events wrap everything in a "properties" object.
	properties, ok := payload["properties"].(map[string]interface{})
	if ok {
		// message.part.updated with a completed text part
		if part, ok := properties["part"].(map[string]interface{}); ok {
			if text, ok := part["text"].(string); ok {
				return text
			}
		}

		// message.part.delta streaming fragments
		if delta, ok := properties["delta"].(string); ok && delta != "" {
			if field, ok := properties["field"].(string); ok && field == "text" {
				return delta
			}
		}

		return ""
	}

	// Fallback: older payloads without the properties wrapper.
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

// ExtractTokens extracts token counts from agent events.
// Looks in the current OpenCode payload shape:
//   - properties.info.tokens   (message.updated events)
//   - properties.part.tokens   (message.part.updated step-finish events)
// Falls back to the older properties.status.tokens_in / tokens_out shape.
func ExtractTokens(event types.AgentEvent) (tokensIn, tokensOut int64) {
	payload, ok := event.Payload.(map[string]interface{})
	if !ok {
		return 0, 0
	}

	properties, ok := payload["properties"].(map[string]interface{})
	if !ok {
		// No properties wrapper — try top-level status tokens directly.
		if status, ok := payload["status"].(map[string]interface{}); ok {
			if ti, ok := status["tokens_in"].(float64); ok {
				tokensIn = int64(ti)
			}
			if to, ok := status["tokens_out"].(float64); ok {
				tokensOut = int64(to)
			}
		}
		return tokensIn, tokensOut
	}

	// Helper: try to read input/output from a tokens map.
	tryTokens := func(m map[string]interface{}) (int64, int64) {
		var ti, to int64
		if tmap, ok := m["tokens"].(map[string]interface{}); ok {
			if v, ok := tmap["input"].(float64); ok {
				ti = int64(v)
			}
			if v, ok := tmap["output"].(float64); ok {
				to = int64(v)
			}
		}
		return ti, to
	}

	// 1. message.updated carries tokens under properties.info.tokens
	if info, ok := properties["info"].(map[string]interface{}); ok {
		if ti, to := tryTokens(info); ti > 0 || to > 0 {
			return ti, to
		}
	}

	// 2. message.part.updated (step-finish) carries tokens under properties.part.tokens
	if part, ok := properties["part"].(map[string]interface{}); ok {
		if ti, to := tryTokens(part); ti > 0 || to > 0 {
			return ti, to
		}
	}

	// 3. Older shape: properties.status.tokens_in / tokens_out
	if status, ok := properties["status"].(map[string]interface{}); ok {
		if ti, ok := status["tokens_in"].(float64); ok {
			tokensIn = int64(ti)
		}
		if to, ok := status["tokens_out"].(float64); ok {
			tokensOut = int64(to)
		}
	}

	return tokensIn, tokensOut
}
