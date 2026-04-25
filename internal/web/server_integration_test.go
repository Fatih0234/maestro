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

// TestIntegration_StateEndpoint verifies GET /api/v1/state returns valid JSON.
func TestIntegration_StateEndpoint(t *testing.T) {
	provider := &mockSnapshotProvider{
		snapshot: Snapshot{
			Running: []RunSnapshot{
				{IssueID: "CB-1", Title: "Fix bug", Stage: "execute", Attempt: 1, PID: 12345, TokensIn: 1500, TokensOut: 2300},
			},
			Backoff: []BackoffSnapshot{
				{IssueID: "CB-2", Stage: "verify", Attempt: 2, RetryAt: time.Now().Add(time.Minute), Error: "verification failed"},
			},
			Review: []ReviewSnapshot{
				{IssueID: "CB-3", Title: "Add OAuth", Branch: "opencode/CB-3", ReadyAt: time.Now()},
			},
			Stats: StatsSnapshot{RunningCount: 1, MaxConcurrency: 3, TotalTokensIn: 1500, TotalTokensOut: 2300},
		},
	}

	hub := NewHub()
	srv := NewServer("127.0.0.1:0", provider, hub, nil, 3)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/state")
	if err != nil {
		t.Fatalf("GET /api/v1/state: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var snap Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}

	if len(snap.Running) != 1 || snap.Running[0].IssueID != "CB-1" {
		t.Errorf("unexpected running: %+v", snap.Running)
	}
	if snap.Stats.MaxConcurrency != 3 {
		t.Errorf("unexpected max_concurrency: %d", snap.Stats.MaxConcurrency)
	}
}

// TestIntegration_EventsEndpoint verifies SSE streaming with snapshot + live events.
func TestIntegration_EventsEndpoint(t *testing.T) {
	provider := &mockSnapshotProvider{
		snapshot: Snapshot{
			Running: []RunSnapshot{{IssueID: "CB-1", Stage: "plan", Attempt: 1}},
			Stats:   StatsSnapshot{RunningCount: 1},
		},
	}

	hub := NewHub()
	eventCh := make(chan types.OrchestratorEvent, 4)
	srv := NewServer("127.0.0.1:0", provider, hub, eventCh, 1)

	bridgeCtx, bridgeCancel := context.WithCancel(context.Background())
	srv.StartEventBridge(bridgeCtx)
	defer bridgeCancel()

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Use a cancellable request context so the server handler exits
	// when the client is done reading.
	reqCtx, reqCancel := context.WithCancel(context.Background())
	defer reqCancel()

	// Start SSE client
	clientDone := make(chan []string, 1)
	go func() {
		req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, ts.URL+"/api/v1/events", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			clientDone <- []string{fmt.Sprintf("error: %v", err)}
			return
		}
		defer resp.Body.Close()

		var lines []string
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
			// Each SSE event is ~3 lines (event: name, data: ..., blank).
			// We need snapshot (3) + orchestrator event (3) = 6 lines minimum.
			if len(lines) >= 6 {
				reqCancel() // close the request, causing server handler to exit
				clientDone <- lines
				return
			}
		}
		clientDone <- lines
	}()

	// Give client time to connect and receive snapshot
	time.Sleep(100 * time.Millisecond)

	// Emit a live event
	eventCh <- types.OrchestratorEvent{
		Type:      "stage.started",
		IssueID:   "CB-1",
		Timestamp: time.Now(),
		Payload:   map[string]string{"stage": "execute"},
	}

	// Wait for client to collect lines
	var lines []string
	select {
	case lines = <-clientDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SSE client")
	}

	// Verify we got a snapshot event
	var foundSnapshot, foundOrchestrator bool
	for _, line := range lines {
		if strings.HasPrefix(line, "event: snapshot") {
			foundSnapshot = true
		}
		if strings.HasPrefix(line, "event: orchestrator") {
			foundOrchestrator = true
		}
	}
	if !foundSnapshot {
		t.Errorf("expected snapshot event in SSE stream; got lines:\n%s", strings.Join(lines, "\n"))
	}
	if !foundOrchestrator {
		t.Errorf("expected orchestrator event in SSE stream; got lines:\n%s", strings.Join(lines, "\n"))
	}
}

// TestIntegration_RefreshEndpoint verifies POST /api/v1/refresh returns 202.
func TestIntegration_RefreshEndpoint(t *testing.T) {
	provider := &mockSnapshotProvider{}
	hub := NewHub()
	srv := NewServer("127.0.0.1:0", provider, hub, nil, 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/refresh", "", nil)
	if err != nil {
		t.Fatalf("POST /api/v1/refresh: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", resp.StatusCode)
	}
}

// TestIntegration_MultipleClients verifies multiple SSE clients receive events.
func TestIntegration_MultipleClients(t *testing.T) {
	provider := &mockSnapshotProvider{
		snapshot: Snapshot{Stats: StatsSnapshot{RunningCount: 0}},
	}

	hub := NewHub()
	eventCh := make(chan types.OrchestratorEvent, 4)
	srv := NewServer("127.0.0.1:0", provider, hub, eventCh, 0)

	bridgeCtx, bridgeCancel := context.WithCancel(context.Background())
	srv.StartEventBridge(bridgeCtx)
	defer bridgeCancel()

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Start two SSE clients with cancellable contexts
	client1Ctx, client1Cancel := context.WithCancel(context.Background())
	client2Ctx, client2Cancel := context.WithCancel(context.Background())
	defer client1Cancel()
	defer client2Cancel()

	client1Done := make(chan []string, 1)
	client2Done := make(chan []string, 1)

	startClient := func(ctx context.Context, cancel context.CancelFunc, done chan<- []string) {
		go func() {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events", nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				done <- []string{fmt.Sprintf("error: %v", err)}
				return
			}
			defer resp.Body.Close()

			var lines []string
			scanner := bufio.NewScanner(resp.Body)
			for scanner.Scan() {
				lines = append(lines, scanner.Text())
				if len(lines) >= 4 {
					cancel() // close request, causing server handler to exit
					done <- lines
					return
				}
			}
			done <- lines
		}()
	}

	startClient(client1Ctx, client1Cancel, client1Done)
	startClient(client2Ctx, client2Cancel, client2Done)

	time.Sleep(100 * time.Millisecond)

	// Broadcast one event
	eventCh <- types.OrchestratorEvent{
		Type:      "poll.started",
		Timestamp: time.Now(),
		Payload:   struct{}{},
	}

	var lines1, lines2 []string
	select {
	case lines1 = <-client1Done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for client 1")
	}
	select {
	case lines2 = <-client2Done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for client 2")
	}

	var c1HasEvent, c2HasEvent bool
	for _, line := range lines1 {
		if strings.HasPrefix(line, "event: orchestrator") {
			c1HasEvent = true
		}
	}
	for _, line := range lines2 {
		if strings.HasPrefix(line, "event: orchestrator") {
			c2HasEvent = true
		}
	}
	if !c1HasEvent {
		t.Errorf("client 1 did not receive orchestrator event; lines: %v", lines1)
	}
	if !c2HasEvent {
		t.Errorf("client 2 did not receive orchestrator event; lines: %v", lines2)
	}
}
