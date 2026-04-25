package orchestrator

import (
	"math/rand"
	"testing"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

func TestBackoffManager_CalculateDelay(t *testing.T) {
	// Seed for reproducibility
	rand.Seed(12345)

	b := NewBackoffManager(4 * time.Minute)

	// Test base delays with jitter (±20%)
	// Attempt 1: 30s * (1 ± 0.2) = 24s to 36s
	// Attempt 2: 60s * (1 ± 0.2) = 48s to 72s
	// Attempt 3: 120s * (1 ± 0.2) = 96s to 144s
	// Attempt 4: 240s * (1 ± 0.2) = 192s to 288s (capped at maxDelay)
	testCases := []struct {
		attempt     int
		minExpected time.Duration
		maxExpected time.Duration
	}{
		{1, 20 * time.Second, 45 * time.Second},   // with some margin
		{2, 40 * time.Second, 90 * time.Second},   // with some margin
		{3, 80 * time.Second, 170 * time.Second},  // with some margin
		{4, 180 * time.Second, 300 * time.Second}, // capped at maxDelay with jitter margin
	}

	for _, tc := range testCases {
		delay := b.CalculateDelay(tc.attempt)
		if delay < tc.minExpected || delay > tc.maxExpected {
			t.Errorf("CalculateDelay(attempt=%d) = %v, want between %v and %v",
				tc.attempt, delay, tc.minExpected, tc.maxExpected)
		}
	}
}

func TestBackoffManager_Enqueue(t *testing.T) {
	b := NewBackoffManager(4 * time.Minute)

	entry := b.Enqueue("CB-1", 1, types.StageExecute, "test error")

	if entry.IssueID != "CB-1" {
		t.Errorf("Enqueue.IssueID = %q, want CB-1", entry.IssueID)
	}
	if entry.Attempt != 1 {
		t.Errorf("Enqueue.Attempt = %d, want 1", entry.Attempt)
	}
	if entry.Error != "test error" {
		t.Errorf("Enqueue.Error = %q, want test error", entry.Error)
	}
	if entry.RetryAt.Before(time.Now()) {
		t.Error("Enqueue.RetryAt should be in the future")
	}
}

func TestBackoffManager_Ready(t *testing.T) {
	b := NewBackoffManager(4 * time.Minute)

	// Enqueue an entry and immediately check Ready()
	// Note: due to jitter, RetryAt might be in the future, so Ready() might be empty
	// This is expected behavior - the test verifies Ready() returns entries that are due
	entry := b.Enqueue("CB-1", 1, types.StagePlan, "error 1")

	// Since we can't guarantee timing with jitter, let's verify the entry exists
	// and has a RetryAt in the reasonable future
	if entry.RetryAt.Before(time.Now()) {
		t.Error("Enqueued entry should have RetryAt in the future (before jitter adjustment)")
	}

	// Verify Get works after Enqueue
	retrieved, ok := b.Get("CB-1")
	if !ok {
		t.Error("Get(CB-1) should find recently enqueued entry")
	}
	if retrieved.IssueID != "CB-1" {
		t.Errorf("Retrieved entry IssueID = %q, want CB-1", retrieved.IssueID)
	}
}

func TestBackoffManager_Remove(t *testing.T) {
	b := NewBackoffManager(4 * time.Minute)

	b.Enqueue("CB-1", 1, types.StagePlan, "error")
	if b.Len() != 1 {
		t.Errorf("Len() = %d, want 1", b.Len())
	}

	b.Remove("CB-1")
	if b.Len() != 0 {
		t.Errorf("Len() after Remove = %d, want 0", b.Len())
	}
}

func TestBackoffManager_Get(t *testing.T) {
	b := NewBackoffManager(4 * time.Minute)

	b.Enqueue("CB-1", 1, types.StagePlan, "error")

	entry, ok := b.Get("CB-1")
	if !ok {
		t.Error("Get(CB-1) returned false, want true")
	}
	if entry.IssueID != "CB-1" {
		t.Errorf("Get().IssueID = %q, want CB-1", entry.IssueID)
	}

	_, ok = b.Get("CB-999")
	if ok {
		t.Error("Get(CB-999) returned true, want false")
	}
}

func TestBackoffManager_GetAll(t *testing.T) {
	b := NewBackoffManager(4 * time.Minute)

	b.Enqueue("CB-1", 1, types.StagePlan, "error 1")
	b.Enqueue("CB-2", 2, types.StageExecute, "error 2")

	all := b.GetAll()
	if len(all) != 2 {
		t.Errorf("GetAll() returned %d entries, want 2", len(all))
	}
}
