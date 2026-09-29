package bootstrap

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/faq"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/reload"
)

// NewReloadableKnowledge leaves disabled knowledge nil, distinct from [] which
// is an enabled empty snapshot. Semantic reload builds off to the side.
func NewReloadableKnowledge(ctx context.Context, c KnowledgeConfig) (*reload.Manager, error) {
	if ctx == nil {
		return nil, ErrInvalidKnowledgeConfiguration
	}
	if strings.TrimSpace(c.FAQPath) == "" {
		if c.Retriever == "semantic" {
			return nil, ErrInvalidKnowledgeConfiguration
		}
		return nil, nil
	}
	if c.Retriever != "" && c.Retriever != "keyword" && c.Retriever != "semantic" {
		return nil, ErrInvalidKnowledgeConfiguration
	}
	return reload.New(ctx, func(parent context.Context) (reload.Candidate, error) {
		timeout := 10 * time.Second
		if c.Retriever == "semantic" {
			timeout += c.IndexTimeout
		}
		child, cancel := context.WithTimeout(parent, timeout)
		defer cancel()
		if err := child.Err(); err != nil {
			return reload.Candidate{}, err
		}
		r, v, e := faq.LoadVersion(c.FAQPath)
		if e != nil {
			return reload.Candidate{}, e
		}
		candidate := reload.Candidate{Retriever: r, Version: v, Entries: len(r.ReferenceIDs())}
		if c.Retriever == "semantic" && candidate.Entries > 0 {
			candidate.Retriever, e = NewSemanticKnowledgeRetriever(child, c, r.Snippets())
		}
		if e == nil {
			e = child.Err()
		}
		return candidate, e
	})
}

type signalOutputKey struct{}

// WithKnowledgeSignals opts executable entry points into process-owner-only
// management. Library composition/tests do not register global signal handlers.
func WithKnowledgeSignals(ctx context.Context, w io.Writer) context.Context {
	return context.WithValue(ctx, signalOutputKey{}, w)
}
func observeKnowledgeSignals(ctx context.Context, m *reload.Manager) {
	w, ok := ctx.Value(signalOutputKey{}).(io.Writer)
	if !ok {
		return
	}
	events := make(chan os.Signal, 4)
	signal.Notify(events, syscall.SIGHUP, syscall.SIGUSR1)
	go func() { defer signal.Stop(events); serveKnowledgeSignals(ctx, m, events, w) }()
}

// One worker at a time; status remains available while an index is building.
// On shutdown, join the canceled worker before releasing management resources.
func serveKnowledgeSignals(ctx context.Context, m *reload.Manager, events <-chan os.Signal, w io.Writer) {
	done := make(chan error, 1)
	loading := false
	emit := func(event string) {
		status := reload.Status{Result: "disabled"}
		if m != nil {
			status = m.Status()
		}
		_ = json.NewEncoder(w).Encode(struct {
			Event     string        `json:"event"`
			Knowledge reload.Status `json:"knowledge"`
		}{event, status})
	}
	for {
		select {
		case <-ctx.Done():
			if loading {
				<-done
			}
			return
		case s, ok := <-events:
			if !ok {
				if loading {
					<-done
				}
				return
			}
			switch s {
			case syscall.SIGUSR1:
				emit("knowledge_status")
			case syscall.SIGHUP:
				if m == nil {
					emit("knowledge_reload_disabled")
					continue
				}
				if loading {
					emit("knowledge_reload_busy")
					continue
				}
				loading = true
				go func() { done <- m.Reload(ctx) }()
			}
		case err := <-done:
			loading = false
			if err != nil {
				emit("knowledge_reload_failed")
			} else {
				emit("knowledge_reload_succeeded")
			}
		}
	}
}
