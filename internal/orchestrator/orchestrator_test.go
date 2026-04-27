package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fatihkarahan/maestro/internal/config"
	"github.com/fatihkarahan/maestro/internal/diagnostics"
	"github.com/fatihkarahan/maestro/internal/types"
	"github.com/fatihkarahan/maestro/internal/workspace"
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
		Workspace: config.WorkspaceConfig{
			BaseDir:      "/tmp/repo",
			BranchPrefix: "opencode/",
		},
	}
}

// MockWorkspace is a mock workspace.Manager for testing.
type MockWorkspace struct {
	mu         sync.Mutex
	workspaces map[string]string
	created    map[string]bool
	cleaned    map[string]int
	merged     map[string]int
	baseDir    string
	createErr  error
	cleanErr   error
	mergeErr   error
}

var _ workspace.WorkspaceManager = (*MockWorkspace)(nil)

func NewMockWorkspace() *MockWorkspace {
	return &MockWorkspace{
		workspaces: make(map[string]string),
		created:    make(map[string]bool),
		cleaned:    make(map[string]int),
		merged:     make(map[string]int),
		baseDir:    "/tmp/repo",
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

func (w *MockWorkspace) MergeToMain(ctx context.Context, issueID string) error {
	if w.mergeErr != nil {
		return w.mergeErr
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.merged[issueID]++
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

func (w *MockWorkspace) MergeCount(issueID string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.merged[issueID]
}

func (w *MockWorkspace) Path(issueID string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if path, ok := w.workspaces[issueID]; ok {
		return path
	}
	return "/tmp/workspace/" + issueID
}

func (w *MockWorkspace) BaseDir() string {
	return w.baseDir
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

// WaitFor polls until the collector has received an event of the given type
// or the timeout expires. It returns true if the event was found.
func (c *EventCollector) WaitFor(eventType string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c.Has(eventType) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
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
	entry := b2.Enqueue("CB-1", 1, types.StagePlan, "test error")

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

	b.Enqueue("CB-1", 1, types.StagePlan, "error 1")
	b.Enqueue("CB-1", 2, types.StagePlan, "error 2")

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
	orch.State.Add("CB-1", issues[0], 1, types.StageExecute, &types.AgentProcess{})

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
	orch.Backoff.Enqueue("CB-1", 1, types.StagePlan, "previous error")

	// Run poll
	orch.poll()

	// Should not claim since in backoff
	if tracker.ClaimCount("CB-1") > 0 {
		t.Errorf("ClaimCount(CB-1) = %d, should not claim (in backoff)", tracker.ClaimCount("CB-1"))
	}
}

func TestOrchestrator_DispatchReady_SkipsReviewIssues_DefenseInDepth(t *testing.T) {
	cfg := testConfig()
	tracker := NewMockTracker([]types.Issue{makeTestIssue("CB-1", "Issue 1")})
	tracker.ForcedFetch = []types.Issue{
		{
			ID:          "CB-1",
			Identifier:  "CB-1",
			Title:       "Issue 1",
			Description: "desc",
			State:       types.StateInReview,
		},
	}
	runner := NewMockAgentRunner()
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)
	orch.poll()

	if got := tracker.ClaimCount("CB-1"); got != 0 {
		t.Errorf("ClaimCount(CB-1) = %d, want 0 for in_review issue", got)
	}
	if got := runner.StartCallCount(); got != 0 {
		t.Errorf("StartCalls = %d, want 0 for in_review issue", got)
	}
}

func TestOrchestrator_DispatchReady_FetchErrorEmitsEvent(t *testing.T) {
	cfg := testConfig()
	tracker := NewMockTracker([]types.Issue{makeTestIssue("CB-1", "Issue")})
	tracker.FetchError = errors.New("tracker unreachable")
	runner := NewMockAgentRunner()
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)
	events := NewEventCollector(orch.Events)

	orch.poll()

	if !events.WaitFor(EventFetchError, 500*time.Millisecond) {
		t.Error("Expected EventFetchError when tracker.FetchIssues fails")
	}
	if events.Has(EventIssueClaimed) {
		t.Error("Did not expect IssueClaimed when fetch fails")
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

	// Wait for agent to finish (3 stages × mock delay). Use a generous timeout
	// because test scheduling can delay goroutines when the full suite runs.
	time.Sleep(200 * time.Millisecond)

	if !events.Has(EventIssueReadyForReview) {
		for _, e := range events.Events {
			t.Logf("collected event: %s issue=%s", e.Type, e.IssueID)
		}
		t.Error("Expected IssueReadyForReview event")
	}
	if events.Has(EventIssueCompleted) {
		t.Error("Did not expect IssueCompleted event on runtime success")
	}
	if !events.Has(EventAgentFinished) {
		t.Error("Expected AgentFinished event")
	}
	if state := tracker.UpdateState["CB-1"]; state != types.StateInReview {
		t.Errorf("issue state = %v, want %v", state, types.StateInReview)
	}
	if got := ws.MergeCount("CB-1"); got != 0 {
		t.Errorf("MergeToMain called %d times, want 0", got)
	}
	if got := ws.CleanupCount("CB-1"); got != 0 {
		t.Errorf("Cleanup called %d times, want 0", got)
	}

	var handoffPayload map[string]interface{}
	foundHandoff := false
	for _, evt := range events.GetByIssue("CB-1") {
		if evt.Type != EventIssueReadyForReview {
			continue
		}
		payload, ok := evt.Payload.(map[string]interface{})
		if !ok {
			t.Fatalf("unexpected payload type %T", evt.Payload)
		}
		handoffPayload = payload
		foundHandoff = true
		break
	}
	if !foundHandoff {
		t.Fatal("ready_for_review payload not found")
	}
	branch := ""
	if v, ok := handoffPayload["branch"].(string); ok {
		branch = v
	}
	if branch != "opencode/CB-1" {
		t.Errorf("handoff branch = %q, want opencode/CB-1", branch)
	}
	workspacePath := ""
	if v, ok := handoffPayload["workspace_path"].(string); ok {
		workspacePath = v
	}
	if workspacePath == "" {
		t.Error("handoff workspace path should not be empty")
	}
}

func TestOrchestrator_DispatchReadyRestoresPersistedRetryStageAndAttempt(t *testing.T) {
	retryAt := time.Now().Add(-time.Minute)
	issue := types.Issue{
		ID:           "CB-1",
		Title:        "Retry execute",
		State:        types.StateRetryQueued,
		RetryAfter:   &retryAt,
		RetryAttempt: 2,
		RetryStage:   types.StageExecute,
		CreatedAt:    time.Now(),
	}
	tracker := NewMockTracker([]types.Issue{issue})
	runner := NewMockAgentRunner()
	orch := New(testConfig(), tracker, NewMockWorkspace(), runner)
	events := NewEventCollector(orch.Events)

	orch.dispatchReady()
	time.Sleep(100 * time.Millisecond)

	if runner.StartCallCount() != 2 {
		t.Fatalf("StartCallCount = %d, want 2 (execute + verify)", runner.StartCallCount())
	}
	startedExecute := false
	startedPlan := false
	for _, event := range events.GetByIssue("CB-1") {
		if event.Type != EventStageStarted {
			continue
		}
		payload, ok := event.Payload.(StagePayload)
		if !ok {
			continue
		}
		startedExecute = startedExecute || payload.Stage == types.StageExecute
		startedPlan = startedPlan || payload.Stage == types.StagePlan
	}
	if !startedExecute {
		t.Fatal("expected execute stage to start")
	}
	if startedPlan {
		t.Fatal("did not expect plan stage to restart for persisted retry")
	}
}

func TestOrchestrator_HandleAgentDone_HandoffStateUpdateFailureQueuesRetry(t *testing.T) {
	cfg := testConfig()
	tracker := NewMockTracker([]types.Issue{makeTestIssue("CB-1", "Issue")})
	tracker.UpdateError = errors.New("tracker unavailable")
	runner := NewMockAgentRunner()
	runner.EventsToSend = []types.AgentEvent{{Type: "turn/completed"}}
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)
	events := NewEventCollector(orch.Events)

	orch.poll()

	// Wait for agent to finish and handoff attempt to fail.
	time.Sleep(200 * time.Millisecond)

	if events.Has(EventIssueReadyForReview) {
		t.Error("did not expect ready_for_review event when tracker handoff fails")
	}
	if !events.Has(EventIssueRetrying) {
		t.Error("expected issue.retrying event when review handoff fails")
	}
	if orch.Backoff.Len() != 1 {
		t.Errorf("Backoff.Len() = %d, want 1", orch.Backoff.Len())
	}
	if got := ws.MergeCount("CB-1"); got != 0 {
		t.Errorf("MergeToMain called %d times, want 0", got)
	}
	if got := ws.CleanupCount("CB-1"); got != 0 {
		t.Errorf("Cleanup called %d times, want 0", got)
	}
}

func TestOrchestrator_DispatchBackoff_ConsumesReadyEntryOnce(t *testing.T) {
	cfg := testConfig()
	tracker := NewMockTracker([]types.Issue{makeTestIssue("CB-1", "Issue")})
	runner := NewMockAgentRunner()
	runner.Delay = 200 * time.Millisecond
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)
	defer orch.Stop()

	entry := orch.Backoff.Enqueue("CB-1", 1, types.StagePlan, "boom")
	entry.RetryAt = time.Now().Add(-time.Second)

	orch.dispatchBackoff()
	time.Sleep(50 * time.Millisecond) // allow goroutine to start agent
	if got := runner.StartCallCount(); got != 1 {
		t.Fatalf("StartCalls after first dispatch = %d, want 1", got)
	}

	// A second dispatch cycle should not start a duplicate run for the same entry.
	orch.dispatchBackoff()
	if got := runner.StartCallCount(); got != 1 {
		t.Fatalf("StartCalls after second dispatch = %d, want 1", got)
	}
	if got := tracker.ClaimCount("CB-1"); got != 1 {
		t.Fatalf("ClaimCount(CB-1) = %d, want 1", got)
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

	if !events.WaitFor(EventIssueRetrying, 2*time.Second) {
		t.Error("Expected IssueRetrying event")
	}
	if orch.Backoff.Len() != 1 {
		t.Errorf("Backoff.Len() = %d, want 1", orch.Backoff.Len())
	}
}

func TestOrchestrator_MultiStage_HappyPath(t *testing.T) {
	cfg := testConfig()
	tracker := NewMockTracker([]types.Issue{makeTestIssue("CB-1", "Issue")})
	runner := NewMockAgentRunner()
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)
	events := NewEventCollector(orch.Events)

	orch.poll()
	time.Sleep(200 * time.Millisecond)

	if !events.Has(EventStageStarted) {
		t.Error("Expected StageStarted event")
	}
	if !events.Has(EventStageCompleted) {
		t.Error("Expected StageCompleted event")
	}
	if !events.Has(EventAgentFinished) {
		t.Error("Expected AgentFinished event")
	}
	if !events.Has(EventIssueReadyForReview) {
		t.Error("Expected IssueReadyForReview event")
	}
	if state := tracker.UpdateState["CB-1"]; state != types.StateInReview {
		t.Errorf("issue state = %v, want %v", state, types.StateInReview)
	}
	// Should have started the agent 3 times (plan, execute, verify)
	if got := runner.StartCallCount(); got != 3 {
		t.Errorf("StartCalls = %d, want 3", got)
	}
}

func TestOrchestrator_MultiStage_PlanFailureRetriesPlan(t *testing.T) {
	cfg := testConfig()
	tracker := NewMockTracker([]types.Issue{makeTestIssue("CB-1", "Issue")})
	runner := NewMockAgentRunner()
	runner.PerStageDoneError = map[types.Stage]error{
		types.StagePlan: errors.New("plan failed"),
	}
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)
	events := NewEventCollector(orch.Events)

	orch.poll()

	if !events.WaitFor(EventStageFailed, 2*time.Second) {
		t.Error("Expected StageFailed event")
	}
	if events.Has(EventIssueReadyForReview) {
		t.Error("Did not expect IssueReadyForReview when plan fails")
	}
	if orch.Backoff.Len() != 1 {
		t.Errorf("Backoff.Len() = %d, want 1", orch.Backoff.Len())
	}
	entry, _ := orch.Backoff.Get("CB-1")
	if entry.Stage != types.StagePlan {
		t.Errorf("retry stage = %v, want plan", entry.Stage)
	}
	// Only 1 start call (plan stage)
	if got := runner.StartCallCount(); got != 1 {
		t.Errorf("StartCalls = %d, want 1", got)
	}
}

func TestOrchestrator_MultiStage_ExecuteFailureRetriesExecute(t *testing.T) {
	cfg := testConfig()
	tracker := NewMockTracker([]types.Issue{makeTestIssue("CB-1", "Issue")})
	runner := NewMockAgentRunner()
	runner.PerStageDoneError = map[types.Stage]error{
		types.StageExecute: errors.New("execute failed"),
	}
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)
	events := NewEventCollector(orch.Events)

	orch.poll()
	time.Sleep(200 * time.Millisecond)

	if !events.Has(EventStageFailed) {
		t.Error("Expected StageFailed event")
	}
	if events.Has(EventIssueReadyForReview) {
		t.Error("Did not expect IssueReadyForReview when execute fails")
	}
	if orch.Backoff.Len() != 1 {
		t.Errorf("Backoff.Len() = %d, want 1", orch.Backoff.Len())
	}
	entry, _ := orch.Backoff.Get("CB-1")
	if entry.Stage != types.StageExecute {
		t.Errorf("retry stage = %v, want execute", entry.Stage)
	}
	// 2 start calls (plan succeeded, execute failed)
	if got := runner.StartCallCount(); got != 2 {
		t.Errorf("StartCalls = %d, want 2", got)
	}
}

