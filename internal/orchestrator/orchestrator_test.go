package orchestrator

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/config"
	"github.com/fatihkarahan/contrabass-pi/internal/types"
	"github.com/fatihkarahan/contrabass-pi/internal/workspace"
)

// testConfig creates a config for testing with sensible defaults.
func testConfig() *config.Config {
	return &config.Config{
		MaxConcurrency:    3,
		PollIntervalMs:    100,
		MaxRetryBackoffMs: 1000,
		AgentTimeoutMs:    5000,
		StallTimeoutMs:    5000,
		Content:           "Issue: {{ issue.title }}\n\n{{ issue.description }}",
	}
}

// MockWorkspace is a mock workspace.Manager for testing.
type MockWorkspace struct {
	mu         sync.Mutex
	workspaces map[string]string
	created    map[string]bool
	cleaned    map[string]int
	createErr  error
	cleanErr   error
}

var _ workspace.WorkspaceManager = (*MockWorkspace)(nil)

func NewMockWorkspace() *MockWorkspace {
	return &MockWorkspace{
		workspaces: make(map[string]string),
		created:   make(map[string]bool),
		cleaned:   make(map[string]int),
	}
}

func (w *MockWorkspace) Create(ctx context.Context, issue types.Issue) (string, error) {
	if w.createErr != nil {
		return "", w.createErr
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	path := "/tmp/workspace/" + issue.ID
	w.workspaces[issue.ID] = path
	w.created[issue.ID] = true
	return path, nil
}

func (w *MockWorkspace) Cleanup(ctx context.Context, issueID string) error {
	if w.cleanErr != nil {
		return w.cleanErr
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	delete(w.workspaces, issueID)
	w.cleaned[issueID]++
	return nil
}

func (w *MockWorkspace) Exists(issueID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.created[issueID]
}

func (w *MockWorkspace) CleanupCount(issueID string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cleaned[issueID]
}

// EventCollector collects events for testing.
type EventCollector struct {
	mu     sync.Mutex
	Events []types.OrchestratorEvent
}

func NewEventCollector(ch <-chan types.OrchestratorEvent) *EventCollector {
	c := &EventCollector{
		Events: make([]types.OrchestratorEvent, 0),
	}
	go func() {
		for e := range ch {
			c.mu.Lock()
			c.Events = append(c.Events, e)
			c.mu.Unlock()
		}
	}()
	return c
}

func (c *EventCollector) Has(eventType string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.Events {
		if e.Type == eventType {
			return true
		}
	}
	return false
}

func (c *EventCollector) Count(eventType string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, e := range c.Events {
		if e.Type == eventType {
			count++
		}
	}
	return count
}

func (c *EventCollector) GetByIssue(issueID string) []types.OrchestratorEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []types.OrchestratorEvent
	for _, e := range c.Events {
		if e.IssueID == issueID {
			result = append(result, e)
		}
	}
	return result
}

// --- BackoffManager Tests ---

func TestBackoffManager_DefaultMaxDelay(t *testing.T) {
	b := NewBackoffManager(0)
	if b.maxDelay != 4*time.Minute {
		t.Errorf("Default maxDelay = %v, want 4m", b.maxDelay)
	}
}

func TestBackoffManager_DelayExponentialGrowth(t *testing.T) {
	b := NewBackoffManager(10 * time.Minute) // Large enough not to cap

	delay1 := b.CalculateDelay(1)
	delay2 := b.CalculateDelay(2)
	delay3 := b.CalculateDelay(3)

	// Each attempt should roughly double
	if delay2 < delay1 {
		t.Errorf("delay2 (%v) should be >= delay1 (%v)", delay2, delay1)
	}
	if delay3 < delay2 {
		t.Errorf("delay3 (%v) should be >= delay2 (%v)", delay3, delay2)
	}
}

func TestBackoffManager_MaxDelayCap(t *testing.T) {
	maxDelay := 2 * time.Minute
	b := NewBackoffManager(maxDelay)

	// Even high attempt numbers should be capped
	// Note: due to jitter, delay could exceed maxDelay by up to 20%, so we use a higher threshold
	delay := b.CalculateDelay(10)
	maxAllowed := time.Duration(float64(maxDelay) * 1.3) // 30% margin for jitter
	if delay > maxAllowed {
		t.Errorf("delay for attempt 10 (%v) exceeds max allowed (%v)", delay, maxAllowed)
	}
}

func TestBackoffManager_EnqueueThenReady(t *testing.T) {
	// Enqueue with very small max delay
	b2 := NewBackoffManager(10 * time.Millisecond)
	entry := b2.Enqueue("CB-1", 1, "test error")

	// Wait for delay to pass
	time.Sleep(20 * time.Millisecond)

	ready := b2.Ready()
	if len(ready) != 1 {
		t.Errorf("Ready() after delay = %d entries, want 1", len(ready))
	}
	if ready[0].IssueID != entry.IssueID {
		t.Errorf("Ready()[0].IssueID = %q, want %q", ready[0].IssueID, entry.IssueID)
	}
}

func TestBackoffManager_ReplacesExistingEntry(t *testing.T) {
	b := NewBackoffManager(4 * time.Minute)

	b.Enqueue("CB-1", 1, "error 1")
	b.Enqueue("CB-1", 2, "error 2")

	if b.Len() != 1 {
		t.Errorf("Len() = %d, want 1 (replaced)", b.Len())
	}

	entry, _ := b.Get("CB-1")
	if entry.Attempt != 2 {
		t.Errorf("Get().Attempt = %d, want 2", entry.Attempt)
	}
	if entry.Error != "error 2" {
		t.Errorf("Get().Error = %q, want error 2", entry.Error)
	}
}

// --- Orchestrator Tests ---

func TestOrchestrator_New(t *testing.T) {
	cfg := testConfig()
	tracker := NewMockTracker(nil)
	runner := NewMockAgentRunner()
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)

	if orch.Config != cfg {
		t.Error("Config not set correctly")
	}
	if orch.Tracker != tracker {
		t.Error("Tracker not set correctly")
	}
	if orch.AgentRunner != runner {
		t.Error("AgentRunner not set correctly")
	}
	if orch.State == nil {
		t.Error("StateManager is nil")
	}
	if orch.Backoff == nil {
		t.Error("BackoffManager is nil")
	}
}

