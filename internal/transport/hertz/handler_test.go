package hertz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
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
	kitstream "github.com/qiuyier/omni-agent-kit/stream"
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

	security := newTestSecurityMiddleware(t)
	if err := RegisterRoutes(nil, handler, security); !errors.Is(err, errInvalidDependency) {
		t.Fatalf("RegisterRoutes(nil, handler) error = %v, want %v", err, errInvalidDependency)
	}
	httpServer := server.New()
	if err := RegisterRoutes(httpServer.Engine, nil, security); !errors.Is(err, errInvalidDependency) {
		t.Fatalf("RegisterRoutes(engine, nil) error = %v, want %v", err, errInvalidDependency)
	}
	if err := RegisterRoutes(httpServer.Engine, handler, nil); !errors.Is(err, errInvalidDependency) {
		t.Fatalf("RegisterRoutes(engine, handler, nil) error = %v, want %v", err, errInvalidDependency)
	}
}

func TestHealth(t *testing.T) {
	engine := newTestEngine(t, &receiverStub{})
	response := ut.PerformRequest(engine, http.MethodGet, healthPath, nil).Result()

	assertStatus(t, response, http.StatusOK)
	assertContentType(t, response, "application/json; charset=utf-8")
	if requestID := string(response.Header.Peek(RequestIDHeader)); requestID == "" {
		t.Fatal("health response X-Request-ID is empty")
	}
	var body model.HealthResponse
	decodeResponse(t, response, &body)
	if body.Status != "ok" {
		t.Fatalf("health status = %q, want %q", body.Status, "ok")
	}

	responseWithWrongToken := ut.PerformRequest(
		engine,
		http.MethodGet,
		healthPath,
		nil,
		ut.Header{Key: "Authorization", Value: "Bearer wrong-token"},
	).Result()
	assertStatus(t, responseWithWrongToken, http.StatusOK)
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
	response := performAuthorizedRequest(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-1/messages",
		&ut.Body{Body: strings.NewReader(payload), Len: len(payload)},
		ut.Header{Key: "Content-Type", Value: "application/x-www-form-urlencoded"},
	)

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

func TestGenerateBotReplyCreated(t *testing.T) {
	application := &receiverStub{replyResult: customerservice.GenerateBotReplyResult{
		ConversationID:   "conversation-from-service",
		TriggerMessageID: "trigger-from-service",
		MessageID:        "message-2",
		Content:          "Your order is on the way.",
	}}
	engine := newTestEngine(t, application)
	target := "/v1/conversations/conversation-from-path/messages/trigger-from-path/reply" +
		"?conversation_id=query-must-not-win&trigger_message_id=query-must-not-win"

	response := performAuthorizedRequest(engine, http.MethodPost, target, nil)

	assertStatus(t, response, http.StatusCreated)
	assertContentType(t, response, "application/json; charset=utf-8")
	if application.replyCalls != 1 {
		t.Fatalf("reply calls = %d, want 1", application.replyCalls)
	}
	wantCommand := customerservice.GenerateBotReplyCommand{
		ConversationID:   "conversation-from-path",
		TriggerMessageID: "trigger-from-path",
	}
	if application.replyCommand != wantCommand {
		t.Fatalf("reply command = %#v, want %#v", application.replyCommand, wantCommand)
	}

	var body model.GenerateBotReplyResponse
	decodeResponse(t, response, &body)
	if body.ConversationID != "conversation-from-service" ||
		body.TriggerMessageID != "trigger-from-service" ||
		body.MessageID != "message-2" ||
		body.Content != "Your order is on the way." ||
		body.Duplicate {
		t.Fatalf("response body = %#v", body)
	}
}

func TestGenerateBotReplyDuplicate(t *testing.T) {
	application := &receiverStub{replyResult: customerservice.GenerateBotReplyResult{
		ConversationID:   "conversation-1",
		TriggerMessageID: "message-1",
		MessageID:        "message-2",
		Content:          "Persisted answer",
		Duplicate:        true,
	}}
	engine := newTestEngine(t, application)

	response := performAuthorizedRequest(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-1/messages/message-1/reply",
		nil,
	)

	assertStatus(t, response, http.StatusOK)
	var body model.GenerateBotReplyResponse
	decodeResponse(t, response, &body)
	if !body.Duplicate {
		t.Fatal("duplicate = false, want true")
	}
	if body.MessageID != "message-2" || body.Content != "Persisted answer" {
		t.Fatalf("response body = %#v", body)
	}
}

func TestGenerateBotReplyErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   fault.Code
		wantDetail string
	}{
		{
			name:       "invalid trigger",
			err:        customerservice.ErrInvalidReplyTrigger,
			wantStatus: http.StatusBadRequest,
			wantCode:   fault.CodeInvalidArgument,
			wantDetail: "invalid request",
		},
		{
			name:       "conversation not found",
			err:        conversation.ErrConversationNotFound,
			wantStatus: http.StatusNotFound,
			wantCode:   fault.CodeNotFound,
			wantDetail: "resource not found",
		},
		{
			name:       "superseded",
			err:        customerservice.ErrReplySuperseded,
			wantStatus: http.StatusConflict,
			wantCode:   fault.CodeConflict,
			wantDetail: "resource conflict",
		},
		{
			name:       "conversation closed",
			err:        conversation.ErrConversationClosed,
			wantStatus: http.StatusPreconditionFailed,
			wantCode:   fault.CodeFailedPrecondition,
			wantDetail: "failed precondition",
		},
		{
			name:       "reply unavailable",
			err:        customerservice.ErrReplyUnavailable,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   fault.CodeUnavailable,
			wantDetail: "reply service unavailable",
		},
		{
			name: "model failure",
			err: fmt.Errorf(
				"%w: %w",
				customerservice.ErrReplyGenerationFailed,
				fault.New(fault.CodeInvalidArgument, "provider api_key=sk-secret"),
			),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   fault.CodeUnavailable,
			wantDetail: "reply service unavailable",
		},
		{
			name:       "empty model reply",
			err:        customerservice.ErrEmptyReply,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   fault.CodeUnavailable,
			wantDetail: "reply service unavailable",
		},
		{
			name:       "cancelled",
			err:        context.Canceled,
			wantStatus: http.StatusRequestTimeout,
			wantCode:   fault.CodeCancelled,
			wantDetail: "request cancelled",
		},
		{
			name:       "deadline",
			err:        context.DeadlineExceeded,
			wantStatus: http.StatusGatewayTimeout,
			wantCode:   fault.CodeDeadlineExceeded,
			wantDetail: "deadline exceeded",
		},
		{
			name:       "unknown internal error",
			err:        errors.New("database_password=secret"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   fault.CodeInternal,
			wantDetail: "internal error",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			application := &receiverStub{replyErr: test.err}
			engine := newTestEngine(t, application)
			response := performAuthorizedRequest(
				engine,
				http.MethodPost,
				"/v1/conversations/conversation-1/messages/message-1/reply",
				nil,
			)

			assertProblem(t, response, test.wantStatus, test.wantCode, test.wantDetail)
			if application.replyCalls != 1 {
				t.Fatalf("reply calls = %d, want 1", application.replyCalls)
			}
			responseBody := string(response.Body())
			for _, secret := range []string{"sk-secret", "api_key", "database_password"} {
				if strings.Contains(responseBody, secret) {
					t.Fatalf("response contains secret marker %q: %s", secret, responseBody)
				}
			}
		})
	}
}