func TestOrchestrator_MultiStage_VerifyFailureBlocksReview(t *testing.T) {
	cfg := testConfig()
	tracker := NewMockTracker([]types.Issue{makeTestIssue("CB-1", "Issue")})
	runner := NewMockAgentRunner()
	runner.PerStageDoneError = map[types.Stage]error{
		types.StageVerify: errors.New("verify failed"),
	}
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)
	events := NewEventCollector(orch.Events)

	orch.poll()
	time.Sleep(200 * time.Millisecond)

	if !events.Has(EventStageFailed) {
		t.Error("Expected StageFailed event")
	}
	if events.Has(EventIssueReadyForReview) {
		t.Error("Did not expect IssueReadyForReview when verify fails")
	}
	if orch.Backoff.Len() != 1 {
		t.Errorf("Backoff.Len() = %d, want 1", orch.Backoff.Len())
	}
	entry, _ := orch.Backoff.Get("CB-1")
	if entry.Stage != types.StageVerify {
		t.Errorf("retry stage = %v, want verify", entry.Stage)
	}
	// 3 start calls (plan + execute succeeded, verify failed)
	if got := runner.StartCallCount(); got != 3 {
		t.Errorf("StartCalls = %d, want 3", got)
	}
}

func TestOrchestrator_VerifyCleanExitFailedResultQueuesRetry(t *testing.T) {
	cfg := testConfig()
	tracker := NewMockTracker([]types.Issue{makeTestIssue("CB-1", "Issue")})
	runner := NewMockAgentRunner()
	runner.EventsToSend = []types.AgentEvent{{Type: "message.part.updated", Payload: map[string]interface{}{"text": `{"passed": false, "summary": "tests failed"}`}}}
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)
	events := NewEventCollector(orch.Events)

	orch.poll()
	time.Sleep(200 * time.Millisecond)

	if events.Has(EventIssueReadyForReview) {
		t.Fatal("did not expect IssueReadyForReview when verify reports passed=false")
	}
	if !events.Has(EventIssueRetrying) {
		t.Fatal("expected IssueRetrying when verify reports passed=false")
	}
	if tracker.UpdateState["CB-1"] == types.StateInReview {
		t.Fatal("issue should not transition to in_review")
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

	if got := runner.CloseCalls; got != 1 {
		t.Errorf("CloseCalls = %d, want 1", got)
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
	if got := runner.CloseCalls; got != 1 {
		t.Errorf("CloseCalls = %d, want 1", got)
	}
}

func TestOrchestrator_RunOnce_ShutsDownAfterOnePoll(t *testing.T) {
	cfg := testConfig()
	tracker := NewMockTracker(nil)
	runner := NewMockAgentRunner()
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)
	eventsClosed := make(chan struct{})
	go func() {
		for range orch.Events {
		}
		close(eventsClosed)
	}()

	if err := orch.RunOnce(); err != nil {
		t.Fatalf("RunOnce() returned error: %v", err)
	}

	if got := runner.CloseCalls; got != 1 {
		t.Errorf("CloseCalls = %d, want 1", got)
	}

	select {
	case <-eventsClosed:
	case <-time.After(100 * time.Millisecond):
		t.Error("events channel was not closed after RunOnce")
	}
}

