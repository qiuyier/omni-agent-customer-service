package webchat

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"strings"
	"testing"
)

type batchQueueTestRepository struct {
	*conversation.MemoryRepository
	calls          int
	channel, agent string
	err            error
}

func (*batchQueueTestRepository) ListHumanQueue(context.Context, string, string) ([]*conversation.Conversation, error) {
	return nil, errors.New("legacy queue must not run")
}
func (*batchQueueTestRepository) DeskAttention(context.Context, string, string, string, []string) (conversation.Attention, error) {
	return conversation.Attention{}, errors.New("per-conversation attention must not run")
}
func (r *batchQueueTestRepository) ReadDeskQueue(ctx context.Context, channel, agent string) ([]conversation.DeskQueueItem, error) {
	r.calls++
	r.channel = channel
	r.agent = agent
	if r.err != nil {
		return nil, r.err
	}
	rows, err := r.MemoryRepository.ListHumanQueue(ctx, channel, agent)
	if err != nil {
		return nil, err
	}
	out := []conversation.DeskQueueItem{}
	for _, c := range rows {
		a, err := r.MemoryRepository.DeskAttention(ctx, c.ID(), channel, agent, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, conversation.DeskQueueItem{Conversation: c, Attention: a})
	}
	return out, nil
}
func TestDeskGatewayBatchQueue(t *testing.T) {
	f := newFixture(t, Config{Agents: testAgents}, nil)
	s := mint(t, f, 0, "queue")
	expect(t, call(f.g, "POST", "/chat/v1/handoff", s.AccessToken, map[string]any{}), 200)
	message := send(t, f, s, "queue-message", strings.Repeat("界", 200))
	r := &batchQueueTestRepository{MemoryRepository: f.repo}
	f.g.repo = r
	w := call(f.g, "GET", "/internal/chat/desk/queue", testAgents[0].Secret, nil)
	expect(t, w, 200)
	var body struct {
		Items []deskState
		Limit int
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if r.calls != 1 || r.channel != mapped("channel", "host-a") || r.agent != mapped("agent", "host-a", "agent-a") {
		t.Fatal("untrusted scope")
	}
	if len(body.Items) != 1 || body.Limit != 100 {
		t.Fatal("invalid queue")
	}
	row := body.Items[0]
	if row.ID != s.ConversationID || !row.AttentionEnabled || row.Unread != 1 || row.FirstUnreadID != message.MessageID || row.Latest == nil || !row.Latest.ContentTruncated || len([]rune(row.Latest.Content)) != 160 || len(row.Messages) != 0 {
		t.Fatal("invalid summary", row)
	}
	w = call(f.g, "GET", "/internal/chat/desk/queue", testAgents[2].Secret, nil)
	expect(t, w, 200)
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Items) != 0 {
		t.Fatal("empty queue")
	}
	r.err = errors.New("query failed")
	expect(t, call(f.g, "GET", "/internal/chat/desk/queue", testAgents[0].Secret, nil), 503)
	before := r.calls
	expect(t, call(f.g, "GET", "/internal/chat/desk/queue", "wrong-secret", nil), 401)
	if r.calls != before {
		t.Fatal("unauthorized batch executed")
	}
}
