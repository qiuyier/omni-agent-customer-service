package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
	pg "github.com/qiuyier/omni-agent-customer-service/internal/persistence/postgres"
	"github.com/qiuyier/omni-agent-customer-service/internal/persistence/postgres/migrations"
)

func knowledgePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	conn := openRawTestConnection(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var name string
	if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.ToLower(name), "_test") {
		t.Fatal("refusing to migrate or reset a non-test database")
	}
	if _, e := migrations.Up(ctx, conn); e != nil {
		t.Fatal(e)
	}
	pool, e := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	return pool
}
func knowledgeService(t *testing.T, p *pgxpool.Pool) *manage.Service {
	t.Helper()
	r, e := pg.NewKnowledgeRepository(p)
	if e != nil {
		t.Fatal(e)
	}
	s, e := manage.New(r, func(context.Context, manage.Permission) (string, error) { return "fictional-admin", nil })
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func knowledgeData(text string) []byte {
	return []byte(" \n" + `[{"id":"hours","title":"Hours","source":"fictional","content":"` + text + `","keywords":["hours"]}]` + "\n ")
}
func knowledgeView(t *testing.T, s *manage.Service) manage.State {
	t.Helper()
	v, e := s.View(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func knowledgeAnswer(t *testing.T, s *manage.Service, want string) {
	t.Helper()
	v, e := s.Retrieve(context.Background(), customerservice.KnowledgeQuery{Text: "hours", Limit: 1})
	if e != nil || len(v) != 1 || v[0].Content != want {
		t.Fatal("wrong persisted answer", e)
	}
}
func knowledgeExec(t *testing.T, p *pgxpool.Pool, sql string) {
	t.Helper()
	if _, e := p.Exec(context.Background(), sql); e != nil {
		t.Fatal(e)
	}
}

func TestKnowledgeDatabaseLifecycle(t *testing.T) {
	p := knowledgePool(t)
	knowledgeExec(t, p, "TRUNCATE customer_service.knowledge_management")
	s := knowledgeService(t, p)
	ctx := context.Background()
	if _, e := s.SaveDraft(ctx, 0, knowledgeData("old")); e != nil {
		t.Fatal(e)
	}
	old := knowledgeView(t, s).Draft.ID
	if _, e := s.PublishDraft(ctx, 1, false); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SaveDraft(ctx, 2, knowledgeData("new")); e != nil {
		t.Fatal(e)
	}
	before := knowledgeView(t, s)
	knowledgeAnswer(t, s, "old")
	if !bytes.Equal(before.Draft.Data, knowledgeData("new")) {
		t.Fatal("raw bytes changed")
	}
	// Reject the complete next snapshot at the database level. No active pointer
	// or audit record may survive this failed publication.
	knowledgeExec(t, p, "ALTER TABLE customer_service.knowledge_management ADD CONSTRAINT reject_knowledge_test_revision CHECK (revision<>4)")
	t.Cleanup(func() {
		_, _ = p.Exec(context.Background(), "ALTER TABLE customer_service.knowledge_management DROP CONSTRAINT IF EXISTS reject_knowledge_test_revision")
	})
	if _, e := s.PublishDraft(ctx, 3, false); e != manage.ErrUnavailable {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, knowledgeView(t, s)) {
		t.Fatal("failed publication partially committed")
	}
	knowledgeAnswer(t, s, "old")
	knowledgeExec(t, p, "ALTER TABLE customer_service.knowledge_management DROP CONSTRAINT reject_knowledge_test_revision")
	p2 := knowledgePool(t)
	other := knowledgeService(t, p2)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, service := range []*manage.Service{s, other} {
		go func(s *manage.Service) { <-start; _, e := s.PublishDraft(ctx, 3, false); results <- e }(service)
	}
	close(start)
	wins, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		e := <-results
		if e == nil {
			wins++
		} else if errors.Is(e, manage.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("publication lost update")
	}
	knowledgeAnswer(t, s, "new")
	if _, e := other.Rollback(ctx, 4, old, false); e != nil {
		t.Fatal(e)
	}
	// A fresh pool/service is independent of all previous service memory.
	restored := knowledgeService(t, knowledgePool(t))
	knowledgeAnswer(t, restored, "old")
	state := knowledgeView(t, restored)
	if state.Revision != 5 || len(state.Events) != 5 || len(state.Versions) != 2 || state.Events[4].Action != "rollback" {
		t.Fatal("history not persisted")
	}
	if !bytes.Equal(state.Draft.Data, knowledgeData("new")) {
		t.Fatal("rollback overwrote draft")
	}
}

// Two explicit invocations separated by restarting the owned test container.
// Never reset on verify: the same five revisions must survive on disk.
func TestKnowledgeDatabaseRestart(t *testing.T) {
	phase := os.Getenv("KNOWLEDGE_RESTART_PHASE")
	if phase != "seed" && phase != "verify" {
		t.Skip("explicit restart phase required")
	}
	p := knowledgePool(t)
	s := knowledgeService(t, p)
	ctx := context.Background()
	if phase == "seed" {
		knowledgeExec(t, p, "TRUNCATE customer_service.knowledge_management")
		if _, e := s.SaveDraft(ctx, 0, knowledgeData("old")); e != nil {
			t.Fatal(e)
		}
		old := knowledgeView(t, s).Draft.ID
		if _, e := s.PublishDraft(ctx, 1, false); e != nil {
			t.Fatal(e)
		}
		if _, e := s.SaveDraft(ctx, 2, knowledgeData("new")); e != nil {
			t.Fatal(e)
		}
		if _, e := s.PublishDraft(ctx, 3, false); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Rollback(ctx, 4, old, false); e != nil {
			t.Fatal(e)
		}
	}
	state := knowledgeView(t, s)
	knowledgeAnswer(t, s, "old")
	if state.Revision != 5 || len(state.Events) != 5 || len(state.Versions) != 2 || state.Events[4].Action != "rollback" || state.Events[4].Actor != "fictional-admin" || !bytes.Equal(state.Draft.Data, knowledgeData("new")) {
		t.Fatal("restart state mismatch")
	}
	for i, e := range state.Events {
		if e.Revision != uint64(i+1) || e.At.IsZero() {
			t.Fatal("audit missing")
		}
	}
}
