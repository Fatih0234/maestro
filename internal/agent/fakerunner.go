package agent

import (
	"context"
	"sync"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

// FakeRunner is a deterministic, scripted agent runner for testing.
// It implements types.AgentRunner and can be configured per pipeline stage.
type FakeRunner struct {
	mu sync.Mutex

	// Scripts maps stage name -> scripted behavior.
	Scripts map[string]*StageScript

	// DefaultScript is used when no stage-specific script is configured.
	DefaultScript *StageScript

	// Tracking counters and history.
	StartCalls int
	StopCalls  int
	CloseCalls int
	Started    []*FakeProcess

	// cancels holds cancel functions for in-flight goroutines so Stop can
	// interrupt them.
	cancels map[string]context.CancelFunc
}

// StageScript defines the behavior for one stage run.
type StageScript struct {
	// Events are emitted sequentially before the stage completes.
	Events []types.AgentEvent

	// DoneErr is sent on the process Done channel (nil = success).
	DoneErr error

	// Delay is waited before emitting events.
	Delay time.Duration
}

// FakeProcess captures the parameters of a started fake process.
type FakeProcess struct {
	PID       int
	SessionID string
	Events    chan types.AgentEvent
	Done      chan error
}

var _ types.AgentRunner = (*FakeRunner)(nil)

// NewFakeRunner creates a new FakeRunner.
func NewFakeRunner() *FakeRunner {
	return &FakeRunner{
		Scripts: make(map[string]*StageScript),
		cancels: make(map[string]context.CancelFunc),
	}
}

// SetStageScript configures behavior for a specific pipeline stage.
func (f *FakeRunner) SetStageScript(stage types.Stage, script *StageScript) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Scripts[stage.String()] = script
}

// Start implements types.AgentRunner.
func (f *FakeRunner) Start(ctx context.Context, issue types.Issue, workspace, prompt string) (*types.AgentProcess, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.StartCalls++

	// Determine which stage we're running.
	stage := types.StagePlan
	if sc, ok := types.StageFromContext(ctx); ok {
		stage = sc.Stage
	}

	script := f.DefaultScript
	if s, ok := f.Scripts[stage.String()]; ok {
		script = s
	}

	events := make(chan types.AgentEvent, 64)
	done := make(chan error, 1)

	proc := &FakeProcess{
		PID:       10000 + f.StartCalls,
		SessionID: "fake-" + issue.ID + "-" + stage.String(),
		Events:    events,
		Done:      done,
	}
	f.Started = append(f.Started, proc)

	procCtx, cancel := context.WithCancel(ctx)
	f.cancels[proc.SessionID] = cancel

	go f.runScript(procCtx, proc.SessionID, stage, script, events, done)

	return &types.AgentProcess{
		PID:       proc.PID,
		SessionID: proc.SessionID,
		Events:    events,
		Done:      done,
	}, nil
}

func (f *FakeRunner) runScript(ctx context.Context, sessionID string, stage types.Stage, script *StageScript, events chan<- types.AgentEvent, done chan<- error) {
	defer func() {
		f.mu.Lock()
		delete(f.cancels, sessionID)
		f.mu.Unlock()
	}()

	if script != nil && script.Delay > 0 {
		select {
		case <-time.After(script.Delay):
		case <-ctx.Done():
			done <- ctx.Err()
			close(done)
			close(events)
			return
		}
	}

	if script != nil {
		for _, ev := range script.Events {
			select {
			case events <- ev:
			case <-ctx.Done():
				done <- ctx.Err()
				close(done)
				close(events)
				return
			}
		}
	}

	var err error
	if script != nil {
		err = script.DoneErr
	}
	if stage == types.StageVerify && err == nil {
		select {
		case events <- types.AgentEvent{Type: EventTypeMessageUpdated, Payload: map[string]interface{}{"text": `{"passed": true, "summary": "ok"}`}}:
		case <-ctx.Done():
			done <- ctx.Err()
			close(done)
			close(events)
			return
		}
	}
	done <- err
	close(done)
	close(events)
}

// Stop implements types.AgentRunner.
func (f *FakeRunner) Stop(proc *types.AgentProcess) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.StopCalls++
	if cancel, ok := f.cancels[proc.SessionID]; ok {
		cancel()
	}
	return nil
}

// Close implements types.AgentRunner.
func (f *FakeRunner) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.CloseCalls++
	for _, cancel := range f.cancels {
		cancel()
	}
	f.cancels = make(map[string]context.CancelFunc)
	return nil
}
