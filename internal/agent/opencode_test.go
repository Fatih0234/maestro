package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

func TestOpenCodeRunner_CompileTimeCheck(t *testing.T) {
	var _ types.AgentRunner = (*OpenCodeRunner)(nil)
	runner := NewOpenCodeRunner("opencode serve", 0, "", "", time.Second, "", "", "")
	if runner == nil {
		t.Fatal("expected non-nil runner")
	}
}

func TestOpenCodeRunner_Close(t *testing.T) {
	runner := NewOpenCodeRunner("opencode serve", 0, "", "", time.Second, "", "", "")
	if err := runner.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestOpenCodeRunner_Start(t *testing.T) {
	startStream := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			_, _ = io.WriteString(w, `{"id":"sess-1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/session/sess-1/prompt_async":
			w.WriteHeader(http.StatusNoContent)
			close(startStream)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			setSSEHeaders(w)
			<-startStream
			writeSSEEvent(w, "message", `{"type":"session.status","properties":{"sessionID":"sess-1","status":{"type":"busy"}}}`)
			writeSSEEvent(w, "message", `{"type":"message.part.updated","properties":{"sessionID":"sess-1"},"content":"hello"}`)
			writeSSEEvent(w, "message", `{"type":"session.status","properties":{"sessionID":"sess-1","status":{"type":"idle"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	workspace := t.TempDir()
	runner := newTestOpenCodeRunner(server.URL)
	primeTestOpenCodeServer(runner, workspace, server.URL, 4242)

	proc, err := runner.Start(context.Background(), types.Issue{ID: "CB-1", Title: "Test"}, workspace, "hello")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if proc.SessionID != "sess-1" {
		t.Errorf("SessionID = %v, want sess-1", proc.SessionID)
	}
	if proc.PID != 4242 {
		t.Errorf("PID = %v, want 4242", proc.PID)
	}

	events := collectOpenCodeEvents(t, proc.Events, proc.Done, 3, 3*time.Second)
	if len(events) != 3 {
		t.Errorf("got %d events, want 3", len(events))
	}
	if events[0].Type != "session.status" {
		t.Errorf("events[0].Type = %v, want session.status", events[0].Type)
	}
	if events[1].Type != "message.part.updated" {
		t.Errorf("events[1].Type = %v, want message.part.updated", events[1].Type)
	}
	if events[2].Type != "session.status" {
		t.Errorf("events[2].Type = %v, want session.status", events[2].Type)
	}
	assertDoneNil(t, proc.Done)
}

func TestOpenCodeRunner_StartWithAuth(t *testing.T) {
	var sessionAuth, promptAuth, eventAuth atomic.Bool
	startStream := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		isAuthed := ok && user == "alice" && pass == "secret"

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			sessionAuth.Store(isAuthed)
			_, _ = io.WriteString(w, `{"id":"sess-1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/session/sess-1/prompt_async":
			promptAuth.Store(isAuthed)
			w.WriteHeader(http.StatusNoContent)
			close(startStream)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			eventAuth.Store(isAuthed)
			setSSEHeaders(w)
			<-startStream
			writeSSEEvent(w, "message", `{"type":"session.status","properties":{"sessionID":"sess-1","status":{"type":"idle"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	workspace := t.TempDir()
	runner := NewOpenCodeRunner("opencode serve", 0, "secret", "alice", 2*time.Second, "", "", "")
	primeTestOpenCodeServer(runner, workspace, server.URL, 4242)

	proc, err := runner.Start(context.Background(), types.Issue{}, workspace, "hello")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	assertDoneNil(t, proc.Done)

	if !sessionAuth.Load() {
		t.Error("expected session endpoint to be called with auth")
	}
	if !promptAuth.Load() {
		t.Error("expected prompt endpoint to be called with auth")
	}
	if !eventAuth.Load() {
		t.Error("expected event endpoint to be called with auth")
	}
}

func TestOpenCodeRunner_Stop(t *testing.T) {
	abortCalled := make(chan string, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session/sess-1/abort":
			abortCalled <- r.URL.Path
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner := newTestOpenCodeRunner(server.URL)
	err := runner.Stop(&types.AgentProcess{SessionID: "sess-1"})
	if err != nil {
		t.Errorf("Stop() error = %v", err)
	}

	select {
	case path := <-abortCalled:
		if path != "/session/sess-1/abort" {
			t.Errorf("abort path = %v, want /session/sess-1/abort", path)
		}
	case <-time.After(time.Second):
		t.Fatal("expected abort endpoint to be called")
	}
}

func TestOpenCodeRunner_SessionError(t *testing.T) {
	startStream := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			_, _ = io.WriteString(w, `{"id":"sess-1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/session/sess-1/prompt_async":
			w.WriteHeader(http.StatusNoContent)
			close(startStream)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			setSSEHeaders(w)
			<-startStream
			writeSSEEvent(w, "message", `{"type":"session.error","properties":{"sessionID":"sess-1"},"error":"boom"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	workspace := t.TempDir()
	runner := newTestOpenCodeRunner(server.URL)
	primeTestOpenCodeServer(runner, workspace, server.URL, 4242)
	proc, err := runner.Start(context.Background(), types.Issue{}, workspace, "hello")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	select {
	case doneErr := <-proc.Done:
		if doneErr == nil {
			t.Error("expected error on Done channel")
		} else if !strings.Contains(doneErr.Error(), "session error") {
			t.Errorf("error = %v, want containing 'session error'", doneErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expected session error to terminate process")
	}
}

func TestOpenCodeRunner_SSEParsing(t *testing.T) {
	raw := strings.Join([]string{
		": this is a comment",
		"id: 42",
		"event: custom",
		"data: hello",
		"data: world",
		"retry: 1000",
		"",
		"data: single line",
		"",
		"data: spaced value",
		"",
	}, "\n")

	reader := newSSEReader(strings.NewReader(raw))

	event1, err := reader.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if event1.ID != "42" {
		t.Errorf("event1.ID = %v, want 42", event1.ID)
	}
	if event1.Event != "custom" {
		t.Errorf("event1.Event = %v, want custom", event1.Event)
	}
	if event1.Data != "hello\nworld" {
		t.Errorf("event1.Data = %v, want 'hello\\nworld'", event1.Data)
	}

	event2, err := reader.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if event2.ID != "42" {
		t.Errorf("event2.ID = %v, want 42", event2.ID)
	}
	if event2.Event != "message" {
		t.Errorf("event2.Event = %v, want message", event2.Event)
	}
	if event2.Data != "single line" {
		t.Errorf("event2.Data = %v, want 'single line'", event2.Data)
	}

	event3, err := reader.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if event3.ID != "42" {
		t.Errorf("event3.ID = %v, want 42", event3.ID)
	}
	if event3.Data != "spaced value" {
		t.Errorf("event3.Data = %v, want 'spaced value'", event3.Data)
	}

	_, err = reader.Next()
	if err != io.EOF {
		t.Errorf("Next() error = %v, want io.EOF", err)
	}
}

func TestOpenCodeRunner_SSESessionFilter(t *testing.T) {
	startStream := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			_, _ = io.WriteString(w, `{"id":"sess-1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/session/sess-1/prompt_async":
			w.WriteHeader(http.StatusNoContent)
			close(startStream)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			setSSEHeaders(w)
			<-startStream
			writeSSEEvent(w, "message", `{"type":"message.part.updated","properties":{"sessionID":"sess-2"},"content":"other"}`)
			writeSSEEvent(w, "message", `{"type":"message.part.updated","properties":{"sessionID":"sess-1"},"content":"mine"}`)
			writeSSEEvent(w, "message", `{"type":"session.status","properties":{"sessionID":"sess-1","status":{"type":"idle"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	workspace := t.TempDir()
	runner := newTestOpenCodeRunner(server.URL)
	primeTestOpenCodeServer(runner, workspace, server.URL, 4242)
	proc, err := runner.Start(context.Background(), types.Issue{}, workspace, "hello")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	events := collectOpenCodeEvents(t, proc.Events, proc.Done, 2, 3*time.Second)
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	// First event should be from our session, not sess-2
	payload := events[0].Payload.(map[string]interface{})
	if payload["content"] != "mine" {
		t.Errorf("events[0].content = %v, want mine", payload["content"])
	}
	if events[1].Type != "session.status" {
		t.Errorf("events[1].Type = %v, want session.status", events[1].Type)
	}
	assertDoneNil(t, proc.Done)
}

func TestOpenCodeRunner_HeartbeatIgnored(t *testing.T) {
	startStream := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			_, _ = io.WriteString(w, `{"id":"sess-1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/session/sess-1/prompt_async":
			w.WriteHeader(http.StatusNoContent)
			close(startStream)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			setSSEHeaders(w)
			<-startStream
			writeSSEEvent(w, "message", `{"type":"server.heartbeat","properties":{"sessionID":"sess-1"}}`)
			writeSSEEvent(w, "message", `{"type":"server.connected","properties":{"sessionID":"sess-1"}}`)
			writeSSEEvent(w, "message", `{"type":"session.status","properties":{"sessionID":"sess-1","status":{"type":"idle"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	workspace := t.TempDir()
	runner := newTestOpenCodeRunner(server.URL)
	primeTestOpenCodeServer(runner, workspace, server.URL, 4242)
	proc, err := runner.Start(context.Background(), types.Issue{}, workspace, "hello")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	events := collectOpenCodeEvents(t, proc.Events, proc.Done, 1, 3*time.Second)
	if len(events) != 1 {
		t.Errorf("got %d events, want 1 (heartbeats should be ignored)", len(events))
	}
	if events[0].Type != "session.status" {
		t.Errorf("events[0].Type = %v, want session.status", events[0].Type)
	}
	assertDoneNil(t, proc.Done)
}

func TestOpenCodeRunner_ConcurrentSessions(t *testing.T) {
	var sessionCounter int32
	var prompts sync.Map
	var aborts sync.Map

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			id := fmt.Sprintf("sess-%d", atomic.AddInt32(&sessionCounter, 1))
			_, _ = io.WriteString(w, mustJSON(map[string]interface{}{"id": id}))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/prompt_async"):
			parts := strings.Split(r.URL.Path, "/")
			if len(parts) >= 3 {
				sessionID := parts[2]
				prompts.Store(sessionID, true)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/abort"):
			parts := strings.Split(r.URL.Path, "/")
			if len(parts) >= 3 {
				sessionID := parts[2]
				aborts.Store(sessionID, true)
			}
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			setSSEHeaders(w)
			for !hasSession(&prompts, "sess-1") || !hasSession(&prompts, "sess-2") {
				time.Sleep(10 * time.Millisecond)
			}
			writeSSEEvent(w, "message", `{"type":"message.part.updated","properties":{"sessionID":"sess-1"},"content":"a"}`)
			writeSSEEvent(w, "message", `{"type":"message.part.updated","properties":{"sessionID":"sess-2"},"content":"b"}`)
			writeSSEEvent(w, "message", `{"type":"session.status","properties":{"sessionID":"sess-1","status":{"type":"idle"}}}`)
			writeSSEEvent(w, "message", `{"type":"session.status","properties":{"sessionID":"sess-2","status":{"type":"idle"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	workspace := t.TempDir()
	runner := newTestOpenCodeRunner(server.URL)
	primeTestOpenCodeServer(runner, workspace, server.URL, 4242)

	proc1, err := runner.Start(context.Background(), types.Issue{}, workspace, "hello-1")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	proc2, err := runner.Start(context.Background(), types.Issue{}, workspace, "hello-2")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if proc1.PID != 4242 {
		t.Errorf("proc1.PID = %v, want 4242", proc1.PID)
	}
	if proc2.PID != 4242 {
		t.Errorf("proc2.PID = %v, want 4242", proc2.PID)
	}

	events1 := collectOpenCodeEvents(t, proc1.Events, proc1.Done, 2, 4*time.Second)
	events2 := collectOpenCodeEvents(t, proc2.Events, proc2.Done, 2, 4*time.Second)

	payload1 := events1[0].Payload.(map[string]interface{})
	if payload1["content"] != "a" {
		t.Errorf("events1[0].content = %v, want a", payload1["content"])
	}
	payload2 := events2[0].Payload.(map[string]interface{})
	if payload2["content"] != "b" {
		t.Errorf("events2[0].content = %v, want b", payload2["content"])
	}
	assertDoneNil(t, proc1.Done)
	assertDoneNil(t, proc2.Done)

	if err := runner.Stop(proc1); err != nil {
		t.Errorf("Stop(proc1) error = %v", err)
	}
	if err := runner.Stop(proc2); err != nil {
		t.Errorf("Stop(proc2) error = %v", err)
	}

	// Wait for aborts to complete
	pollUntil(t, func() bool {
		return hasSession(&aborts, "sess-1") && hasSession(&aborts, "sess-2")
	}, time.Second, 20*time.Millisecond)
}

func TestOpenCodeRunner_PortForNewServerLocked(t *testing.T) {
	runner := NewOpenCodeRunner("opencode serve", 8787, "", "", time.Second, "", "", "")

	// No servers yet, should return base port
	runner.mu.Lock()
	port := runner.portForNewServerLocked()
	runner.mu.Unlock()
	if port != 8787 {
		t.Errorf("portForNewServerLocked() = %v, want 8787", port)
	}

	// With a server on 8787, should return next available
	runner.mu.Lock()
	runner.servers["w1"] = &openCodeServer{port: 8787}
	port = runner.portForNewServerLocked()
	runner.mu.Unlock()
	if port != 8788 {
		t.Errorf("portForNewServerLocked() with port 8787 in use = %v, want 8788", port)
	}

	// With servers on 8787 and 8788, should return next available
	runner.mu.Lock()
	runner.servers["w2"] = &openCodeServer{port: 8788}
	port = runner.portForNewServerLocked()
	runner.mu.Unlock()
	if port != 8789 {
		t.Errorf("portForNewServerLocked() with ports 8787,8788 in use = %v, want 8789", port)
	}

	// Disabled when port is 0
	runner2 := NewOpenCodeRunner("opencode serve", 0, "", "", time.Second, "", "", "")
	runner2.mu.Lock()
	port = runner2.portForNewServerLocked()
	runner2.mu.Unlock()
	if port != 0 {
		t.Errorf("portForNewServerLocked() with port=0 = %v, want 0", port)
	}
}

func TestOpenCodeRunner_WorkspaceScopedServers(t *testing.T) {
	startA := make(chan struct{})
	startB := make(chan struct{})

	serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			_, _ = io.WriteString(w, `{"id":"sess-a"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/session/sess-a/prompt_async":
			w.WriteHeader(http.StatusNoContent)
			close(startA)
		case r.Method == http.MethodPost && r.URL.Path == "/session/sess-a/abort":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			setSSEHeaders(w)
			<-startA
			writeSSEEvent(w, "message", `{"type":"message.part.updated","properties":{"sessionID":"sess-a"},"content":"workspace-a"}`)
			writeSSEEvent(w, "message", `{"type":"session.status","properties":{"sessionID":"sess-a","status":{"type":"idle"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer serverA.Close()

	serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			_, _ = io.WriteString(w, `{"id":"sess-b"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/session/sess-b/prompt_async":
			w.WriteHeader(http.StatusNoContent)
			close(startB)
		case r.Method == http.MethodPost && r.URL.Path == "/session/sess-b/abort":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			setSSEHeaders(w)
			<-startB
			writeSSEEvent(w, "message", `{"type":"message.part.updated","properties":{"sessionID":"sess-b"},"content":"workspace-b"}`)
			writeSSEEvent(w, "message", `{"type":"session.status","properties":{"sessionID":"sess-b","status":{"type":"idle"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer serverB.Close()

	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	runner := NewOpenCodeRunner("opencode serve", 0, "", "", 2*time.Second, "", "", "")
	primeTestOpenCodeServer(runner, workspaceA, serverA.URL, 4242)
	primeTestOpenCodeServer(runner, workspaceB, serverB.URL, 4343)

	procA, err := runner.Start(context.Background(), types.Issue{}, workspaceA, "hello-a")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	procB, err := runner.Start(context.Background(), types.Issue{}, workspaceB, "hello-b")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if procA.PID != 4242 {
		t.Errorf("procA.PID = %v, want 4242", procA.PID)
	}
	if procB.PID != 4343 {
		t.Errorf("procB.PID = %v, want 4343", procB.PID)
	}

	eventsA := collectOpenCodeEvents(t, procA.Events, procA.Done, 2, 3*time.Second)
	eventsB := collectOpenCodeEvents(t, procB.Events, procB.Done, 2, 3*time.Second)

	payloadA := eventsA[0].Payload.(map[string]interface{})
	if payloadA["content"] != "workspace-a" {
		t.Errorf("eventsA[0].content = %v, want workspace-a", payloadA["content"])
	}
	payloadB := eventsB[0].Payload.(map[string]interface{})
	if payloadB["content"] != "workspace-b" {
		t.Errorf("eventsB[0].content = %v, want workspace-b", payloadB["content"])
	}
	assertDoneNil(t, procA.Done)
	assertDoneNil(t, procB.Done)

	if err := runner.Stop(procA); err != nil {
		t.Errorf("Stop(procA) error = %v", err)
	}
	if err := runner.Stop(procB); err != nil {
		t.Errorf("Stop(procB) error = %v", err)
	}
}

func TestOpenCodeRunner_EnsureServerStartsWorkspacesInParallel(t *testing.T) {
	// This test requires actual process spawning which may not work in all test environments
	// Skipping for unit test coverage; covered by integration tests
	t.Skip("Skipping parallel server startup test - requires process spawning environment")
}

// Helper functions for tests

func newTestOpenCodeRunner(serverURL string) *OpenCodeRunner {
	r := NewOpenCodeRunner("opencode serve", 0, "", "", 2*time.Second, "", "", "")
	primeTestOpenCodeServer(r, "", serverURL, 4242)
	return r
}

func primeTestOpenCodeServer(r *OpenCodeRunner, workspace, serverURL string, pid int) {
	r.mu.Lock()
	r.servers[serverKey(workspace)] = &openCodeServer{
		url:     serverURL,
		process: &exec.Cmd{Process: &os.Process{Pid: pid}},
		port:    0,
	}
	r.mu.Unlock()
}

func setSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
}

func writeSSEEvent(w http.ResponseWriter, eventType, data string) {
	_, _ = fmt.Fprintf(w, "event: %s\n", eventType)
	for _, line := range strings.Split(data, "\n") {
		_, _ = fmt.Fprintf(w, "data: %s\n", line)
	}
	_, _ = fmt.Fprint(w, "\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func collectOpenCodeEvents(t *testing.T, events <-chan types.AgentEvent, done <-chan error, expected int, timeout time.Duration) []types.AgentEvent {
	t.Helper()
	out := make([]types.AgentEvent, 0, expected)
	deadline := time.After(timeout)

	for len(out) < expected {
		select {
		case ev, ok := <-events:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-deadline:
			t.Fatalf("timed out collecting events: got %d", len(out))
		}
	}

	return out
}

func assertDoneNil(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("expected nil on Done channel, got error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expected done channel to be signaled")
	}
}

func mustJSON(v map[string]interface{}) string {
	bytes, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(bytes)
}

func hasSession(m *sync.Map, sessionID string) bool {
	_, ok := m.Load(sessionID)
	return ok
}

// assert is a minimal assertion helper
func assert(t *testing.T, condition bool, msg string) {
	t.Helper()
	if !condition {
		t.Error(msg)
	}
}

// pollUntil polls until condition is true or timeout
func pollUntil(t *testing.T, condition func() bool, timeout time.Duration, interval time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if condition() {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("condition not met within %s", timeout)
		case <-time.After(interval):
		}
	}
}
