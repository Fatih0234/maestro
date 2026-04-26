// Package web provides a lightweight HTTP API for the orchestrator.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

//go:embed static/index.html
var dashboardHTML embed.FS

// SnapshotProvider provides a point-in-time snapshot of orchestrator state.
type SnapshotProvider interface {
	Snapshot() Snapshot
}

// RunSnapshot represents a single running issue in the snapshot.
type RunSnapshot struct {
	IssueID     string    `json:"issue_id"`
	Title       string    `json:"title,omitempty"`
	Stage       string    `json:"stage"`
	Attempt     int       `json:"attempt"`
	PID         int       `json:"pid,omitempty"`
	TokensIn    int64     `json:"tokens_in"`
	TokensOut   int64     `json:"tokens_out"`
	StartedAt   time.Time `json:"started_at"`
	LastEventAt time.Time `json:"last_event_at"`
	Error       string    `json:"error,omitempty"`
}

// BackoffSnapshot represents a queued retry in the snapshot.
type BackoffSnapshot struct {
	IssueID string    `json:"issue_id"`
	Stage   string    `json:"stage"`
	Attempt int       `json:"attempt"`
	RetryAt time.Time `json:"retry_at"`
	Error   string    `json:"error,omitempty"`
}

// ReviewSnapshot represents an issue awaiting human review.
type ReviewSnapshot struct {
	IssueID         string    `json:"issue_id"`
	Title           string    `json:"title,omitempty"`
	Branch          string    `json:"branch,omitempty"`
	WorkspacePath   string    `json:"workspace_path,omitempty"`
	ReadyAt         time.Time `json:"ready_at"`
	StagesCompleted []string  `json:"stages_completed,omitempty"`
}

// StatsSnapshot contains aggregate counters.
type StatsSnapshot struct {
	RunningCount    int   `json:"running_count"`
	MaxConcurrency  int   `json:"max_concurrency"`
	TotalTokensIn   int64 `json:"total_tokens_in"`
	TotalTokensOut  int64 `json:"total_tokens_out"`
}

// Snapshot is the full state exposed by the API.
type Snapshot struct {
	Running []RunSnapshot    `json:"running"`
	Backoff []BackoffSnapshot `json:"backoff"`
	Review  []ReviewSnapshot `json:"review"`
	Stats   StatsSnapshot    `json:"stats"`
}

// Server is a lightweight HTTP API for the orchestrator.
type Server struct {
	addr             string
	snapshotProvider SnapshotProvider
	hub              *Hub
	eventSource      <-chan types.OrchestratorEvent
	maxConcurrency   int
	httpServer       *http.Server
}

// NewServer creates a new web server.
func NewServer(addr string, provider SnapshotProvider, hub *Hub, eventSource <-chan types.OrchestratorEvent, maxConcurrency int) *Server {
	return &Server{
		addr:             addr,
		snapshotProvider: provider,
		hub:              hub,
		eventSource:      eventSource,
		maxConcurrency:   maxConcurrency,
	}
}

// Handler returns the HTTP handler for the server.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/state", s.handleState)
	mux.HandleFunc("/api/v1/events", s.handleEvents)
	mux.HandleFunc("/api/v1/refresh", s.handleRefresh)
	mux.HandleFunc("/", s.handleDashboard)
	return mux
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := dashboardHTML.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "Dashboard not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

// Start runs the HTTP server. It blocks until the server stops.
func (s *Server) Start() error {
	s.httpServer = &http.Server{
		Addr:        s.addr,
		Handler:     s.Handler(),
		ReadTimeout: 5 * time.Second,
		IdleTimeout: 120 * time.Second,
		// WriteTimeout is intentionally 0 because SSE streams are long-lived.
	}

	log.Printf("[web] Starting HTTP API on %s", s.addr)
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}

// StartEventBridge starts a goroutine that forwards orchestrator events to the hub.
// The goroutine exits when ctx is cancelled or the event source is closed.
func (s *Server) StartEventBridge(ctx context.Context) {
	go func() {
		for {
			select {
			case event, ok := <-s.eventSource:
				if !ok {
					return
				}
				webEvent, err := toWebEvent(event)
				if err != nil {
					log.Printf("[web] failed to convert event: %v", err)
					continue
				}
				s.hub.Broadcast(webEvent)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	snapshot := s.snapshotProvider.Snapshot()
	snapshot.Stats.MaxConcurrency = s.maxConcurrency

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(snapshot); err != nil {
		log.Printf("[web] failed to encode snapshot: %v", err)
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	if _, ok := w.(http.Flusher); !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Send initial snapshot
	snapshot := s.snapshotProvider.Snapshot()
	snapshot.Stats.MaxConcurrency = s.maxConcurrency
	data, err := json.Marshal(snapshot)
	if err != nil {
		http.Error(w, "Failed to marshal snapshot", http.StatusInternalServerError)
		return
	}
	if err := WriteEvent(w, "snapshot", data); err != nil {
		return
	}

	// Subscribe to live events
	ch := s.hub.Subscribe(64)
	defer s.hub.Unsubscribe(ch)

	// Heartbeat ticker
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case event, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				log.Printf("[web] failed to marshal event for SSE: %v", err)
				continue
			}
			if err := WriteEvent(w, "orchestrator", data); err != nil {
				return
			}
		case <-ticker.C:
			if err := WriteHeartbeat(w); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	fmt.Fprint(w, "{\"status\":\"accepted\"}\n")
}

func toWebEvent(event types.OrchestratorEvent) (WebEvent, error) {
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return WebEvent{}, err
	}

	timestamp := event.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}

	return WebEvent{
		Type:      event.Type,
		IssueID:   event.IssueID,
		Timestamp: timestamp,
		Payload:   payload,
	}, nil
}
