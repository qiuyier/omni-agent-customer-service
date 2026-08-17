package hertz

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/cloudwego/hertz/pkg/route"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/transport/hertz/model"
	"github.com/qiuyier/omni-agent-kit/fault"
	"github.com/qiuyier/omni-agent-kit/transport/hertz/problem"
)

func TestNewHandlerRejectsNilReceiver(t *testing.T) {
	handler, err := NewHandler(nil)
	if !errors.Is(err, errInvalidDependency) {
		t.Fatalf("NewHandler() error = %v, want %v", err, errInvalidDependency)
	}
	if handler != nil {
		t.Fatalf("NewHandler() handler = %#v, want nil", handler)
	}
}

func TestRegisterRoutesRejectsInvalidDependencies(t *testing.T) {
	receiver := &receiverStub{}
	handler, err := NewHandler(receiver)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	if err := RegisterRoutes(nil, handler); !errors.Is(err, errInvalidDependency) {
		t.Fatalf("RegisterRoutes(nil, handler) error = %v, want %v", err, errInvalidDependency)
	}
	httpServer := server.New()
	if err := RegisterRoutes(httpServer.Engine, nil); !errors.Is(err, errInvalidDependency) {
		t.Fatalf("RegisterRoutes(engine, nil) error = %v, want %v", err, errInvalidDependency)
	}
}

func TestHealth(t *testing.T) {
	engine := newTestEngine(t, &receiverStub{})
	response := ut.PerformRequest(engine, http.MethodGet, healthPath, nil).Result()

	assertStatus(t, response, http.StatusOK)
	assertContentType(t, response, "application/json; charset=utf-8")
	var body model.HealthResponse
	decodeResponse(t, response, &body)
	if body.Status != "ok" {
		t.Fatalf("health status = %q, want %q", body.Status, "ok")
	}
}

func TestReceiveMessageCreated(t *testing.T) {
	receiver := &receiverStub{result: customerservice.ReceiveMessageResult{
		ConversationID: "conversation-from-service",
		MessageID:      "message-1",
		Status:         conversation.StatusBotActive,
		NextAction:     customerservice.NextActionGenerateBotReply,
	}}
	engine := newTestEngine(t, receiver)
	payload := `{
		"ConversationID":"body-must-not-win",
		"customer_id":"customer-1",
		"channel":"web",
		"external_message_id":"external-1",
		"content":"Where is my order?"
	}`

	response := performJSON(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-from-path/messages",
		payload,
	)

	assertStatus(t, response, http.StatusCreated)
	assertContentType(t, response, "application/json; charset=utf-8")
	if receiver.calls != 1 {
		t.Fatalf("receiver calls = %d, want 1", receiver.calls)
	}
	wantCommand := customerservice.ReceiveMessageCommand{
		ConversationID:    "conversation-from-path",
		CustomerID:        "customer-1",
		Channel:           "web",
		ExternalMessageID: "external-1",
		Content:           "Where is my order?",
	}
	if receiver.command != wantCommand {
		t.Fatalf("command = %#v, want %#v", receiver.command, wantCommand)
	}

	var body model.ReceiveMessageResponse
	decodeResponse(t, response, &body)
	if body.ConversationID != "conversation-from-service" ||
		body.MessageID != "message-1" ||
		body.Status != string(conversation.StatusBotActive) ||
		body.NextAction != string(customerservice.NextActionGenerateBotReply) ||
		body.Duplicate {
		t.Fatalf("response body = %#v", body)
	}
}

func TestReceiveMessageIgnoresQueryValues(t *testing.T) {
	receiver := &receiverStub{result: customerservice.ReceiveMessageResult{
		ConversationID: "conversation-1",
		MessageID:      "message-1",
		Status:         conversation.StatusBotActive,
		NextAction:     customerservice.NextActionGenerateBotReply,
	}}
	engine := newTestEngine(t, receiver)
	target := "/v1/conversations/conversation-1/messages" +
		"?customer_id=attacker&channel=attacker&external_message_id=attacker&content=attacker"

	response := performJSON(engine, http.MethodPost, target, validReceiveMessageJSON)

	assertStatus(t, response, http.StatusCreated)
	wantCommand := customerservice.ReceiveMessageCommand{
		ConversationID:    "conversation-1",
		CustomerID:        "customer-1",
		Channel:           "web",
		ExternalMessageID: "external-1",
		Content:           "Where is my order?",
	}
	if receiver.command != wantCommand {
		t.Fatalf("command = %#v, want JSON command %#v", receiver.command, wantCommand)
	}
}

