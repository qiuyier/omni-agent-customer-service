// Package hertz exposes the customer-service application through Hertz HTTP
// handlers. It translates transport DTOs into application commands and keeps
// Hertz and Thrift types out of the business layers.
package hertz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/transport/hertz/model"
	"github.com/qiuyier/omni-agent-kit/fault"
	kitstream "github.com/qiuyier/omni-agent-kit/stream"
	"github.com/qiuyier/omni-agent-kit/transport/hertz/problem"
	"github.com/qiuyier/omni-agent-kit/transport/hertz/sse"
)

// MessageReceiver is the application port required by the HTTP handler.
// *customerservice.Service satisfies it without depending on Hertz.
type MessageReceiver interface {
	ReceiveMessage(
		ctx context.Context,
		command customerservice.ReceiveMessageCommand,
	) (customerservice.ReceiveMessageResult, error)
}

// BotReplyGenerator is the application port required by the reply endpoint.
type BotReplyGenerator interface {
	GenerateBotReply(
		ctx context.Context,
		command customerservice.GenerateBotReplyCommand,
	) (customerservice.GenerateBotReplyResult, error)
}

// BotReplyStreamer is the application port required by the SSE endpoint.
type BotReplyStreamer interface {
	StreamBotReply(
		ctx context.Context,
		command customerservice.GenerateBotReplyCommand,
	) (kitstream.Reader[customerservice.BotReplyStreamEvent], error)
}

// Application contains exactly the customer-service use cases exposed over
// HTTP. *customerservice.Service satisfies both embedded ports.
type Application interface {
	MessageReceiver
	BotReplyGenerator
	BotReplyStreamer
}

// StreamBotReply streams temporary model deltas and a final authoritative
// outcome. Only the completed event means a bot message was persisted.
func (h *Handler) StreamBotReply(ctx context.Context, requestContext *app.RequestContext) {
	request := model.GenerateBotReplyRequest{
		ConversationID:   requestContext.Param("conversation_id"),
		TriggerMessageID: requestContext.Param("trigger_message_id"),
	}
	if err := requestContext.Validate(&request); err != nil {
		writeProblem(requestContext, fault.Wrap(
			fault.CodeInvalidArgument,
			"invalid request",
			err,
		))
		return
	}

	reader, err := h.application.StreamBotReply(ctx, customerservice.GenerateBotReplyCommand{
		ConversationID:   request.ConversationID,
		TriggerMessageID: request.TriggerMessageID,
	})
	if err != nil {
		writeProblem(requestContext, classifyError(err))
		return
	}
	if reader == nil {
		writeProblem(requestContext, invalidDependency("application returned nil reply stream"))
		return
	}

	// Reader failures happen after the application method returns, so classify
	// them lazily before the generic SSE writer builds its safe error event.
	classified := kitstream.NewReader(func() (customerservice.BotReplyStreamEvent, error) {
		event, recvErr := reader.Recv()
		if recvErr != nil && !errors.Is(recvErr, io.EOF) {
			recvErr = classifyError(recvErr)
		}
		return event, recvErr
	}, reader.Close)
	started, err := sse.Write(ctx, requestContext, classified, encodeBotReplyStreamEvent)
	if err != nil && !started {
		writeProblem(requestContext, classifyError(err))
	}
}

func encodeBotReplyStreamEvent(
	_ context.Context,
	event customerservice.BotReplyStreamEvent,
) (sse.Event, error) {
	var eventType string
	var payload any
	switch event.Type {
	case customerservice.BotReplyStreamEventDelta:
		eventType = "reply.delta"
		payload = &model.ReplyDeltaEventData{Delta: event.Delta}

	case customerservice.BotReplyStreamEventCompleted:
		eventType = "reply.completed"
		payload = &model.ReplyCompletedEventData{
			ConversationID:   event.Result.ConversationID,
			TriggerMessageID: event.Result.TriggerMessageID,
			MessageID:        event.Result.MessageID,
			Content:          event.Result.Content,
			Duplicate:        event.Result.Duplicate,
		}

	case customerservice.BotReplyStreamEventSuperseded:
		eventType = "reply.superseded"
		payload = &model.ReplySupersededEventData{
			ConversationID:   event.Result.ConversationID,
			TriggerMessageID: event.Result.TriggerMessageID,
			Message:          "reply superseded by a newer customer message",
		}

	default:
		return sse.Event{}, fmt.Errorf("unknown bot reply stream event type %q", event.Type)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return sse.Event{}, fmt.Errorf("encode bot reply stream event: %w", err)
	}
	return sse.Event{Type: eventType, Data: data}, nil
}

