package main

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	kitstream "github.com/qiuyier/omni-agent-kit/stream"
)

// Probes measure application ports, not SDK retries or provider billing calls.
// They belong to this opt-in acceptance command, not the production service.
type acceptanceProbe struct {
	mu         sync.Mutex
	retrievals []retrievalObservation
	models     []modelObservation
	finished   []customerservice.ReplyFinishedEvent
}

type retrievalObservation struct {
	IDs        []string
	DurationMS float64
	ErrorClass string
}

type modelObservation struct {
	Mode             string
	KnowledgeEnabled bool
	ReferenceIDs     []string
	ErrorClass       string
	HTTPStatus       int
	ErrorStage       string
}

type probeSnapshot struct {
	Retrievals int
	Models     int
	Finished   int
}

func (p *acceptanceProbe) snapshot() probeSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return probeSnapshot{len(p.retrievals), len(p.models), len(p.finished)}
}

func (p *acceptanceProbe) retrieve(inner customerservice.KnowledgeRetriever) customerservice.KnowledgeRetriever {
	return customerservice.KnowledgeRetrieverFunc(func(ctx context.Context, q customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
		start := time.Now()
		refs, err := inner.Retrieve(ctx, q)
		observed := retrievalObservation{IDs: referenceIDs(refs), DurationMS: elapsedMS(start)}
		observed.ErrorClass, _ = safeModelError(err)
		p.mu.Lock()
		p.retrievals = append(p.retrievals, observed)
		p.mu.Unlock()
		return refs, err
	})
}

func (p *acceptanceProbe) recordModel(mode string, request customerservice.ReplyRequest) int {
	observed := modelObservation{Mode: mode, KnowledgeEnabled: request.Knowledge != nil, ReferenceIDs: []string{}}
	if request.Knowledge != nil {
		observed.ReferenceIDs = referenceIDs(request.Knowledge.Snippets)
	}
	p.mu.Lock()
	index := len(p.models)
	p.models = append(p.models, observed)
	p.mu.Unlock()
	return index
}

func (p *acceptanceProbe) modelError(index int, stage string, err error) {
	if err == nil || errors.Is(err, io.EOF) {
		return
	}
	class, status := safeModelError(err)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.models[index].ErrorClass == "" {
		p.models[index].ErrorClass, p.models[index].HTTPStatus, p.models[index].ErrorStage = class, status, stage
	}
}

func (*acceptanceProbe) ReplyStarted(context.Context, customerservice.ReplyStartedEvent)   {}
func (*acceptanceProbe) ReplyRejected(context.Context, customerservice.ReplyRejectedEvent) {}
func (p *acceptanceProbe) ReplyFinished(_ context.Context, e customerservice.ReplyFinishedEvent) {
	p.mu.Lock()
	p.finished = append(p.finished, e)
	p.mu.Unlock()
}

// The suite makes requests sequentially. Copy observations under the lock because
// their writers run in the HTTP server goroutines.
func (p *acceptanceProbe) observations(before probeSnapshot) (r retrievalObservation, m modelObservation, finished customerservice.ReplyFinishedEvent, delta probeSnapshot) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delta = probeSnapshot{len(p.retrievals) - before.Retrievals, len(p.models) - before.Models, len(p.finished) - before.Finished}
	if delta.Retrievals > 0 {
		r = p.retrievals[before.Retrievals]
	}
	if delta.Models > 0 {
		m = p.models[before.Models]
	}
	if delta.Finished > 0 {
		finished = p.finished[before.Finished]
	}
	return
}

type observedGenerator struct {
	inner bootstrap.AIReplyGenerator
	probe *acceptanceProbe
}

func (g *observedGenerator) Generate(ctx context.Context, r customerservice.ReplyRequest) (customerservice.Reply, error) {
	index := g.probe.recordModel("generate", r)
	reply, err := g.inner.Generate(ctx, r)
	g.probe.modelError(index, "generate", err)
	return reply, err
}

func (g *observedGenerator) Stream(ctx context.Context, r customerservice.ReplyRequest) (kitstream.Reader[customerservice.ReplyDelta], error) {
	index := g.probe.recordModel("stream", r)
	source, err := g.inner.Stream(ctx, r)
	g.probe.modelError(index, "initialize", err)
	if err != nil || source == nil {
		return source, err
	}
	return kitstream.NewReader(func() (customerservice.ReplyDelta, error) {
		delta, err := source.Recv()
		g.probe.modelError(index, "receive", err)
		return delta, err
	}, func() error {
		err := source.Close()
		g.probe.modelError(index, "close", err)
		return err
	}), nil
}

// Preserve only bounded diagnostic categories. SDK error strings include the
// request URL and raw provider response and must never reach the report.
func safeModelError(err error) (string, int) {
	if err == nil {
		return "", 0
	}
	if errors.Is(err, context.Canceled) {
		return "canceled", 0
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded", 0
	}
	var apiError *openai.Error
	if errors.As(err, &apiError) {
		switch apiError.Code {
		case "invalid_api_key", "model_not_found", "insufficient_quota", "rate_limit_exceeded", "unsupported_parameter", "unsupported_value", "invalid_request_error":
			return apiError.Code, apiError.StatusCode
		default:
			return "provider_http_error", apiError.StatusCode
		}
	}
	return "request_failed", 0
}

func referenceIDs(refs []customerservice.KnowledgeSnippet) []string {
	ids := make([]string, len(refs))
	for i, ref := range refs {
		ids[i] = ref.ID
	}
	return ids
}

func elapsedMS(start time.Time) float64 {
	return float64(time.Since(start)) / float64(time.Millisecond)
}
