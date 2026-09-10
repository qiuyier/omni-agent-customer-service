package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestAcceptanceHTTPMessageAndGenerate(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer local-test-token" {
			t.Error("request method or bearer authentication is wrong")
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		switch r.URL.Path {
		case "/v1/conversations/test-conversation/messages":
			if r.Header.Get("Content-Type") != "application/json" {
				t.Error("customer message is not JSON")
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			want := map[string]string{"customer_id": "acceptance-customer", "channel": "web", "external_message_id": "external-one", "content": "怎么找回密码？"}
			if !reflect.DeepEqual(body, want) {
				t.Errorf("request body = %#v, want %#v", body, want)
			}
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"conversation_id":"test-conversation","message_id":"trigger-one","duplicate":false,"status":"bot_active","next_action":"generate_bot_reply"}`)
		case "/v1/conversations/test-conversation/messages/trigger-one/reply":
			body, _ := io.ReadAll(r.Body)
			if len(body) != 0 {
				t.Error("reply endpoint should receive no request body")
			}
			duplicate := requests == 3
			if !duplicate {
				w.WriteHeader(http.StatusCreated)
			}
			json.NewEncoder(w).Encode(testReplyPayload(duplicate))
		default:
			t.Error("unexpected route")
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	ctx := context.Background()
	received, err := receiveMessage(ctx, server.Client(), server.URL, "local-test-token", "test-conversation", "external-one", "怎么找回密码？")
	if err != nil || received.MessageID != "trigger-one" || received.Duplicate {
		t.Fatalf("received = %#v, err = %v", received, err)
	}
	first, err := requestReply(ctx, server.Client(), server.URL, "local-test-token", "generate", received.ConversationID, received.MessageID)
	if err != nil || first.Status != http.StatusCreated || first.Reply.Duplicate {
		t.Fatalf("first = %#v, err = %v", first, err)
	}
	duplicate, err := requestReply(ctx, server.Client(), server.URL, "local-test-token", "generate", received.ConversationID, received.MessageID)
	if err != nil || duplicate.Status != http.StatusOK || !duplicate.Reply.Duplicate || duplicate.Reply.Content != first.Reply.Content || duplicate.Reply.MessageID != first.Reply.MessageID {
		t.Fatalf("duplicate = %#v, err = %v", duplicate, err)
	}
}

func TestAcceptanceHTTPStreamAndCompletedOnlyReplay(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprintf("duplicate_%t", duplicate), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/conversations/test-conversation/messages/trigger-one/reply/stream" || r.Header.Get("Accept") != "text/event-stream" {
					t.Error("invalid stream request")
				}
				w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
				io.WriteString(w, ": keepalive\r\n\r\n")
				if !duplicate {
					io.WriteString(w, "event: reply.delta\r\ndata: {\"delta\":\"请使用\"}\r\n\r\n")
					w.(http.Flusher).Flush()
					io.WriteString(w, "event: reply.delta\ndata: {\"delta\":\"注册邮箱。\"}\n\n")
				}
				io.WriteString(w, testCompletedEvent(duplicate))
			}))
			defer server.Close()
			result, err := requestReply(context.Background(), server.Client(), server.URL, "local-test-token", "stream", "test-conversation", "trigger-one")
			if err != nil || result.Status != http.StatusOK || result.Reply.Duplicate != duplicate || result.TotalMS < 0 {
				t.Fatalf("result = %#v, err = %v", result, err)
			}
			if duplicate {
				if result.DeltaCount != 0 || result.FirstDeltaMS != nil || result.DeltaText != "" || !reflect.DeepEqual(result.Events, []string{"reply.completed"}) {
					t.Fatalf("replay generated unexpected events: %#v", result)
				}
			} else if result.DeltaCount != 2 || result.DeltaText != result.Reply.Content || result.FirstDeltaMS == nil || *result.FirstDeltaMS < 0 || *result.FirstDeltaMS > result.TotalMS || !reflect.DeepEqual(result.Events, []string{"reply.delta", "reply.delta", "reply.completed"}) {
				t.Fatalf("stream events or timing are wrong: %#v", result)
			}
		})
	}
}

func TestAcceptanceHTTPRejectsBrokenStreamContracts(t *testing.T) {
	fullDelta := "event: reply.delta\ndata: {\"delta\":\"请使用注册邮箱。\"}\n\n"
	tests := []struct {
		name string
		body string
	}{
		{"missing_completed", fullDelta},
		{"duplicate_completed", testCompletedEvent(true) + testCompletedEvent(true)},
		{"delta_after_completed", testCompletedEvent(true) + fullDelta},
		{"error_after_delta", fullDelta + "event: error\ndata: {\"message\":\"private-provider-data\"}\n\n"},
		{"superseded", "event: reply.superseded\ndata: {}\n\n"},
		{"unknown_event", "event: private-provider-data\ndata: {}\n\n"},
		{"malformed_json", "event: reply.delta\ndata: not-json-private-provider-data\n\n"},
		{"mismatched_content", "event: reply.delta\ndata: {\"delta\":\"different\"}\n\n" + testCompletedEvent(false)},
		{"nonduplicate_completed_only", testCompletedEvent(false)},
		{"duplicate_with_delta", fullDelta + testCompletedEvent(true)},
		{"wrong_trigger", fullDelta + strings.ReplaceAll(testCompletedEvent(false), "trigger-one", "different-trigger")},
		{"unterminated_event", strings.TrimSuffix(testCompletedEvent(true), "\n")},
		{"oversized_event", "data: " + strings.Repeat("x", maxReplyResponseBytes) + "\n\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, test.body)
			}))
			defer server.Close()
			_, err := requestReply(context.Background(), server.Client(), server.URL, "local-test-token", "stream", "test-conversation", "trigger-one")
			if err == nil {
				t.Fatal("accepted a broken SSE response")
			}
			if strings.Contains(err.Error(), "private-provider-data") || strings.Contains(err.Error(), server.URL) || strings.Contains(err.Error(), "local-test-token") {
				t.Fatal("error exposed response or request data")
			}
		})
	}
}

func TestAcceptanceHTTPRejectsInvalidResponseAndPreservesCancellation(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		status      int
		contentType string
		body        string
	}{
		{"provider_failure", "generate", http.StatusServiceUnavailable, "application/problem+json", "private-provider-data"},
		{"sse_problem_before_first_event", "stream", http.StatusGatewayTimeout, "application/problem+json", "private-provider-data"},
		{"wrong_media_type", "stream", http.StatusOK, "application/json", "{}"},
		{"wrong_duplicate_status", "generate", http.StatusOK, "application/json", testReplyJSON(false)},
		{"oversized_json", "generate", http.StatusCreated, "application/json", strings.Repeat("x", maxReplyResponseBytes+1)},
		{"extra_json_value", "generate", http.StatusCreated, "application/json", testReplyJSON(false) + "{}"},
		{"wrong_reply_identity", "generate", http.StatusCreated, "application/json", strings.ReplaceAll(testReplyJSON(false), "trigger-one", "other-trigger")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				w.WriteHeader(test.status)
				io.WriteString(w, test.body)
			}))
			defer server.Close()
			_, err := requestReply(context.Background(), server.Client(), server.URL, "local-test-token", test.mode, "test-conversation", "trigger-one")
			if err == nil || strings.Contains(err.Error(), "private-provider-data") || strings.Contains(err.Error(), server.URL) || strings.Contains(err.Error(), "local-test-token") {
				t.Fatalf("unsafe or missing error: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := requestReply(ctx, http.DefaultClient, "http://127.0.0.1:1", "local-test-token", "generate", "test-conversation", "trigger-one")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request error = %v", err)
	}
}

func testReplyPayload(duplicate bool) replyPayload {
	return replyPayload{ConversationID: "test-conversation", TriggerMessageID: "trigger-one", MessageID: "reply-one", Content: "请使用注册邮箱。", Duplicate: duplicate}
}

func testReplyJSON(duplicate bool) string {
	data, _ := json.Marshal(testReplyPayload(duplicate))
	return string(data)
}

func testCompletedEvent(duplicate bool) string {
	return "event: reply.completed\ndata: " + testReplyJSON(duplicate) + "\n\n"
}
