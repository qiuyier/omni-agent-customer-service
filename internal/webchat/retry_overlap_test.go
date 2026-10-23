package webchat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

// Channels establish the ordering, rather than relying on a slow sleep:
// receive -> model entered -> handoff/claim/reply/close -> model completes.
func TestRetryAcrossHumanLifecycle(t *testing.T) {
	for _, phase := range []string{"waiting", "active", "closed"} {
		t.Run(phase, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var enterOnce, releaseOnce sync.Once
			var attempts atomic.Int32
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			f := newFixture(t, Config{Agents: testAgents, RequestTimeout: 5 * time.Second}, func(ctx context.Context, _ customerservice.ReplyRequest) (customerservice.Reply, error) {
				if attempts.Add(1) == 1 {
					return customerservice.Reply{}, errors.New("injected first failure")
				}
				enterOnce.Do(func() { close(entered) })
				select {
				case <-release:
					return customerservice.Reply{Content: "late bot must never appear"}, nil
				case <-ctx.Done():
					return customerservice.Reply{}, ctx.Err()
				}
			})
			s := mint(t, f, 0, "overlap-customer")
			body := map[string]string{"event_id": "slow", "content": "original question"}
			expect(t, call(f.g, "POST", MessagesPath, s.AccessToken, body), 503)
			assertRetryReceipt(t, f.g, s.AccessToken, body, true, "not_replied")
			other := mint(t, f, 0, "overlap-customer")
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				done <- call(f.g, "POST", MessagesPath, s.AccessToken, map[string]string{"event_id": "slow", "content": "original question"})
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("model did not start")
			}
			expect(t, call(f.g, "POST", MessagesPath, other.AccessToken, body), 429)
			expect(t, call(f.g, "POST", "/chat/v1/handoff", s.AccessToken, map[string]any{}), 200)
			row := deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "history"}, 200)
			if len(row.Messages) != 1 || row.Messages[0].Content != "original question" {
				t.Fatalf("customer message unavailable while generation pending: %+v", row)
			}
			wantStatus := conversation.StatusWaitingHuman
			wantMessages := 1
			if phase != "waiting" {
				row = deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "claim", "revision": row.Revision}, 200)
				deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "reply", "event_id": "human", "content": "human answer before model completes"}, 200)
				wantStatus = conversation.StatusHumanActive
				wantMessages++
			}
			// Same-conversation sends remain serialized while the first request
			// is pending; handoff and staff actions must still be available.
			expect(t, call(f.g, "POST", MessagesPath, s.AccessToken, map[string]string{"event_id": "followup", "content": "not yet accepted"}), 429)
			if phase == "closed" {
				deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "close", "revision": row.Revision}, 200)
				wantStatus = conversation.StatusClosed
			}
			assertRetryReceipt(t, f.g, s.AccessToken, body, false, "unavailable")
			// Reading history during generation must neither block nor release
			// the write exclusion held by the original send.
			before := history(t, f, s)
			expect(t, call(f.g, "POST", MessagesPath, s.AccessToken, map[string]string{"event_id": "still-busy", "content": "blocked"}), 429)
			if before.Status != wantStatus || len(before.Messages) != wantMessages {
				t.Fatalf("unexpected state before model completes: %+v", before)
			}
			unblock()
			var response *httptest.ResponseRecorder
			select {
			case response = <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("late completion did not return")
			}
			expect(t, response, 200)
			var result SendResult
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Outcome != "superseded" || result.MessageID != before.Messages[0].ID || result.ReplyMessageID != "" {
				t.Fatalf("late reply not suppressed: %+v", result)
			}
			// A lost HTTP receipt must not cause a new model call or duplicate message.
			replay := send(t, f, s, "slow", "original question")
			wantOutcome := "waiting_human"
			if phase == "closed" {
				wantOutcome = "superseded"
			}
			if replay.Outcome != wantOutcome || replay.MessageID != result.MessageID || replay.ReplyMessageID != "" {
				t.Fatalf("unsafe replay: %+v", replay)
			}
			// Re-establish identity and reload the history, as a refreshed page does.
			renewed := mint(t, f, 0, "overlap-customer")
			if renewed.ConversationID != s.ConversationID {
				t.Fatal("refresh changed conversation")
			}
			after := history(t, f, renewed)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("late reply/replay changed history: before=%+v after=%+v", before, after)
			}
			for _, message := range after.Messages {
				if message.Role == conversation.SenderBot {
					t.Fatalf("late bot leaked: %+v", message)
				}
			}
			if f.models.Load() != 2 {
				t.Fatalf("unexpected model attempts: %d", f.models.Load())
			}
			if phase == "closed" {
				expect(t, call(f.g, "POST", MessagesPath, renewed.AccessToken, map[string]string{"event_id": "after-close", "content": "blocked"}), 409)
			} else {
				// The pending model did not break the next human reply.
				if phase == "waiting" {
					row = deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "claim", "revision": row.Revision}, 200)
				}
				deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "reply", "event_id": "human-after", "content": "human answer after model completes"}, 200)
				final := history(t, f, renewed)
				if len(final.Messages) != wantMessages+1 || final.Messages[len(final.Messages)-1].Role != conversation.SenderHumanAgent {
					t.Fatalf("human continuation failed: %+v", final)
				}
			}
		})
	}
}
