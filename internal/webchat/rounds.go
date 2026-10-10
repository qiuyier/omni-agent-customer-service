package webchat

import (
	"errors"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"net/http"
	"time"
)

const NextRoundPath = "/chat/v1/consultations"
const PreviousRoundPath = "/chat/v1/previous-consultation"

// The old capability is an idempotency key, not an instruction to reopen or
// advance whichever round happens to be current when a retry arrives.
func (g *Gateway) nextRound(w http.ResponseWriter, r *http.Request, claims identity) {
	var request struct{}
	if !decode(w, r, &request) {
		return
	}
	next := claims
	next.conversation = mapped("next-conversation", claims.conversation)
	err := g.repo.WithinTx(r.Context(), func(tx conversation.Transaction) error {
		old, err := tx.FindConversation(claims.conversation)
		if err != nil {
			return err
		}
		if err = ownership(old, claims); err != nil {
			return err
		}
		if old.Status() != conversation.StatusClosed {
			return conversation.ErrInvalidTransition
		}
		existing, err := tx.FindConversation(next.conversation)
		if err == nil {
			return ownership(existing, next)
		}
		if !errors.Is(err, conversation.ErrConversationNotFound) {
			return err
		}
		now := g.now().UTC().Truncate(time.Microsecond)
		floor := old.UpdatedAt().Truncate(time.Microsecond).Add(time.Microsecond)
		if now.Before(floor) {
			now = floor
		}
		created, err := conversation.New(conversation.CreateParams{ID: next.conversation, CustomerID: next.customer, Channel: next.channel, CreatedAt: now})
		if err != nil {
			return err
		}
		return tx.InsertConversation(created)
	})
	if errors.Is(err, conversation.ErrInvalidTransition) {
		problem(w, 409, "state_conflict")
		return
	}
	if err != nil {
		applicationError(w, err)
		return
	}
	writeJSON(w, 200, struct {
		ConversationID string `json:"conversation_id"`
	}{next.conversation})
}

func (g *Gateway) previousRound(w http.ResponseWriter, r *http.Request, claims identity) {
	current, err := g.repo.FindConversation(r.Context(), claims.conversation)
	if errors.Is(err, conversation.ErrConversationNotFound) {
		writeJSON(w, 200, map[string]any{"history": nil})
		return
	}
	if err == nil {
		err = ownership(current, claims)
	}
	if err != nil {
		applicationError(w, err)
		return
	}
	previous, err := g.repo.(conversation.Rounds).FindLatestRound(r.Context(), claims.customer, claims.channel, current.CreatedAt())
	if errors.Is(err, conversation.ErrConversationNotFound) {
		writeJSON(w, 200, map[string]any{"history": nil})
		return
	}
	if err != nil {
		applicationError(w, err)
		return
	}
	claims.conversation = previous.ID()
	result, err := g.readHistory(r.Context(), claims)
	if err != nil {
		applicationError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"history": result})
}