func TestGenerateBotReplyHTTPIsIdempotent(t *testing.T) {
	repository := conversation.NewMemoryRepository()
	messageIDs := []string{"message-1", "message-2"}
	idCalls := 0
	generatorCalls := 0
	service, err := customerservice.New(
		repository,
		customerservice.MessageIDGeneratorFunc(func() (string, error) {
			messageID := messageIDs[idCalls]
			idCalls++
			return messageID, nil
		}),
		customerservice.ClockFunc(func() time.Time {
			return time.Date(2026, time.August, 17, 10, 0, 0, 0, time.UTC)
		}),
		customerservice.WithReplyGenerator(customerservice.ReplyGeneratorFunc(func(
			_ context.Context,
			request customerservice.ReplyRequest,
		) (customerservice.Reply, error) {
			generatorCalls++
			if request.ConversationID != "conversation-1" ||
				request.TriggerMessageID != "message-1" {
				t.Fatalf("reply request = %#v", request)
			}
			return customerservice.Reply{Content: "Your order is on the way."}, nil
		})),
	)
	if err != nil {
		t.Fatalf("customerservice.New() error = %v", err)
	}
	engine := newTestEngine(t, service)

	receiveResponse := performJSON(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-1/messages",
		validReceiveMessageJSON,
	)
	assertStatus(t, receiveResponse, http.StatusCreated)
	target := "/v1/conversations/conversation-1/messages/message-1/reply"
	firstResponse := performAuthorizedRequest(engine, http.MethodPost, target, nil)
	secondResponse := performAuthorizedRequest(engine, http.MethodPost, target, nil)

	assertStatus(t, firstResponse, http.StatusCreated)
	assertStatus(t, secondResponse, http.StatusOK)
	var first model.GenerateBotReplyResponse
	var second model.GenerateBotReplyResponse
	decodeResponse(t, firstResponse, &first)
	decodeResponse(t, secondResponse, &second)
	if first.Duplicate {
		t.Fatal("first response duplicate = true, want false")
	}
	if !second.Duplicate {
		t.Fatal("second response duplicate = false, want true")
	}
	if first.MessageID != "message-2" || first.MessageID != second.MessageID ||
		first.Content != "Your order is on the way." || first.Content != second.Content {
		t.Fatalf("first = %#v; second = %#v", first, second)
	}
	if generatorCalls != 1 {
		t.Fatalf("generator calls = %d, want 1", generatorCalls)
	}
	if idCalls != 2 {
		t.Fatalf("ID generator calls = %d, want 2", idCalls)
	}
	messages, err := repository.ListMessages(context.Background(), "conversation-1")
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 2 || messages[1].Sender() != conversation.SenderBot ||
		messages[1].ReplyToMessageID() != "message-1" ||
		messages[1].Content() != "Your order is on the way." {
		t.Fatalf("stored messages = %#v", messages)
	}
}

