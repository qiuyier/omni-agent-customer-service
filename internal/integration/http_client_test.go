package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

func TestHTTPClientProtocol(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("Accept") != "application/json" {
			t.Error("invalid request headers")
		}
		if calls == 1 {
			if r.URL.EscapedPath() != "/v1/conversations/c%2F1/messages" {
				t.Errorf("path: %s", r.URL.EscapedPath())
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["customer_id"] != "u" || body["external_message_id"] != "e" || body["content"] != "help" || body["channel"] != "web" {
				t.Error(body)
			}
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"conversation_id":"c/1","message_id":"m","status":"bot_active","next_action":"generate_bot_reply","duplicate":false}`))
		} else {
			if r.URL.EscapedPath() != "/v1/conversations/c%2F1/messages/m%2F1/reply" {
				t.Error(r.URL.EscapedPath())
			}
			_, _ = w.Write([]byte(`{"conversation_id":"c/1","trigger_message_id":"m/1","message_id":"r","content":"hello","duplicate":true}`))
		}
	}))
	defer server.Close()
	c, err := NewHTTPClient(HTTPConfig{BaseURL: server.URL, BearerToken: "test-token", AllowInsecureHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	received, err := c.ReceiveMessage(context.Background(), customerservice.ReceiveMessageCommand{ConversationID: "c/1", CustomerID: "u", ExternalMessageID: "e", Channel: "web", Content: "help"})
	if err != nil || received.MessageID != "m" || received.Duplicate {
		t.Fatalf("%+v %v", received, err)
	}
	reply, err := c.GenerateBotReply(context.Background(), customerservice.GenerateBotReplyCommand{ConversationID: "c/1", TriggerMessageID: "m/1"})
	if err != nil || reply.Content != "hello" || !reply.Duplicate {
		t.Fatalf("%+v %v", reply, err)
	}
}

func TestHTTPClientRejectsInvalidResponsesAndDoesNotFollowRedirects(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("redirect followed") }))
	defer destination.Close()
	for _, tc := range []struct {
		name        string
		status      int
		body, retry string
		code        FailureCode
		retryable   bool
	}{
		{"missing duplicate", 201, `{}`, "", FailureInvalidResponse, false},
		{"wrong duplicate", 200, `{"duplicate":false}`, "", FailureInvalidResponse, false},
		{"oversized", 201, strings.Repeat("x", (512<<10)+1), "", FailureInvalidResponse, false},
		{"conflict", 409, "secret-provider-detail", "", FailureConflict, false},
		{"unauthorized", 401, "secret-provider-detail", "", FailureUnauthorized, false},
		{"busy", 429, "secret-provider-detail", "2", FailureBusy, true},
		{"excessive delay", 503, "secret-provider-detail", "3601", FailureUnavailable, false},
		{"redirect", 307, "secret-provider-detail", "", FailureInvalidResponse, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", destination.URL)
				w.Header().Set("Retry-After", tc.retry)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			c, err := NewHTTPClient(HTTPConfig{BaseURL: server.URL, BearerToken: "test-token", AllowInsecureHTTP: true})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			_, err = c.ReceiveMessage(context.Background(), customerservice.ReceiveMessageCommand{ConversationID: "c"})
			var failure *CallError
			if !errors.As(err, &failure) || failure.Code != tc.code || failure.Retryable != tc.retryable {
				t.Fatalf("%+v", err)
			}
			if tc.retry == "2" && failure.RetryAfter != 2*time.Second {
				t.Fatal(failure)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("raw error leaked")
			}
		})
	}
}

func TestHTTPClientConfigurationAndCancellation(t *testing.T) {
	for _, base := range []string{"http://example.test", "https://user:pass@example.test", "https://example.test?x=1", "https://example.test?", "https://example.test#x", "file:///tmp/test"} {
		if c, err := NewHTTPClient(HTTPConfig{BaseURL: base, BearerToken: "token"}); err == nil {
			c.Close()
			t.Errorf("accepted %s", base)
		}
	}
	c, err := NewHTTPClient(HTTPConfig{BaseURL: "https://example.invalid", BearerToken: "token"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.ReceiveMessage(ctx, customerservice.ReceiveMessageCommand{ConversationID: "c"})
	var failure *CallError
	if !errors.As(err, &failure) || failure.Code != FailureCancelled {
		t.Fatal(err)
	}
}
