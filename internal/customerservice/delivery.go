package customerservice

import (
	"context"
	"errors"
	"strings"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
)

// DeliveryCheck is a read-only snapshot request, not a lease or authorization token.
type DeliveryCheck struct {
	ConversationID   string
	CustomerID       string
	Channel          string
	TriggerMessageID string
	ReplyMessageID   string
	Content          string
}
type DeliveryEligibility struct {
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason"`
}

// CheckDeliveryEligibility reads ownership, trigger and saved reply under the
// same conversation lock used by writers. No external call runs under this lock.
// A successful check cannot prevent a transition after the transaction ends.
func (s *Service) CheckDeliveryEligibility(ctx context.Context, q DeliveryCheck) (DeliveryEligibility, error) {
	if ctx == nil {
		return DeliveryEligibility{}, ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return DeliveryEligibility{}, err
	}
	for _, v := range []string{q.ConversationID, q.CustomerID, q.Channel, q.TriggerMessageID, q.ReplyMessageID, q.Content} {
		if strings.TrimSpace(v) == "" {
			return DeliveryEligibility{}, ErrInvalidInput
		}
	}
	result := DeliveryEligibility{Reason: "reply_mismatch"}
	err := s.repository.WithinTx(ctx, func(tx conversation.Transaction) error {
		current, err := tx.FindConversation(q.ConversationID)
		if err != nil {
			return err
		}
		if current.CustomerID() != q.CustomerID || current.Channel() != q.Channel {
			result.Reason = "ownership_mismatch"
			return nil
		}
		if current.Status() == conversation.StatusClosed {
			result.Reason = "conversation_closed"
			return nil
		}
		if current.Status() != conversation.StatusBotActive {
			result.Reason = "not_bot_owned"
			return nil
		}
		messages, err := tx.ListMessages(q.ConversationID)
		if err != nil {
			return err
		}
		latest := ""
		triggerExists := false
		for _, m := range messages {
			if m.Sender() == conversation.SenderCustomer {
				latest = m.ID()
				if m.ID() == q.TriggerMessageID {
					triggerExists = true
				}
			}
		}
		if !triggerExists {
			result.Reason = "invalid_trigger"
			return nil
		}
		if latest != q.TriggerMessageID {
			result.Reason = "superseded"
			return nil
		}
		reply, err := tx.FindReplyByTriggerMessageID(q.TriggerMessageID)
		if errors.Is(err, conversation.ErrReplyNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if reply.ID() != q.ReplyMessageID || reply.ConversationID() != q.ConversationID || reply.ReplyToMessageID() != q.TriggerMessageID || reply.Sender() != conversation.SenderBot || reply.Content() != q.Content {
			return nil
		}
		result = DeliveryEligibility{Eligible: true, Reason: "eligible"}
		return nil
	})
	if err != nil {
		return DeliveryEligibility{}, err
	}
	return result, nil
}
