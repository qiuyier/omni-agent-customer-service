package integration

import (
	"context"
	"encoding/json"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPDeliveryEligibility(t *testing.T) {
	for _, body := range []string{`{"eligible":true,"reason":"eligible"}`, `{"eligible":false,"reason":"superseded"}`, `{}`, `{"eligible":true,"reason":"superseded"}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.EscapedPath() != "/v1/conversations/c%2F1/messages/t%2F1/reply/eligibility" || r.Header.Get("Authorization") != "Bearer token" {
					t.Error("incorrect check request")
				}
				var payload map[string]string
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if payload["customer_id"] != "u" || payload["channel"] != "web" || payload["reply_message_id"] != "r" || payload["content"] != "answer" {
					t.Error(payload)
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			c, err := NewHTTPClient(HTTPConfig{BaseURL: server.URL, BearerToken: "token", AllowInsecureHTTP: true})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			result, err := c.CheckDeliveryEligibility(context.Background(), customerservice.DeliveryCheck{ConversationID: "c/1", TriggerMessageID: "t/1", CustomerID: "u", Channel: "web", ReplyMessageID: "r", Content: "answer"})
			valid := body == `{"eligible":true,"reason":"eligible"}` || body == `{"eligible":false,"reason":"superseded"}`
			if valid != (err == nil) {
				t.Fatalf("%+v %v", result, err)
			}
		})
	}
}
