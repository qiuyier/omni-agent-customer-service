package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/faq"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
)

// KnowledgeRepository borrows a caller-owned, already migrated pool. It never
// migrates on read, owns no login, and supports one managed source per database.
type KnowledgeRepository struct{ pool *pgxpool.Pool }

// KnowledgeStore shares the conversation repository's connection lifecycle.
func (r *Repository) KnowledgeStore() (manage.Repository, error) {
	if r == nil {
		return nil, manage.ErrUnavailable
	}
	return NewKnowledgeRepository(r.pool)
}

var _ manage.Repository = (*KnowledgeRepository)(nil)

func NewKnowledgeRepository(pool *pgxpool.Pool) (*KnowledgeRepository, error) {
	if pool == nil {
		return nil, manage.ErrUnavailable
	}
	return &KnowledgeRepository{pool: pool}, nil
}
func (k *KnowledgeRepository) Load(ctx context.Context) (manage.State, error) {
	if ctx == nil || k == nil || k.pool == nil {
		return manage.State{}, manage.ErrUnavailable
	}
	var revision int64
	var raw []byte
	err := k.pool.QueryRow(ctx, `SELECT revision, snapshot FROM customer_service.knowledge_management WHERE singleton=true`).Scan(&revision, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return manage.State{}, nil
	}
	if err != nil || len(raw) > 48<<20 {
		return manage.State{}, manage.ErrUnavailable
	}
	var state manage.State
	if json.Unmarshal(raw, &state) != nil || state.Revision != uint64(revision) || !validKnowledgeState(state) {
		return manage.State{}, manage.ErrUnavailable
	}
	return state, nil
}
func (k *KnowledgeRepository) CompareAndSwap(ctx context.Context, expected uint64, state manage.State) error {
	if ctx == nil || k == nil || k.pool == nil {
		return manage.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if expected >= 1000 || state.Revision != expected+1 || !validKnowledgeState(state) {
		return manage.ErrInvalid
	}
	raw, err := json.Marshal(state)
	if err != nil || len(raw) > 48<<20 {
		return manage.ErrInvalid
	}
	var revision int64
	if expected == 0 {
		err = k.pool.QueryRow(ctx, `INSERT INTO customer_service.knowledge_management(singleton,revision,snapshot)
 VALUES(true,$1,$2) ON CONFLICT(singleton) DO NOTHING RETURNING revision`, int64(state.Revision), raw).Scan(&revision)
	} else {
		err = k.pool.QueryRow(ctx, `UPDATE customer_service.knowledge_management SET revision=$1,snapshot=$2
 WHERE singleton=true AND revision=$3 RETURNING revision`, int64(state.Revision), raw, int64(expected)).Scan(&revision)
	}
	return knowledgeWriteError(err)
}
func knowledgeWriteError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return manage.ErrConflict
	}
	var pgErr *pgconn.PgError
	// A server statement rejection is a known rollback. Transport loss, fatal
	// connection errors and timeouts may have happened after commit: never retry
	// them automatically or tell the caller the previous state is guaranteed.
	if errors.As(err, &pgErr) && pgErr.Severity == "ERROR" {
		return manage.ErrUnavailable
	}
	return manage.ErrOutcomeUnknown
}
func validKnowledgeState(s manage.State) bool {
	if s.Revision == 0 || s.Revision > 1000 || len(s.Events) != int(s.Revision) || len(s.Versions) > 32 {
		return false
	}
	valid := func(v manage.Version) bool { _, id, e := faq.ParseVersion(v.Data); return e == nil && id == v.ID }
	if s.Draft != nil && !valid(*s.Draft) {
		return false
	}
	versions := map[string]bool{}
	for _, v := range s.Versions {
		if versions[v.ID] || !valid(v) {
			return false
		}
		versions[v.ID] = true
	}
	if s.Active != "" && !versions[s.Active] {
		return false
	}
	if s.Draft == nil {
		return false
	}
	for i, e := range s.Events {
		if e.Revision != uint64(i+1) || e.At.IsZero() || !validActor(e.Actor) || !validHash(e.Version) || e.Previous != "" && !validHash(e.Previous) {
			return false
		}
		if e.Action != "save_draft" && e.Action != "publish" && e.Action != "rollback" {
			return false
		}
	}
	return true
}
func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func validActor(s string) bool {
	return len(s) > 0 && len(s) <= 128 && strings.TrimSpace(s) == s && utf8.ValidString(s) && strings.IndexFunc(s, unicode.IsControl) < 0
}
