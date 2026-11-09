//go:build browserlab

package adminauth

import (
	"context"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/feedback"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
	"sync"
	"time"
)

// Ephemeral fictional fixture, never part of production.
type feedbackLab struct {
	memory
	lock  sync.Mutex
	items []feedback.Item
}

func newFeedbackLab() *feedbackLab {
	return &feedbackLab{items: []feedback.Item{{ID: "00000000-0000-4000-8000-000000000001", Question: "如何修改通知方式？邮箱 [已脱敏]", Revision: 1, Count: 2, Status: "pending", FirstSeen: time.Now(), LastSeen: time.Now(), ExpiresAt: time.Now().Add(feedback.Retention)}}}
}
func (m *feedbackLab) RecordFeedback(context.Context, string) error { return nil }
func (m *feedbackLab) PurgeFeedback(context.Context) error          { return nil }
func (m *feedbackLab) ListFeedback(context.Context) ([]feedback.Item, error) {
	m.lock.Lock()
	defer m.lock.Unlock()
	return append([]feedback.Item{}, m.items...), nil
}
func (m *feedbackLab) UpdateFeedback(_ context.Context, c feedback.Change) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	for i := range m.items {
		if m.items[i].ID == c.ID && m.items[i].Revision == c.Revision {
			m.items[i].Status = c.Status
			m.items[i].FAQID = c.FAQID
			m.items[i].Revision++
			return nil
		}
	}
	return manage.ErrConflict
}
