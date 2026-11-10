package postgres

import (
	"context"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
)

func (k *KnowledgeRepository) FeedbackCounts(ctx context.Context) (active, expired int, err error) {
	err = k.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE expires_at>now()),count(*) FILTER (WHERE expires_at<=now()) FROM customer_service.knowledge_feedback`).Scan(&active, &expired)
	if err != nil {
		return 0, 0, manage.ErrUnavailable
	}
	return
}