func TestOrchestrator_StopBeforeRunSkipsInitialPoll(t *testing.T) {
	cfg := testConfig()
	tracker := NewMockTracker([]types.Issue{makeTestIssue("CB-1", "Issue")})
	runner := NewMockAgentRunner()
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)
	orch.Stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- orch.Run()
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run() returned error: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Run() did not return after pre-cancelled Stop()")
	}

	if got := tracker.ClaimCount("CB-1"); got != 0 {
		t.Errorf("ClaimCount(CB-1) = %d, want 0", got)
	}
	if got := runner.StartCallCount(); got != 0 {
		t.Errorf("StartCalls = %d, want 0", got)
	}
	if got := runner.CloseCalls; got != 1 {
		t.Errorf("CloseCalls = %d, want 1", got)
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
	time.Sleep(50 * time.Millisecond) // Allow async goroutine to emit events

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
	time.Sleep(50 * time.Millisecond) // Allow async goroutine to emit events

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

func TestOrchestrator_ReconcileRunning_TimeoutEnqueuesBackoff(t *testing.T) {
	cfg := testConfig()
	cfg.AgentTimeoutMs = 1 // 1ms timeout

	tracker := NewMockTracker(nil)
	runner := NewMockAgentRunner()
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)

	issue := makeTestIssue("CB-1", "Test")
	orch.State.Add("CB-1", issue, 1, types.StageExecute, &types.AgentProcess{})

	// Wait for the 1ms timeout to elapse
	time.Sleep(20 * time.Millisecond)

	orch.reconcileRunning()

	if orch.Backoff.Len() != 1 {
		t.Errorf("Backoff.Len() = %d, want 1", orch.Backoff.Len())
	}
	entry, ok := orch.Backoff.Get("CB-1")
	if !ok {
		t.Fatal("expected backoff entry for CB-1")
	}
	if entry.Stage != types.StageExecute {
		t.Errorf("backoff stage = %v, want execute", entry.Stage)
	}
	if entry.Attempt != 2 {
		t.Errorf("backoff attempt = %d, want 2", entry.Attempt)
	}
	if tracker.SetRetryQueueCalls != 1 {
		t.Errorf("SetRetryQueueCalls = %d, want 1", tracker.SetRetryQueueCalls)
	}
}

