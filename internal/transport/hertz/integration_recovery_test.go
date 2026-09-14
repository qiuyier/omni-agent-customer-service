package hertz

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/route"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/transport/hertz/model"
	kitstream "github.com/qiuyier/omni-agent-kit/stream"
)

// These cases deliberately discard initial response data from the simulated
// adapter, not the server. They verify result recovery, not TCP failure,
// durable adapter scheduling, process restart, or downstream delivery.
func TestIntegrationRecoveryAfterLostAcknowledgements(t *testing.T) {
	for _, firstMode := range []string{"ordinary", "stream"} {
		t.Run(firstMode, func(t *testing.T) {
			const conversationID = "helpdesk-session-42"
			const customerText = "帮助中心在哪里？"
			const replyText = "请从应用首页打开帮助中心。"
			const payload = `{"customer_id":"user-27","channel":"helpdesk-web","external_message_id":"event-305","content":"帮助中心在哪里？"}`
			repository := conversation.NewMemoryRepository()
			idCalls, modelCalls, retrievalCalls := 0, 0, 0
			checkModelRequest := func(request customerservice.ReplyRequest) {
				t.Helper()
				modelCalls++
				if request.ConversationID != conversationID || request.TriggerMessageID != "message-1" ||
					len(request.History) != 1 || request.History[0].Content != customerText ||
					request.Knowledge == nil || len(request.Knowledge.Snippets) != 1 {
					t.Fatalf("unexpected model request: %+v", request)
				}
			}
			service, err := customerservice.New(
				repository,
				customerservice.MessageIDGeneratorFunc(func() (string, error) {
					idCalls++
					return fmt.Sprintf("message-%d", idCalls), nil
				}),
				customerservice.ClockFunc(func() time.Time {
					return time.Date(2026, time.September, 11, 1, 0, 0, 0, time.UTC)
				}),
				customerservice.WithKnowledgeRetriever(customerservice.KnowledgeRetrieverFunc(func(
					_ context.Context, query customerservice.KnowledgeQuery,
				) ([]customerservice.KnowledgeSnippet, error) {
					retrievalCalls++
					if query.Text != customerText {
						t.Fatalf("query=%+v", query)
					}
					return []customerservice.KnowledgeSnippet{{
						ID: "help-entry", Title: "帮助入口", Source: "synthetic/help",
						Content: "虚构测试资料：帮助中心入口在应用首页。",
					}}, nil
				})),
				customerservice.WithReplyGenerator(customerservice.ReplyGeneratorFunc(func(
					_ context.Context, request customerservice.ReplyRequest,
				) (customerservice.Reply, error) {
					checkModelRequest(request)
					return customerservice.Reply{Content: replyText}, nil
				})),
				customerservice.WithReplyStreamGenerator(customerservice.ReplyStreamGeneratorFunc(func(
					_ context.Context, request customerservice.ReplyRequest,
				) (kitstream.Reader[customerservice.ReplyDelta], error) {
					checkModelRequest(request)
					return kitstream.FromSlice([]customerservice.ReplyDelta{{Content: replyText}}), nil
				})),
			)
			if err != nil {
				t.Fatal(err)
			}
			engine := newTestEngine(t, service)
			receivePath := "/v1/conversations/" + conversationID + "/messages"

			// The server commits, but the adapter has no acknowledgement data.
			assertStatus(t, performJSON(engine, http.MethodPost, receivePath, payload), http.StatusCreated)
			recoveredReceive := performJSON(engine, http.MethodPost, receivePath, payload)
			assertStatus(t, recoveredReceive, http.StatusOK)
			var received model.ReceiveMessageResponse
			decodeResponse(t, recoveredReceive, &received)
			if !received.Duplicate || received.NextAction != string(customerservice.NextActionNone) ||
				received.Status != string(conversation.StatusBotActive) || received.MessageID != "message-1" {
				t.Fatalf("recovered receive=%+v", received)
			}
			messages, err := repository.ListMessages(context.Background(), conversationID)
			if err != nil || len(messages) != 1 || modelCalls != 0 || retrievalCalls != 0 {
				t.Fatalf("duplicate receive does not imply a reply: messages=%v model=%d retrieval=%d err=%v", messages, modelCalls, retrievalCalls, err)
			}

			// A still-pending adapter task explicitly recovers using the returned
			// customer message ID, even though duplicate Receive said "none".
			replyPath := receivePath + "/" + received.MessageID + "/reply"
			// Discard the acknowledgement (or completed event) client-side.
			if firstMode == "stream" {
				_ = performIntegrationRecoverySSE(t, engine, replyPath+"/stream", false)
			} else {
				assertStatus(t, performAuthorizedRequest(engine, http.MethodPost, replyPath, nil), http.StatusCreated)
			}
			recoveredReply := performAuthorizedRequest(engine, http.MethodPost, replyPath, nil)
			assertStatus(t, recoveredReply, http.StatusOK)
			var reply model.GenerateBotReplyResponse
			decodeResponse(t, recoveredReply, &reply)
			if !reply.Duplicate || reply.ConversationID != conversationID ||
				reply.TriggerMessageID != received.MessageID || reply.MessageID != "message-2" || reply.Content != replyText {
				t.Fatalf("recovered reply=%+v", reply)
			}
			// The same saved result is recoverable through SSE without deltas,
			// regardless of which mode originally generated it.
			streamed := performIntegrationRecoverySSE(t, engine, replyPath+"/stream", true)
			if streamed.MessageID != reply.MessageID || streamed.Content != reply.Content ||
				streamed.ConversationID != conversationID || streamed.TriggerMessageID != received.MessageID {
				t.Fatalf("SSE recovery differs from saved reply: %+v", streamed)
			}
			messages, err = repository.ListMessages(context.Background(), conversationID)
			if err != nil || len(messages) != 2 || messages[1].Sender() != conversation.SenderBot ||
				messages[1].ID() != reply.MessageID || messages[1].ReplyToMessageID() != received.MessageID ||
				messages[1].Content() != reply.Content {
				t.Fatalf("persisted messages=%v err=%v", messages, err)
			}
			if idCalls != 2 || modelCalls != 1 || retrievalCalls != 1 {
				t.Fatalf("recovery added calls: ids=%d model=%d retrieval=%d", idCalls, modelCalls, retrievalCalls)
			}
		})
	}
}