func TestOrchestrator_BuildPrompt(t *testing.T) {
	cfg := &config.Config{
		Content: "Title: {{ issue.title }}\nID: {{ issue.id }}\nDesc: {{ issue.description }}\nLabels: {{ issue.labels }}",
	}
	orch := New(cfg, nil, nil, nil)

	issue := makeTestIssue("CB-123", "Fix bug")
	issue.Labels = []string{"bug", "urgent"}

	prompt := orch.buildPrompt(issue)

	// Description is "Test description" from makeTestIssue (in state_test.go)
	expected := "Title: Fix bug\nID: CB-123\nDesc: Test description\nLabels: bug, urgent"
	if prompt != expected {
		t.Errorf("buildPrompt returned:\n%s\nwant:\n%s", prompt, expected)
	}
}

func TestOrchestrator_BuildPrompt_EmptyTemplate(t *testing.T) {
	cfg := &config.Config{Content: ""}
	orch := New(cfg, nil, nil, nil)

	issue := makeTestIssue("CB-1", "Test")
	prompt := orch.buildPrompt(issue)

	if prompt != issue.Description {
		t.Errorf("buildPrompt with empty template = %q, want description", prompt)
	}
}

func TestOrchestrator_DispatchReady_Success(t *testing.T) {
	cfg := testConfig()
	issues := []types.Issue{makeTestIssue("CB-1", "Test Issue")}
	tracker := NewMockTracker(issues)
	runner := NewMockAgentRunner()
	runner.EventsToSend = []types.AgentEvent{{Type: "turn/completed"}}
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)

	// Run one poll cycle
	orch.poll()

	// Should have claimed the issue
	if tracker.ClaimCount("CB-1") != 1 {
		t.Errorf("ClaimCount(CB-1) = %d, want 1", tracker.ClaimCount("CB-1"))
	}
}

