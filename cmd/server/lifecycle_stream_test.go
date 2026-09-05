package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	hertztransport "github.com/qiuyier/omni-agent-customer-service/internal/transport/hertz"
	kitstream "github.com/qiuyier/omni-agent-kit/stream"
)

const lifecycleStreamBearerToken = "lifecycle-stream-test-token"

func TestHertzSSEGracefulShutdownLetsAdmittedStreamFinish(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	requestRootContext, cancelRequests := context.WithCancel(context.Background())
	stopContext, stop := context.WithCancel(context.Background())
	httpServer := newHertzServer(listener, requestRootContext, 3*time.Second)
	application := newLifecycleStreamApplication()
	availability := newLifecycleStreamAvailability()

	handler, err := hertztransport.NewHandler(application)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	security, err := hertztransport.NewSecurityMiddleware(lifecycleStreamBearerToken)
	if err != nil {
		t.Fatalf("NewSecurityMiddleware() error = %v", err)
	}
	accessLog, err := hertztransport.NewAccessLogMiddleware(zap.NewNop())
	if err != nil {
		t.Fatalf("NewAccessLogMiddleware() error = %v", err)
	}
	rateLimit, err := hertztransport.NewRateLimitMiddleware(100, 100)
	if err != nil {
		t.Fatalf("NewRateLimitMiddleware() error = %v", err)
	}
	if err := hertztransport.RegisterRoutes(
		httpServer.Engine,
		handler,
		availability.Availability,
		security,
		accessLog,
		rateLimit,
	); err != nil {
		t.Fatalf("RegisterRoutes() error = %v", err)
	}

	transport := &http.Transport{
		Proxy:             nil,
		DisableKeepAlives: true,
	}
	client := &http.Client{Transport: transport}
	t.Cleanup(func() {
		application.releaseStream()
		stop()
		cancelRequests()
		transport.CloseIdleConnections()
		_ = httpServer.Close()
		_ = listener.Close()
	})

	serveResult := make(chan error, 1)
	go func() {
		serveResult <- serveHTTP(
			stopContext,
			httpServer,
			availability,
			3*time.Second,
			func() {},
			cancelRequests,
			zap.NewNop(),
		)
	}()

	requestContext, cancelRequest := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancelRequest)
	request, err := http.NewRequestWithContext(
		requestContext,
		http.MethodPost,
		"http://"+listener.Addr().String()+"/v1/conversations/conversation-1/messages/message-1/reply/stream",
		nil,
	)
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+lifecycleStreamBearerToken)
	request.Header.Set("Accept", "text/event-stream")
	request.Close = true

	type responseResult struct {
		response *http.Response
		err      error
	}
	responseResults := make(chan responseResult, 1)
	go func() {
		response, requestErr := client.Do(request)
		responseResults <- responseResult{response: response, err: requestErr}
	}()
	responseValue := awaitValue(t, responseResults, "SSE response headers")
	if responseValue.err != nil {
		t.Fatalf("SSE request error = %v", responseValue.err)
	}
	response := responseValue.response
	if response == nil {
		t.Fatal("SSE response is nil")
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if response.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(contentType), "text/event-stream") {
		t.Fatalf("SSE Content-Type = %q", contentType)
	}

	streamResults := make(chan lifecycleStreamReadResult, 3)
	go readLifecycleSSE(response.Body, streamResults)

	first := awaitValue(t, streamResults, "first SSE event")
	if first.err != nil {
		t.Fatalf("read first SSE event: %v", first.err)
	}
	if first.event.Type != "reply.delta" {
		t.Fatalf("first SSE event type = %q, want reply.delta", first.event.Type)
	}
	var delta struct {
		Delta string `json:"delta"`
	}
	if err := json.Unmarshal(first.event.Data, &delta); err != nil {
		t.Fatalf("decode reply.delta data: %v", err)
	}
	if delta.Delta != "hello " {
		t.Fatalf("reply.delta = %q, want %q", delta.Delta, "hello ")
	}
	awaitSignal(t, application.readerBlocked, "stream reader block after first delta")

	stop()
	awaitSignal(t, availability.draining, "availability draining transition")

	contextInspection := make(chan error, 1)
	application.contextInspections <- contextInspection
	if contextErr := awaitValue(t, contextInspection, "active SSE request context inspection"); contextErr != nil {
		t.Fatalf("active SSE request was cancelled when draining began: %v", contextErr)
	}

	application.releaseStream()
	completed := awaitValue(t, streamResults, "completed SSE event")
	if completed.err != nil {
		t.Fatalf("read completed SSE event: %v", completed.err)
	}
	if completed.event.Type != "reply.completed" {
		t.Fatalf("second SSE event type = %q, want reply.completed", completed.event.Type)
	}
	var result struct {
		ConversationID   string `json:"conversation_id"`
		TriggerMessageID string `json:"trigger_message_id"`
		MessageID        string `json:"message_id"`
		Content          string `json:"content"`
	}
	if err := json.Unmarshal(completed.event.Data, &result); err != nil {
		t.Fatalf("decode reply.completed data: %v", err)
	}
	if result.ConversationID != "conversation-1" ||
		result.TriggerMessageID != "message-1" ||
		result.MessageID != "message-2" ||
		result.Content != "hello world" {
		t.Fatalf("reply.completed data = %#v", result)
	}

	terminal := awaitValue(t, streamResults, "SSE EOF")
	if !errors.Is(terminal.err, io.EOF) {
		t.Fatalf("SSE terminal error = %v, want io.EOF", terminal.err)
	}
	awaitSignal(t, application.readerClosed, "application stream reader close")
	if err := awaitValue(t, serveResult, "graceful SSE shutdown result"); err != nil {
		t.Fatalf("serveHTTP() error = %v", err)
	}
	if requestRootContext.Err() != nil {
		t.Fatalf("request root context error = %v, want nil after graceful drain", requestRootContext.Err())
	}
}

