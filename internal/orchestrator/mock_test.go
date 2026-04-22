package orchestrator

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

// MockAgentRunner is a mock implementation of AgentRunner for testing.
type MockAgentRunner struct {
	mu      sync.Mutex
	started []*MockAgentProcess
	stopped int

	// Configurable behavior
	ShouldFail   bool
	FailError    error
	Delay        time.Duration
	EventsToSend []types.AgentEvent
	DoneError   error // Error to send on Done channel (nil = success)

	// Tracking
	StartCalls    int
	StopCalls     int
	WorkspacePaths []string
	Prompts       []string
}

type MockAgentProcess struct {
	PID       int
	SessionID string
	Events    chan types.AgentEvent
	Done      chan error
	Workspace string
	Prompt    string
}

var _ types.AgentRunner = (*MockAgentRunner)(nil)

// NewMockAgentRunner creates a new mock agent runner.
func NewMockAgentRunner() *MockAgentRunner {
	return &MockAgentRunner{
		started:        make([]*MockAgentProcess, 0),
		EventsToSend:   make([]types.AgentEvent, 0),
		WorkspacePaths: make([]string, 0),
		Prompts:        make([]string, 0),
		Delay:          10 * time.Millisecond,
	}
}

// Start implements AgentRunner.Start
func (m *MockAgentRunner) Start(ctx context.Context, issue types.Issue, workspace, prompt string) (*types.AgentProcess, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.StartCalls++
	m.WorkspacePaths = append(m.WorkspacePaths, workspace)
	m.Prompts = append(m.Prompts, prompt)

	if m.ShouldFail {
		return nil, m.FailError
	}

	events := make(chan types.AgentEvent, 64)
	done := make(chan error, 1)

	// Capture error to send
	doneErr := m.DoneError

	// Start goroutine to send events then close
	go func() {
		time.Sleep(m.Delay)
		for _, e := range m.EventsToSend {
			select {
			case events <- e:
			case <-ctx.Done():
				return
			}
		}
		// Close events to signal completion
		close(events)
		// Send done with configured error (nil for success, error for failure)
		done <- doneErr
		close(done)
	}()

	proc := &MockAgentProcess{
		PID:       10000 + m.StartCalls,
		SessionID: "session-" + issue.ID,
		Events:    events,
		Done:      done,
		Workspace: workspace,
		Prompt:    prompt,
	}
	m.started = append(m.started, proc)

	return &types.AgentProcess{
		PID:       proc.PID,
		SessionID: proc.SessionID,
		Events:    events,
		Done:      done,
	}, nil
}

// Stop implements AgentRunner.Stop
func (m *MockAgentRunner) Stop(proc *types.AgentProcess) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.StopCalls++
	return nil
}

// Close implements AgentRunner.Close
func (m *MockAgentRunner) Close() error {
	return nil
}

// GetStarted returns the started processes.
func (m *MockAgentRunner) GetStarted() []*MockAgentProcess {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.started
}

// MockTracker is a mock implementation of IssueTracker for testing.
type MockTracker struct {
	mu      sync.Mutex
	issues  map[string]types.Issue
	claimed map[string]bool
	claims  map[string]int
	releases map[string]int

	// Configurable behavior
	FetchError error
	ClaimError error

	// Call tracking
	ClaimCalls     int
	ReleaseCalls   int
	UpdateCalls    int
	UpdateState    map[string]types.IssueState
}

var _ types.IssueTracker = (*MockTracker)(nil)

// NewMockTracker creates a new mock tracker.
func NewMockTracker(issues []types.Issue) *MockTracker {
	m := &MockTracker{
		issues:   make(map[string]types.Issue),
		claimed:  make(map[string]bool),
		claims:   make(map[string]int),
		UpdateState: make(map[string]types.IssueState),
	}
	for _, issue := range issues {
		m.issues[issue.ID] = issue
	}
	return m
}

// FetchIssues implements IssueTracker.FetchIssues
func (m *MockTracker) FetchIssues() ([]types.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.FetchError != nil {
		return nil, m.FetchError
	}

	result := make([]types.Issue, 0, len(m.issues))
	now := time.Now()
	for _, issue := range m.issues {
		// Only return issues that are not claimed (or in retry state past their wait time)
		if issue.State == types.StateUnclaimed || issue.State == types.StateRetryQueued {
			// Check retry_after time
			if issue.RetryAfter != nil && issue.RetryAfter.After(now) {
				continue
			}
			if !m.claimed[issue.ID] || issue.State == types.StateRetryQueued {
				result = append(result, issue)
			}
		}
	}
	return result, nil
}

// ClaimIssue implements IssueTracker.ClaimIssue
func (m *MockTracker) ClaimIssue(id string) (types.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ClaimError != nil {
		return types.Issue{}, m.ClaimError
	}

	issue, ok := m.issues[id]
	if !ok {
		return types.Issue{}, errors.New("issue not found")
	}

	m.claimed[id] = true
	m.claims[id]++
	m.ClaimCalls++
	issue.State = types.StateRunning
	m.issues[id] = issue

	return issue, nil
}

// ReleaseIssue implements IssueTracker.ReleaseIssue
func (m *MockTracker) ReleaseIssue(id string) (types.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ReleaseCalls++
	m.releases[id]++
	issue, ok := m.issues[id]
	if !ok {
		return types.Issue{}, errors.New("issue not found")
	}

	m.claimed[id] = false
	issue.State = types.StateUnclaimed
	m.issues[id] = issue

	return issue, nil
}

// GetIssue implements IssueTracker.GetIssue
func (m *MockTracker) GetIssue(id string) (types.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	issue, ok := m.issues[id]
	if !ok {
		return types.Issue{}, errors.New("issue not found")
	}
	return issue, nil
}

// UpdateIssueState implements IssueTracker.UpdateIssueState
func (m *MockTracker) UpdateIssueState(id string, state types.IssueState) (types.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.UpdateCalls++
	issue, ok := m.issues[id]
	if !ok {
		return types.Issue{}, errors.New("issue not found")
	}

	issue.State = state
	m.issues[id] = issue
	m.UpdateState[id] = state

	return issue, nil
}

// SetRetryQueue implements IssueTracker.SetRetryQueue
func (m *MockTracker) SetRetryQueue(id string, retryAt time.Time) (types.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	issue, ok := m.issues[id]
	if !ok {
		return types.Issue{}, errors.New("issue not found")
	}

	issue.State = types.StateRetryQueued
	issue.RetryAfter = &retryAt
	m.issues[id] = issue

	return issue, nil
}

// ClaimCount returns how many times an issue was claimed.
func (m *MockTracker) ClaimCount(id string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.claims[id]
}

// ReleaseCount returns how many times ReleaseIssue was called for an issue.
func (m *MockTracker) ReleaseCount(id string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.releases[id]
}