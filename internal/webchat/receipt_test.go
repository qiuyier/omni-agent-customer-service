package webchat

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestMessageReceiptIsScopedReadOnlyAndWorksAfterClosure(t *testing.T) {
	f := newFixture(t, Config{Agents: testAgents}, nil)
	s := mint(t, f, 0, "receipt-user")
	other := mint(t, f, 0, "other-user")
	host := mint(t, f, 1, "receipt-user")
	body := map[string]string{"event_id": "event-1", "content": " original content "}
	query := func(token string, input any, status int) map[string]any {
		t.Helper()
		w := call(f.g, "POST", MessageReceiptPath, token, input)
		expect(t, w, status)
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if query(s.AccessToken, body, 200)["accepted"] != false {
		t.Fatal("unknown event accepted")
	}
	expect(t, call(f.g, "POST", "/chat/v1/handoff", s.AccessToken, map[string]any{}), 200)
	sent := send(t, f, s, "event-1", body["content"])
	before := history(t, f, s)
	got := query(s.AccessToken, body, 200)
	if got["accepted"] != true || got["message_id"] != sent.MessageID || got["conversation_id"] != s.ConversationID || got["event_id"] != "event-1" {
		t.Fatal(got)
	}
	for _, token := range []string{other.AccessToken, host.AccessToken} {
		r := query(token, body, 200)
		if r["accepted"] != false || r["message_id"] != nil {
			t.Fatal("foreign receipt exposed")
		}
	}
	if query(s.AccessToken, map[string]string{"event_id": "different", "content": body["content"]}, 200)["accepted"] != false {
		t.Fatal("matched text instead of event")
	}
	query(s.AccessToken, map[string]string{"event_id": "event-1", "content": "changed"}, 409)
	query(s.AccessToken, map[string]string{"event_id": "event-1", "content": body["content"], "conversation_id": other.ConversationID}, 400)
	query("", body, 401)
	expect(t, call(f.g, "GET", MessageReceiptPath, s.AccessToken, nil), 405)
	if !reflect.DeepEqual(before, history(t, f, s)) || f.models.Load() != 0 {
		t.Fatal("receipt mutated state or invoked model")
	}
	row := deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "history"}, 200)
	row = deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "claim", "revision": row.Revision}, 200)
	deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "close", "revision": row.Revision}, 200)
	closed := history(t, f, s)
	if query(s.AccessToken, body, 200)["accepted"] != true || !reflect.DeepEqual(closed, history(t, f, s)) {
		t.Fatal("closed receipt failed or reopened conversation")
	}
}
