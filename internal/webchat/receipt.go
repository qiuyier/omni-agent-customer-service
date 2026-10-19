package webchat

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

const MessageReceiptPath = "/chat/v1/message-receipt"

// POST carries the original event/content without placing user content in URLs.
// This is strictly a read: no receive, generation, status change or replay.
func (g *Gateway) receipt(w http.ResponseWriter, r *http.Request, claims identity) {
	var request struct {
		EventID string `json:"event_id"`
		Content string `json:"content"`
	}
	if !decode(w, r, &request, "event_id", "content") {
		return
	}
	if !validID(request.EventID) || strings.TrimSpace(request.Content) == "" || !utf8.ValidString(request.Content) || len(request.Content) > MaxContentBytes {
		problem(w, 400, "invalid_request")
		return
	}
	result := struct {
		ConversationID string `json:"conversation_id"`
		EventID        string `json:"event_id"`
		Accepted       bool   `json:"accepted"`
		MessageID      string `json:"message_id,omitempty"`
	}{ConversationID: claims.conversation, EventID: request.EventID}
	err := g.repo.WithinTx(r.Context(), func(tx conversation.Transaction) error {
		current, err := tx.FindConversation(claims.conversation)
		if errors.Is(err, conversation.ErrConversationNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = ownership(current, claims); err != nil {
			return err
		}
		message, err := tx.FindMessageByExternalID(claims.channel, mapped("event", claims.conversation, request.EventID))
		if errors.Is(err, conversation.ErrMessageNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if message.ConversationID() != claims.conversation || message.Sender() != conversation.SenderCustomer || message.SenderID() != claims.customer {
			return customerservice.ErrConversationMismatch
		}
		if message.Content() != request.Content {
			return customerservice.ErrIdempotencyConflict
		}
		result.Accepted = true
		result.MessageID = message.ID()
		return nil
	})
	if err != nil {
		applicationError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