func TestGenerateBotReplyRejectsStaleTriggerOverHTTP(t *testing.T) {
	repository := conversation.NewMemoryRepository()
	messageIDs := []string{"message-1", "message-2"}
	idCalls := 0
	generatorCalls := 0
	service, err := customerservice.New(
		repository,
		customerservice.MessageIDGeneratorFunc(func() (string, error) {
			messageID := messageIDs[idCalls]
			idCalls++
			return messageID, nil
		}),
		customerservice.ClockFunc(func() time.Time {
			return time.Date(2026, time.August, 17, 10, 0, 0, 0, time.UTC)
		}),
		customerservice.WithReplyGenerator(customerservice.ReplyGeneratorFunc(func(
			_ context.Context,
			_ customerservice.ReplyRequest,
		) (customerservice.Reply, error) {
			generatorCalls++
			return customerservice.Reply{Content: "must not be generated"}, nil
		})),
	)
	if err != nil {
		t.Fatalf("customerservice.New() error = %v", err)
	}
	engine := newTestEngine(t, service)

	firstResponse := performJSON(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-1/messages",
		validReceiveMessageJSON,
	)
	secondResponse := performJSON(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-1/messages",
		`{
			"customer_id":"customer-1",
			"channel":"web",
			"external_message_id":"external-2",
			"content":"I also need to change the address."
		}`,
	)
	assertStatus(t, firstResponse, http.StatusCreated)
	assertStatus(t, secondResponse, http.StatusCreated)

	replyResponse := performAuthorizedRequest(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-1/messages/message-1/reply",
		nil,
	)

	assertProblem(t, replyResponse, http.StatusConflict, fault.CodeConflict, "resource conflict")
	if generatorCalls != 0 {
		t.Fatalf("generator calls = %d, want 0", generatorCalls)
	}
	messages, err := repository.ListMessages(context.Background(), "conversation-1")
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("stored messages = %d, want 2", len(messages))
	}
}

func TestStreamBotReplyFailureBeforeFirstEventIsProblem(t *testing.T) {
	secret := errors.New("provider api_key=sk-secret")
	application := &receiverStub{
		streamReader: kitstream.NewReader(func() (customerservice.BotReplyStreamEvent, error) {
			return customerservice.BotReplyStreamEvent{}, fmt.Errorf(
				"%w: %w",
				customerservice.ErrReplyGenerationFailed,
				secret,
			)
		}, nil),
	}
	engine := newTestEngine(t, application)

	response := performAuthorizedRequest(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-1/messages/message-1/reply/stream",
		nil,
	)

	assertProblem(
		t,
		response,
		http.StatusServiceUnavailable,
		fault.CodeUnavailable,
		"reply service unavailable",
	)
	if strings.Contains(string(response.Body()), "sk-secret") {
		t.Fatalf("problem response leaked provider error: %s", response.Body())
	}
}

func TestStreamBotReplyRejectsNilApplicationReader(t *testing.T) {
	application := &receiverStub{}
	engine := newTestEngine(t, application)

	response := performAuthorizedRequest(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-1/messages/message-1/reply/stream",
		nil,
	)

	assertProblem(t, response, http.StatusInternalServerError, fault.CodeInternal, "internal error")
}

