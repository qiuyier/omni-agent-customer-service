package postgres_test

import (
	"context"
	"errors"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/feedback"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
	pg "github.com/qiuyier/omni-agent-customer-service/internal/persistence/postgres"
	"strings"
	"testing"
)

func TestKnowledgeFeedbackDatabase(t *testing.T) {
	p := knowledgePool(t)
	ctx := context.Background()
	knowledgeExec(t, p, "TRUNCATE customer_service.knowledge_feedback,customer_service.knowledge_management")
	repo, _ := pg.NewKnowledgeRepository(p)
	for i := 0; i < 2; i++ {
		if err := repo.RecordFeedback(ctx, "请帮忙 13800138000"); err != nil {
			t.Fatal(err)
		}
	}
	items, err := repo.ListFeedback(ctx)
	if err != nil || len(items) != 1 || items[0].Count != 2 || strings.Contains(items[0].Question, "13800138000") {
		t.Fatal("capture", err)
	}
	item := items[0]
	if item.ExpiresAt.Sub(item.FirstSeen) != feedback.Retention {
		t.Fatal("retention")
	}
	change := feedback.Change{ID: item.ID, Revision: item.Revision, Status: "resolved", FAQID: "hours"}
	if !errors.Is(repo.UpdateFeedback(ctx, change), manage.ErrInvalid) {
		t.Fatal("unpublished resolution accepted")
	}
	service := knowledgeService(t, p)
	if _, err = service.SaveDraft(ctx, 0, knowledgeData("fictional answer")); err != nil {
		t.Fatal(err)
	}
	if _, err = service.PublishDraft(ctx, 1, false); err != nil {
		t.Fatal(err)
	}
	if err = repo.UpdateFeedback(ctx, change); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(repo.UpdateFeedback(ctx, change), manage.ErrConflict) {
		t.Fatal("stale revision accepted")
	}
	knowledgeExec(t, p, "UPDATE customer_service.knowledge_feedback SET first_seen=first_seen-interval '8 days',expires_at=expires_at-interval '8 days'")
	items, err = repo.ListFeedback(ctx)
	if err != nil || len(items) != 0 {
		t.Fatal("expired visible")
	}
	if err = repo.PurgeFeedback(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = p.QueryRow(ctx, "SELECT count(*) FROM customer_service.knowledge_feedback").Scan(&count); err != nil || count != 0 {
		t.Fatal("purge failed")
	}
	// Concurrent identical misses aggregate without extending expiry.
	failures := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func() { failures <- repo.RecordFeedback(ctx, "并发虚构问题") }()
	}
	for i := 0; i < 16; i++ {
		if err = <-failures; err != nil {
			t.Fatal(err)
		}
	}
	items, err = repo.ListFeedback(ctx)
	if err != nil || len(items) != 1 || items[0].Count != 16 {
		t.Fatal("concurrent aggregation")
	}
	knowledgeExec(t, p, `INSERT INTO customer_service.knowledge_feedback(id,question) SELECT '00000000-0000-4000-8000-'||lpad(n::text,12,'0'),'fixture-'||n FROM generate_series(1,499) n`)
	if err = repo.RecordFeedback(ctx, "容量之外的虚构问题"); !errors.Is(err, feedback.ErrFull) {
		t.Fatal(err)
	}
	items, err = repo.ListFeedback(ctx)
	if err != nil || len(items) != 500 {
		t.Fatal("queue cap")
	}
	for _, i := range items {
		if i.Question == "容量之外的虚构问题" {
			t.Fatal("overflow inserted")
		}
	}
}
