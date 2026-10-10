package webchat

import (
	"context"
	"encoding/json"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"sync"
	"testing"
	"time"
)

func closeRound(t *testing.T, f *fixture, id string) {
	t.Helper()
	if err := f.repo.WithinTx(context.Background(), func(tx conversation.Transaction) error {
		c, err := tx.FindConversation(id)
		if err != nil {
			return err
		}
		if err = c.Close(time.Now().Add(time.Second)); err != nil {
			return err
		}
		return tx.UpdateConversation(c)
	}); err != nil {
		t.Fatal(err)
	}
}
func nextRoundID(t *testing.T, f *fixture, s SessionResponse) string {
	t.Helper()
	w := call(f.g, "POST", NextRoundPath, s.AccessToken, `{}`)
	expect(t, w, 200)
	var result struct {
		ID string `json:"conversation_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.ID
}
func TestConsultationRoundsIsolationAndRestart(t *testing.T) {
	f := newFixture(t, Config{}, nil)
	old := mint(t, f, 0, "customer")
	expect(t, call(f.g, "POST", NextRoundPath, old.AccessToken, `{}`), 404)
	send(t, f, old, "same-event", "old history")
	expect(t, call(f.g, "POST", NextRoundPath, old.AccessToken, `{}`), 409)
	closeRound(t, f, old.ConversationID)
	next := nextRoundID(t, f, old)
	if next == old.ConversationID || nextRoundID(t, f, old) != next {
		t.Fatal("non-idempotent successor")
	}
	current := mint(t, f, 0, "customer")
	if current.ConversationID != next {
		t.Fatal("exchange did not select latest")
	}
	c, err := f.repo.FindConversation(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status() != conversation.StatusBotActive || c.AgentID() != "" || len(history(t, f, current).Messages) != 0 {
		t.Fatal("new round inherited old state")
	}
	expect(t, call(f.g, "POST", MessagesPath, old.AccessToken, map[string]string{"event_id": "new-old-event", "content": "must not migrate"}), 409)
	if result := send(t, f, old, "same-event", "old history"); result.Outcome != "superseded" {
		t.Fatal(result)
	}
	send(t, f, current, "same-event", "fresh history")
	if len(history(t, f, old).Messages) != 2 || len(history(t, f, current).Messages) != 2 {
		t.Fatal("mixed history")
	}
	previous := call(f.g, "GET", PreviousRoundPath, current.AccessToken, nil)
	expect(t, previous, 200)
	var data struct {
		History History `json:"history"`
	}
	if err = json.Unmarshal(previous.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data.History.ConversationID != old.ConversationID || data.History.Status != conversation.StatusClosed || len(data.History.Messages) != 2 {
		t.Fatal(data)
	}
	for _, s := range []SessionResponse{mint(t, f, 1, "customer"), mint(t, f, 0, "other")} {
		if s.ConversationID == next {
			t.Fatal("cross-owner round")
		}
		result := call(f.g, "GET", PreviousRoundPath, s.AccessToken, nil)
		expect(t, result, 200)
		if result.Body.String() != "{\"history\":null}\n" {
			t.Fatal(result.Body.String())
		}
	}
	closeRound(t, f, next)
	third := nextRoundID(t, f, current)
	if nextRoundID(t, f, old) != next {
		t.Fatal("stale retry advanced chain")
	}
	restarted, err := New(f.repo, f.g.app, Config{Hosts: testHosts})
	if err != nil {
		t.Fatal(err)
	}
	f.g = restarted
	if mint(t, f, 0, "customer").ConversationID != third {
		t.Fatal("restart lost latest round")
	}
	if f.models.Load() != 2 {
		t.Fatalf("unexpected generation %d", f.models.Load())
	}
}
func TestConcurrentSuccessorAcrossGateways(t *testing.T) {
	f := newFixture(t, Config{}, nil)
	s := mint(t, f, 0, "customer")
	send(t, f, s, "event", "hello")
	closeRound(t, f, s.ConversationID)
	second, err := New(f.repo, f.g.app, Config{Hosts: testHosts})
	if err != nil {
		t.Fatal(err)
	}
	other := &fixture{g: second, repo: f.repo}
	s2 := mint(t, other, 0, "customer")
	var wg sync.WaitGroup
	ids := make([]string, 2)
	for i, x := range []struct {
		f *fixture
		s SessionResponse
	}{{f, s}, {other, s2}} {
		wg.Add(1)
		go func(i int, x struct {
			f *fixture
			s SessionResponse
		}) { defer wg.Done(); ids[i] = nextRoundID(t, x.f, x.s) }(i, x)
	}
	wg.Wait()
	if ids[0] == "" || ids[0] != ids[1] {
		t.Fatal(ids)
	}
}
func TestRoundRoutesRejectArbitraryIdentity(t *testing.T) {
	f := newFixture(t, Config{}, nil)
	s := mint(t, f, 0, "customer")
	expect(t, call(f.g, "POST", NextRoundPath, "", `{}`), 401)
	expect(t, call(f.g, "POST", NextRoundPath, s.AccessToken, `{"conversation_id":"other"}`), 400)
	expect(t, call(f.g, "GET", NextRoundPath, s.AccessToken, nil), 405)
	expect(t, call(f.g, "GET", PreviousRoundPath+"?conversation_id=other", s.AccessToken, nil), 400)
	expect(t, call(f.g, "POST", PreviousRoundPath, s.AccessToken, `{}`), 405)
}
