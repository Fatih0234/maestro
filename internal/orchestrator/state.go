// Package orchestrator provides the main orchestrator that ties together
// tracker, workspace, and agent components.
package orchestrator

import (
	"sync"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

// RunState tracks an active issue execution.
type RunState struct {
	Issue       types.Issue         // The issue being run
	Attempt     int                 // Current attempt number (1, 2, 3, ...)
	Process     *types.AgentProcess // The agent process
	Phase       types.RunPhase      // Current phase
	Stage       types.Stage         // Current pipeline stage
	StartedAt   time.Time           // When the run started
	LastEventAt time.Time           // When the last event was received
	TokensIn    int64               // Tokens sent to agent
	TokensOut   int64               // Tokens received from agent
	Error       string              // Last error message (if any)
}

// StateManager manages the state of all active runs.
type StateManager struct {
	runs map[string]*RunState
	mu   sync.RWMutex
}

// NewStateManager creates a new state manager.
func NewStateManager() *StateManager {
	return &StateManager{
		runs: make(map[string]*RunState),
	}
}

// Add creates a new run state.
func (s *StateManager) Add(issueID string, issue types.Issue, attempt int, stage types.Stage, proc *types.AgentProcess) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	s.runs[issueID] = &RunState{
		Issue:       issue,
		Attempt:     attempt,
		Process:     proc,
		Phase:       types.PhaseLaunchingAgentProcess,
		Stage:       stage,
		StartedAt:   now,
		LastEventAt: now,
		TokensIn:    0,
		TokensOut:   0,
		Error:       "",
	}
}

// UpdateStage updates the pipeline stage for a run.
func (s *StateManager) UpdateStage(issueID string, stage types.Stage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if run, ok := s.runs[issueID]; ok {
		run.Stage = stage
	}
}

// UpdatePhase updates the phase for a run.
func (s *StateManager) UpdatePhase(issueID string, phase types.RunPhase) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if run, ok := s.runs[issueID]; ok {
		run.Phase = phase
	}
}

// UpdateTokens updates token counts for a run.
func (s *StateManager) UpdateTokens(issueID string, tokensIn, tokensOut int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if run, ok := s.runs[issueID]; ok {
		run.TokensIn = tokensIn
		run.TokensOut = tokensOut
	}
}

// UpdateLastEvent updates the last event timestamp.
func (s *StateManager) UpdateLastEvent(issueID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if run, ok := s.runs[issueID]; ok {
		run.LastEventAt = time.Now()
	}
}

// SetError sets the error for a run.
func (s *StateManager) SetError(issueID string, err string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if run, ok := s.runs[issueID]; ok {
		run.Error = err
	}
}

// Get returns a run state.
func (s *StateManager) Get(issueID string) (*RunState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	run, ok := s.runs[issueID]
	return run, ok
}

// Remove deletes a run state.
func (s *StateManager) Remove(issueID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.runs, issueID)
}

// Len returns the number of active runs.
func (s *StateManager) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.runs)
}

// GetAll returns all run states.
func (s *StateManager) GetAll() []*RunState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*RunState, 0, len(s.runs))
	for _, run := range s.runs {
		result = append(result, run)
	}
	return result
}

// GetByPhase returns runs in a specific phase.
func (s *StateManager) GetByPhase(phase types.RunPhase) []*RunState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*RunState, 0)
	for _, run := range s.runs {
		if run.Phase == phase {
			result = append(result, run)
		}
	}
	return result
}
