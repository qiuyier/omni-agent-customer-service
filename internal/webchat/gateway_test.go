package webchat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

var testHosts = []Host{{"host-a", strings.Repeat("a", 40)}, {"host-b", strings.Repeat("b", 40)}}

type fixture struct {
	g      *Gateway
	repo   *conversation.MemoryRepository
	models atomic.Int32
}

func newFixture(t *testing.T, config Config, generate customerservice.ReplyGeneratorFunc) *fixture {
	t.Helper()
	f := &fixture{repo: conversation.NewMemoryRepository()}
	var ids atomic.Int32
	app, err := customerservice.New(f.repo, customerservice.MessageIDGeneratorFunc(func() (string, error) { return fmt.Sprintf("m-%d", ids.Add(1)), nil }), customerservice.ClockFunc(time.Now), customerservice.WithReplyGenerator(customerservice.ReplyGeneratorFunc(func(ctx context.Context, request customerservice.ReplyRequest) (customerservice.Reply, error) {
		f.models.Add(1)
		if generate != nil {
			return generate(ctx, request)
		}
		return customerservice.Reply{Content: "测试回复"}, nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	if config.Hosts == nil {
		config.Hosts = testHosts
	}
	f.g, err = New(f.repo, app, config)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func call(handler http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	var data []byte
	if raw, ok := body.(string); ok {
		data = []byte(raw)
	} else if body != nil {
		data, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}
func expect(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("HTTP %d want %d: %s", w.Code, status, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("response must not be cached")
	}
}
func mint(t *testing.T, f *fixture, host int, customer string) SessionResponse {
	t.Helper()
	w := call(f.g, "POST", SessionPath, testHosts[host].Secret, map[string]string{"customer_id": customer})
	expect(t, w, 201)
	var s SessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if s.AccessToken == "" || s.ConversationID == "" {
		t.Fatal("missing capability")
	}
	return s
}
func send(t *testing.T, f *fixture, s SessionResponse, event, content string) SendResult {
	t.Helper()
	w := call(f.g, "POST", MessagesPath, s.AccessToken, map[string]string{"event_id": event, "content": content})
	expect(t, w, 200)
	var result SendResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func history(t *testing.T, f *fixture, s SessionResponse) History {
	t.Helper()
	w := call(f.g, "GET", MessagesPath, s.AccessToken, nil)
	expect(t, w, 200)
	var result History
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestScopeIsolationAndRefresh(t *testing.T) {
	f := newFixture(t, Config{}, nil)
	seen := map[string]bool{}
	for host := 0; host < 2; host++ {
		for _, customer := range []string{"customer-a", "customer-b"} {
			s := mint(t, f, host, customer)
			if seen[s.ConversationID] {
				t.Fatal("identity collision")
			}
			seen[s.ConversationID] = true
			if h := history(t, f, s); len(h.Messages) != 0 {
				t.Fatal("new identity has foreign history")
			}
			content := fmt.Sprintf("host %d / %s", host, customer)
			first := send(t, f, s, "same-event", content)
			if first.Outcome != "replied" {
				t.Fatal(first)
			}
			renewed := mint(t, f, host, customer)
			if renewed.AccessToken == s.AccessToken || renewed.ConversationID != s.ConversationID {
				t.Fatal("unstable scope or reused credential")
			}
			replay := send(t, f, renewed, "same-event", content)
			if replay != first {
				t.Fatalf("replay changed result: %+v vs %+v", first, replay)
			}
			h := history(t, f, renewed)
			if len(h.Messages) != 2 || h.Messages[0].Content != content || h.Messages[1].ReplyToMessageID != first.MessageID {
				t.Fatalf("bad history: %+v", h)
			}
		}
	}
	if f.models.Load() != 4 {
		t.Fatalf("models=%d", f.models.Load())
	}
}

func TestCredentialSeparationAndUntrustedFields(t *testing.T) {
	f := newFixture(t, Config{}, nil)
	s := mint(t, f, 0, "one")
	for _, token := range []string{"", testHosts[0].Secret, "service-level-token", s.AccessToken + "x", s.AccessToken[:20] + strings.Repeat("z", 28)} {
		expect(t, call(f.g, "GET", MessagesPath, token, nil), 401)
	}
	expect(t, call(f.g, "POST", SessionPath, s.AccessToken, `{"customer_id":"another"}`), 401)
	for _, body := range []string{`{"customer_id":"another","source_id":"host-b"}`, `{"customer_id":" another"}`, `null`, `{"customer_id":"x"} {}`, `{"customer_id":"x","customer_id":"y"}`, `{"Customer_ID":"x"}`} {
		expect(t, call(f.g, "POST", SessionPath, testHosts[0].Secret, body), 400)
	}
	for _, body := range []string{`{"event_id":"1","content":"hi","customer_id":"foreign"}`, `{"event_id":"1","content":"hi","role":"bot"}`, `{"event_id":"1","content":"hi","conversation_id":"foreign"}`, `{"event_id":"1","content":"hi","trigger_message_id":"foreign"}`} {
		expect(t, call(f.g, "POST", MessagesPath, s.AccessToken, body), 400)
	}
	for _, path := range []string{"/chat/v1/conversations/foreign/messages", "/v1/conversations/foreign/messages", "/chat/v1/messages/foreign/reply"} {
		expect(t, call(f.g, "POST", path, s.AccessToken, `{}`), 404)
	}
	expect(t, call(f.g, "GET", MessagesPath+"?conversation_id=foreign", s.AccessToken, nil), 400)
	expect(t, call(f.g, "GET", MessagesPath+"?", s.AccessToken, nil), 400)
	r := httptest.NewRequest("GET", MessagesPath, nil)
	r.Header.Add("Authorization", "Bearer "+s.AccessToken)
	r.Header.Add("Authorization", "Bearer "+s.AccessToken)
	w := httptest.NewRecorder()
	f.g.ServeHTTP(w, r)
	expect(t, w, 401)
	if f.models.Load() != 0 {
		t.Fatal("unauthorized operation invoked model")
	}
}

func TestExpiryAndSessionBounds(t *testing.T) {
	f := newFixture(t, Config{MaxSessions: 1, MaxPrincipals: 1}, nil)
	now := time.Now()
	f.g.now = func() time.Time { return now }
	s := mint(t, f, 0, "one")
	expect(t, call(f.g, "POST", SessionPath, testHosts[0].Secret, `{"customer_id":"two"}`), 429)
	now = s.ExpiresAt
	expect(t, call(f.g, "GET", MessagesPath, s.AccessToken, nil), 401)
	// A principal's rate state outlives its last token for one minute.
	now = now.Add(time.Minute)
	_ = mint(t, f, 0, "two")
	if len(f.g.sessions) != 1 || len(f.g.principals) != 1 {
		t.Fatal("expired entries not pruned")
	}
	// Never store bearer plaintext in the session map.
	for key := range f.g.sessions {
		if string(key[:]) == s.AccessToken {
			t.Fatal("plaintext session")
		}
	}
}

func TestRetryAfterGenerationFailureAndEventConflict(t *testing.T) {
	var calls atomic.Int32
	f := newFixture(t, Config{}, func(context.Context, customerservice.ReplyRequest) (customerservice.Reply, error) {
		if calls.Add(1) == 1 {
			return customerservice.Reply{}, errors.New("SECRET provider failure")
		}
		return customerservice.Reply{Content: "recovered"}, nil
	})
	s := mint(t, f, 0, "one")
	w := call(f.g, "POST", MessagesPath, s.AccessToken, `{"event_id":"retry","content":"question"}`)
	expect(t, w, 503)
	if strings.Contains(w.Body.String(), "SECRET") {
		t.Fatal("provider error leaked")
	}
	if len(history(t, f, s).Messages) != 1 {
		t.Fatal("accepted question not preserved")
	}
	result := send(t, f, mint(t, f, 0, "one"), "retry", "question")
	if result.Outcome != "replied" || len(history(t, f, s).Messages) != 2 {
		t.Fatal("failed recovery")
	}
	expect(t, call(f.g, "POST", MessagesPath, s.AccessToken, `{"event_id":"retry","content":"changed"}`), 409)
	if calls.Load() != 2 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestHistoryFiltersInternalMessagesAndChecksOwnership(t *testing.T) {
	f := newFixture(t, Config{}, nil)
	s := mint(t, f, 0, "one")
	send(t, f, s, "first", "<img src=x onerror=alert(1)>")
	claims := identify(testHosts[0].SourceID, "one")
	err := f.repo.WithinTx(context.Background(), func(tx conversation.Transaction) error {
		for i, role := range []conversation.Sender{conversation.SenderSystem, conversation.SenderTool, conversation.SenderHumanAgent} {
			content := "INTERNAL-SENTINEL"
			if role == conversation.SenderHumanAgent {
				content = "human reply"
			}
			m, err := conversation.NewMessage(conversation.MessageParams{ID: fmt.Sprintf("internal-%d", i), ConversationID: claims.conversation, Sender: role, SenderID: "PRIVATE-ACTOR-ID", Content: content, CreatedAt: time.Now()})
			if err != nil {
				return err
			}
			if err := tx.InsertMessage(claims.channel, m); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	w := call(f.g, "GET", MessagesPath, s.AccessToken, nil)
	expect(t, w, 200)
	if strings.Contains(w.Body.String(), "SENTINEL") || strings.Contains(w.Body.String(), "PRIVATE-ACTOR") || strings.Contains(w.Body.String(), "<img") {
		t.Fatal("unsafe history serialization")
	}
	h := history(t, f, s)
	if len(h.Messages) != 3 || h.Messages[0].Content != "<img src=x onerror=alert(1)>" || h.Messages[2].Role != conversation.SenderHumanAgent {
		t.Fatal("public content changed")
	}
	// Even if a misconfigured trusted producer created a conflicting ID, the
	// gateway must not read it or call application code on that user's behalf.
	other := mint(t, f, 0, "collision")
	err = f.repo.WithinTx(context.Background(), func(tx conversation.Transaction) error {
		c, err := conversation.New(conversation.CreateParams{ID: other.ConversationID, CustomerID: "foreign", Channel: "foreign", CreatedAt: time.Now()})
		if err != nil {
			return err
		}
		return tx.InsertConversation(c)
	})
	if err != nil {
		t.Fatal(err)
	}
	expect(t, call(f.g, "GET", MessagesPath, other.AccessToken, nil), 404)
	expect(t, call(f.g, "POST", MessagesPath, other.AccessToken, `{"event_id":"foreign","content":"hi"}`), 404)
	if f.models.Load() != 1 {
		t.Fatal("ownership mismatch invoked model")
	}
}

func TestConcurrentRenewalCannotBypassBusyPrincipal(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	f := newFixture(t, Config{}, func(ctx context.Context, _ customerservice.ReplyRequest) (customerservice.Reply, error) {
		close(entered)
		select {
		case <-release:
			return customerservice.Reply{Content: "done"}, nil
		case <-ctx.Done():
			return customerservice.Reply{}, ctx.Err()
		}
	})
	s := mint(t, f, 0, "one")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- call(f.g, "POST", MessagesPath, s.AccessToken, `{"event_id":"one","content":"question"}`)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("model did not start")
	}
	renewed := mint(t, f, 0, "one")
	expect(t, call(f.g, "POST", MessagesPath, renewed.AccessToken, `{"event_id":"two","content":"other"}`), 429)
	close(release)
	select {
	case w := <-done:
		expect(t, w, 200)
	case <-time.After(time.Second):
		t.Fatal("send did not finish")
	}
	if len(history(t, f, renewed).Messages) != 2 || f.models.Load() != 1 {
		t.Fatal("busy bypass")
	}
}

func TestBodyHistoryAndRateLimits(t *testing.T) {
	f := newFixture(t, Config{}, nil)
	s := mint(t, f, 0, "one")
	expect(t, call(f.g, "POST", MessagesPath, s.AccessToken, map[string]string{"event_id": "long", "content": strings.Repeat("a", MaxContentBytes+1)}), 400)
	expect(t, call(f.g, "POST", MessagesPath, s.AccessToken, strings.Repeat(" ", maxBodyBytes+1)+`{}`), 400)
	send(t, f, s, "good", "hello")
	claims := identify(testHosts[0].SourceID, "one")
	err := f.repo.WithinTx(context.Background(), func(tx conversation.Transaction) error {
		for i := 0; i < 110; i++ {
			m, err := conversation.NewMessage(conversation.MessageParams{ID: fmt.Sprintf("bulk-%d", i), ConversationID: claims.conversation, Sender: conversation.SenderBot, Content: "history", CreatedAt: time.Now()})
			if err != nil {
				return err
			}
			if err := tx.InsertMessage(claims.channel, m); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	h := history(t, f, s)
	if !h.Truncated || len(h.Messages) != 100 || h.Messages[99].ID != "bulk-109" {
		t.Fatal("history bound/order")
	}
	// Rate state is attached to identity, not to a newly issued token.
	renewed := mint(t, f, 0, "one")
	for i := 0; i < 20; i++ {
		_ = call(f.g, "GET", MessagesPath, s.AccessToken, nil)
	}
	expect(t, call(f.g, "GET", MessagesPath, renewed.AccessToken, nil), 429)
}

func TestConfigRejectsAmbiguousHostsAndWeakLimits(t *testing.T) {
	f := newFixture(t, Config{}, nil)
	for _, config := range []Config{
		{}, {Hosts: []Host{{"a", "short"}}}, {Hosts: []Host{testHosts[0], testHosts[0]}},
		{Hosts: []Host{{"same", testHosts[0].Secret}, {"same", testHosts[1].Secret}}},
		{Hosts: testHosts, SessionTTL: time.Hour}, {Hosts: testHosts, MaxSessions: -1},
	} {
		if _, err := New(f.repo, f.g.app, config); err == nil {
			t.Fatalf("accepted invalid config: %+v", config)
		}
	}
	if mapped("conversation", "a:b", "c") == mapped("conversation", "a", "b:c") {
		t.Fatal("tuple collision")
	}
}