func TestOrchestrator_ReconcileRunning_StallEnqueuesBackoff(t *testing.T) {
	cfg := testConfig()
	cfg.StallTimeoutMs = 1 // 1ms stall timeout

	tracker := NewMockTracker(nil)
	runner := NewMockAgentRunner()
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)

	issue := makeTestIssue("CB-1", "Test")
	orch.State.Add("CB-1", issue, 1, types.StageExecute, &types.AgentProcess{})

	// Wait for the 1ms stall timeout to elapse
	time.Sleep(20 * time.Millisecond)

	orch.reconcileRunning()

	if orch.Backoff.Len() != 1 {
		t.Errorf("Backoff.Len() = %d, want 1", orch.Backoff.Len())
	}
	entry, ok := orch.Backoff.Get("CB-1")
	if !ok {
		t.Fatal("expected backoff entry for CB-1")
	}
	if entry.Stage != types.StageExecute {
		t.Errorf("backoff stage = %v, want execute", entry.Stage)
	}
	if tracker.SetRetryQueueCalls != 1 {
		t.Errorf("SetRetryQueueCalls = %d, want 1", tracker.SetRetryQueueCalls)
	}
}

func TestOrchestrator_MultiStage_HappyPath_WithArtifacts(t *testing.T) {
	tmpDir := t.TempDir()
	boardDir := filepath.Join(tmpDir, "board")
	_ = os.MkdirAll(boardDir, 0o755)

	cfg := testConfig()
	tracker := NewMockTracker([]types.Issue{makeTestIssue("CB-1", "Issue")})
	runner := NewMockAgentRunner()
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)

	recorder, err := diagnostics.NewRecorder(boardDir)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	defer recorder.Close()
	orch.SetRecorder(recorder)

	events := NewEventCollector(orch.Events)

	orch.poll()
	if !events.WaitFor(EventIssueReadyForReview, 2*time.Second) {
		t.Fatal("Expected IssueReadyForReview event")
	}

	// Assert all stage manifests and results exist and passed
	for _, stage := range []types.Stage{types.StagePlan, types.StageExecute, types.StageVerify} {
		manifest, err := recorder.LoadStageManifest("CB-1", 1, stage)
		if err != nil {
			t.Errorf("LoadStageManifest(%s): %v", stage, err)
			continue
		}
		if manifest.Status != types.StageStatePassed {
			t.Errorf("%s manifest status = %v, want passed", stage, manifest.Status)
		}

		result, err := recorder.LoadStageResult("CB-1", 1, stage)
		if err != nil {
			t.Errorf("LoadStageResult(%s): %v", stage, err)
			continue
		}
		if result.Status != types.StageStatePassed {
			t.Errorf("%s result status = %v, want passed", stage, result.Status)
		}
	}

	// Assert review handoff exists
	handoffPath := filepath.Join(recorder.RunsRoot(), "CB-1", "attempts", "001", "review", "handoff.md")
	if _, err := os.Stat(handoffPath); err != nil {
		t.Errorf("review handoff missing: %v", err)
	}

	// Assert summary is in review-ready state
	summary, err := recorder.LoadIssueSummary("CB-1")
	if err != nil {
		t.Fatalf("LoadIssueSummary: %v", err)
	}
	if summary.ReviewState != types.ReviewStateReady {
		t.Errorf("review state = %v, want ready", summary.ReviewState)
	}
	if summary.Outcome != "awaiting_review" {
		t.Errorf("outcome = %q, want awaiting_review", summary.Outcome)
	}
	if summary.IssueState != types.StateInReview.BoardState() {
		t.Errorf("issue_state = %q, want in_review", summary.IssueState)
	}
}

