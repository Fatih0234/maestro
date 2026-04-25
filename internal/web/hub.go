// Package web provides a lightweight HTTP API for the orchestrator.
package web

import (
	"sync"
)

// Hub is a fan-out broadcast mechanism for WebEvents.
// Each subscriber gets its own buffered channel. Slow consumers
// have events dropped individually so they never block fast consumers.
type Hub struct {
	mu      sync.RWMutex
	clients map[chan WebEvent]struct{}
}

// NewHub creates a new broadcast hub.
func NewHub() *Hub {
	return &Hub{
		clients: make(map[chan WebEvent]struct{}),
	}
}

// Subscribe registers a new client and returns its receive channel.
// The channel is buffered with the given size.
func (h *Hub) Subscribe(bufSize int) chan WebEvent {
	ch := make(chan WebEvent, bufSize)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe removes a client from the hub.
// The caller is responsible for detecting the channel is no longer
// needed (e.g. via request context cancellation).
func (h *Hub) Unsubscribe(ch chan WebEvent) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
}

// Broadcast sends an event to all subscribers non-blockingly.
// If a client's buffer is full, the event is dropped for that client.
func (h *Hub) Broadcast(event WebEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for ch := range h.clients {
		select {
		case ch <- event:
		default:
			// Slow consumer: drop event for this client only.
		}
	}
}