// ut.PerformRequest has no connection-backed writer for successful SSE. Supply
// a memory ExtWriter, but keep the real router, middleware and SSE encoder.
// This intentionally does not test TCP flushing or network disconnection.
func performIntegrationRecoverySSE(t *testing.T, engine *route.Engine, target string, duplicate bool) model.ReplyCompletedEventData {
	t.Helper()
	request := engine.NewContext()
	request.Request.SetMethod(http.MethodPost)
	request.Request.SetRequestURI(target)
	request.Request.Header.Set("Authorization", "Bearer "+testAPIAuthToken)
	writer := &integrationSSEWriter{}
	request.Response.HijackWriter(writer)
	engine.ServeHTTP(context.Background(), request)
	if request.Response.StatusCode() != http.StatusOK || !writer.finalized ||
		!strings.HasPrefix(string(request.Response.Header.ContentType()), "text/event-stream") {
		t.Fatalf("SSE status=%d finalized=%t content-type=%s", request.Response.StatusCode(), writer.finalized, request.Response.Header.ContentType())
	}
	var completed model.ReplyCompletedEventData
	completedCount, deltaCount := 0, 0
	for _, frame := range strings.Split(strings.TrimSuffix(writer.String(), "\n\n"), "\n\n") {
		lines := strings.SplitN(frame, "\n", 2)
		if len(lines) != 2 || !strings.HasPrefix(lines[1], "data: ") || completedCount != 0 {
			t.Fatalf("invalid or post-terminal SSE frame: %q", frame)
		}
		switch lines[0] {
		case "event: reply.delta":
			deltaCount++
		case "event: reply.completed":
			completedCount++
			if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[1], "data: ")), &completed); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("unexpected SSE event: %q", frame)
		}
	}
	if completedCount != 1 || completed.Duplicate != duplicate ||
		(duplicate && deltaCount != 0) || (!duplicate && deltaCount != 1) {
		t.Fatalf("completed=%d deltas=%d duplicate=%t, want duplicate=%t", completedCount, deltaCount, completed.Duplicate, duplicate)
	}
	return completed
}

type integrationSSEWriter struct {
	bytes.Buffer
	finalized bool
}

func (*integrationSSEWriter) Flush() error { return nil }

func (w *integrationSSEWriter) Finalize() error {
	w.finalized = true
	return nil
}