func TestReceiveMessageRejectsQueryOnlyInput(t *testing.T) {
	receiver := &receiverStub{}
	engine := newTestEngine(t, receiver)
	target := "/v1/conversations/conversation-1/messages" +
		"?customer_id=customer-1&channel=web&external_message_id=external-1&content=secret"

	response := performJSON(engine, http.MethodPost, target, `{}`)

	assertProblem(t, response, http.StatusBadRequest, fault.CodeInvalidArgument, "invalid request")
	if receiver.calls != 0 {
		t.Fatalf("receiver calls = %d, want 0", receiver.calls)
	}
}

func TestReceiveMessageRejectsFormBody(t *testing.T) {
	receiver := &receiverStub{}
	engine := newTestEngine(t, receiver)
	payload := "customer_id=customer-1&channel=web&external_message_id=external-1&content=secret"
	response := ut.PerformRequest(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-1/messages",
		&ut.Body{Body: strings.NewReader(payload), Len: len(payload)},
		ut.Header{Key: "Content-Type", Value: "application/x-www-form-urlencoded"},
	).Result()

	assertProblem(t, response, http.StatusBadRequest, fault.CodeInvalidArgument, "invalid request")
	if receiver.calls != 0 {
		t.Fatalf("receiver calls = %d, want 0", receiver.calls)
	}
}

func TestReceiveMessageDuplicate(t *testing.T) {
	receiver := &receiverStub{result: customerservice.ReceiveMessageResult{
		ConversationID: "conversation-1",
		MessageID:      "message-1",
		Status:         conversation.StatusBotActive,
		NextAction:     customerservice.NextActionNone,
		Duplicate:      true,
	}}
	engine := newTestEngine(t, receiver)

	response := performJSON(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-1/messages",
		validReceiveMessageJSON,
	)

	assertStatus(t, response, http.StatusOK)
	var body model.ReceiveMessageResponse
	decodeResponse(t, response, &body)
	if !body.Duplicate {
		t.Fatal("duplicate = false, want true")
	}
	if body.MessageID != "message-1" {
		t.Fatalf("message ID = %q, want %q", body.MessageID, "message-1")
	}
	if body.NextAction != string(customerservice.NextActionNone) {
		t.Fatalf("next action = %q, want %q", body.NextAction, customerservice.NextActionNone)
	}
}

func TestReceiveMessageRejectsMalformedJSON(t *testing.T) {
	receiver := &receiverStub{}
	engine := newTestEngine(t, receiver)

	response := performJSON(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-1/messages",
		`{"customer_id":`,
	)

	assertProblem(t, response, http.StatusBadRequest, fault.CodeInvalidArgument, "invalid request")
	if receiver.calls != 0 {
		t.Fatalf("receiver calls = %d, want 0", receiver.calls)
	}
	if strings.Contains(string(response.Body()), "unexpected") {
		t.Fatalf("response leaked parser detail: %s", response.Body())
	}
}

func TestReceiveMessageRejectsMissingRequiredField(t *testing.T) {
	receiver := &receiverStub{}
	engine := newTestEngine(t, receiver)
	payload := `{
		"customer_id":"customer-1",
		"channel":"web",
		"external_message_id":"external-1"
	}`

	response := performJSON(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-1/messages",
		payload,
	)

	assertProblem(t, response, http.StatusBadRequest, fault.CodeInvalidArgument, "invalid request")
	if receiver.calls != 0 {
		t.Fatalf("receiver calls = %d, want 0", receiver.calls)
	}
}

func TestReceiveMessageRejectsBlankContent(t *testing.T) {
	service, err := customerservice.New(
		conversation.NewMemoryRepository(),
		customerservice.MessageIDGeneratorFunc(func() (string, error) {
			return "message-1", nil
		}),
		customerservice.ClockFunc(func() time.Time {
			return time.Date(2026, time.August, 17, 10, 0, 0, 0, time.UTC)
		}),
	)
	if err != nil {
		t.Fatalf("customerservice.New() error = %v", err)
	}
	engine := newTestEngine(t, service)
	payload := `{
		"customer_id":"customer-1",
		"channel":"web",
		"external_message_id":"external-1",
		"content":"   "
	}`

	response := performJSON(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-1/messages",
		payload,
	)

	assertProblem(t, response, http.StatusBadRequest, fault.CodeInvalidArgument, "invalid request")
}

func TestReceiveMessageDoesNotLeakInternalError(t *testing.T) {
	receiver := &receiverStub{err: errors.New(
		"provider api_key=sk-secret database_password=secret",
	)}
	engine := newTestEngine(t, receiver)

	response := performJSON(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-1/messages",
		validReceiveMessageJSON,
	)

	assertProblem(t, response, http.StatusInternalServerError, fault.CodeInternal, "internal error")
	responseBody := string(response.Body())
	for _, secret := range []string{"sk-secret", "database_password", "api_key"} {
		if strings.Contains(responseBody, secret) {
			t.Fatalf("response contains secret marker %q: %s", secret, responseBody)
		}
	}
}

