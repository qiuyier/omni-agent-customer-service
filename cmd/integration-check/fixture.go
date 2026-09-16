package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/google/uuid"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/integration"
	transport "github.com/qiuyier/omni-agent-customer-service/internal/transport/hertz"
	"go.uber.org/zap"
)

type crashGate struct {
	phase             string
	reached, release  chan struct{}
	once, releaseOnce sync.Once
}

func newGate(phase string) *crashGate {
	return &crashGate{phase: phase, reached: make(chan struct{}), release: make(chan struct{})}
}
func (g *crashGate) unblock() { g.releaseOnce.Do(func() { close(g.release) }) }
func (g *crashGate) afterCommit(ctx context.Context, phase string) {
	if phase != g.phase {
		return
	}
	block := false
	g.once.Do(func() { block = true; close(g.reached) })
	if block {
		select {
		case <-g.release:
		case <-ctx.Done():
		}
	}
}

type countedApplication struct {
	*customerservice.Service
	gate                              *crashGate
	receives, replies, checks, models atomic.Int32
	mu                                sync.Mutex
	commands                          []customerservice.ReceiveMessageCommand
	received                          []customerservice.ReceiveMessageResult
	replyResults                      []customerservice.GenerateBotReplyResult
}

func (a *countedApplication) ReceiveMessage(ctx context.Context, q customerservice.ReceiveMessageCommand) (customerservice.ReceiveMessageResult, error) {
	a.receives.Add(1)
	r, err := a.Service.ReceiveMessage(ctx, q)
	a.mu.Lock()
	a.commands = append(a.commands, q)
	a.received = append(a.received, r)
	a.mu.Unlock()
	if err == nil {
		a.gate.afterCommit(ctx, "receive")
	}
	return r, err
}
func (a *countedApplication) GenerateBotReply(ctx context.Context, q customerservice.GenerateBotReplyCommand) (customerservice.GenerateBotReplyResult, error) {
	a.replies.Add(1)
	r, err := a.Service.GenerateBotReply(ctx, q)
	a.mu.Lock()
	a.replyResults = append(a.replyResults, r)
	a.mu.Unlock()
	if err == nil {
		a.gate.afterCommit(ctx, "reply")
	}
	return r, err
}
func (a *countedApplication) CheckDeliveryEligibility(ctx context.Context, q customerservice.DeliveryCheck) (customerservice.DeliveryEligibility, error) {
	a.checks.Add(1)
	return a.Service.CheckDeliveryEligibility(ctx, q)
}

type fakeSink struct {
	mu                    sync.Mutex
	token                 string
	gate                  *crashGate
	calls                 int
	effects               map[string]integration.Delivery
	failBefore, failAfter bool
}

func (s *fakeSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/deliver" || r.Method != http.MethodPost {
		w.WriteHeader(404)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+s.token {
		w.WriteHeader(401)
		return
	}
	var d integration.Delivery
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&d) != nil || decoder.Decode(new(any)) != io.EOF || d.Key == "" || d.Content == "" {
		w.WriteHeader(400)
		return
	}
	s.mu.Lock()
	s.calls++
	if s.failBefore {
		s.failBefore = false
		s.mu.Unlock()
		w.WriteHeader(503)
		return
	}
	if previous, ok := s.effects[d.Key]; ok {
		s.mu.Unlock()
		if previous != d {
			w.WriteHeader(409)
		} else {
			w.WriteHeader(204)
		}
		return
	}
	s.effects[d.Key] = d
	failAfter := s.failAfter
	s.failAfter = false
	s.mu.Unlock()
	s.gate.afterCommit(r.Context(), "delivery")
	if failAfter {
		w.WriteHeader(503)
		return
	}
	w.WriteHeader(204)
}
func (s *fakeSink) receipt(key string) (integration.Delivery, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.effects[key]
	return d, ok
}

type callCounts struct {
	Receive     int `json:"receive_http"`
	Reply       int `json:"reply_http"`
	Eligibility int `json:"eligibility_http"`
	Delivery    int `json:"delivery_http"`
	Models      int `json:"fake_model_calls"`
	Unique      int `json:"unique_delivery_effects"`
}
type fixture struct {
	application                 *countedApplication
	repository                  *conversation.MemoryRepository
	sink                        *fakeSink
	gate                        *crashGate
	api                         *server.Hertz
	listener                    net.Listener
	sinkServer                  *httptest.Server
	apiURL, token               string
	cancel                      context.CancelFunc
	serveDone                   chan error
	serveStarted, serveFinished bool
	closeOnce                   sync.Once
	closeErr                    error
}

