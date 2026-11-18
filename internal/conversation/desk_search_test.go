package conversation_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"strings"
	"testing"
	"time"
)

func TestDeskSearchContract(t *testing.T) {
	r := conversation.NewMemoryRepository()
	ctx := context.Background()
	now := time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC)
	seed := func(id, channel, agent string, status conversation.Status, keyword string, sender conversation.Sender) {
		t.Helper()
		c, err := conversation.Restore(conversation.RestoreParams{ID: id, CustomerID: "customer-" + id, Channel: channel, AgentID: agent, Status: status, Revision: 1, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		m, err := conversation.NewMessage(conversation.MessageParams{ID: "m-" + id, ConversationID: id, Sender: sender, SenderID: "fixture", Content: keyword, CreatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		if err = r.WithinTx(ctx, func(tx conversation.Transaction) error {
			if e := tx.InsertConversation(c); e != nil {
				return e
			}
			return tx.InsertMessage(channel, m)
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 25; i++ {
		seed(fmt.Sprintf("own-%02d", i), "one", "agent", conversation.StatusClosed, "虚构 Needle 50%_ literal", conversation.SenderCustomer)
	}
	seed("waiting", "one", "", conversation.StatusWaitingHuman, "Needle", conversation.SenderCustomer)
	seed("other-agent", "one", "other", conversation.StatusClosed, "Needle", conversation.SenderCustomer)
	seed("other-source", "two", "agent", conversation.StatusClosed, "Needle", conversation.SenderCustomer)
	seed("private-system", "one", "agent", conversation.StatusClosed, "HiddenInternal", conversation.SenderSystem)
	seed("private-tool", "one", "agent", conversation.StatusClosed, "HiddenInternal", conversation.SenderTool)
	seed("bot-only", "one", "", conversation.StatusBotActive, "Needle", conversation.SenderCustomer)
	q := conversation.DeskSearchQuery{Keyword: "Needle"}
	p, err := r.SearchDesk(ctx, "one", "agent", q)
	if err != nil || len(p.Items) != 20 || p.Next != "own-19" {
		t.Fatalf("page=%+v err=%v", p, err)
	}
	q.After = p.Next
	p, err = r.SearchDesk(ctx, "one", "agent", q)
	if err != nil || len(p.Items) != 6 || p.Next != "" {
		t.Fatalf("page2=%+v err=%v", p, err)
	}
	for _, tc := range []struct {
		q    conversation.DeskSearchQuery
		want int
	}{
		{conversation.DeskSearchQuery{Keyword: "50%_", ID: "own-2", Status: "closed"}, 5},
		{conversation.DeskSearchQuery{Keyword: "%"}, 20},
		{conversation.DeskSearchQuery{Keyword: "_ literal", ID: "own-24"}, 1},
		{conversation.DeskSearchQuery{Keyword: "needle"}, 0},
		{conversation.DeskSearchQuery{Keyword: "HiddenInternal"}, 0},
		{conversation.DeskSearchQuery{Status: "waiting_human"}, 1},
		{conversation.DeskSearchQuery{ID: "other-source"}, 0},
		{conversation.DeskSearchQuery{ID: "other-agent"}, 0},
		{conversation.DeskSearchQuery{Status: "human_active"}, 0},
	} {
		p, e := r.SearchDesk(ctx, "one", "agent", tc.q)
		if e != nil || len(p.Items) != tc.want {
			t.Fatalf("query=%+v page=%+v err=%v", tc.q, p, e)
		}
	}
	for _, q := range []conversation.DeskSearchQuery{{Keyword: strings.Repeat("a", 257)}, {ID: strings.Repeat("b", 129)}, {After: strings.Repeat("c", 129)}, {Status: "bot_active"}, {Keyword: "a\x00b"}} {
		if _, e := r.SearchDesk(ctx, "one", "agent", q); !errors.Is(e, conversation.ErrInvalidArgument) {
			t.Fatalf("invalid accepted: %v", e)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e := r.SearchDesk(canceled, "one", "agent", conversation.DeskSearchQuery{}); e == nil {
		t.Fatal("canceled search succeeded")
	}
}