func TestEncodeBotReplyStreamEvents(t *testing.T) {
	tests := []struct {
		name       string
		input      customerservice.BotReplyStreamEvent
		wantType   string
		wantFields map[string]any
	}{
		{
			name:       "delta",
			input:      customerservice.BotReplyStreamEvent{Type: customerservice.BotReplyStreamEventDelta, Delta: "hello"},
			wantType:   "reply.delta",
			wantFields: map[string]any{"delta": "hello"},
		},
		{
			name: "completed",
			input: customerservice.BotReplyStreamEvent{
				Type: customerservice.BotReplyStreamEventCompleted,
				Result: customerservice.GenerateBotReplyResult{
					ConversationID:   "conversation-1",
					TriggerMessageID: "message-1",
					MessageID:        "message-2",
					Content:          "hello",
					Duplicate:        true,
				},
			},
			wantType: "reply.completed",
			wantFields: map[string]any{
				"conversation_id":    "conversation-1",
				"trigger_message_id": "message-1",
				"message_id":         "message-2",
				"content":            "hello",
				"duplicate":          true,
			},
		},
		{
			name: "superseded",
			input: customerservice.BotReplyStreamEvent{
				Type: customerservice.BotReplyStreamEventSuperseded,
				Result: customerservice.GenerateBotReplyResult{
					ConversationID:   "conversation-1",
					TriggerMessageID: "message-1",
				},
			},
			wantType: "reply.superseded",
			wantFields: map[string]any{
				"conversation_id":    "conversation-1",
				"trigger_message_id": "message-1",
				"message":            "reply superseded by a newer customer message",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event, err := encodeBotReplyStreamEvent(context.Background(), test.input)
			if err != nil {
				t.Fatalf("encodeBotReplyStreamEvent() error = %v", err)
			}
			if event.Type != test.wantType || event.ID != "" {
				t.Fatalf("encoded event type = %q, ID = %q", event.Type, event.ID)
			}
			var fields map[string]any
			if err := json.Unmarshal(event.Data, &fields); err != nil {
				t.Fatalf("decode event data %q: %v", event.Data, err)
			}
			if !reflect.DeepEqual(fields, test.wantFields) {
				t.Fatalf("event data = %#v, want %#v", fields, test.wantFields)
			}
		})
	}

	if _, err := encodeBotReplyStreamEvent(context.Background(), customerservice.BotReplyStreamEvent{}); err == nil {
		t.Fatal("unknown event type error = nil")
	}
}

const validReceiveMessageJSON = `{
	"customer_id":"customer-1",
	"channel":"web",
	"external_message_id":"external-1",
	"content":"Where is my order?"
}`

type receiverStub struct {
	calls         int
	command       customerservice.ReceiveMessageCommand
	result        customerservice.ReceiveMessageResult
	err           error
	replyCalls    int
	replyCommand  customerservice.GenerateBotReplyCommand
	replyResult   customerservice.GenerateBotReplyResult
	replyErr      error
	streamCalls   int
	streamCommand customerservice.GenerateBotReplyCommand
	streamReader  kitstream.Reader[customerservice.BotReplyStreamEvent]
	streamErr     error
}

func (stub *receiverStub) StreamBotReply(
	_ context.Context,
	command customerservice.GenerateBotReplyCommand,
) (kitstream.Reader[customerservice.BotReplyStreamEvent], error) {
	stub.streamCalls++
	stub.streamCommand = command
	return stub.streamReader, stub.streamErr
}

func (stub *receiverStub) ReceiveMessage(
	_ context.Context,
	command customerservice.ReceiveMessageCommand,
) (customerservice.ReceiveMessageResult, error) {
	stub.calls++
	stub.command = command
	return stub.result, stub.err
}

func (stub *receiverStub) GenerateBotReply(
	_ context.Context,
	command customerservice.GenerateBotReplyCommand,
) (customerservice.GenerateBotReplyResult, error) {
	stub.replyCalls++
	stub.replyCommand = command
	return stub.replyResult, stub.replyErr
}

func newTestEngine(t *testing.T, application Application) *route.Engine {
	t.Helper()
	handler, err := NewHandler(application)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	httpServer := server.New()
	if err := RegisterRoutes(httpServer.Engine, handler, newTestSecurityMiddleware(t)); err != nil {
		t.Fatalf("RegisterRoutes() error = %v", err)
	}
	return httpServer.Engine
}

func performJSON(engine *route.Engine, method, target, payload string) *protocol.Response {
	return performAuthorizedRequest(
		engine,
		method,
		target,
		&ut.Body{Body: strings.NewReader(payload), Len: len(payload)},
		ut.Header{Key: "Content-Type", Value: "application/json; charset=utf-8"},
	)
}

func performAuthorizedRequest(
	engine *route.Engine,
	method string,
	target string,
	body *ut.Body,
	headers ...ut.Header,
) *protocol.Response {
	headers = append(headers, ut.Header{
		Key:   "Authorization",
		Value: "Bearer " + testAPIAuthToken,
	})
	return ut.PerformRequest(engine, method, target, body, headers...).Result()
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
