package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"os"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestDatabaseHistoryPagesAndAttention(t *testing.T) {
	r := openTestRepository(t, true)
	ctx := context.Background()
	c := newConversation(t, "sql-page", "customer", "web")
	if err := c.RequestHandoff(integrationTime); err != nil {
		t.Fatal(err)
	}
	if err := r.WithinTx(ctx, func(tx conversation.Transaction) error { return tx.InsertConversation(c) }); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, os.Getenv(testDatabaseEnvironment))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, `INSERT INTO customer_service.messages(id,conversation_id,channel,external_message_id,sender,sender_id,content,created_at)
 SELECT 'sql-'||lpad((300-n)::text,3,'0'),$1,'web','event-'||n,CASE WHEN n%11=0 THEN 'system' ELSE 'customer' END,'customer',repeat('界',1300),$2 FROM generate_series(1,237) n ORDER BY n`, c.ID(), integrationTime)
	if err != nil {
		t.Fatal(err)
	}
	all, err := r.ListMessages(ctx, c.ID())
	if err != nil {
		t.Fatal(err)
	}
	public := []conversation.Message{}
	for _, m := range all {
		if m.Sender() == conversation.SenderCustomer {
			public = append(public, m)
		}
	}
	page := func(before, after, from string) conversation.HistoryPage {
		var out conversation.HistoryPage
		err := r.WithinTx(ctx, func(tx conversation.Transaction) error {
			var e error
			out, e = tx.(conversation.MessagePager).PageMessages(c.ID(), before, after, from)
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	p := page("", "", "")
	if len(p.Messages) != 33 || !p.HasOlder || p.HasNewer {
		t.Fatal("incorrect latest byte-limited page", len(p.Messages))
	}
	got := append([]conversation.Message{}, p.Messages...)
	for p.HasOlder {
		p = page(p.Messages[0].ID(), "", "")
		got = append(p.Messages, got...)
	}
	if len(got) != len(public) {
		t.Fatal("lost messages", len(got), len(public))
	}
	for i, m := range got {
		if m.ID() != public[i].ID() {
			t.Fatal("wrong append order", i)
		}
	}
	forward := page("", "", public[0].ID())
	n := len(forward.Messages)
	for forward.HasNewer {
		forward = page("", forward.Messages[len(forward.Messages)-1].ID(), "")
		n += len(forward.Messages)
	}
	if n != len(public) {
		t.Fatal("forward paging omitted messages")
	}
	empty := page("", public[len(public)-1].ID(), "")
	if len(empty.Messages) != 0 || empty.HasNewer || !empty.HasOlder {
		t.Fatal("empty boundary wrong")
	}
	for _, cursor := range []string{"missing", "sql-289"} {
		err := r.WithinTx(ctx, func(tx conversation.Transaction) error {
			_, e := tx.(conversation.MessagePager).PageMessages(c.ID(), cursor, "", "")
			return e
		})
		if !errors.Is(err, conversation.ErrHistoryCursor) {
			t.Fatal("non-public cursor accepted", err)
		}
	}
	seenIDs := []string{public[0].ID(), public[1].ID()}
	if _, err = r.DeskAttention(ctx, c.ID(), "web", "agent", seenIDs); err != nil {
		t.Fatal(err)
	}
	summary, err := r.ReadDeskAttention(ctx, c.ID(), "web", "agent", []string{public[1].ID(), public[2].ID()})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Unread != len(public)-2 || summary.FirstUnreadID != public[2].ID() || len(summary.Seen) != 1 || !summary.Seen[public[1].ID()] || len([]rune(summary.Latest.Content())) != 161 {
		t.Fatal("summary mismatch", summary)
	}
	queue, err := r.ReadDeskAttention(ctx, c.ID(), "web", "agent", nil)
	if err != nil || len(queue.Seen) != 0 {
		t.Fatal("queue fetched receipts", err)
	}
	if _, err = r.DeskAttention(ctx, c.ID(), "web", "agent", []string{public[2].ID(), "missing"}); !errors.Is(err, conversation.ErrInvalidArgument) {
		t.Fatal(err)
	}
	unchanged, err := r.ReadDeskAttention(ctx, c.ID(), "web", "agent", nil)
	if err != nil || unchanged.Unread != summary.Unread {
		t.Fatal("partial receipt batch", err)
	}
	if _, err = r.ReadDeskAttention(ctx, c.ID(), "other", "agent", nil); !errors.Is(err, conversation.ErrAttentionDenied) {
		t.Fatal("cross-source summary allowed", err)
	}
	_, err = conn.Exec(ctx, "INSERT INTO customer_service.messages(id,conversation_id,channel,sender,sender_id,content,created_at) VALUES('giant',$1,'web','customer','customer',repeat('界',50000),$2)", c.ID(), integrationTime)
	if err != nil {
		t.Fatal(err)
	}
	giant := page("", "", "")
	if len(giant.Messages) != 1 || !giant.Truncated["giant"] || len(giant.Messages[0].Content()) > 128<<10 {
		t.Fatal("oversized page mismatch")
	}
}

// Opt-in, serial local comparison; not a throughput/SLA benchmark.
func TestDeskSQLLongHistoryMeasurement(t *testing.T) {
	if os.Getenv("DESK_SQL_MEASURE") != "1" {
		t.Skip("set DESK_SQL_MEASURE=1 for local query measurement")
	}
	r := openTestRepository(t, true)
	ctx := context.Background()
	c := newConversation(t, "sql-measure", "customer", "web")
	if err := c.RequestHandoff(integrationTime); err != nil {
		t.Fatal(err)
	}
	if err := r.WithinTx(ctx, func(tx conversation.Transaction) error { return tx.InsertConversation(c) }); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, os.Getenv(testDatabaseEnvironment))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, `INSERT INTO customer_service.messages(id,conversation_id,channel,external_message_id,sender,sender_id,content,created_at)
 SELECT 'measure-'||n,$1,'web','event-'||n,'customer','customer',repeat(md5(n::text),32),$2 FROM generate_series(1,10000) n ORDER BY n`, c.ID(), integrationTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "ANALYZE customer_service.messages"); err != nil {
		t.Fatal(err)
	}
	measure := func(name string, fn func() (int, int, error)) {
		_, _, err := fn()
		if err != nil {
			t.Fatal(err)
		}
		times := []float64{}
		var allocated uint64
		var count, bytes int
		for i := 0; i < 9; i++ {
			var a, z runtime.MemStats
			runtime.ReadMemStats(&a)
			start := time.Now()
			count, bytes, err = fn()
			elapsed := time.Since(start)
			runtime.ReadMemStats(&z)
			if err != nil {
				t.Fatal(err)
			}
			times = append(times, float64(elapsed.Microseconds())/1000)
			allocated += z.TotalAlloc - a.TotalAlloc
		}
		sort.Float64s(times)
		t.Logf("%s rows=%d body_bytes=%d median_ms=%.3f max_ms=%.3f mean_alloc_bytes=%d", name, count, bytes, times[4], times[8], allocated/9)
	}
	measure("baseline_full_history", func() (int, int, error) {
		all, e := r.ListMessages(ctx, c.ID())
		bytes := 0
		for _, m := range all {
			bytes += len(m.Content())
		}
		return len(all), bytes, e
	})
	measure("database_page", func() (int, int, error) {
		var p conversation.HistoryPage
		e := r.WithinTx(ctx, func(tx conversation.Transaction) error {
			var e error
			p, e = tx.(conversation.MessagePager).PageMessages(c.ID(), "", "", "")
			return e
		})
		bytes := 0
		for _, m := range p.Messages {
			bytes += len(m.Content())
		}
		if len(p.Messages) != 100 {
			return 0, 0, fmt.Errorf("wrong page size: %d", len(p.Messages))
		}
		return len(p.Messages), bytes, e
	})
	measure("baseline_attention_full_history", func() (int, int, error) {
		all, e := r.ListMessages(ctx, c.ID())
		a := conversation.SummarizeAttention(all, map[string]bool{})
		if a.Unread != 10000 {
			return 0, 0, fmt.Errorf("wrong unread")
		}
		bytes := 0
		for _, m := range all {
			bytes += len(m.Content())
		}
		return len(all), bytes, e
	})
	measure("database_attention_preview", func() (int, int, error) {
		a, e := r.ReadDeskAttention(ctx, c.ID(), "web", "agent", nil)
		if a.Unread != 10000 || a.FirstUnreadID != "measure-1" {
			return 0, 0, fmt.Errorf("wrong summary: %v", e)
		}
		return 1, len(a.Latest.Content()), e
	})
	plans := []string{
		"SELECT id,sequence_no,octet_length(content) FROM customer_service.messages WHERE conversation_id='sql-measure' AND sender IN ('customer','bot','human_agent') ORDER BY sequence_no DESC LIMIT 100",
		"SELECT count(*),min(m.sequence_no) FROM customer_service.messages m WHERE m.conversation_id='sql-measure' AND m.sender='customer' AND NOT EXISTS(SELECT 1 FROM customer_service.desk_read_receipts r WHERE r.agent_id='agent' AND r.message_id=m.id)",
	}
	for i, q := range plans {
		rows, e := conn.Query(ctx, "EXPLAIN (ANALYZE,BUFFERS) "+q)
		if e != nil {
			t.Fatal(e)
		}
		lines := []string{}
		for rows.Next() {
			var line string
			if e = rows.Scan(&line); e != nil {
				t.Fatal(e)
			}
			lines = append(lines, line)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			t.Fatal(e)
		}
		t.Logf("plan_%d:\n%s", i, strings.Join(lines, "\n"))
	}
}
