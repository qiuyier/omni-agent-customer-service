package webchat

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

type Message struct {
	ID               string              `json:"id"`
	Role             conversation.Sender `json:"role"`
	Content          string              `json:"content"`
	CreatedAt        time.Time           `json:"created_at"`
	ReplyToMessageID string              `json:"reply_to_message_id,omitempty"`
	ContentTruncated bool                `json:"content_truncated,omitempty"`
}
type History struct {
	ConversationID string              `json:"conversation_id"`
	Status         conversation.Status `json:"status"`
	Messages       []Message           `json:"messages"`
	Truncated      bool                `json:"truncated"`
}
type SendResult struct {
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
	ReplyMessageID string `json:"reply_message_id,omitempty"`
	Outcome        string `json:"outcome"`
}

func (g *Gateway) history(w http.ResponseWriter, r *http.Request, claims identity) {
	result := History{ConversationID: claims.conversation, Status: conversation.StatusBotActive, Messages: []Message{}}
	// Ownership and the history snapshot share the repository's conversation
	// lock. System/tool content and sender identities never leave this adapter.
	err := g.repo.WithinTx(r.Context(), func(tx conversation.Transaction) error {
		current, err := tx.FindConversation(claims.conversation)
		if errors.Is(err, conversation.ErrConversationNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := ownership(current, claims); err != nil {
			return err
		}
		result.Status = current.Status()
		all, err := tx.ListMessages(claims.conversation)
		if err != nil {
			return err
		}
		// The current repository port loads the full list. This limits response
		// size, not database work; real history pagination is a later change.
		remaining := 128 << 10
		for i := len(all) - 1; i >= 0; i-- {
			m := all[i]
			switch m.Sender() {
			case conversation.SenderCustomer, conversation.SenderBot, conversation.SenderHumanAgent:
			default:
				continue
			}
			if len(result.Messages) == 100 {
				result.Truncated = true
				break
			}
			content := m.Content()
			cut := false
			if len(content) > 16<<10 {
				content = content[:16<<10]
				for !utf8.ValidString(content) {
					content = content[:len(content)-1]
				}
				cut = true
			}
			if len(content) > remaining {
				result.Truncated = true
				break
			}
			remaining -= len(content)
			result.Messages = append(result.Messages, Message{m.ID(), m.Sender(), content, m.CreatedAt(), m.ReplyToMessageID(), cut})
		}
		for i, j := 0, len(result.Messages)-1; i < j; i, j = i+1, j-1 {
			result.Messages[i], result.Messages[j] = result.Messages[j], result.Messages[i]
		}
		return nil
	})
	if err != nil {
		applicationError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func (g *Gateway) send(w http.ResponseWriter, r *http.Request, claims identity) {
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
	// Check ownership even on retries. ReceiveMessage independently checks the
	// same immutable customer/channel identifiers inside its write transaction.
	current, err := g.repo.FindConversation(r.Context(), claims.conversation)
	if err == nil {
		err = ownership(current, claims)
	}
	if err != nil && !errors.Is(err, conversation.ErrConversationNotFound) {
		applicationError(w, err)
		return
	}
	received, err := g.app.ReceiveMessage(r.Context(), customerservice.ReceiveMessageCommand{
		ConversationID: claims.conversation, CustomerID: claims.customer, Channel: claims.channel,
		ExternalMessageID: mapped("event", claims.conversation, request.EventID), Content: request.Content,
	})
	if err != nil {
		applicationError(w, err)
		return
	}
	result := SendResult{ConversationID: claims.conversation, MessageID: received.MessageID}
	if received.Status == conversation.StatusClosed {
		// A duplicate of an accepted message may be replayed after closure.
		// Acknowledge it without starting generation; history remains readable.
		result.Outcome = "superseded"
		writeJSON(w, 200, result)
		return
	}
	if received.Status == conversation.StatusWaitingHuman || received.Status == conversation.StatusHumanActive {
		result.Outcome = "waiting_human"
		writeJSON(w, 200, result)
		return
	}
	// A duplicate receive alone does not mean the bot replied. Explicitly retry
	// generation with the original trigger; Service returns a persisted reply
	// without another model call or rejects a superseded trigger safely.
	reply, err := g.app.GenerateBotReply(r.Context(), customerservice.GenerateBotReplyCommand{ConversationID: claims.conversation, TriggerMessageID: received.MessageID})
	if errors.Is(err, customerservice.ErrReplySuperseded) || errors.Is(err, customerservice.ErrInvalidReplyTrigger) {
		result.Outcome = "superseded"
		writeJSON(w, 200, result)
		return
	}
	if err != nil {
		applicationError(w, err)
		return
	}
	result.Outcome = "replied"
	result.ReplyMessageID = reply.MessageID
	writeJSON(w, 200, result)
}