func newFixture(ctx context.Context, phase string, failBefore, failAfter bool) (*fixture, error) {
	gate := newGate(phase)
	repo := conversation.NewMemoryRepository()
	a := &countedApplication{gate: gate}
	var ids atomic.Int32
	service, err := customerservice.New(repo, customerservice.MessageIDGeneratorFunc(func() (string, error) { return fmt.Sprintf("message-%d", ids.Add(1)), nil }), customerservice.ClockFunc(time.Now), customerservice.WithReplyGenerator(customerservice.ReplyGeneratorFunc(func(context.Context, customerservice.ReplyRequest) (customerservice.Reply, error) {
		a.models.Add(1)
		return customerservice.Reply{Content: "请打开应用中的帮助中心。"}, nil
	})))
	if err != nil {
		return nil, err
	}
	a.Service = service
	token := uuid.NewString()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	root, cancel := context.WithCancel(ctx)
	f := &fixture{application: a, repository: repo, gate: gate, token: token, listener: listener, apiURL: "http://" + listener.Addr().String(), cancel: cancel, serveDone: make(chan error, 1)}
	f.sink = &fakeSink{token: token, gate: gate, effects: map[string]integration.Delivery{}, failBefore: failBefore, failAfter: failAfter}
	f.sinkServer = httptest.NewServer(f.sink)
	f.api = server.New(server.WithListener(listener), server.WithExitWaitTime(time.Second), server.WithSenseClientDisconnection(true), server.WithOnAccept(func(net.Conn) context.Context { return root }))
	handler, err := transport.NewHandler(a)
	if err != nil {
		f.close()
		return nil, err
	}
	security, err := transport.NewSecurityMiddleware(token)
	if err != nil {
		f.close()
		return nil, err
	}
	access, err := transport.NewAccessLogMiddleware(zap.NewNop())
	if err != nil {
		f.close()
		return nil, err
	}
	limit, err := transport.NewRateLimitMiddleware(1000, 1000)
	if err != nil {
		f.close()
		return nil, err
	}
	if err := transport.RegisterRoutes(f.api.Engine, handler, transport.NewAvailability(), security, access, limit); err != nil {
		f.close()
		return nil, err
	}
	f.serveStarted = true
	go func() { f.serveDone <- f.api.Run() }()
	if err := f.waitReady(ctx); err != nil {
		f.close()
		return nil, err
	}
	return f, nil
}
func (f *fixture) close() error {
	f.closeOnce.Do(func() {
		f.gate.unblock()
		f.cancel()
		if f.sinkServer != nil {
			f.sinkServer.CloseClientConnections()
			f.sinkServer.Close()
		}
		if f.api != nil {
			_ = f.api.Close()
		}
		_ = f.listener.Close()
		if f.serveStarted && !f.serveFinished {
			select {
			case <-f.serveDone:
				f.serveFinished = true
			case <-time.After(2 * time.Second):
				f.closeErr = errors.New("local server did not exit after close")
			}
		}
	})
	return f.closeErr
}
func (f *fixture) waitReady(ctx context.Context) error {
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, Timeout: time.Second}
	defer client.CloseIdleConnections()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.apiURL+"/readyz", nil)
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-deadline.C:
			timer.Stop()
			return errors.New("local server readiness timeout")
		case <-f.serveDone:
			f.serveFinished = true
			timer.Stop()
			return errors.New("local server stopped")
		case <-timer.C:
		}
	}
	// Real middleware must reject a missing credential before reaching the app.
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, f.apiURL+"/v1/conversations/unauthorized/messages", nil)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 || f.application.receives.Load() != 0 {
		return errors.New("authentication acceptance failed")
	}
	return nil
}
func (f *fixture) config(directory, source string, maxAttempts int) workerConfig {
	return workerConfig{Directory: directory, Source: source, APIURL: f.apiURL, SinkURL: f.sinkServer.URL, Token: f.token, MaxAttempts: maxAttempts}
}
func (f *fixture) counts() callCounts {
	f.sink.mu.Lock()
	defer f.sink.mu.Unlock()
	return callCounts{Receive: int(f.application.receives.Load()), Reply: int(f.application.replies.Load()), Eligibility: int(f.application.checks.Load()), Delivery: f.sink.calls, Models: int(f.application.models.Load()), Unique: len(f.sink.effects)}
}
func (f *fixture) assertSaved(j integration.Job, expectReceipt bool) error {
	f.application.mu.Lock()
	defer f.application.mu.Unlock()
	if len(f.application.received) == 0 {
		return errors.New("no HTTP receive observed")
	}
	cid := f.application.received[0].ConversationID
	messages, err := f.repository.ListMessages(context.Background(), cid)
	if err != nil {
		return err
	}
	if len(messages) != 2 || messages[0].ID() != j.TriggerMessageID || messages[1].ID() != j.ReplyMessageID || messages[1].ReplyToMessageID() != j.TriggerMessageID || messages[1].Content() != j.ReplyContent {
		return errors.New("saved message identity changed")
	}
	for _, r := range f.application.received {
		if r.ConversationID != cid || r.MessageID != j.TriggerMessageID {
			return errors.New("receive replay identity changed")
		}
	}
	for _, r := range f.application.replyResults {
		if r.ConversationID != cid || r.TriggerMessageID != j.TriggerMessageID || r.MessageID != j.ReplyMessageID || r.Content != j.ReplyContent {
			return errors.New("reply replay identity changed")
		}
	}
	d, ok := f.sink.receipt(j.DeliveryKey)
	if ok != expectReceipt {
		return errors.New("original delivery key receipt presence mismatch")
	}
	if ok {
		e := event()
		if d.Key != j.DeliveryKey || d.TriggerMessageID != j.TriggerMessageID || d.ReplyMessageID != j.ReplyMessageID || d.Content != j.ReplyContent || d.ConversationID != e.ConversationID || d.CustomerID != e.CustomerID || d.ExternalMessageID != e.ExternalMessageID {
			return errors.New("delivery payload identity changed")
		}
	}
	return nil
}
func (f *fixture) closeConversation(ctx context.Context) error {
	f.application.mu.Lock()
	cid := f.application.received[0].ConversationID
	f.application.mu.Unlock()
	return f.repository.WithinTx(ctx, func(tx conversation.Transaction) error {
		c, err := tx.FindConversation(cid)
		if err != nil {
			return err
		}
		if err = c.Close(time.Now()); err != nil {
			return err
		}
		return tx.UpdateConversation(c)
	})
}

