package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

func TestFakeRunner_StartCalls(t *testing.T) {
	f := NewFakeRunner()
	f.DefaultScript = &StageScript{
		Delay:   10 * time.Millisecond,
		Events:  []types.AgentEvent{{Type: "test"}},
		DoneErr: nil,
	}

	proc, err := f.Start(context.Background(), types.Issue{ID: "CB-1"}, "/tmp", "prompt")
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}
	if f.StartCalls != 1 {
		t.Errorf("StartCalls = %d, want 1", f.StartCalls)
	}

	<-proc.Done
	f.Close()
}

func TestFakeRunner_StageScript(t *testing.T) {
	f := NewFakeRunner()

	planScript := &StageScript{
		Events:  []types.AgentEvent{{Type: "plan.event"}},
		DoneErr: nil,
	}
	executeScript := &StageScript{
		Events:  []types.AgentEvent{{Type: "execute.event"}},
		DoneErr: errors.New("execute failed"),
	}

	f.SetStageScript(types.StagePlan, planScript)
	f.SetStageScript(types.StageExecute, executeScript)

	// Run plan stage
	ctx := types.WithStage(context.Background(), types.StagePlan, "plan-agent")
	proc, err := f.Start(ctx, types.Issue{ID: "CB-1"}, "/tmp", "prompt")
	if err != nil {
		t.Fatalf("Start(plan) error: %v", err)
	}

	ev := <-proc.Events
	if ev.Type != "plan.event" {
		t.Errorf("plan event type = %q, want plan.event", ev.Type)
	}
	if err := <-proc.Done; err != nil {
		t.Errorf("plan done error = %v, want nil", err)
	}

	// Run execute stage
	ctx = types.WithStage(context.Background(), types.StageExecute, "exec-agent")
	proc, err = f.Start(ctx, types.Issue{ID: "CB-1"}, "/tmp", "prompt")
	if err != nil {
		t.Fatalf("Start(execute) error: %v", err)
	}

	ev = <-proc.Events
	if ev.Type != "execute.event" {
		t.Errorf("execute event type = %q, want execute.event", ev.Type)
	}
	if err := <-proc.Done; err == nil {
		t.Error("execute done error = nil, want error")
	}

	f.Close()
}

func TestFakeRunner_ContextCancellation(t *testing.T) {
	f := NewFakeRunner()
	f.DefaultScript = &StageScript{
		Delay: 10 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	proc, err := f.Start(ctx, types.Issue{ID: "CB-1"}, "/tmp", "prompt")
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}

	cancel()

	select {
	case err := <-proc.Done:
		if err != context.Canceled {
			t.Errorf("done error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("expected done after context cancel")
	}
	f.Close()
}

func TestFakeRunner_DefaultScriptFallback(t *testing.T) {
	f := NewFakeRunner()
	f.DefaultScript = &StageScript{
		Events:  []types.AgentEvent{{Type: "default.event"}},
		DoneErr: nil,
	}

	// Run a stage without a specific script
	ctx := types.WithStage(context.Background(), types.StageVerify, "verify-agent")
	proc, err := f.Start(ctx, types.Issue{ID: "CB-1"}, "/tmp", "prompt")
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}

	ev := <-proc.Events
	if ev.Type != "default.event" {
		t.Errorf("event type = %q, want default.event", ev.Type)
	}
	<-proc.Done
	f.Close()
}

func TestFakeRunner_StopAndClose(t *testing.T) {
	f := NewFakeRunner()
	f.DefaultScript = &StageScript{}

	proc, _ := f.Start(context.Background(), types.Issue{ID: "CB-1"}, "/tmp", "prompt")
	<-proc.Done

	if err := f.Stop(proc); err != nil {
		t.Errorf("Stop error: %v", err)
	}
	if f.StopCalls != 1 {
		t.Errorf("StopCalls = %d, want 1", f.StopCalls)
	}

	if err := f.Close(); err != nil {
		t.Errorf("Close error: %v", err)
	}
	if f.CloseCalls != 1 {
		t.Errorf("CloseCalls = %d, want 1", f.CloseCalls)
	}
}
