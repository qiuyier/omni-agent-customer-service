package webchat

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func assertRetryReceipt(t *testing.T, handler http.Handler, token string, body any, allowed bool, status string) {
	t.Helper()
	w := call(handler, "POST", MessageReceiptPath, token, body)
	expect(t, w, 200)
	var r struct {
		Accepted bool   `json:"accepted"`
		Allowed  bool   `json:"retry_allowed"`
		Status   string `json:"reply_status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if !r.Accepted || r.Allowed != allowed || r.Status != status {
		t.Fatalf("receipt=%+v", r)
	}
}

func TestRetrySnapshotDoesNotAuthorizeAfterHandoff(t *testing.T) {
	for _, phase := range []string{"waiting", "active", "closed"} {
		t.Run(phase, func(t *testing.T) {
			f := newFixture(t, Config{Agents: testAgents}, func(context.Context, customerservice.ReplyRequest) (customerservice.Reply, error) {
				return customerservice.Reply{}, errors.New("injected")
			})
			s := mint(t, f, 0, "snapshot-customer")
			body := map[string]string{"event_id": "failed", "content": "question"}
			expect(t, call(f.g, "POST", MessagesPath, s.AccessToken, body), 503)
			assertRetryReceipt(t, f.g, s.AccessToken, body, true, "not_replied")
			expect(t, call(f.g, "POST", "/chat/v1/handoff", s.AccessToken, map[string]any{}), 200)
			row := deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "history"}, 200)
			if phase != "waiting" {
				row = deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "claim", "revision": row.Revision}, 200)
			}
			if phase == "closed" {
				deskAction(t, f, 0, map[string]any{"conversation_id": s.ConversationID, "action": "close", "revision": row.Revision}, 200)
			}
			result := send(t, f, s, "failed", "question")
			if result.ReplyMessageID != "" || f.models.Load() != 1 {
				t.Fatalf("stale eligibility generated: %+v calls=%d", result, f.models.Load())
			}
			assertRetryReceipt(t, f.g, s.AccessToken, body, false, "unavailable")
			if len(history(t, f, s).Messages) != 1 {
				t.Fatal("retry duplicated customer message")
			}
		})
	}
}

func TestTwoSessionsRetrySameFailedEvent(t *testing.T) {
	var attempts atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	f := newFixture(t, Config{RequestTimeout: 5 * time.Second}, func(ctx context.Context, _ customerservice.ReplyRequest) (customerservice.Reply, error) {
		if attempts.Add(1) == 1 {
			return customerservice.Reply{}, errors.New("injected")
		}
		close(entered)
		select {
		case <-release:
			return customerservice.Reply{Content: "recovered"}, nil
		case <-ctx.Done():
			return customerservice.Reply{}, ctx.Err()
		}
	})
	s := mint(t, f, 0, "two-tabs")
	other := mint(t, f, 0, "two-tabs")
	body := map[string]string{"event_id": "failed", "content": "question"}
	expect(t, call(f.g, "POST", MessagesPath, s.AccessToken, body), 503)
	assertRetryReceipt(t, f.g, s.AccessToken, body, true, "not_replied")
	assertRetryReceipt(t, f.g, other.AccessToken, body, true, "not_replied")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call(f.g, "POST", MessagesPath, s.AccessToken, body) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("retry not entered")
	}
	expect(t, call(f.g, "POST", MessagesPath, other.AccessToken, body), 429)
	unblock()
	select {
	case w := <-done:
		expect(t, w, 200)
	case <-time.After(3 * time.Second):
		t.Fatal("retry did not finish")
	}
	assertRetryReceipt(t, f.g, other.AccessToken, body, false, "replied")
	replay := send(t, f, other, "failed", "question")
	if replay.ReplyMessageID == "" || attempts.Load() != 2 || len(history(t, f, s).Messages) != 2 {
		t.Fatalf("duplicate retry: %+v calls=%d", replay, attempts.Load())
	}
}
