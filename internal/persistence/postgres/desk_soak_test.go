package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/webchat"
)

// This opt-in test resets only the same guarded *_test database as the other
// integration tests. Do not run it concurrently with other database tests.
func TestDatabaseDeskMixedSoak(t *testing.T) {
	raw := os.Getenv("DESK_SOAK_DURATION")
	if raw == "" {
		t.Skip("set DESK_SOAK_DURATION=10m (and go test -timeout 15m)")
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration < 10*time.Second || duration > 15*time.Minute {
		t.Fatal("duration must be between 10s and 15m")
	}
	l := newDeskConcurrentLab(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	known := map[string]int{}
	totals := map[string]int{}
	windows := map[string][]float64{}
	cycles, failures := 0, 0
	fail := func(err error) { mu.Lock(); failures++; mu.Unlock(); t.Error(err); cancel() }
	check := func(op string, r deskHTTPResult, want int, out any) error {
		mu.Lock()
		totals[op]++
		windows[op] = append(windows[op], float64(r.elapsed)/float64(time.Millisecond))
		mu.Unlock()
		if r.err != nil || r.status != want {
			return fmt.Errorf("%s HTTP=%d want=%d err=%v body=%s", op, r.status, want, r.err, r.body)
		}
		if out != nil {
			return json.Unmarshal(r.body, out)
		}
		return nil
	}
	// Keep a nontrivial waiting queue present during every mixed read/write cycle.
	for host := 0; host < 2; host++ {
		s := l.session(t, host, "soak-seed")
		requireDeskHTTP(t, l.request("POST", "/chat/v1/handoff", s.AccessToken, map[string]any{}), 200, nil)
		c, err := l.repo.FindConversation(ctx, s.ConversationID)
		if err != nil {
			t.Fatal(err)
		}
		known[s.ConversationID] = host
		prefix := fmt.Sprintf("soak-%d", host)
		_, err = l.pool.Exec(ctx, `INSERT INTO customer_service.conversations(id,customer_id,channel,status,revision,created_at,updated_at)
 SELECT $1||'-'||lpad(n::text,3,'0'),'customer',$2,'waiting_human',2,$3,$3 FROM generate_series(1,90) n`, prefix, c.Channel(), integrationTime)
		if err != nil {
			t.Fatal(err)
		}
		for n := 1; n <= 90; n++ {
			known[fmt.Sprintf("%s-%03d", prefix, n)] = host
		}
	}
	_, err = l.pool.Exec(ctx, `INSERT INTO customer_service.messages(id,conversation_id,channel,sender,sender_id,content,created_at)
 SELECT c.id||'-m-'||n,c.id,c.channel,'customer','customer',c.id||' / '||n,$1
 FROM customer_service.conversations c CROSS JOIN generate_series(1,100) n WHERE c.id LIKE 'soak-%'`, integrationTime)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"conversations", "messages", "desk_read_receipts"} {
		if _, err := l.pool.Exec(ctx, "ANALYZE customer_service."+table); err != nil {
			t.Fatal(err)
		}
	}

	cycle := func(agent, n int) error {
		host := agent / 8
		var s webchat.SessionResponse
		if err := check("session", l.request("POST", webchat.SessionPath, l.hosts[host].Secret, map[string]string{"customer_id": fmt.Sprintf("soak-%d-%d", agent%8, n)}), 201, &s); err != nil {
			return err
		}
		mu.Lock()
		known[s.ConversationID] = host
		mu.Unlock()
		action := func(name string, extra map[string]any, out any) error {
			return check(name, l.action(agent, s.ConversationID, name, extra), 200, out)
		}
		if err := check("handoff", l.request("POST", "/chat/v1/handoff", s.AccessToken, map[string]any{}), 200, nil); err != nil {
			return err
		}
		prefix := fmt.Sprintf("source-%d / cycle-%d / ", host, n)
		send := func(event, content string, out any) error {
			return check("send", l.request("POST", webchat.MessagesPath, s.AccessToken, map[string]string{"event_id": event, "content": content}), 200, out)
		}
		var old, duplicate, fresh webchat.SendResult
		if err := send("old", prefix+"old", &old); err != nil {
			return err
		}
		if err := send("old", prefix+"old", &duplicate); err != nil {
			return err
		}
		if old.MessageID != duplicate.MessageID {
			return fmt.Errorf("customer retry duplicated message")
		}
		var row concurrentDeskRow
		if err := action("history", nil, &row); err != nil {
			return err
		}
		if row.Unread != 1 || row.First != old.MessageID {
			return fmt.Errorf("initial unread mismatch")
		}
		if err := action("claim", map[string]any{"revision": row.Revision}, &row); err != nil {
			return err
		}
		results := deskParallel(
			func() deskHTTPResult {
				return l.action(agent, s.ConversationID, "read", map[string]any{"message_ids": []string{old.MessageID}})
			},
			func() deskHTTPResult {
				return l.request("POST", webchat.MessagesPath, s.AccessToken, map[string]string{"event_id": "new", "content": prefix + "new"})
			},
		)
		if err := check("read", results[0], 200, nil); err != nil {
			return err
		}
		if err := check("send", results[1], 200, &fresh); err != nil {
			return err
		}
		for retry := 0; retry < 2; retry++ {
			if err := action("reply", map[string]any{"event_id": "reply", "content": prefix + "reply"}, nil); err != nil {
				return err
			}
		}
		if err := action("history", nil, &row); err != nil {
			return err
		}
		if row.Unread != 1 || row.First != fresh.MessageID {
			return fmt.Errorf("fresh message incorrectly marked read")
		}
		if err := action("read", map[string]any{"message_ids": []string{fresh.MessageID}}, nil); err != nil {
			return err
		}
		// first_unread_id is omitted when empty; do not retain the prior decoded value.
		row = concurrentDeskRow{}
		if err := action("history", nil, &row); err != nil {
			return err
		}
		if row.Unread != 0 || row.First != "" {
			return fmt.Errorf("acknowledged message still unread")
		}
		foreign := (agent + 8) % 16
		if err := check("foreign_history", l.action(foreign, s.ConversationID, "history", nil), 403, nil); err != nil {
			return err
		}
		// A different agent in the same source must not read a claimed conversation.
		if err := check("other_agent_history", l.action(host*8+6, s.ConversationID, "history", nil), 403, nil); err != nil {
			return err
		}
		messages, err := l.repo.ListMessages(ctx, s.ConversationID)
		if err != nil {
			return err
		}
		if len(messages) != 3 {
			return fmt.Errorf("messages lost/duplicated: %d", len(messages))
		}
		expected := map[string]string{prefix + "old": "customer", prefix + "new": "customer", prefix + "reply": "human_agent"}
		for _, m := range messages {
			role, ok := expected[m.Content()]
			if !ok || string(m.Sender()) != role {
				return fmt.Errorf("message content/role mismatch")
			}
			delete(expected, m.Content())
		}
		if len(expected) != 0 {
			return fmt.Errorf("missing expected message")
		}
		if err := action("close", map[string]any{"revision": row.Revision}, nil); err != nil {
			return err
		}
		mu.Lock()
		cycles++
		mu.Unlock()
		return nil
	}
	start := time.Now()
	deadline := start.Add(duration)
	pause := func(d time.Duration) bool {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		}
	}
	var wg sync.WaitGroup
	for _, agent := range []int{0, 1, 8, 9} {
		wg.Add(1)
		go func(agent int) {
			defer wg.Done()
			if !pause(time.Duration(agent%8) * 250 * time.Millisecond) {
				return
			}
			for n := 0; time.Now().Before(deadline) && ctx.Err() == nil; n++ {
				began := time.Now()
				if err := cycle(agent, n); err != nil {
					fail(err)
					return
				}
				if !pause(max(0, 5*time.Second-time.Since(began))) {
					return
				}
			}
		}(agent)
	}
	for _, agent := range []int{2, 3, 4, 5, 10, 11, 12, 13} {
		wg.Add(1)
		go func(agent int) {
			defer wg.Done()
			if !pause(time.Duration(agent%8) * 70 * time.Millisecond) {
				return
			}
			for time.Now().Before(deadline) && ctx.Err() == nil {
				began := time.Now()
				var body struct {
					Items []struct {
						ID     string           `json:"conversation_id"`
						Status string           `json:"status"`
						Unread int              `json:"unread_count"`
						Latest *webchat.Message `json:"latest_message"`
					} `json:"items"`
				}
				if err := check("queue", l.request("GET", "/internal/chat/desk/queue", l.agents[agent].Secret, nil), 200, &body); err != nil {
					fail(err)
					return
				}
				if len(body.Items) < 91 || len(body.Items) > 93 {
					fail(fmt.Errorf("unexpected waiting queue size %d", len(body.Items)))
					return
				}
				for _, row := range body.Items {
					mu.Lock()
					host, ok := known[row.ID]
					mu.Unlock()
					if !ok || host != agent/8 || row.Status != "waiting_human" {
						fail(fmt.Errorf("queue scope/ownership mismatch"))
						return
					}
					if strings.HasPrefix(row.ID, "soak-") && (row.Unread != 100 || row.Latest == nil || !strings.HasPrefix(row.Latest.Content, row.ID+" / ")) {
						fail(fmt.Errorf("seed queue summary mismatch"))
						return
					}
				}
				if !pause(max(0, time.Second-time.Since(began))) {
					return
				}
			}
		}(agent)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	before := l.pool.Stat()
	report := func() {
		after := l.pool.Stat()
		mu.Lock()
		defer mu.Unlock()
		names := make([]string, 0, len(windows))
		for name := range windows {
			names = append(names, name)
		}
		sort.Strings(names)
		t.Logf("elapsed_s=%.1f cycles=%d failures=%d pool_acquired=%d pool_total=%d interval_acquires=%d interval_empty_acquires=%d interval_acquire_ms=%.3f", time.Since(start).Seconds(), cycles, failures, after.AcquiredConns(), after.TotalConns(), after.AcquireCount()-before.AcquireCount(), after.EmptyAcquireCount()-before.EmptyAcquireCount(), float64(after.AcquireDuration()-before.AcquireDuration())/float64(time.Millisecond))
		for _, name := range names {
			v := windows[name]
			sort.Float64s(v)
			t.Logf("op=%s interval_n=%d total_n=%d p50_ms=%.3f p95_ms=%.3f max_ms=%.3f", name, len(v), totals[name], v[int(math.Ceil(.5*float64(len(v))))-1], v[int(math.Ceil(.95*float64(len(v))))-1], v[len(v)-1])
		}
		windows = map[string][]float64{}
		before = after
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	running := true
	for running {
		select {
		case <-ticker.C:
			report()
		case <-done:
			running = false
		}
	}
	report()
	if failures != 0 {
		return
	}
	if cycles < 4 {
		t.Fatal("insufficient completed cycles")
	}
	if l.models.Load() != 0 || l.pool.Stat().AcquiredConns() != 0 {
		t.Fatal("model invocation or unreturned pool connection")
	}
	var count, closed, receipts int
	if err := l.pool.QueryRow(ctx, "SELECT count(*) FROM customer_service.messages").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := l.pool.QueryRow(ctx, "SELECT count(*) FROM customer_service.conversations WHERE status='closed'").Scan(&closed); err != nil {
		t.Fatal(err)
	}
	if err := l.pool.QueryRow(ctx, "SELECT count(*) FROM customer_service.desk_read_receipts").Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if count != 18000+cycles*3 || closed != cycles || receipts != cycles*2 {
		t.Fatalf("final accounting mismatch messages=%d closed=%d receipts=%d cycles=%d", count, closed, receipts, cycles)
	}
	t.Logf("FINAL duration_s=%.1f cycles=%d messages=%d closed=%d receipts=%d models=%d acquired=0", time.Since(start).Seconds(), cycles, count, closed, receipts, l.models.Load())
}
