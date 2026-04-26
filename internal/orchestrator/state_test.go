package orchestrator

import (
	"sync"
	"testing"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

func makeTestProcess(sessionID string) *types.AgentProcess {
	return &types.AgentProcess{
		PID:       12345,
		SessionID: sessionID,
		Events:    make(chan types.AgentEvent, 64),
		Done:      make(chan error, 1),
	}
}

func makeTestIssue(id, title string) types.Issue {
	return types.Issue{
		ID:          id,
		Identifier:  id,
		Title:       title,
		Description: "Test description",
		State:       types.StateUnclaimed,
		Labels:      []string{},
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
}

func TestStateManager_Add(t *testing.T) {
	s := NewStateManager()

	issue := makeTestIssue("CB-1", "Test Issue CB-1")
	proc := makeTestProcess("sess-1")

	s.Add("CB-1", issue, 1, types.StageExecute, proc)

	run, ok := s.Get("CB-1")
	if !ok {
		t.Fatal("Get(CB-1) returned false, want true")
	}
	if run.Issue.ID != "CB-1" {
		t.Errorf("Issue.ID = %q, want CB-1", run.Issue.ID)
	}
	if run.Attempt != 1 {
		t.Errorf("Attempt = %d, want 1", run.Attempt)
	}
	if run.Process.SessionID != "sess-1" {
		t.Errorf("Process.SessionID = %q, want sess-1", run.Process.SessionID)
	}
}

func TestStateManager_UpdatePhase(t *testing.T) {
	s := NewStateManager()

	issue := makeTestIssue("CB-1", "Test Issue CB-1")
	proc := makeTestProcess("sess-1")
	s.Add("CB-1", issue, 1, types.StageExecute, proc)

	s.UpdatePhase("CB-1", types.PhaseStreamingTurn)

	run, _ := s.Get("CB-1")
	if run.Phase != types.PhaseStreamingTurn {
		t.Errorf("Phase = %v, want PhaseStreamingTurn", run.Phase)
	}
}

func TestStateManager_UpdateTokens(t *testing.T) {
	s := NewStateManager()

	issue := makeTestIssue("CB-1", "Test Issue CB-1")
	proc := makeTestProcess("sess-1")
	s.Add("CB-1", issue, 1, types.StageExecute, proc)

	s.UpdateTokens("CB-1", 500, 1000)

	run, _ := s.Get("CB-1")
	if run.TokensIn != 500 {
		t.Errorf("TokensIn = %d, want 500", run.TokensIn)
	}
	if run.TokensOut != 1000 {
		t.Errorf("TokensOut = %d, want 1000", run.TokensOut)
	}
}

func TestStateManager_UpdateLastEvent(t *testing.T) {
	s := NewStateManager()

	issue := makeTestIssue("CB-1", "Test Issue CB-1")
	proc := makeTestProcess("sess-1")
	s.Add("CB-1", issue, 1, types.StageExecute, proc)

	time.Sleep(10 * time.Millisecond)
	s.UpdateLastEvent("CB-1")

	run, _ := s.Get("CB-1")
	if run.LastEventAt.IsZero() {
		t.Error("LastEventAt is zero")
	}
}

func TestStateManager_Remove(t *testing.T) {
	s := NewStateManager()

	issue := makeTestIssue("CB-1", "Test Issue CB-1")
	proc := makeTestProcess("sess-1")
	s.Add("CB-1", issue, 1, types.StageExecute, proc)

	s.Remove("CB-1")

	if _, ok := s.Get("CB-1"); ok {
		t.Error("Get(CB-1) returned true after Remove, want false")
	}
}

func TestStateManager_Len(t *testing.T) {
	s := NewStateManager()

	if s.Len() != 0 {
		t.Errorf("Len() = %d, want 0", s.Len())
	}

	issue1 := makeTestIssue("CB-1", "Test Issue CB-1")
	s.Add("CB-1", issue1, 1, types.StageExecute, makeTestProcess("sess-1"))

	if s.Len() != 1 {
		t.Errorf("Len() = %d, want 1", s.Len())
	}

	issue2 := makeTestIssue("CB-2", "Test Issue CB-2")
	s.Add("CB-2", issue2, 1, types.StageExecute, makeTestProcess("sess-2"))

	if s.Len() != 2 {
		t.Errorf("Len() = %d, want 2", s.Len())
	}

	s.Remove("CB-1")

	if s.Len() != 1 {
		t.Errorf("Len() after Remove = %d, want 1", s.Len())
	}
}

func TestStateManager_GetAll(t *testing.T) {
	s := NewStateManager()

	issue1 := makeTestIssue("CB-1", "Test Issue CB-1")
	s.Add("CB-1", issue1, 1, types.StageExecute, makeTestProcess("sess-1"))

	issue2 := makeTestIssue("CB-2", "Test Issue CB-2")
	s.Add("CB-2", issue2, 1, types.StageExecute, makeTestProcess("sess-2"))

	all := s.GetAll()
	if len(all) != 2 {
		t.Errorf("GetAll() returned %d entries, want 2", len(all))
	}
}

func TestStateManager_ConcurrentAccess(t *testing.T) {
	s := NewStateManager()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			issue := makeTestIssue("CB-"+string(rune('0'+i)), "Test")
			proc := &types.AgentProcess{PID: i, SessionID: "sess"}
			s.Add("CB-"+string(rune('0'+i)), issue, 1, types.StageExecute, proc)
		}(i)
	}
	wg.Wait()

	if s.Len() != 10 {
		t.Errorf("Len() = %d, want 10", s.Len())
	}
}

func TestStateManager_UpdateNonExistent(t *testing.T) {
	s := NewStateManager()

	// Should not panic
	s.UpdatePhase("CB-999", types.PhaseSucceeded)
	s.UpdateTokens("CB-999", 100, 200)
	s.UpdateLastEvent("CB-999")

}

func TestStateManager_RemoveNonExistent(t *testing.T) {
	s := NewStateManager()

	// Should not panic
	s.Remove("CB-999")
}

func TestStateManager_ConcurrentReadWrite(t *testing.T) {
	s := NewStateManager()
	issue := makeTestIssue("CB-1", "Test")
	proc := makeTestProcess("sess-1")
	s.Add("CB-1", issue, 1, types.StageExecute, proc)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = s.Get("CB-1")
			_ = s.GetAll()

		}()
		go func(i int) {
			defer wg.Done()
			s.UpdateLastEvent("CB-1")
			s.UpdateTokens("CB-1", int64(i), int64(i))
		}(i)
	}
	wg.Wait()
}
