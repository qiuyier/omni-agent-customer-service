package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/persistence/postgres"
	"github.com/qiuyier/omni-agent-customer-service/internal/webchat"
)

type deskConcurrentLab struct {
	repo   *postgres.Repository
	pool   *pgxpool.Pool
	server *httptest.Server
	hosts  []webchat.Host
	agents []webchat.Agent
	models atomic.Int32
}
type deskHTTPResult struct {
	status  int
	body    []byte
	err     error
	elapsed time.Duration
}
type concurrentDeskRow struct {
	ID       string           `json:"conversation_id"`
	Revision uint64           `json:"revision"`
	Unread   int              `json:"unread_count"`
	First    string           `json:"first_unread_id"`
	Latest   *webchat.Message `json:"latest_message"`
}

func newDeskConcurrentLab(t *testing.T) *deskConcurrentLab {
	t.Helper()
	openTestRepository(t, true) // Verifies actual database name ends in _test before reset.
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(os.Getenv(testDatabaseEnvironment))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	repo, err := postgres.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	lab := &deskConcurrentLab{repo: repo, pool: pool}
	lab.hosts = []webchat.Host{{SourceID: "concurrent-a", Secret: strings.Repeat("a", 40)}, {SourceID: "concurrent-b", Secret: strings.Repeat("b", 40)}}
	for i := 0; i < 16; i++ {
		lab.agents = append(lab.agents, webchat.Agent{SourceID: lab.hosts[i/8].SourceID, ID: fmt.Sprintf("agent-%d", i%8), Secret: fmt.Sprintf("concurrent-agent-%02d-%s", i, strings.Repeat("x", 32))})
	}
	var ids atomic.Int64
	app, err := customerservice.New(repo, customerservice.MessageIDGeneratorFunc(func() (string, error) { return fmt.Sprintf("concurrent-message-%d", ids.Add(1)), nil }), customerservice.ClockFunc(time.Now),
		customerservice.WithReplyGenerator(customerservice.ReplyGeneratorFunc(func(context.Context, customerservice.ReplyRequest) (customerservice.Reply, error) {
			lab.models.Add(1)
			return customerservice.Reply{Content: "local stub"}, nil
		})))
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := webchat.New(repo, app, webchat.Config{Hosts: lab.hosts, Agents: lab.agents})
	if err != nil {
		t.Fatal(err)
	}
	lab.server = httptest.NewServer(gateway)
	t.Cleanup(lab.server.Close)
	// Bound the client as well as the gateway to avoid hanging test workers.
	lab.server.Client().Timeout = 25 * time.Second
	return lab
}
func (l *deskConcurrentLab) request(method, path, token string, body any) deskHTTPResult {
	start := time.Now()
	var b []byte
	var err error
	if body != nil {
		b, err = json.Marshal(body)
		if err != nil {
			return deskHTTPResult{err: err}
		}
	}
	req, err := http.NewRequest(method, l.server.URL+path, bytes.NewReader(b))
	if err != nil {
		return deskHTTPResult{err: err}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := l.server.Client().Do(req)
	if err != nil {
		return deskHTTPResult{err: err, elapsed: time.Since(start)}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return deskHTTPResult{resp.StatusCode, data, err, time.Since(start)}
}
func requireDeskHTTP(t *testing.T, r deskHTTPResult, status int, out any) {
	t.Helper()
	if r.err != nil || r.status != status {
		t.Fatalf("HTTP=%d want=%d err=%v body=%s", r.status, status, r.err, r.body)
	}
	if out != nil {
		if err := json.Unmarshal(r.body, out); err != nil {
			t.Fatal(err)
		}
	}
}
func (l *deskConcurrentLab) session(t *testing.T, host int, customer string) webchat.SessionResponse {
	t.Helper()
	var s webchat.SessionResponse
	requireDeskHTTP(t, l.request("POST", webchat.SessionPath, l.hosts[host].Secret, map[string]string{"customer_id": customer}), 201, &s)
	return s
}
func (l *deskConcurrentLab) action(agent int, id, action string, extra map[string]any) deskHTTPResult {
	body := map[string]any{"conversation_id": id, "action": action}
	for k, v := range extra {
		body[k] = v
	}
	return l.request("POST", "/internal/chat/desk/action", l.agents[agent].Secret, body)
}
func deskParallel(actions ...func() deskHTTPResult) []deskHTTPResult {
	start := make(chan struct{})
	out := make([]deskHTTPResult, len(actions))
	var wg sync.WaitGroup
	for i, fn := range actions {
		wg.Add(1)
		go func(i int, fn func() deskHTTPResult) { defer wg.Done(); <-start; out[i] = fn() }(i, fn)
	}
	close(start)
	wg.Wait()
	return out
}

func TestDatabaseDeskHTTPConcurrency(t *testing.T) {
	l := newDeskConcurrentLab(t)
	for trial := 0; trial < 8; trial++ {
		host := trial / 4
		a, b := trial*2, trial*2+1
		// Reuse customer IDs across sources to exercise source-scoped identities.
		s := l.session(t, host, fmt.Sprintf("customer-%d", trial%4))
		requireDeskHTTP(t, l.request("POST", "/chat/v1/handoff", s.AccessToken, map[string]any{}), 200, nil)
		var old webchat.SendResult
		requireDeskHTTP(t, l.request("POST", webchat.MessagesPath, s.AccessToken, map[string]string{"event_id": "old", "content": "old-visible"}), 200, &old)
		results := deskParallel(
			func() deskHTTPResult {
				return l.action(a, s.ConversationID, "read", map[string]any{"message_ids": []string{old.MessageID}})
			},
			func() deskHTTPResult {
				return l.request("POST", webchat.MessagesPath, s.AccessToken, map[string]string{"event_id": "new", "content": "new-not-yet-visible"})
			},
		)
		requireDeskHTTP(t, results[0], 200, nil)
		var fresh webchat.SendResult
		requireDeskHTTP(t, results[1], 200, &fresh)
		var state concurrentDeskRow
		requireDeskHTTP(t, l.action(a, s.ConversationID, "history", nil), 200, &state)
		if state.Unread != 1 || state.First != fresh.MessageID {
			t.Fatal("new message incorrectly read", state)
		}
		requireDeskHTTP(t, l.action(b, s.ConversationID, "history", nil), 200, &state)
		if state.Unread != 2 || state.First != old.MessageID {
			t.Fatal("read receipt crossed agents", state)
		}
		// Duplicate confirmation must remain idempotent while both queue readers run.
		results = deskParallel(
			func() deskHTTPResult {
				return l.action(a, s.ConversationID, "read", map[string]any{"message_ids": []string{old.MessageID}})
			},
			func() deskHTTPResult { return l.request("GET", "/internal/chat/desk/queue", l.agents[b].Secret, nil) },
		)
		for _, r := range results {
			requireDeskHTTP(t, r, 200, nil)
		}
		revision := state.Revision
		results = deskParallel(
			func() deskHTTPResult {
				return l.action(a, s.ConversationID, "claim", map[string]any{"revision": revision})
			},
			func() deskHTTPResult {
				return l.action(b, s.ConversationID, "claim", map[string]any{"revision": revision})
			},
		)
		winner, loser := a, b
		if results[1].status == 200 {
			winner, loser = b, a
			results[0], results[1] = results[1], results[0]
		}
		requireDeskHTTP(t, results[0], 200, nil)
		requireDeskHTTP(t, results[1], 403, nil)
		stored, err := l.repo.FindConversation(context.Background(), s.ConversationID)
		if err != nil || stored.Revision() != revision+1 {
			t.Fatalf("claim revision must advance once: %v", err)
		}
		requireDeskHTTP(t, l.action(loser, s.ConversationID, "history", nil), 403, nil)
		requireDeskHTTP(t, l.action(loser, s.ConversationID, "reply", map[string]any{"event_id": "forbidden", "content": "must not persist"}), 403, nil)
		var queue struct {
			Items []concurrentDeskRow `json:"items"`
		}
		requireDeskHTTP(t, l.request("GET", "/internal/chat/desk/queue", l.agents[loser].Secret, nil), 200, &queue)
		for _, r := range queue.Items {
			if r.ID == s.ConversationID {
				t.Fatal("claimed row remained visible to loser")
			}
		}
		requireDeskHTTP(t, l.action(winner, s.ConversationID, "history", nil), 200, nil)
		foreign := (a + 8) % 16
		requireDeskHTTP(t, l.action(foreign, s.ConversationID, "history", nil), 403, nil)
		requireDeskHTTP(t, l.action(foreign, s.ConversationID, "read", map[string]any{"message_ids": []string{fresh.MessageID}}), 403, nil)
		messages, err := l.repo.ListMessages(context.Background(), s.ConversationID)
		if err != nil || len(messages) != 2 {
			t.Fatalf("unexpected message write: %d %v", len(messages), err)
		}
	}
	if l.models.Load() != 0 {
		t.Fatal("unexpected model invocation")
	}
	t.Log("8 synchronized claim races: exactly one winner; 8 read/send races: new message unread; agent/source isolation passed; model calls=0")
}

func TestDatabaseDeskConcurrentPolling(t *testing.T) {
	if os.Getenv("DESK_CONCURRENCY_MEASURE") != "1" {
		t.Skip("set DESK_CONCURRENCY_MEASURE=1 for bounded HTTP load measurement")
	}
	l := newDeskConcurrentLab(t)
	ctx := context.Background()
	for host := 0; host < 2; host++ {
		s := l.session(t, host, "seed")
		// Session minting is lazy; handoff creates the conversation without a model call.
		requireDeskHTTP(t, l.request("POST", "/chat/v1/handoff", s.AccessToken, map[string]any{}), 200, nil)
		c, err := l.repo.FindConversation(ctx, s.ConversationID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = l.pool.Exec(ctx, `INSERT INTO customer_service.conversations(id,customer_id,channel,status,revision,created_at,updated_at)
 SELECT $1||'-'||lpad(n::text,3,'0'),'customer',$2,'waiting_human',2,$3,$3 FROM generate_series(1,100) n`, fmt.Sprintf("load-%d", host), c.Channel(), integrationTime)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := l.pool.Exec(ctx, `INSERT INTO customer_service.messages(id,conversation_id,channel,sender,sender_id,content,created_at)
 SELECT c.id||'-m-'||n,c.id,c.channel,'customer','customer',c.id||' / message '||n,$1
 FROM customer_service.conversations c CROSS JOIN generate_series(1,100) n WHERE c.id LIKE 'load-%'`, integrationTime)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"conversations", "messages", "desk_read_receipts"} {
		if _, err := l.pool.Exec(ctx, "ANALYZE customer_service."+table); err != nil {
			t.Fatal(err)
		}
	}
	for _, workers := range []int{1, 8, 16} {
		const samplesPerAgent = 50
		// Warm up each identity before paced, closed-loop requests at <=5 requests/sec.
		for i := 0; i < workers; i++ {
			requireDeskHTTP(t, l.request("GET", "/internal/chat/desk/queue", l.agents[i].Secret, nil), 200, nil)
		}
		before := l.pool.Stat()
		var peak atomic.Int32
		stop, done := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(done)
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					n := l.pool.Stat().AcquiredConns()
					for old := peak.Load(); n > old; old = peak.Load() {
						if peak.CompareAndSwap(old, n) {
							break
						}
					}
				}
			}
		}()
		results := make(chan deskHTTPResult, workers*samplesPerAgent)
		start := make(chan struct{})
		var wg sync.WaitGroup
		begin := time.Now()
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(agent int) {
				defer wg.Done()
				<-start
				for n := 0; n < samplesPerAgent; n++ {
					tick := time.Now()
					r := l.request("GET", "/internal/chat/desk/queue", l.agents[agent].Secret, nil)
					if r.err == nil && r.status == 200 {
						var body struct {
							Items []concurrentDeskRow `json:"items"`
						}
						if err := json.Unmarshal(r.body, &body); err != nil {
							r.err = err
						} else if len(body.Items) != 100 {
							r.err = fmt.Errorf("queue size=%d", len(body.Items))
						} else {
							prefix := fmt.Sprintf("load-%d-", agent/8)
							for _, row := range body.Items {
								if !strings.HasPrefix(row.ID, prefix) || row.Unread != 100 || row.Latest == nil || !strings.HasPrefix(row.Latest.Content, prefix) {
									r.err = fmt.Errorf("invalid scoped summary")
									break
								}
							}
						}
					}
					results <- r
					if n < samplesPerAgent-1 {
						if delay := 200*time.Millisecond - time.Since(tick); delay > 0 {
							time.Sleep(delay)
						}
					}
				}
			}(i)
		}
		close(start)
		wg.Wait()
		elapsed := time.Since(begin)
		close(stop)
		<-done
		close(results)
		after := l.pool.Stat()
		times := []float64{}
		statuses := map[int]int{}
		failures := 0
		for r := range results {
			statuses[r.status]++
			times = append(times, float64(r.elapsed)/float64(time.Millisecond))
			if r.err != nil || r.status != 200 {
				failures++
				t.Logf("request failure status=%d err=%v", r.status, r.err)
			}
		}
		sort.Float64s(times)
		percentile := func(p float64) float64 { return times[int(math.Ceil(p*float64(len(times))))-1] }
		t.Logf("workers=%d requests=%d elapsed_s=%.3f achieved_rps=%.2f errors=%d statuses=%v p50_ms=%.3f p95_ms=%.3f max_ms=%.3f pool_limit=4 sampled_peak_acquired=%d acquire_count=%d empty_acquire_count=%d total_acquire_ms=%.3f acquired_after=%d",
			workers, len(times), elapsed.Seconds(), float64(len(times))/elapsed.Seconds(), failures, statuses, percentile(.5), percentile(.95), times[len(times)-1], peak.Load(),
			after.AcquireCount()-before.AcquireCount(), after.EmptyAcquireCount()-before.EmptyAcquireCount(), float64(after.AcquireDuration()-before.AcquireDuration())/float64(time.Millisecond), after.AcquiredConns())
		if failures != 0 || after.AcquiredConns() != 0 {
			t.Fatalf("polling failures=%d acquired_after=%d", failures, after.AcquiredConns())
		}
	}
	if l.models.Load() != 0 {
		t.Fatal("unexpected model invocation")
	}
}
