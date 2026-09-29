// Package reload atomically replaces immutable knowledge retrievers.
package reload

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

type Candidate struct {
	Retriever customerservice.KnowledgeRetriever
	Version   string
	Entries   int
}
type Loader func(context.Context) (Candidate, error)
type Status struct {
	Version     string    `json:"version"`
	Entries     int       `json:"entries"`
	LoadedAt    time.Time `json:"loaded_at"`
	AttemptedAt time.Time `json:"attempted_at"`
	Result      string    `json:"result"`
	Loading     bool      `json:"loading"`
}
type Manager struct {
	mu      sync.RWMutex
	gate    sync.Mutex
	current Candidate
	status  Status
	load    Loader
}

var ErrLoad = errors.New("knowledge reload failed; previous version retained")
var ErrBusy = errors.New("knowledge reload already running")

func New(ctx context.Context, loader Loader) (*Manager, error) {
	if ctx == nil || loader == nil {
		return nil, ErrLoad
	}
	m := &Manager{load: loader}
	if e := m.Reload(ctx); e != nil {
		return nil, e
	}
	return m, nil
}
func (m *Manager) Reload(ctx context.Context) error {
	if ctx == nil {
		return ErrLoad
	}
	if !m.gate.TryLock() {
		return ErrBusy
	}
	defer m.gate.Unlock()
	m.mu.Lock()
	m.status.AttemptedAt = time.Now().UTC()
	m.status.Loading = true
	m.status.Result = "loading"
	m.mu.Unlock()
	var c Candidate
	err := ctx.Err()
	if err == nil {
		c, err = m.load(ctx)
	}
	if err == nil {
		err = ctx.Err()
	}
	if c.Retriever == nil || c.Version == "" || c.Entries < 0 {
		err = ErrLoad
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil {
		err = ErrLoad
	}
	m.status.Loading = false
	if err != nil {
		m.status.Result = "failed"
		return ErrLoad
	}
	m.current = c
	m.status.Version = c.Version
	m.status.Entries = c.Entries
	m.status.LoadedAt = time.Now().UTC()
	m.status.Result = "success"
	return nil
}
func (m *Manager) Status() Status { m.mu.RLock(); defer m.mu.RUnlock(); return m.status }
func (m *Manager) Retrieve(ctx context.Context, q customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
	m.mu.RLock()
	r := m.current.Retriever
	m.mu.RUnlock()
	return r.Retrieve(ctx, q)
}
