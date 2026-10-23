package webchat

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"sync/atomic"
	"testing"
)

func TestReceiptReplyRecoveryEligibility(t *testing.T) {
	var calls atomic.Int32
	fail := true
	f := newFixture(t, Config{Agents: testAgents}, func(context.Context, customerservice.ReplyRequest) (customerservice.Reply, error) {
		calls.Add(1)
		if fail {
			return customerservice.Reply{}, errors.New("injected")
		}
		return customerservice.Reply{Content: "recovered"}, nil
	})
	s := mint(t, f, 0, "reload-customer")
	body := func(event string) map[string]string {
		return map[string]string{"event_id": event, "content": "question"}
	}
	query := func(event string, allowed bool, status string) {
		t.Helper()
		before := calls.Load()
		w := call(f.g, "POST", MessageReceiptPath, s.AccessToken, body(event))
		expect(t, w, 200)
		var r struct {
			Accepted bool   `json:"accepted"`
			Retry    bool   `json:"retry_allowed"`
			Status   string `json:"reply_status"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		if !r.Accepted || r.Retry != allowed || r.Status != status || calls.Load() != before {
			t.Fatalf("receipt=%+v calls before=%d after=%d", r, before, calls.Load())
		}
	}
	expect(t, call(f.g, "POST", MessagesPath, s.AccessToken, body("first")), 503)
	query("first", true, "not_replied")
	// Another customer turn supersedes this trigger even though neither has a reply.
	expect(t, call(f.g, "POST", MessagesPath, s.AccessToken, body("second")), 503)
	query("first", false, "unavailable")
	query("second", true, "not_replied")
	fail = false
	send(t, f, s, "second", "question")
	query("second", false, "replied")
	fail = true
	expect(t, call(f.g, "POST", MessagesPath, s.AccessToken, body("third")), 503)
	query("third", true, "not_replied")
	expect(t, call(f.g, "POST", "/chat/v1/handoff", s.AccessToken, map[string]any{}), 200)
	query("third", false, "unavailable")
	row := deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "history"}, 200)
	row = deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "claim", "revision": row.Revision}, 200)
	query("third", false, "unavailable")
	deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "close", "revision": row.Revision}, 200)
	query("third", false, "unavailable")
}
