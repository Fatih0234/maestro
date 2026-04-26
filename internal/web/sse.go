// Package web provides a lightweight HTTP API for the orchestrator.
package web

import (
	"fmt"
	"net/http"
	"strings"
)

// WriteEvent writes an SSE event to the response writer.
// The eventName may be empty for unnamed events.
// Multi-line data is split and each line is prefixed with "data: "
// as required by the SSE spec.
func WriteEvent(w http.ResponseWriter, eventName string, data []byte) error {
	if eventName != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", eventName); err != nil {
			return err
		}
	}
	// SSE requires each line of data to be prefixed with "data: ".
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if _, err := fmt.Fprintf(w, "data: %s\n", line); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprint(w, "\n"); err != nil {
		return err
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

// WriteHeartbeat writes an SSE comment heartbeat to keep the connection alive.
func WriteHeartbeat(w http.ResponseWriter) error {
	if _, err := fmt.Fprint(w, ":heartbeat\n\n"); err != nil {
		return err
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}