func (f *fixture) assertNamespaces(jobs []integration.Job) error {
	f.application.mu.Lock()
	defer f.application.mu.Unlock()
	if len(f.application.commands) != 2 || len(f.application.received) != 2 || len(f.application.replyResults) != 2 {
		return errors.New("expected two observed HTTP sessions")
	}
	a, b := f.application.commands[0], f.application.commands[1]
	if a.ConversationID == b.ConversationID || a.CustomerID == b.CustomerID || a.Channel == b.Channel || a.ExternalMessageID == b.ExternalMessageID {
		return errors.New("source mapping was not distinct")
	}
	for i, j := range jobs {
		cmd := f.application.commands[i]
		received := f.application.received[i]
		reply := f.application.replyResults[i]
		if received.ConversationID != cmd.ConversationID || received.MessageID != j.TriggerMessageID || reply.MessageID != j.ReplyMessageID {
			return errors.New("source response identity mismatch")
		}
		messages, err := f.repository.ListMessages(context.Background(), cmd.ConversationID)
		if err != nil || len(messages) != 2 || messages[0].ID() != j.TriggerMessageID || messages[1].ID() != j.ReplyMessageID {
			return errors.New("source saved messages mismatch")
		}
		d, ok := f.sink.receipt(j.DeliveryKey)
		if !ok || d.SourceID != []string{"system-a", "system-b"}[i] || d.ConversationID != event().ConversationID || d.CustomerID != event().CustomerID || d.ReplyMessageID != j.ReplyMessageID {
			return errors.New("source sink receipt mismatch")
		}
	}
	return nil
}
