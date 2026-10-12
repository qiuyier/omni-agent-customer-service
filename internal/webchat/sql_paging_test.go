package webchat

import (
	"context"
	"errors"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"testing"
)

type pagingTestRepository struct {
	*conversation.MemoryRepository
	pageCalls, summaryCalls int
	inspected               []string
}

func (r *pagingTestRepository) WithinTx(ctx context.Context, fn func(conversation.Transaction) error) error {
	return r.MemoryRepository.WithinTx(ctx, func(tx conversation.Transaction) error { return fn(&pagingTestTransaction{Transaction: tx, owner: r}) })
}
func (r *pagingTestRepository) ReadDeskAttention(ctx context.Context, id, channel, agent string, ids []string) (conversation.Attention, error) {
	r.summaryCalls++
	r.inspected = append([]string{}, ids...)
	a, err := r.MemoryRepository.DeskAttention(ctx, id, channel, agent, nil)
	if err != nil {
		return a, err
	}
	bounded := map[string]bool{}
	for _, mid := range ids {
		if a.Seen[mid] {
			bounded[mid] = true
		}
	}
	a.Seen = bounded
	return a, nil
}

type pagingTestTransaction struct {
	conversation.Transaction
	owner *pagingTestRepository
}

func (tx *pagingTestTransaction) ListMessages(string) ([]conversation.Message, error) {
	return nil, errors.New("gateway must not request full history when pager exists")
}
func (tx *pagingTestTransaction) PageMessages(id, before, after, from string) (conversation.HistoryPage, error) {
	tx.owner.pageCalls++
	all, err := tx.Transaction.ListMessages(id)
	if err != nil {
		return conversation.HistoryPage{}, err
	}
	var dto deskState
	if err = deskHistory(all, before, after, from, &dto); err != nil {
		return conversation.HistoryPage{}, err
	}
	byID := map[string]conversation.Message{}
	for _, m := range all {
		byID[m.ID()] = m
	}
	out := conversation.HistoryPage{HasOlder: dto.HasOlder, HasNewer: dto.HasNewer}
	for _, m := range dto.Messages {
		out.Messages = append(out.Messages, byID[m.ID])
	}
	return out, nil
}
func TestDeskGatewayUsesBoundedRepositoryCapabilities(t *testing.T) {
	f := newFixture(t, Config{Agents: testAgents}, nil)
	s := mint(t, f, 0, "bounded")
	expect(t, call(f.g, "POST", "/chat/v1/handoff", s.AccessToken, map[string]any{}), 200)
	msg := send(t, f, s, "one", "hello")
	r := &pagingTestRepository{MemoryRepository: f.repo}
	f.g.repo = r
	history := deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "history"}, 200)
	if len(history.Messages) != 1 || r.pageCalls != 1 || r.summaryCalls != 1 || len(r.inspected) != 1 || r.inspected[0] != msg.MessageID {
		t.Fatal("bounded page/summary not used")
	}
	expect(t, call(f.g, "GET", "/internal/chat/desk/queue", testAgents[0].Secret, nil), 200)
	if len(r.inspected) != 0 || r.pageCalls != 1 {
		t.Fatal("queue fetched message receipts")
	}
	deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "history", "after_message_id": "foreign"}, 400)
}
