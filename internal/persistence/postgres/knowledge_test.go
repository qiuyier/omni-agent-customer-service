package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
	"testing"
)

func TestKnowledgeWriteOutcome(t *testing.T) {
	for _, c := range []struct{ in, want error }{
		{nil, nil}, {pgx.ErrNoRows, manage.ErrConflict},
		{&pgconn.PgError{Severity: "ERROR", Code: "23514", Message: "private detail"}, manage.ErrUnavailable},
		{&pgconn.PgError{Severity: "FATAL", Code: "57P01"}, manage.ErrOutcomeUnknown},
		{errors.New("private network detail"), manage.ErrOutcomeUnknown}, {context.DeadlineExceeded, manage.ErrOutcomeUnknown},
	} {
		if got := knowledgeWriteError(c.in); got != c.want {
			t.Fatalf("classification: %v", got)
		}
	}
}
func TestKnowledgeInvalidConfiguration(t *testing.T) {
	if _, e := NewKnowledgeRepository(nil); e != manage.ErrUnavailable {
		t.Fatal(e)
	}
	if validKnowledgeState(manage.State{}) {
		t.Fatal("zero snapshot accepted for write")
	}
}
