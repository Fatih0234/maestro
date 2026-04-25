package web

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

// mockSnapshotProvider is a test double for SnapshotProvider.
type mockSnapshotProvider struct {
	snapshot Snapshot
}

func (m *mockSnapshotProvider) Snapshot() Snapshot {
	return m.snapshot
}

func TestHandleState(t *testing.T) {
	provider := &mockSnapshotProvider{
		snapshot: Snapshot{
			Running: []RunSnapshot{
				{IssueID: "CB-1", Stage: "plan", Attempt: 1},
			},
			Backoff: []BackoffSnapshot{
				{IssueID: "CB-2", Stage: "verify", Attempt: 2, RetryAt: time.Now().Add(time.Minute)},
			},
			Review: []ReviewSnapshot{
				{IssueID: "CB-3", Title: "Add OAuth", ReadyAt: time.Now()},
			},
			Stats: StatsSnapshot{RunningCount: 1, MaxConcurrency: 3},
		},
	}

	hub := NewHub()
	srv := NewServer(":0", provider, hub, nil, 3)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	rec := httptest.NewRecorder()

	srv.handleState(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var result Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(result.Running) != 1 || result.Running[0].IssueID != "CB-1" {
		t.Errorf("unexpected running: %+v", result.Running)
	}
	if len(result.Backoff) != 1 || result.Backoff[0].IssueID != "CB-2" {
		t.Errorf("unexpected backoff: %+v", result.Backoff)
	}
	if len(result.Review) != 1 || result.Review[0].IssueID != "CB-3" {
		t.Errorf("unexpected review: %+v", result.Review)
	}
	if result.Stats.RunningCount != 1 {
		t.Errorf("unexpected stats.running_count: %d", result.Stats.RunningCount)
	}
	if result.Stats.MaxConcurrency != 3 {
		t.Errorf("unexpected stats.max_concurrency: %d", result.Stats.MaxConcurrency)
	}
}

func TestHandleStateMethodNotAllowed(t *testing.T) {
	provider := &mockSnapshotProvider{}
	hub := NewHub()
	srv := NewServer(":0", provider, hub, nil, 0)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/state", nil)
	rec := httptest.NewRecorder()

	srv.handleState(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rec.Code)
	}
}

func TestHandleRefresh(t *testing.T) {
	provider := &mockSnapshotProvider{}
	hub := NewHub()
	srv := NewServer(":0", provider, hub, nil, 0)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/refresh", nil)
	rec := httptest.NewRecorder()

	srv.handleRefresh(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", rec.Code)
	}
}

func TestHandleRefreshMethodNotAllowed(t *testing.T) {
	provider := &mockSnapshotProvider{}
	hub := NewHub()
	srv := NewServer(":0", provider, hub, nil, 0)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/refresh", nil)
	rec := httptest.NewRecorder()

	srv.handleRefresh(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rec.Code)
	}
}

func TestHandleEventsStreamsSnapshotAndEvents(t *testing.T) {
	provider := &mockSnapshotProvider{
		snapshot: Snapshot{
			Running: []RunSnapshot{
				{IssueID: "CB-1", Stage: "execute", Attempt: 1},
			},
			Stats: StatsSnapshot{RunningCount: 1},
		},
	}

	hub := NewHub()
	eventCh := make(chan types.OrchestratorEvent, 4)
	srv := NewServer(":0", provider, hub, eventCh, 0)
	srv.StartEventBridge(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	// httptest.ResponseRecorder does not implement http.Flusher, so we wrap it.
	flushingRecorder := &flushingResponseRecorder{ResponseRecorder: rec}

	go func() {
		// Give the handler time to start and send the snapshot
		time.Sleep(50 * time.Millisecond)
		eventCh <- types.OrchestratorEvent{
			Type:      "stage.started",
			IssueID:   "CB-1",
			Timestamp: time.Now(),
			Payload:   map[string]string{"stage": "verify"},
		}
		// Wait for handler to consume, then close
		time.Sleep(100 * time.Millisecond)
		close(eventCh)
	}()

	srv.handleEvents(flushingRecorder, req)

	// Parse SSE lines
	scanner := bufio.NewScanner(rec.Body)
	var foundSnapshot, foundEvent bool
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: snapshot") {
			foundSnapshot = true
		}
		if strings.HasPrefix(line, "event: orchestrator") {
			foundEvent = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	if !foundSnapshot {
		t.Error("expected snapshot event, not found")
	}
	if !foundEvent {
		t.Error("expected orchestrator event, not found")
	}
}

func TestHandleEventsMethodNotAllowed(t *testing.T) {
	provider := &mockSnapshotProvider{}
	hub := NewHub()
	srv := NewServer(":0", provider, hub, nil, 0)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", nil)
	rec := httptest.NewRecorder()

	srv.handleEvents(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rec.Code)
	}
}

