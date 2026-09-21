package main

import (
	"net/http"
	"sync"
)

// A mutex pairs admission with drain so no handler can join once teardown
// starts waiting. Canceling/closing HTTP alone does not join model or DB work.
type demoRequestTracker struct {
	next     http.Handler
	mu       sync.Mutex
	draining bool
	active   sync.WaitGroup
}

func (h *demoRequestTracker) beginDrain() {
	h.mu.Lock()
	h.draining = true
	h.mu.Unlock()
}

func (h *demoRequestTracker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	if h.draining {
		h.mu.Unlock()
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	h.active.Add(1)
	h.mu.Unlock()
	defer h.active.Done()
	h.next.ServeHTTP(w, r)
}
