package hertz

import (
	"context"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"net/http"
	"strings"
	"testing"
)

type deliveryApplication struct {
	receiverStub
	calls  int
	denied bool
}

func (a *deliveryApplication) CheckDeliveryEligibility(_ context.Context, q customerservice.DeliveryCheck) (customerservice.DeliveryEligibility, error) {
	a.calls++
	if q.ConversationID != "c" || q.TriggerMessageID != "t" || q.CustomerID != "u" || q.Channel != "web" || q.ReplyMessageID != "r" || q.Content != "answer" {
		return customerservice.DeliveryEligibility{}, customerservice.ErrInvalidInput
	}
	if a.denied {
		return customerservice.DeliveryEligibility{Reason: "superseded"}, nil
	}
	return customerservice.DeliveryEligibility{Eligible: true, Reason: "eligible"}, nil
}
func TestDeliveryEligibilityHTTPBoundary(t *testing.T) {
	application := &deliveryApplication{}
	engine := newTestEngine(t, application)
	path := "/v1/conversations/c/messages/t/reply/eligibility"
	payload := `{"conversation_id":"spoof","trigger_message_id":"spoof","customer_id":"u","channel":"web","reply_message_id":"r","content":"answer"}`
	unauth := ut.PerformRequest(engine, http.MethodPost, path, &ut.Body{Body: strings.NewReader(payload), Len: len(payload)}).Result()
	assertStatus(t, unauth, http.StatusUnauthorized)
	if application.calls != 0 {
		t.Fatal("unauthorized check")
	}
	for _, body := range []string{`{`, `{}`, `{"customer_id":"u"}`} {
		assertStatus(t, performJSON(engine, http.MethodPost, path, body), http.StatusBadRequest)
	}
	response := performJSON(engine, http.MethodPost, path, payload)
	assertStatus(t, response, http.StatusOK)
	if !strings.Contains(string(response.Body()), `"eligible":true`) || string(response.Header.Peek("Cache-Control")) != "no-store" {
		t.Fatal(string(response.Body()))
	}
	application.denied = true
	response = performJSON(engine, http.MethodPost, path, payload)
	assertStatus(t, response, http.StatusOK)
	if !strings.Contains(string(response.Body()), `"reason":"superseded"`) {
		t.Fatal(string(response.Body()))
	}
}