// Handler translates Hertz requests and responses at the HTTP boundary.
type Handler struct {
	application Application
}

// NewHandler creates a handler with its application dependency.
func NewHandler(application Application) (*Handler, error) {
	if application == nil {
		return nil, invalidDependency("application is nil")
	}
	return &Handler{application: application}, nil
}

// Health reports whether the HTTP process is running. It intentionally does
// not call the model provider or persistence layer, so it remains a cheap
// liveness probe.
func (h *Handler) Health(_ context.Context, ctx *app.RequestContext) {
	ctx.JSON(http.StatusOK, &model.HealthResponse{Status: "ok"})
}

// ReceiveMessage accepts one customer message and delegates all business rules
// and idempotency decisions to the application service.
func (h *Handler) ReceiveMessage(ctx context.Context, requestContext *app.RequestContext) {
	var request model.ReceiveMessageRequest
	if err := bindReceiveMessageRequest(requestContext, &request); err != nil {
		writeProblem(requestContext, fault.Wrap(
			fault.CodeInvalidArgument,
			"invalid request",
			err,
		))
		return
	}

	result, err := h.application.ReceiveMessage(ctx, customerservice.ReceiveMessageCommand{
		ConversationID:    request.ConversationID,
		CustomerID:        request.CustomerID,
		Channel:           request.Channel,
		ExternalMessageID: request.ExternalMessageID,
		Content:           request.Content,
	})
	if err != nil {
		writeProblem(requestContext, classifyError(err))
		return
	}

	statusCode := http.StatusCreated
	if result.Duplicate {
		statusCode = http.StatusOK
	}
	requestContext.JSON(statusCode, &model.ReceiveMessageResponse{
		ConversationID: result.ConversationID,
		MessageID:      result.MessageID,
		Status:         string(result.Status),
		NextAction:     string(result.NextAction),
		Duplicate:      result.Duplicate,
	})
}

// GenerateBotReply creates or returns the persisted bot reply linked to one
// explicit customer trigger message.
func (h *Handler) GenerateBotReply(ctx context.Context, requestContext *app.RequestContext) {
	request := model.GenerateBotReplyRequest{
		ConversationID:   requestContext.Param("conversation_id"),
		TriggerMessageID: requestContext.Param("trigger_message_id"),
	}
	if err := requestContext.Validate(&request); err != nil {
		writeProblem(requestContext, fault.Wrap(
			fault.CodeInvalidArgument,
			"invalid request",
			err,
		))
		return
	}

	result, err := h.application.GenerateBotReply(ctx, customerservice.GenerateBotReplyCommand{
		ConversationID:   request.ConversationID,
		TriggerMessageID: request.TriggerMessageID,
	})
	if err != nil {
		writeProblem(requestContext, classifyError(err))
		return
	}

	statusCode := http.StatusCreated
	if result.Duplicate {
		statusCode = http.StatusOK
	}
	requestContext.JSON(statusCode, &model.GenerateBotReplyResponse{
		ConversationID:   result.ConversationID,
		TriggerMessageID: result.TriggerMessageID,
		MessageID:        result.MessageID,
		Content:          result.Content,
		Duplicate:        result.Duplicate,
	})
}

func bindReceiveMessageRequest(
	ctx *app.RequestContext,
	request *model.ReceiveMessageRequest,
) error {
	mediaType, _, err := mime.ParseMediaType(string(ctx.Request.Header.ContentType()))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return errors.New("content type must be application/json")
	}
	// BindJSON intentionally ignores generated form tags, so query and form
	// values cannot override the JSON payload consumed by this handler.
	if err := ctx.BindJSON(request); err != nil {
		return err
	}
	// The URL identifies the target aggregate and is always authoritative over
	// the generated field's default JSON tag.
	request.ConversationID = ctx.Param("conversation_id")
	return ctx.Validate(request)
}

func writeProblem(ctx *app.RequestContext, err error) {
	// Problem contains only scalar fields, so JSON marshaling cannot fail. These
	// handlers do not install a streaming writer; SSE handlers must handle
	// problem.ErrStreamingResponse separately after a stream has started.
	_ = problem.Write(ctx, err)
}
