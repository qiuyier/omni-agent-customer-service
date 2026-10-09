package webchat

import (
	"context"
	"encoding/json"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"strings"
	"sync"
	"testing"
)

var testAgents = []Agent{{SourceID: "host-a", ID: "agent-a", Secret: strings.Repeat("c", 40)}, {SourceID: "host-a", ID: "agent-b", Secret: strings.Repeat("d", 40)}, {SourceID: "host-b", ID: "agent-c", Secret: strings.Repeat("e", 40)}}

func deskAction(t *testing.T, f *fixture, agent int, body map[string]any, status int) deskState {
	t.Helper()
	w := call(f.g, "POST", "/internal/chat/desk/action", testAgents[agent].Secret, body)
	expect(t, w, status)
	var result deskState
	if status == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
	}
	return result
}
func TestHumanLoopAndAuthorization(t *testing.T) {
	f := newFixture(t, Config{Agents: testAgents}, nil)
	s := mint(t, f, 0, "customer")
	other := mint(t, f, 1, "customer")
	expect(t, call(f.g, "GET", "/internal/chat/desk/queue", s.AccessToken, nil), 401)
	expect(t, call(f.g, "POST", "/chat/v1/handoff", s.AccessToken, map[string]string{"conversation_id": other.ConversationID}), 400)
	expect(t, call(f.g, "POST", "/chat/v1/handoff", s.AccessToken, map[string]any{}), 200)
	expect(t, call(f.g, "POST", "/chat/v1/handoff", s.AccessToken, map[string]any{}), 200)
	if got := send(t, f, s, "waiting-event", "waiting message"); got.Outcome != "waiting_human" {
		t.Fatal(got)
	}
	if f.models.Load() != 0 {
		t.Fatal("model invoked while waiting")
	}
	row := deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "history"}, 200)
	deskAction(t, f, 2, map[string]any{"conversation_id": s.ConversationID, "action": "history"}, 403)
	deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "reply", "event_id": "early", "content": "no"}, 403)
	deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "claim", "revision": row.Revision - 1}, 409)
	claimed := deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "claim", "revision": row.Revision}, 200)
	for _, action := range []string{"history", "claim", "close", "reply"} {
		deskAction(t, f, 1, map[string]any{"conversation_id": s.ConversationID, "action": action, "revision": claimed.Revision, "event_id": "attack", "content": "no"}, 403)
	}
	expect(t, call(f.g, "POST", "/internal/chat/desk/action", testAgents[0].Secret, map[string]any{"conversation_id": s.ConversationID, "action": "claim", "agent_id": "injected"}), 400)
	body := map[string]any{"conversation_id": s.ConversationID, "action": "reply", "event_id": "agent-event", "content": "human answer"}
	deskAction(t, f, 0, body, 200)
	deskAction(t, f, 0, body, 200)
	body["content"] = "changed"
	deskAction(t, f, 0, body, 409)
	if got := send(t, f, s, "human-event", "human question"); got.Outcome != "waiting_human" {
		t.Fatal(got)
	}
	var history History
	w := call(f.g, "GET", MessagesPath, s.AccessToken, nil)
	expect(t, w, 200)
	json.Unmarshal(w.Body.Bytes(), &history)
	if len(history.Messages) != 3 || history.Messages[1].Role != conversation.SenderHumanAgent || f.models.Load() != 0 {
		t.Fatal("human message missing or model replied")
	}
	deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "close", "revision": claimed.Revision}, 200)
	deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "close", "revision": claimed.Revision}, 200)
	expect(t, call(f.g, "POST", MessagesPath, s.AccessToken, map[string]string{"event_id": "after-close", "content": "blocked"}), 409)
	expect(t, call(f.g, "POST", "/chat/v1/handoff", s.AccessToken, map[string]any{}), 409)
	rows, err := f.repo.ListHumanQueue(context.Background(), identify("host-a", "customer").channel, mapped("agent", "host-a", "agent-a"))
	if err != nil || len(rows) != 0 {
		t.Fatal("closed remains in queue")
	}
}
func TestHandoffSuppressesInFlightBotAndCompetingClaims(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	f := newFixture(t, Config{Agents: testAgents}, func(ctx context.Context, _ customerservice.ReplyRequest) (customerservice.Reply, error) {
		close(entered)
		select {
		case <-release:
			return customerservice.Reply{Content: "late bot"}, nil
		case <-ctx.Done():
			return customerservice.Reply{}, ctx.Err()
		}
	})
	s := mint(t, f, 0, "race-customer")
	done := make(chan SendResult, 1)
	go func() {
		w := call(f.g, "POST", MessagesPath, s.AccessToken, map[string]string{"event_id": "slow", "content": "slow reply"})
		var result SendResult
		json.Unmarshal(w.Body.Bytes(), &result)
		done <- result
	}()
	<-entered
	expect(t, call(f.g, "POST", "/chat/v1/handoff", s.AccessToken, map[string]any{}), 200)
	row := deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "history"}, 200)
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			codes <- call(f.g, "POST", "/internal/chat/desk/action", testAgents[i].Secret, map[string]any{"conversation_id": s.ConversationID, "action": "claim", "revision": row.Revision}).Code
		}(i)
	}
	a, b := <-codes, <-codes
	if !((a == 200 && b == 403) || (a == 403 && b == 200)) {
		t.Fatalf("claim race: %d %d", a, b)
	}
	once.Do(func() { close(release) })
	if result := <-done; result.Outcome != "superseded" {
		t.Fatal(result)
	}
	messages, err := f.repo.ListMessages(context.Background(), s.ConversationID)
	if err != nil || len(messages) != 1 {
		t.Fatal("late bot persisted")
	}
}
func TestHumanDefaultOffAndConfiguration(t *testing.T) {
	f := newFixture(t, Config{}, nil)
	s := mint(t, f, 0, "customer")
	expect(t, call(f.g, "POST", "/chat/v1/handoff", s.AccessToken, map[string]any{}), 404)
	for _, agents := range [][]Agent{{{SourceID: "host-a", ID: "agent", Secret: testHosts[0].Secret}}, {{SourceID: "missing", ID: "agent", Secret: strings.Repeat("z", 40)}}, {testAgents[0], testAgents[0]}} {
		if ValidateAgentConfiguration(agents, testHosts) == nil {
			t.Fatal("unsafe configuration accepted")
		}
	}
}