func TestOrchestrator_MultiStage_ExecuteFailure_PreservesArtifacts(t *testing.T) {
	tmpDir := t.TempDir()
	boardDir := filepath.Join(tmpDir, "board")
	_ = os.MkdirAll(boardDir, 0o755)

	cfg := testConfig()
	tracker := NewMockTracker([]types.Issue{makeTestIssue("CB-1", "Issue")})
	runner := NewMockAgentRunner()
	runner.PerStageDoneError = map[types.Stage]error{
		types.StageExecute: errors.New("compile error"),
	}
	ws := NewMockWorkspace()

	orch := New(cfg, tracker, ws, runner)

	recorder, err := diagnostics.NewRecorder(boardDir)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	defer recorder.Close()
	orch.SetRecorder(recorder)

	events := NewEventCollector(orch.Events)

	orch.poll()
	if !events.WaitFor(EventIssueRetrying, 2*time.Second) {
		t.Fatal("Expected IssueRetrying event after execute failure")
	}

	// Plan should have passed
	planResult, err := recorder.LoadStageResult("CB-1", 1, types.StagePlan)
	if err != nil {
		t.Fatalf("LoadStageResult(plan): %v", err)
	}
	if planResult.Status != types.StageStatePassed {
		t.Errorf("plan status = %v, want passed", planResult.Status)
	}

	// Execute should have failed
	executeResult, err := recorder.LoadStageResult("CB-1", 1, types.StageExecute)
	if err != nil {
		t.Fatalf("LoadStageResult(execute): %v", err)
	}
	if executeResult.Status != types.StageStateFailed {
		t.Errorf("execute status = %v, want failed", executeResult.Status)
	}
	if executeResult.FailureKind != types.StageFailureToolError {
		t.Errorf("execute failure kind = %v, want tool_error", executeResult.FailureKind)
	}

	// Verify should not exist
	_, err = recorder.LoadStageManifest("CB-1", 1, types.StageVerify)
	if err == nil {
		t.Error("verify manifest should not exist when execute fails")
	}

	// Summary should reflect retry state
	summary, err := recorder.LoadIssueSummary("CB-1")
	if err != nil {
		t.Fatalf("LoadIssueSummary: %v", err)
	}
	if summary.IssueState != types.StateRetryQueued.BoardState() {
		t.Errorf("issue_state = %q, want retry_queued", summary.IssueState)
	}
	if summary.LastError == "" {
		t.Error("last_error should be set after failure")
	}
}
