package webchat

import (
	"encoding/json"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"strings"
	"testing"
)

func TestDeskSearchAuthorization(t *testing.T) {
	f := newFixture(t, Config{Agents: testAgents}, nil)
	s := mint(t, f, 0, "search-customer")
	expect(t, call(f.g, "POST", "/chat/v1/handoff", s.AccessToken, map[string]any{}), 200)
	send(t, f, s, "search-event", "只用于搜索的虚构关键词")
	search := func(agent int, q any) conversation.DeskSearchPage {
		w := call(f.g, "POST", "/internal/chat/desk/search", testAgents[agent].Secret, q)
		expect(t, w, 200)
		var p conversation.DeskSearchPage
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if len(search(0, map[string]string{"keyword": "虚构关键词"}).Items) != 1 {
		t.Fatal("waiting not found")
	}
	if len(search(2, map[string]string{}).Items) != 0 {
		t.Fatal("cross-source leak")
	}
	row := deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "history"}, 200)
	row = deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "claim", "revision": row.Revision}, 200)
	if len(search(1, map[string]string{}).Items) != 0 {
		t.Fatal("other-agent leak")
	}
	deskAction(t, f, 1, map[string]any{"conversation_id": s.ConversationID, "action": "history"}, 403)
	deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "close", "revision": row.Revision}, 200)
	p := search(0, map[string]string{"status": "closed", "conversation_id": s.ConversationID})
	if len(p.Items) != 1 || p.Items[0].Status != conversation.StatusClosed {
		t.Fatal("closed not found")
	}
	deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "history"}, 200)
	deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "reply", "event_id": "late", "content": "not allowed"}, 409)
	expect(t, call(f.g, "POST", "/internal/chat/desk/search", s.AccessToken, map[string]any{}), 401)
	expect(t, call(f.g, "GET", "/internal/chat/desk/search", testAgents[0].Secret, nil), 405)
	for _, q := range []any{map[string]string{"agent_id": "injected"}, map[string]string{"source_id": "host-b"}, map[string]string{"keyword": strings.Repeat("a", 257)}, map[string]string{"status": "bot_active"}, `{"keyword":"x","keyword":"y"}`} {
		expect(t, call(f.g, "POST", "/internal/chat/desk/search", testAgents[0].Secret, q), 400)
	}
	if f.models.Load() != 0 {
		t.Fatal("search invoked model")
	}
}
