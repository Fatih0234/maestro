// Package orchestrator provides the main orchestrator that ties together
// tracker, workspace, and agent components.
package orchestrator

import (
	"math/rand"
	"sync"
	"time"

	"github.com/fatihkarahan/contrabass-pi/internal/types"
)

// BackoffManager manages retry backoff for failed issues.
// It implements exponential backoff with jitter to avoid thundering herd.
type BackoffManager struct {
	entries  map[string]*types.BackoffEntry
	maxDelay time.Duration
	mu       sync.Mutex
}

// NewBackoffManager creates a BackoffManager with the given max delay cap.
func NewBackoffManager(maxDelay time.Duration) *BackoffManager {
	if maxDelay <= 0 {
		maxDelay = 4 * time.Minute
	}
	return &BackoffManager{
		entries:  make(map[string]*types.BackoffEntry),
		maxDelay: maxDelay,
	}
}

// CalculateDelay computes the delay for a given attempt number with ±20% jitter.
// Base delays: attempt 1=30s, 2=60s, 3=120s, 4+=240s (capped)
func (b *BackoffManager) CalculateDelay(attempt int) time.Duration {
	// Base delay: 30 * 2^(attempt-1) seconds
	baseDelay := 30 * time.Second * (1 << (attempt - 1))

	// Cap at maxDelay
	if baseDelay > b.maxDelay {
		baseDelay = b.maxDelay
	}

	// Apply ±20% jitter
	jitter := float64(baseDelay) * 0.2 * (2*rand.Float64() - 1)
	return time.Duration(float64(baseDelay) + jitter)
}

// Enqueue adds a retry entry for an issue with the given attempt number.
func (b *BackoffManager) Enqueue(issueID string, attempt int, errorMsg string) *types.BackoffEntry {
	delay := b.CalculateDelay(attempt)

	entry := &types.BackoffEntry{
		IssueID: issueID,
		Attempt: attempt,
		RetryAt: time.Now().Add(delay),
		Error:   errorMsg,
	}

	b.mu.Lock()
	b.entries[issueID] = entry
	b.mu.Unlock()

	return entry
}

// Ready returns entries whose retry time has passed.
func (b *BackoffManager) Ready() []*types.BackoffEntry {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	ready := make([]*types.BackoffEntry, 0, len(b.entries))

	for _, entry := range b.entries {
		if entry.RetryAt.Before(now) || entry.RetryAt.Equal(now) {
			ready = append(ready, entry)
		}
	}

	return ready
}

// Remove deletes an entry for the given issue (e.g., when issue completes successfully).
func (b *BackoffManager) Remove(issueID string) {
	b.mu.Lock()
	delete(b.entries, issueID)
	b.mu.Unlock()
}

// Get returns an entry by issue ID and whether it exists.
func (b *BackoffManager) Get(issueID string) (*types.BackoffEntry, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry, ok := b.entries[issueID]
	return entry, ok
}

// Len returns the number of entries in the backoff queue.
func (b *BackoffManager) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return len(b.entries)
}

// GetAll returns a snapshot of all backoff entries.
func (b *BackoffManager) GetAll() []*types.BackoffEntry {
	b.mu.Lock()
	defer b.mu.Unlock()

	result := make([]*types.BackoffEntry, 0, len(b.entries))
	for _, entry := range b.entries {
		result = append(result, entry)
	}
	return result
}