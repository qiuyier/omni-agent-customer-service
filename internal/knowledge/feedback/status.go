package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"
)

// Status contains only bounded categories, counts and timestamps. Never attach
// question text, identity, DSNs, keys or underlying errors to this structure.
type Status struct {
	Event     string     `json:"event"`
	Capture   string     `json:"capture"`
	At        time.Time  `json:"at"`
	Recorded  uint64     `json:"recorded"`
	Failed    uint64     `json:"failed"`
	Full      uint64     `json:"full"`
	Skipped   uint64     `json:"skipped"`
	Cleanup   string     `json:"cleanup"`
	CleanupAt *time.Time `json:"cleanup_at"`
	Queue     string     `json:"queue"`
	Active    *int       `json:"active"`
	Expired   *int       `json:"expired"`
	Capacity  int        `json:"capacity"`
}
type Counts interface {
	FeedbackCounts(context.Context) (active, expired int, err error)
}

var ErrFull = errors.New("feedback queue full")

type Monitor struct {
	mu    sync.Mutex
	state Status
}
type monitorKey struct{}

func WithMonitor(ctx context.Context, m *Monitor) context.Context {
	return context.WithValue(ctx, monitorKey{}, m)
}
func MonitorFrom(ctx context.Context) *Monitor { m, _ := ctx.Value(monitorKey{}).(*Monitor); return m }
func NewMonitor(capture string) *Monitor {
	if capture != "enabled" && capture != "disabled" {
		capture = "not_owned"
	}
	return &Monitor{state: Status{Event: "knowledge_feedback_status", Capture: capture, Cleanup: "not_run", Queue: "unknown", Capacity: 500}}
}
func (m *Monitor) record(err error) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if errors.Is(err, ErrFull) {
		m.state.Full++
	} else if err != nil {
		m.state.Failed++
	} else {
		m.state.Recorded++
	}
}
func (m *Monitor) skip() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.Skipped++
}
func (m *Monitor) clean(err error) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	m.state.CleanupAt = &now
	m.state.Cleanup = "ok"
	if err != nil {
		m.state.Cleanup = "failed"
	}
}
func (m *Monitor) Snapshot(ctx context.Context, repo Counts) Status {
	m.mu.Lock()
	s := m.state
	m.mu.Unlock()
	s.At = time.Now().UTC()
	s.Queue = "unavailable"
	s.Active = nil
	s.Expired = nil
	if active, expired, err := repo.FeedbackCounts(ctx); err == nil {
		s.Active = &active
		s.Expired = &expired
		s.Queue = "ok"
		if active >= s.Capacity {
			s.Queue = "full"
		}
	}
	return s
}

// Reporter is owned by one process and resets its counters on restart. Queue
// counts are shared database observations; capture describes ONLY this process.
func (m *Monitor) StartReporter(parent context.Context, repo Counts, w io.Writer) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTicker(time.Minute)
		defer timer.Stop()
		for {
			bounded, stop := context.WithTimeout(ctx, 5*time.Second)
			s := m.Snapshot(bounded, repo)
			stop()
			_ = json.NewEncoder(w).Encode(s)
			select {
			case <-ctx.Done():
				final, stop := context.WithTimeout(context.Background(), 2*time.Second)
				_ = json.NewEncoder(w).Encode(m.Snapshot(final, repo))
				stop()
				return
			case <-timer.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}