func TestHubBroadcast(t *testing.T) {
	hub := NewHub()
	ch1 := hub.Subscribe(64)
	ch2 := hub.Subscribe(64)
	defer hub.Unsubscribe(ch1)
	defer hub.Unsubscribe(ch2)

	event := WebEvent{Type: "test.event", IssueID: "CB-1"}
	hub.Broadcast(event)

	select {
	case e := <-ch1:
		if e.Type != "test.event" {
			t.Errorf("ch1: unexpected event type: %s", e.Type)
		}
	case <-time.After(time.Second):
		t.Error("ch1: timed out waiting for event")
	}

	select {
	case e := <-ch2:
		if e.Type != "test.event" {
			t.Errorf("ch2: unexpected event type: %s", e.Type)
		}
	case <-time.After(time.Second):
		t.Error("ch2: timed out waiting for event")
	}
}

func TestHubSlowConsumerDropsEvents(t *testing.T) {
	hub := NewHub()
	ch1 := hub.Subscribe(1) // tiny buffer
	ch2 := hub.Subscribe(64)
	defer hub.Unsubscribe(ch1)
	defer hub.Unsubscribe(ch2)

	// Fill ch1's buffer without reading
	hub.Broadcast(WebEvent{Type: "first"})

	// Now ch1 is full; this broadcast should drop for ch1 but deliver to ch2
	hub.Broadcast(WebEvent{Type: "second"})

	select {
	case e := <-ch1:
		if e.Type != "first" {
			t.Errorf("ch1: expected first event, got %s", e.Type)
		}
	case <-time.After(time.Second):
		t.Error("ch1: timed out waiting for first event")
	}

	// ch1 should not have second event (buffer was full)
	select {
	case <-ch1:
		t.Error("ch1: should not have received second event")
	case <-time.After(50 * time.Millisecond):
		// expected
	}

	select {
	case e := <-ch2:
		if e.Type != "first" {
			t.Errorf("ch2: expected first event, got %s", e.Type)
		}
	case <-time.After(time.Second):
		t.Error("ch2: timed out waiting for first event")
	}

	select {
	case e := <-ch2:
		if e.Type != "second" {
			t.Errorf("ch2: expected second event, got %s", e.Type)
		}
	case <-time.After(time.Second):
		t.Error("ch2: timed out waiting for second event")
	}
}

func TestWriteEvent(t *testing.T) {
	rec := httptest.NewRecorder()
	data := []byte(`{"key":"value"}`)
	if err := WriteEvent(rec, "test", data); err != nil {
		t.Fatalf("WriteEvent failed: %v", err)
	}

	body := rec.Body.String()
	expected := fmt.Sprintf("event: test\ndata: %s\n\n", data)
	if body != expected {
		t.Errorf("unexpected body:\n%s\nexpected:\n%s", body, expected)
	}
}

func TestWriteHeartbeat(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := WriteHeartbeat(rec); err != nil {
		t.Fatalf("WriteHeartbeat failed: %v", err)
	}

	body := rec.Body.String()
	expected := ":heartbeat\n\n"
	if body != expected {
		t.Errorf("unexpected body:\n%s\nexpected:\n%s", body, expected)
	}
}

func TestToWebEvent(t *testing.T) {
	event := types.OrchestratorEvent{
		Type:      "stage.started",
		IssueID:   "CB-1",
		Timestamp: time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC),
		Payload:   map[string]string{"stage": "plan"},
	}

	webEvent, err := toWebEvent(event)
	if err != nil {
		t.Fatalf("toWebEvent failed: %v", err)
	}

	if webEvent.Type != "stage.started" {
		t.Errorf("unexpected type: %s", webEvent.Type)
	}
	if webEvent.IssueID != "CB-1" {
		t.Errorf("unexpected issue_id: %s", webEvent.IssueID)
	}
	if !webEvent.Timestamp.Equal(event.Timestamp) {
		t.Errorf("unexpected timestamp: %v", webEvent.Timestamp)
	}
	if len(webEvent.Payload) == 0 {
		t.Error("expected non-empty payload")
	}
}

// flushingResponseRecorder wraps httptest.ResponseRecorder with a no-op Flusher.
type flushingResponseRecorder struct {
	*httptest.ResponseRecorder
}

func (f *flushingResponseRecorder) Flush() {}
