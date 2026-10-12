package webchat

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDeskReadReceipts(t *testing.T) {
	f := newFixture(t, Config{Agents: testAgents}, nil)
	s := mint(t, f, 0, "read-customer")
	expect(t, call(f.g, "POST", "/chat/v1/handoff", s.AccessToken, map[string]any{}), 200)
	first := send(t, f, s, "first", "第一条")
	history := func(agent int) deskState {
		return deskAction(t, f, agent, map[string]any{"conversation_id": s.ConversationID, "action": "history"}, 200)
	}
	read := func(agent int, ids []string, status int) {
		deskAction(t, f, agent, map[string]any{"conversation_id": s.ConversationID, "action": "read", "message_ids": ids}, status)
	}
	initial := history(0)
	if !initial.AttentionEnabled || initial.Unread != 1 || len(initial.UnreadIDs) != 1 {
		t.Fatal(initial)
	}
	if history(0).Unread != 1 {
		t.Fatal("history implicitly marked read")
	}
	second := send(t, f, s, "second", strings.Repeat("新", 180))
	read(0, []string{first.MessageID}, 200)
	if h := history(0); h.Unread != 1 || len(h.UnreadIDs) != 1 || h.UnreadIDs[0] != second.MessageID || h.Revision != initial.Revision {
		t.Fatal(h)
	}
	if history(1).Unread != 2 {
		t.Fatal("agent receipt leaked")
	}
	read(0, []string{first.MessageID}, 200)
	read(2, []string{second.MessageID}, 403)
	other := mint(t, f, 0, "another-customer")
	expect(t, call(f.g, "POST", "/chat/v1/handoff", other.AccessToken, map[string]any{}), 200)
	foreign := send(t, f, other, "foreign", "另一位客户")
	read(0, []string{second.MessageID, foreign.MessageID}, 400)
	if history(0).Unread != 1 {
		t.Fatal("invalid batch partially applied")
	}
	read(0, nil, 400)
	w := call(f.g, "GET", "/internal/chat/desk/queue", testAgents[0].Secret, nil)
	expect(t, w, 200)
	var q struct {
		Items []deskState `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &q); err != nil {
		t.Fatal(err)
	}
	for _, row := range q.Items {
		if row.ID == s.ConversationID && (row.Unread != 1 || row.Latest == nil || !row.Latest.ContentTruncated || len([]rune(row.Latest.Content)) != 160) {
			t.Fatal(row)
		}
	}
	claimed := deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "claim", "revision": initial.Revision}, 200)
	read(1, []string{second.MessageID}, 403)
	deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "reply", "event_id": "reply", "content": "人工回复"}, 200)
	h := history(0)
	read(0, []string{h.Messages[len(h.Messages)-1].ID}, 400)
	read(0, []string{second.MessageID}, 200)
	if history(0).Unread != 0 {
		t.Fatal("exact acknowledgement failed")
	}
	deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "close", "revision": claimed.Revision}, 200)
	read(0, []string{first.MessageID}, 200)
}
