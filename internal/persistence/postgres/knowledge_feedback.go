package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/feedback"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
)

var _ feedback.Repository = (*KnowledgeRepository)(nil)

func (k *KnowledgeRepository) PurgeFeedback(ctx context.Context) error {
	_, err := k.pool.Exec(ctx, `DELETE FROM customer_service.knowledge_feedback WHERE expires_at<=now()`)
	if err != nil {
		return manage.ErrUnavailable
	}
	return nil
}
func (k *KnowledgeRepository) RecordFeedback(ctx context.Context, question string) error {
	// Re-sanitize at the persistence boundary; never accept raw caller input.
	question = feedback.Sanitize(question)
	if question == "" {
		return manage.ErrInvalid
	}
	tx, err := k.pool.Begin(ctx)
	if err != nil {
		return manage.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	// Serialize the bounded queue, including concurrent insert/cleanup. Lock
	// scope is only this table; no conversation or managed FAQ lock is taken.
	if _, err = tx.Exec(ctx, `LOCK TABLE customer_service.knowledge_feedback IN EXCLUSIVE MODE`); err != nil {
		return manage.ErrUnavailable
	}
	if _, err = tx.Exec(ctx, `DELETE FROM customer_service.knowledge_feedback WHERE expires_at<=now()`); err != nil {
		return manage.ErrUnavailable
	}
	tag, err := tx.Exec(ctx, `UPDATE customer_service.knowledge_feedback SET count=least(count+1,1000000),last_seen=now() WHERE question=$1`, question)
	if err != nil {
		return manage.ErrUnavailable
	}
	if tag.RowsAffected() == 0 {
		tag, err = tx.Exec(ctx, `INSERT INTO customer_service.knowledge_feedback(id,question) SELECT $1,$2 WHERE (SELECT count(*) FROM customer_service.knowledge_feedback)<500`, uuid.NewString(), question)
		if err != nil {
			return manage.ErrUnavailable
		}
	}
	if err = knowledgeWriteError(tx.Commit(ctx)); err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return feedback.ErrFull
	}
	return nil
}
func (k *KnowledgeRepository) ListFeedback(ctx context.Context) ([]feedback.Item, error) {
	rows, err := k.pool.Query(ctx, `SELECT id,question,count,revision,status,faq_id,first_seen,last_seen,expires_at FROM customer_service.knowledge_feedback WHERE expires_at>now() ORDER BY last_seen DESC,id LIMIT 500`)
	if err != nil {
		return nil, manage.ErrUnavailable
	}
	defer rows.Close()
	items := make([]feedback.Item, 0)
	for rows.Next() {
		var item feedback.Item
		if err = rows.Scan(&item.ID, &item.Question, &item.Count, &item.Revision, &item.Status, &item.FAQID, &item.FirstSeen, &item.LastSeen, &item.ExpiresAt); err != nil {
			return nil, manage.ErrUnavailable
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, manage.ErrUnavailable
	}
	return items, nil
}
func (k *KnowledgeRepository) UpdateFeedback(ctx context.Context, c feedback.Change) error {
	if _, err := uuid.Parse(c.ID); err != nil || c.Revision < 1 || c.Revision >= 1000000 || !utf8.ValidString(c.FAQID) || utf8.RuneCountInString(c.FAQID) > 128 {
		return manage.ErrInvalid
	}
	if c.Status != "pending" && c.Status != "ignored" && c.Status != "resolved" || (c.Status == "resolved") != (c.FAQID != "") {
		return manage.ErrInvalid
	}
	tx, err := k.pool.Begin(ctx)
	if err != nil {
		return manage.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if c.Status == "resolved" {
		// SHARE prevents the publication from changing during this transaction.
		var raw []byte
		err = tx.QueryRow(ctx, `SELECT snapshot FROM customer_service.knowledge_management WHERE singleton=true FOR SHARE`).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return manage.ErrInvalid
		}
		if err != nil {
			return manage.ErrUnavailable
		}
		var state manage.State
		if json.Unmarshal(raw, &state) != nil || !validKnowledgeState(state) {
			return manage.ErrUnavailable
		}
		found := false
		for _, v := range state.Versions {
			if v.ID == state.Active {
				var entries []struct {
					ID string `json:"id"`
				}
				if json.Unmarshal(v.Data, &entries) != nil {
					return manage.ErrUnavailable
				}
				for _, entry := range entries {
					if entry.ID == c.FAQID {
						found = true
					}
				}
			}
		}
		if !found {
			return manage.ErrInvalid
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE customer_service.knowledge_feedback SET status=$1,faq_id=$2,revision=revision+1 WHERE id=$3 AND revision=$4 AND expires_at>now()`, c.Status, c.FAQID, c.ID, c.Revision)
	if err != nil {
		return knowledgeWriteError(err)
	}
	if tag.RowsAffected() != 1 {
		return manage.ErrConflict
	}
	return knowledgeWriteError(tx.Commit(ctx))
}