func TestReceiveMessageHTTPIsIdempotent(t *testing.T) {
	repository := conversation.NewMemoryRepository()
	idCalls := 0
	service, err := customerservice.New(
		repository,
		customerservice.MessageIDGeneratorFunc(func() (string, error) {
			idCalls++
			return "message-1", nil
		}),
		customerservice.ClockFunc(func() time.Time {
			return time.Date(2026, time.August, 17, 10, 0, 0, 0, time.UTC)
		}),
	)
	if err != nil {
		t.Fatalf("customerservice.New() error = %v", err)
	}
	engine := newTestEngine(t, service)
	target := "/v1/conversations/conversation-1/messages"

	firstResponse := performJSON(engine, http.MethodPost, target, validReceiveMessageJSON)
	secondResponse := performJSON(engine, http.MethodPost, target, validReceiveMessageJSON)

	assertStatus(t, firstResponse, http.StatusCreated)
	assertStatus(t, secondResponse, http.StatusOK)
	var first model.ReceiveMessageResponse
	var second model.ReceiveMessageResponse
	decodeResponse(t, firstResponse, &first)
	decodeResponse(t, secondResponse, &second)
	if first.Duplicate {
		t.Fatal("first response duplicate = true, want false")
	}
	if !second.Duplicate {
		t.Fatal("second response duplicate = false, want true")
	}
	if first.MessageID != second.MessageID {
		t.Fatalf("message IDs = %q and %q, want equal", first.MessageID, second.MessageID)
	}
	if second.NextAction != string(customerservice.NextActionNone) {
		t.Fatalf("second next action = %q, want %q", second.NextAction, customerservice.NextActionNone)
	}
	if idCalls != 1 {
		t.Fatalf("ID generator calls = %d, want 1", idCalls)
	}
	messages, err := repository.ListMessages(context.Background(), "conversation-1")
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("stored messages = %d, want 1", len(messages))
	}
}

const validReceiveMessageJSON = `{
	"customer_id":"customer-1",
	"channel":"web",
	"external_message_id":"external-1",
	"content":"Where is my order?"
}`

type receiverStub struct {
	calls   int
	command customerservice.ReceiveMessageCommand
	result  customerservice.ReceiveMessageResult
	err     error
}

func (stub *receiverStub) ReceiveMessage(
	_ context.Context,
	command customerservice.ReceiveMessageCommand,
) (customerservice.ReceiveMessageResult, error) {
	stub.calls++
	stub.command = command
	return stub.result, stub.err
}

func newTestEngine(t *testing.T, receiver MessageReceiver) *route.Engine {
	t.Helper()
	handler, err := NewHandler(receiver)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	httpServer := server.New()
	if err := RegisterRoutes(httpServer.Engine, handler); err != nil {
		t.Fatalf("RegisterRoutes() error = %v", err)
	}
	return httpServer.Engine
}

func performJSON(engine *route.Engine, method, target, payload string) *protocol.Response {
	return ut.PerformRequest(
		engine,
		method,
		target,
		&ut.Body{Body: strings.NewReader(payload), Len: len(payload)},
		ut.Header{Key: "Content-Type", Value: "application/json; charset=utf-8"},
	).Result()
}

func assertStatus(t *testing.T, response *protocol.Response, want int) {
	t.Helper()
	if got := response.StatusCode(); got != want {
		t.Fatalf("status = %d, want %d; body = %s", got, want, response.Body())
	}
}

func assertContentType(t *testing.T, response *protocol.Response, want string) {
	t.Helper()
	if got := string(response.Header.ContentType()); got != want {
		t.Fatalf("content type = %q, want %q", got, want)
	}
}

func decodeResponse(t *testing.T, response *protocol.Response, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body(), target); err != nil {
		t.Fatalf("decode response %q: %v", response.Body(), err)
	}
}

func assertProblem(
	t *testing.T,
	response *protocol.Response,
	wantStatus int,
	wantCode fault.Code,
	wantDetail string,
) {
	t.Helper()
	assertStatus(t, response, wantStatus)
	assertContentType(t, response, problem.MediaType)
	var body problem.Problem
	decodeResponse(t, response, &body)
	if body.Type != "about:blank" ||
		body.Status != wantStatus ||
		body.Code != wantCode ||
		body.Detail != wantDetail {
		t.Fatalf("problem = %#v", body)
	}
}