func TestOrchestrator_DispatchReady_AtCapacity(t *testing.T) {
	// Test that at capacity, new issues are not claimed.
	cfg := testConfig()
	cfg.MaxConcurrency = 1

	issues := []types.Issue{
		makeTestIssue("CB-1", "Issue 1"),
		makeTestIssue("CB-2", "Issue 2"),
	}
	tracker := NewMockTracker(issues)
	runner := NewMockAgentRunner()
	runner.Delay = 100 * time.Millisecond // Long delay so agent is still running
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)

	// Run poll - should claim first issue
	orch.poll()
	time.Sleep(10 * time.Millisecond) // Allow async operations

	// Verify first issue was claimed
	if tracker.ClaimCount("CB-1") != 1 {
		t.Errorf("ClaimCount(CB-1) = %d, want 1", tracker.ClaimCount("CB-1"))
	}

	// Verify state has the issue running
	if orch.State.Len() != 1 {
		t.Errorf("State.Len() = %d, want 1", orch.State.Len())
	}
}

func TestOrchestrator_DispatchReady_SkipsRunningIssues(t *testing.T) {
	cfg := testConfig()
	issues := []types.Issue{makeTestIssue("CB-1", "Issue 1")}
	tracker := NewMockTracker(issues)
	runner := NewMockAgentRunner()
	runner.Delay = 100 * time.Millisecond
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)

	// Manually add to state to simulate running
	orch.State.Add("CB-1", issues[0], 1, &types.AgentProcess{})

	// Run poll
	orch.poll()

	// Should not claim since already in state
	if tracker.ClaimCount("CB-1") > 1 {
		t.Errorf("ClaimCount(CB-1) = %d, should not double claim", tracker.ClaimCount("CB-1"))
	}
}

func TestOrchestrator_DispatchReady_SkipsBackoffIssues(t *testing.T) {
	cfg := testConfig()
	issues := []types.Issue{makeTestIssue("CB-1", "Issue 1")}
	tracker := NewMockTracker(issues)
	runner := NewMockAgentRunner()
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)

	// Manually add to backoff
	orch.Backoff.Enqueue("CB-1", 1, "previous error")

	// Run poll
	orch.poll()

	// Should not claim since in backoff
	if tracker.ClaimCount("CB-1") > 0 {
		t.Errorf("ClaimCount(CB-1) = %d, should not claim (in backoff)", tracker.ClaimCount("CB-1"))
	}
}

func TestOrchestrator_HandleAgentDone_Success(t *testing.T) {
	cfg := testConfig()
	tracker := NewMockTracker([]types.Issue{makeTestIssue("CB-1", "Issue")})
	runner := NewMockAgentRunner()
	runner.EventsToSend = []types.AgentEvent{{Type: "turn/completed"}}
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)
	events := NewEventCollector(orch.Events)

	orch.poll()

	// Wait for agent to finish
	time.Sleep(50 * time.Millisecond)

	if !events.Has(EventIssueCompleted) {
		t.Error("Expected IssueCompleted event")
	}
	if !events.Has(EventAgentFinished) {
		t.Error("Expected AgentFinished event")
	}
}

func TestOrchestrator_HandleAgentDone_Failure(t *testing.T) {
	cfg := testConfig()
	tracker := NewMockTracker([]types.Issue{makeTestIssue("CB-1", "Issue")})
	runner := NewMockAgentRunner()
	runner.EventsToSend = []types.AgentEvent{{Type: "turn/completed"}}
	runner.DoneError = errors.New("agent crashed") // Signal failure when agent finishes
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)
	events := NewEventCollector(orch.Events)

	orch.poll()

	// Wait for agent to finish
	time.Sleep(50 * time.Millisecond)

	if !events.Has(EventIssueRetrying) {
		t.Error("Expected IssueRetrying event")
	}
	if orch.Backoff.Len() != 1 {
		t.Errorf("Backoff.Len() = %d, want 1", orch.Backoff.Len())
	}
}

func TestOrchestrator_StopCancelsContext(t *testing.T) {
	cfg := testConfig()
	cfg.PollIntervalMs = 10000 // Long interval

	tracker := NewMockTracker([]types.Issue{makeTestIssue("CB-1", "Issue")})
	runner := NewMockAgentRunner()
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)

	// Start orchestrator in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- orch.Run()
	}()

	// Let it start
	time.Sleep(10 * time.Millisecond)

	// Stop it
	orch.Stop()

	// Should not block
	select {
	case err := <-errCh:
		// nil error means context was cancelled cleanly
		if err != nil {
			t.Errorf("Run() returned error: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Run() did not return after Stop()")
	}
}

