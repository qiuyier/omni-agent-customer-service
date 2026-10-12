package postgres_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/persistence/postgres"
)

type queueTrace struct {
	mu      sync.Mutex
	queries []string
}

func (q *queueTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.queries = append(q.queries, d.SQL)
	return ctx
}
func (*queueTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func (q *queueTrace) reset()                                                        { q.mu.Lock(); defer q.mu.Unlock(); q.queries = nil }
func (q *queueTrace) snapshot() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]string{}, q.queries...)
}

func TestDatabaseDeskQueueBatch(t *testing.T) {
	openTestRepository(t, true) // Initializes and guards the dedicated *_test database.
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv(testDatabaseEnvironment))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO customer_service.conversations(id,customer_id,channel,status,agent_id,revision,created_at,updated_at)
 SELECT CASE WHEN n>=120 THEN 'a-' ELSE 'q-' END||lpad(n::text,3,'0'),'customer',
 CASE WHEN n=124 THEN 'other' ELSE 'web' END,
 CASE WHEN n IN (120,121) THEN 'human_active' WHEN n=122 THEN 'closed' WHEN n=123 THEN 'bot_active' ELSE 'waiting_human' END,
 CASE WHEN n=120 THEN 'agent-a' WHEN n=121 THEN 'agent-b' ELSE NULL END,2,$1,$1
 FROM generate_series(0,127) n`, integrationTime)
	exec(`INSERT INTO customer_service.messages(id,conversation_id,channel,sender,sender_id,content,created_at)
 SELECT c.id||'-'||lpad(n::text,3,'0'),c.id,c.channel,
 CASE WHEN c.id='a-126' OR n=100 THEN 'system' WHEN c.id='a-127' OR n=99 THEN 'bot'
 WHEN n=98 THEN 'human_agent' WHEN n%10=0 THEN 'tool' ELSE 'customer' END,
 'customer',repeat('界',200),$1
 FROM customer_service.conversations c CROSS JOIN generate_series(1,100) n
 WHERE c.id<>'a-125' ORDER BY c.id,n`, integrationTime)
	exec(`INSERT INTO customer_service.desk_read_receipts(agent_id,message_id)
 SELECT CASE WHEN right(id,3)::int%2=1 THEN 'agent-a' ELSE 'agent-b' END,id
 FROM customer_service.messages WHERE sender='customer'`)
	exec(`INSERT INTO customer_service.desk_read_receipts(agent_id,message_id)
 SELECT a,id FROM customer_service.messages CROSS JOIN (VALUES ('agent-a'),('agent-b')) agents(a)
 WHERE conversation_id='q-001' AND sender='customer' ON CONFLICT DO NOTHING`)
	exec("UPDATE customer_service.messages SET sender='human_agent' WHERE id='q-002-099'")
	exec("ANALYZE customer_service.conversations")
	exec("ANALYZE customer_service.messages")
	exec("ANALYZE customer_service.desk_read_receipts")
	config, err := pgxpool.ParseConfig(os.Getenv(testDatabaseEnvironment))
	if err != nil {
		t.Fatal(err)
	}
	trace := &queueTrace{}
	config.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := postgres.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	old := func(agent string) []conversation.DeskQueueItem {
		t.Helper()
		rows, err := r.ListHumanQueue(ctx, "web", agent)
		if err != nil {
			t.Fatal(err)
		}
		items := make([]conversation.DeskQueueItem, 0, len(rows))
		for _, c := range rows {
			info, err := r.ReadDeskAttention(ctx, c.ID(), "web", agent, nil)
			if err != nil {
				t.Fatal(err)
			}
			// Queue previews expose no external/reply IDs or receipt maps.
			info.Seen = nil
			if info.HasLatest {
				m := info.Latest
				info.Latest, err = conversation.NewMessage(conversation.MessageParams{ID: m.ID(), ConversationID: m.ConversationID(), Sender: m.Sender(), SenderID: m.SenderID(), Content: m.Content(), CreatedAt: m.CreatedAt()})
				if err != nil {
					t.Fatal(err)
				}
			}
			items = append(items, conversation.DeskQueueItem{Conversation: c, Attention: info})
		}
		return items
	}
	for _, agent := range []string{"agent-a", "agent-b"} {
		trace.reset()
		want := old(agent)
		oldCount := len(trace.snapshot())
		trace.reset()
		got, err := r.ReadDeskQueue(ctx, "web", agent)
		if err != nil {
			t.Fatal(err)
		}
		queries := trace.snapshot()
		if len(queries) != 1 {
			t.Fatalf("batch statements=%d", len(queries))
		}
		if len(got) != 100 || !reflect.DeepEqual(got, want) {
			t.Fatalf("batch differs for %s (size %d)", agent, len(got))
		}
		for _, item := range got {
			c, a := item.Conversation, item.Attention
			if c.Channel() != "web" || c.Status() == conversation.StatusHumanActive && c.AgentID() != agent {
				t.Fatal("scope leak")
			}
			switch c.ID() {
			case "q-001":
				if a.Unread != 0 || a.FirstUnreadID != "" || !a.HasLatest {
					t.Fatal("fully read summary")
				}
			case "a-125", "a-126":
				if a.HasLatest || a.Unread != 0 || a.FirstUnreadID != "" {
					t.Fatal("empty/private-only conversation has summary")
				}
			case "a-127":
				if !a.HasLatest || a.Latest.Sender() != conversation.SenderBot || a.Unread != 0 {
					t.Fatal("bot-only summary")
				}
			default:
				count, first := 39, "002"
				if agent == "agent-b" {
					count, first = 49, "001"
				}
				if a.Unread != count || a.FirstUnreadID != c.ID()+"-"+first || !a.HasLatest || a.Latest.ID() != c.ID()+"-099" || len([]rune(a.Latest.Content())) != 161 {
					t.Fatalf("incorrect summary %s: %+v", c.ID(), a)
				}
			}
		}
		t.Logf("%s conversations=%d old_statements=%d batch_statements=%d", agent, len(got), oldCount, len(queries))
		if os.Getenv("DESK_QUEUE_MEASURE") == "1" && agent == "agent-a" {
			// One warm-up per path, then nine serial samples; statement counts include BEGIN/COMMIT/locks.
			old(agent)
			if _, err := r.ReadDeskQueue(ctx, "web", agent); err != nil {
				t.Fatal(err)
			}
			for _, batch := range []bool{false, true} {
				samples := []float64{}
				for i := 0; i < 9; i++ {
					trace.reset()
					start := time.Now()
					if batch {
						if _, err := r.ReadDeskQueue(ctx, "web", agent); err != nil {
							t.Fatal(err)
						}
					} else {
						old(agent)
					}
					ms := float64(time.Since(start)) / float64(time.Millisecond)
					samples = append(samples, ms)
					t.Logf("batch=%t sample=%d ms=%.3f statements=%d", batch, i+1, ms, len(trace.snapshot()))
				}
				sort.Float64s(samples)
				t.Logf("batch=%t median_ms=%.3f max_ms=%.3f", batch, samples[4], samples[8])
			}
			plan, err := conn.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+queries[0], "web", agent)
			if err != nil {
				t.Fatal(err)
			}
			for plan.Next() {
				var line string
				if err := plan.Scan(&line); err != nil {
					t.Fatal(err)
				}
				t.Log(line)
			}
			if err := plan.Err(); err != nil {
				t.Fatal(err)
			}
			plan.Close()
		}
	}
	// A committed claim immediately excludes the row from other agents' next queue read.
	exec("UPDATE customer_service.conversations SET status='human_active',agent_id='agent-b',revision=revision+1 WHERE id='q-000'")
	got, err := r.ReadDeskQueue(ctx, "web", "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range got {
		if item.Conversation.ID() == "q-000" {
			t.Fatal("claimed conversation leaked")
		}
	}
	other, err := r.ReadDeskQueue(ctx, "other", "agent-a")
	if err != nil || len(other) != 1 || other[0].Conversation.ID() != "a-124" {
		t.Fatal("source scope", err)
	}
	empty, err := r.ReadDeskQueue(ctx, "missing", "agent-a")
	if err != nil || len(empty) != 0 {
		t.Fatal("empty scope", err)
	}
	for _, args := range [][2]string{{"", "a"}, {"web", ""}, {"web", "   "}} {
		if _, err := r.ReadDeskQueue(ctx, args[0], args[1]); !errors.Is(err, conversation.ErrInvalidArgument) {
			t.Fatal("invalid scope", err)
		}
	}
	if _, err := r.ReadDeskQueue(nil, "web", "a"); !errors.Is(err, conversation.ErrInvalidArgument) {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.ReadDeskQueue(cancelled, "web", "a"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDeskQueueNilRepository(t *testing.T) {
	var r *postgres.Repository
	if _, err := r.ReadDeskQueue(context.Background(), "web", "a"); err == nil {
		t.Fatal("nil repository accepted")
	}
}
