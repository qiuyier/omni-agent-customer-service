package webchat

import (
	"context"
	"fmt"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"strings"
	"testing"
	"time"
)

func TestDeskHistoryPaginationAndUnread(t *testing.T) {
	f := newFixture(t, Config{Agents: testAgents}, nil)
	s := mint(t, f, 0, "long-history")
	expect(t, call(f.g, "POST", "/chat/v1/handoff", s.AccessToken, map[string]any{}), 200)
	ids := []string{}
	if err := f.repo.WithinTx(context.Background(), func(tx conversation.Transaction) error {
		c, err := tx.FindConversation(s.ConversationID)
		if err != nil {
			return err
		}
		for i := 0; i < 237; i++ {
			id := fmt.Sprintf("message-%03d", 300-i)
			ids = append(ids, id)
			m, err := conversation.NewMessage(conversation.MessageParams{ID: id, ConversationID: c.ID(), ExternalMessageID: id, Sender: conversation.SenderCustomer, SenderID: "customer", Content: fmt.Sprintf("message %d", i), CreatedAt: time.Now()})
			if err != nil {
				return err
			}
			if err = tx.InsertMessage(c.Channel(), m); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	page := func(key, value string) deskState {
		body := map[string]any{"conversation_id": s.ConversationID, "action": "history"}
		if key != "" {
			body[key] = value
		}
		return deskAction(t, f, 0, body, 200)
	}
	latest := page("", "")
	if len(latest.Messages) != 100 || latest.Messages[0].ID != ids[137] || !latest.HasOlder || latest.HasNewer || latest.FirstUnreadID != ids[0] || latest.Unread != 237 {
		t.Fatal("invalid latest page", latest)
	}
	previous := page("before_message_id", latest.Messages[0].ID)
	oldest := page("before_message_id", previous.Messages[0].ID)
	if len(previous.Messages) != 100 || len(oldest.Messages) != 37 || oldest.HasOlder {
		t.Fatal("backwards page boundaries")
	}
	all := append(append(oldest.Messages, previous.Messages...), latest.Messages...)
	for i, m := range all {
		if m.ID != ids[i] {
			t.Fatalf("lost order %d: %s", i, m.ID)
		}
	}
	forward := page("after_message_id", ids[36])
	if forward.Messages[0].ID != ids[37] || len(forward.Messages) != 100 || !forward.HasNewer {
		t.Fatal("forward skipped messages")
	}
	unread := page("from_message_id", latest.FirstUnreadID)
	if unread.Messages[0].ID != ids[0] || unread.Unread != 237 {
		t.Fatal("unread lookup marked or skipped")
	}
	empty := page("after_message_id", ids[236])
	if len(empty.Messages) != 0 || empty.HasNewer {
		t.Fatal("tail should be empty")
	}
	deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "history", "before_message_id": ids[10], "after_message_id": ids[20]}, 400)
	deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "history", "from_message_id": "foreign"}, 400)
	deskAction(t, f, 2, map[string]any{"conversation_id": s.ConversationID, "action": "history", "before_message_id": ids[10]}, 403)
	deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "read", "message_ids": ids[:100]}, 200)
	if page("", "").FirstUnreadID != ids[100] {
		t.Fatal("unread cursor did not advance")
	}
	if page("", "").Unread != 137 {
		t.Fatal("page fetch cleared unseen messages")
	}
}
func TestDeskHistoryByteBudgetAndOversizedProgress(t *testing.T) {
	all := []conversation.Message{}
	for i := 0; i < 40; i++ {
		m, err := conversation.NewMessage(conversation.MessageParams{ID: fmt.Sprint(i), ConversationID: "c", ExternalMessageID: fmt.Sprint(i), Sender: conversation.SenderCustomer, SenderID: "u", Content: strings.Repeat("界", 1300), CreatedAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, m)
	}
	var a, b deskState
	if err := deskHistory(all, "", "", "", &a); err != nil {
		t.Fatal(err)
	}
	if !a.HasOlder || len(a.Messages) != 33 {
		t.Fatal("byte budget ignored")
	}
	if err := deskHistory(all, a.Messages[0].ID, "", "", &b); err != nil {
		t.Fatal(err)
	}
	if len(a.Messages)+len(b.Messages) != 40 {
		t.Fatal("byte limited pages lost messages")
	}
	oversized, err := conversation.NewMessage(conversation.MessageParams{ID: "large", ConversationID: "c", Sender: conversation.SenderCustomer, SenderID: "u", Content: strings.Repeat("界", 50000), CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	all = append(all, oversized)
	var tail deskState
	if err = deskHistory(all, "", "", "", &tail); err != nil {
		t.Fatal(err)
	}
	if len(tail.Messages) != 1 || !tail.Messages[0].ContentTruncated || len(tail.Messages[0].Content) > 128<<10 || !tail.HasOlder {
		t.Fatal("oversized message stalls history")
	}
}