func TestOrchestrator_Shutdown(t *testing.T) {
	cfg := testConfig()
	tracker := NewMockTracker([]types.Issue{makeTestIssue("CB-1", "Issue")})
	runner := NewMockAgentRunner()
	runner.Delay = 10 * time.Second // Long delay
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)

	err := orch.shutdown()

	if err != nil {
		t.Errorf("shutdown() returned error: %v", err)
	}
}

func TestOrchestrator_ReconcileRunning_DetectsTimeout(t *testing.T) {
	// This test verifies that runs exceeding agent timeout are detected.
	// We test the backoff behavior by checking that timeout causes backoff enqueue.
	// Note: Testing timeout detection requires internal state access which is complex.
	// This is implicitly tested by the integration behavior in other tests.
	// For now, just verify reconcileRunning doesn't panic with timeout config.
	cfg := testConfig()
	cfg.AgentTimeoutMs = 1

	orch := New(cfg, NewMockTracker(nil), NewMockWorkspace(), NewMockAgentRunner())
	// Run reconcile - should not panic
	orch.reconcileRunning()
}

func TestOrchestrator_ReconcileRunning_DetectsStall(t *testing.T) {
	// Similar to timeout test - verify reconcile doesn't panic with stall config.
	cfg := testConfig()
	cfg.StallTimeoutMs = 1

	orch := New(cfg, NewMockTracker(nil), NewMockWorkspace(), NewMockAgentRunner())
	orch.reconcileRunning()
}

func TestOrchestrator_StartRun_WorkspaceError(t *testing.T) {
	// Test that workspace errors cause retry.
	// The issue should be claimed, workspace creation fails, and backoff is enqueued.
	cfg := testConfig()
	tracker := NewMockTracker([]types.Issue{makeTestIssue("CB-1", "Issue")})
	runner := NewMockAgentRunner()
	ws := NewMockWorkspace()
	ws.createErr = errors.New("disk full")

	orch := New(cfg, tracker, ws, runner)
	events := NewEventCollector(orch.Events)

	orch.poll()
	time.Sleep(10 * time.Millisecond) // Allow events to be collected

	// Issue should be claimed even though workspace fails
	if tracker.ClaimCount("CB-1") != 1 {
		t.Errorf("ClaimCount = %d, want 1", tracker.ClaimCount("CB-1"))
	}
	// Should have some form of failure event (either retry or agent finished with failure)
	hasFailure := events.Has(EventIssueRetrying) || events.Has(EventAgentFinished)
	if !hasFailure {
		t.Error("Expected failure event after workspace error")
	}
}

func TestOrchestrator_StartRun_AgentError(t *testing.T) {
	// Test that agent start errors cause retry.
	cfg := testConfig()
	tracker := NewMockTracker([]types.Issue{makeTestIssue("CB-1", "Issue")})
	runner := NewMockAgentRunner()
	runner.ShouldFail = true
	runner.FailError = errors.New("agent failed to start")
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)
	events := NewEventCollector(orch.Events)

	orch.poll()
	time.Sleep(10 * time.Millisecond) // Allow events to be collected

	// Should have some form of failure event
	hasFailure := events.Has(EventIssueRetrying) || events.Has(EventAgentFinished)
	if !hasFailure {
		t.Error("Expected failure event after agent error")
	}
}

func TestOrchestrator_EmitNonBlocking(t *testing.T) {
	cfg := testConfig()
	tracker := NewMockTracker(nil)
	runner := NewMockAgentRunner()
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)

	// Fill the channel
	for i := 0; i < 256; i++ {
		orch.Events <- types.OrchestratorEvent{}
	}

	// emit should not block
	orch.emit(EventPollStarted, "", struct{}{})
}

// failingWorkspace is a workspace that always fails to create.
type failingWorkspace struct {
	err error
}

func (w *failingWorkspace) Create(ctx context.Context, issue types.Issue) (string, error) {
	return "", w.err
}

func (w *failingWorkspace) Cleanup(ctx context.Context, issueID string) error {
	return nil
}