type lifecycleStreamApplication struct {
	release            chan struct{}
	releaseOnce        sync.Once
	readerBlocked      chan struct{}
	readerBlockedOnce  sync.Once
	readerClosed       chan struct{}
	readerClosedOnce   sync.Once
	contextInspections chan chan error
}

func newLifecycleStreamApplication() *lifecycleStreamApplication {
	return &lifecycleStreamApplication{
		release:            make(chan struct{}),
		readerBlocked:      make(chan struct{}),
		readerClosed:       make(chan struct{}),
		contextInspections: make(chan chan error, 1),
	}
}

func (a *lifecycleStreamApplication) StreamBotReply(
	ctx context.Context,
	command customerservice.GenerateBotReplyCommand,
) (kitstream.Reader[customerservice.BotReplyStreamEvent], error) {
	if command.ConversationID != "conversation-1" || command.TriggerMessageID != "message-1" {
		return nil, errors.New("unexpected stream command")
	}
	receiveIndex := 0
	return kitstream.NewReader(func() (customerservice.BotReplyStreamEvent, error) {
		switch receiveIndex {
		case 0:
			receiveIndex++
			return customerservice.BotReplyStreamEvent{
				Type:  customerservice.BotReplyStreamEventDelta,
				Delta: "hello ",
			}, nil

		case 1:
			a.readerBlockedOnce.Do(func() { close(a.readerBlocked) })
			for {
				select {
				case <-a.release:
					receiveIndex++
					return customerservice.BotReplyStreamEvent{
						Type: customerservice.BotReplyStreamEventCompleted,
						Result: customerservice.GenerateBotReplyResult{
							ConversationID:   "conversation-1",
							TriggerMessageID: "message-1",
							MessageID:        "message-2",
							Content:          "hello world",
						},
					}, nil

				case inspection := <-a.contextInspections:
					inspection <- ctx.Err()

				case <-ctx.Done():
					return customerservice.BotReplyStreamEvent{}, ctx.Err()
				}
			}

		default:
			return customerservice.BotReplyStreamEvent{}, io.EOF
		}
	}, func() error {
		a.readerClosedOnce.Do(func() { close(a.readerClosed) })
		return nil
	}), nil
}

func (*lifecycleStreamApplication) ReceiveMessage(
	context.Context,
	customerservice.ReceiveMessageCommand,
) (customerservice.ReceiveMessageResult, error) {
	return customerservice.ReceiveMessageResult{}, errors.New("unexpected ReceiveMessage call")
}

func (*lifecycleStreamApplication) GenerateBotReply(
	context.Context,
	customerservice.GenerateBotReplyCommand,
) (customerservice.GenerateBotReplyResult, error) {
	return customerservice.GenerateBotReplyResult{}, errors.New("unexpected GenerateBotReply call")
}

func (a *lifecycleStreamApplication) releaseStream() {
	a.releaseOnce.Do(func() { close(a.release) })
}

type lifecycleStreamAvailability struct {
	*hertztransport.Availability
	draining     chan struct{}
	drainingOnce sync.Once
}

func newLifecycleStreamAvailability() *lifecycleStreamAvailability {
	return &lifecycleStreamAvailability{
		Availability: hertztransport.NewAvailability(),
		draining:     make(chan struct{}),
	}
}

func (a *lifecycleStreamAvailability) BeginDraining() bool {
	transitioned := a.Availability.BeginDraining()
	if transitioned {
		a.drainingOnce.Do(func() { close(a.draining) })
	}
	return transitioned
}

type lifecycleSSEEvent struct {
	Type string
	Data []byte
}

type lifecycleStreamReadResult struct {
	event lifecycleSSEEvent
	err   error
}

func readLifecycleSSE(body io.Reader, results chan<- lifecycleStreamReadResult) {
	reader := bufio.NewReader(body)
	for {
		event, err := readLifecycleSSEEvent(reader)
		results <- lifecycleStreamReadResult{event: event, err: err}
		if err != nil {
			return
		}
	}
}

func readLifecycleSSEEvent(reader *bufio.Reader) (lifecycleSSEEvent, error) {
	var event lifecycleSSEEvent
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return lifecycleSSEEvent{}, err
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			if event.Type == "" && len(event.Data) == 0 {
				continue
			}
			return event, nil
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			event.Type = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if len(event.Data) != 0 {
				event.Data = append(event.Data, '\n')
			}
			event.Data = append(
				event.Data,
				strings.TrimSpace(strings.TrimPrefix(line, "data:"))...,
			)
		}
	}
}
