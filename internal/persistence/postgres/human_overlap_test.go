package postgres_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/persistence/postgres"
	"github.com/qiuyier/omni-agent-customer-service/internal/webchat"
)

// A real HTTP server and PostgreSQL pool, with only the model replaced.
// Handoff must finish while generation is still blocked on release.
func TestDatabaseDelayedBotAcrossHumanLifecycle(t *testing.T) {
	for _, phase := range []string{"waiting", "active", "closed"} {
		t.Run(phase, func(t *testing.T) {
			repo := openTestRepository(t, true)
			entered, release := make(chan struct{}), make(chan struct{})
			var enterOnce, releaseOnce sync.Once
			var calls atomic.Int32
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			generate := customerservice.ReplyGeneratorFunc(func(ctx context.Context, _ customerservice.ReplyRequest) (customerservice.Reply, error) {
				calls.Add(1)
				enterOnce.Do(func() { close(entered) })
				select {
				case <-release:
					return customerservice.Reply{Content: "late bot"}, nil
				case <-ctx.Done():
					return customerservice.Reply{}, ctx.Err()
				}
			})
			start := func(repository *postgres.Repository) *deskConcurrentLab {
				t.Helper()
				app, err := customerservice.New(repository, customerservice.MessageIDGeneratorFunc(func() (string, error) { return uuid.NewString(), nil }), customerservice.ClockFunc(time.Now), customerservice.WithReplyGenerator(generate))
				if err != nil {
					t.Fatal(err)
				}
				l := &deskConcurrentLab{repo: repository, hosts: []webchat.Host{{SourceID: "overlap", Secret: strings.Repeat("h", 40)}}, agents: []webchat.Agent{{SourceID: "overlap", ID: "staff", Secret: strings.Repeat("a", 40)}}}
				gateway, err := webchat.New(repository, app, webchat.Config{Hosts: l.hosts, Agents: l.agents, RequestTimeout: 10 * time.Second})
				if err != nil {
					t.Fatal(err)
				}
				l.server = httptest.NewServer(gateway)
				l.server.Client().Timeout = 12 * time.Second
				t.Cleanup(l.server.Close)
				return l
			}
			l := start(repo)
			s := l.session(t, 0, "customer")
			body := map[string]string{"event_id": "slow", "content": "original question"}
			done := make(chan deskHTTPResult, 1)
			go func() { done <- l.request("POST", webchat.MessagesPath, s.AccessToken, body) }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("model did not start")
			}
			// Bound every transition independently so a database lock held across
			// generation cannot be mistaken for successful handoff after a timeout.
			quick := func(action func() deskHTTPResult, out any) {
				t.Helper()
				result := make(chan deskHTTPResult, 1)
				go func() { result <- action() }()
				select {
				case r := <-result:
					requireDeskHTTP(t, r, 200, out)
				case <-time.After(3 * time.Second):
					t.Fatal("human action blocked while model pending")
				}
			}
			quick(func() deskHTTPResult { return l.request("POST", "/chat/v1/handoff", s.AccessToken, map[string]any{}) }, nil)
			var row concurrentDeskRow
			quick(func() deskHTTPResult { return l.action(0, s.ConversationID, "history", nil) }, &row)
			wantStatus := conversation.StatusWaitingHuman
			wantCount := 1
			if phase != "waiting" {
				quick(func() deskHTTPResult {
					return l.action(0, s.ConversationID, "claim", map[string]any{"revision": row.Revision})
				}, &row)
				quick(func() deskHTTPResult {
					return l.action(0, s.ConversationID, "reply", map[string]any{"event_id": "human", "content": "human answer"})
				}, nil)
				wantStatus = conversation.StatusHumanActive
				wantCount++
			}
			if phase == "closed" {
				quick(func() deskHTTPResult {
					return l.action(0, s.ConversationID, "close", map[string]any{"revision": row.Revision})
				}, nil)
				wantStatus = conversation.StatusClosed
			}
			// Independent pool reads committed state while the model is still blocked.
			independent := openTestRepository(t, false)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			before, err := independent.ListMessages(ctx, s.ConversationID)
			cancel()
			if err != nil || len(before) != wantCount {
				t.Fatalf("committed messages=%d err=%v", len(before), err)
			}
			if before[0].Content() != "original question" {
				t.Fatal("customer message lost")
			}
			unblock()
			var result webchat.SendResult
			select {
			case r := <-done:
				requireDeskHTTP(t, r, 200, &result)
			case <-time.After(3 * time.Second):
				t.Fatal("model completion blocked")
			}
			if result.Outcome != "superseded" || result.ReplyMessageID != "" || result.MessageID != before[0].ID() {
				t.Fatalf("late receipt: %+v", result)
			}
			var history webchat.History
			requireDeskHTTP(t, l.request("GET", webchat.MessagesPath, s.AccessToken, nil), 200, &history)
			if history.Status != wantStatus || len(history.Messages) != wantCount {
				t.Fatalf("history: %+v", history)
			}
			for _, m := range history.Messages {
				if m.Role == conversation.SenderBot {
					t.Fatal("late bot persisted")
				}
			}
			// Tear down the original HTTP gateway and pool, then reopen against the
			// same database. No process-local idempotency or status can survive this.
			l.server.Close()
			repo.Close()
			independent.Close()
			reopened := openTestRepository(t, false)
			fresh := start(reopened)
			renewed := fresh.session(t, 0, "customer")
			if renewed.ConversationID != s.ConversationID {
				t.Fatal("reconnect changed conversation")
			}
			var loaded webchat.History
			requireDeskHTTP(t, fresh.request("GET", webchat.MessagesPath, renewed.AccessToken, nil), 200, &loaded)
			if !reflect.DeepEqual(history, loaded) {
				t.Fatalf("reconnect changed history: %+v", loaded)
			}
			var replay webchat.SendResult
			requireDeskHTTP(t, fresh.request("POST", webchat.MessagesPath, renewed.AccessToken, body), 200, &replay)
			wantOutcome := "waiting_human"
			if phase == "closed" {
				wantOutcome = "superseded"
			}
			if replay.Outcome != wantOutcome || replay.MessageID != result.MessageID || replay.ReplyMessageID != "" {
				t.Fatalf("unsafe replay: %+v", replay)
			}
			requireDeskHTTP(t, fresh.request("GET", webchat.MessagesPath, renewed.AccessToken, nil), 200, &loaded)
			if !reflect.DeepEqual(history, loaded) {
				t.Fatal("replay changed persisted history")
			}
			if phase == "closed" {
				requireDeskHTTP(t, fresh.request("POST", webchat.MessagesPath, renewed.AccessToken, map[string]string{"event_id": "new", "content": "blocked"}), 409, nil)
			} else {
				if phase == "waiting" {
					requireDeskHTTP(t, fresh.action(0, s.ConversationID, "claim", map[string]any{"revision": row.Revision}), 200, nil)
				}
				requireDeskHTTP(t, fresh.action(0, s.ConversationID, "reply", map[string]any{"event_id": "after-reconnect", "content": "human continuation"}), 200, nil)
				requireDeskHTTP(t, fresh.request("GET", webchat.MessagesPath, renewed.AccessToken, nil), 200, &loaded)
				if len(loaded.Messages) != wantCount+1 || loaded.Messages[wantCount].Role != conversation.SenderHumanAgent {
					t.Fatal("human continuation missing")
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("unexpected simulated model calls: %d", calls.Load())
			}
			t.Log(fmt.Sprintf("%s: handoff unblocked; late reply suppressed; reconnect/replay durable; model calls=%d", phase, calls.Load()))
		})
	}
}
