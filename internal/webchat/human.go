package webchat

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"github.com/google/uuid"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"golang.org/x/time/rate"
	"net/http"
	"strings"
	"unicode/utf8"
)

type Agent struct {
	SourceID string `json:"source_id"`
	ID       string `json:"id"`
	Secret   string `json:"secret"`
}

// ValidateAgentConfiguration performs only trusted configuration validation.
func ValidateAgentConfiguration(agents []Agent, hosts []Host) error {
	h, err := configureHosts(hosts)
	if err != nil {
		return err
	}
	_, err = configureAgents(agents, h)
	return err
}

type trustedAgent struct {
	channel, id string
	digest      [32]byte
	limit       *rate.Limiter
}

func configureAgents(input []Agent, hosts []trustedHost) ([]trustedAgent, error) {
	if len(input) > 32 {
		return nil, errors.New("webchat: at most 32 agents")
	}
	out := []trustedAgent{}
	for _, a := range input {
		if !validID(a.ID) || len(a.Secret) < 32 || len(a.Secret) > 128 || strings.ContainsAny(a.Secret, " \r\n\t") || strings.HasPrefix(a.Secret, "chat_") {
			return nil, errors.New("webchat: invalid agent configuration")
		}
		digest := sha256.Sum256([]byte(a.Secret))
		found := false
		for _, h := range hosts {
			if h.source == a.SourceID {
				found = true
			}
			if h.digest == digest {
				return nil, errors.New("webchat: agent credential must be independent")
			}
		}
		if !found {
			return nil, errors.New("webchat: agent host is unknown")
		}
		channel, id := mapped("channel", a.SourceID), mapped("agent", a.SourceID, a.ID)
		for _, v := range out {
			if v.digest == digest || (v.channel == channel && v.id == id) {
				return nil, errors.New("webchat: duplicate agent")
			}
		}
		out = append(out, trustedAgent{channel, id, digest, rate.NewLimiter(5, 30)})
	}
	return out, nil
}

type deskState struct {
	FirstUnreadID    string              `json:"first_unread_id,omitempty"`
	HasOlder         bool                `json:"has_older"`
	HasNewer         bool                `json:"has_newer"`
	AttentionEnabled bool                `json:"attention_enabled"`
	Unread           int                 `json:"unread_count"`
	UnreadIDs        []string            `json:"unread_message_ids,omitempty"`
	Latest           *Message            `json:"latest_message,omitempty"`
	ID               string              `json:"conversation_id"`
	Status           conversation.Status `json:"status"`
	Revision         uint64              `json:"revision"`
	Messages         []Message           `json:"messages,omitempty"`
}

var errDeskDenied = errors.New("desk denied")
var errDeskConflict = errors.New("desk conflict")

func (g *Gateway) handoff(w http.ResponseWriter, r *http.Request, claims identity) {
	enabled := false
	for _, a := range g.agents {
		if a.channel == claims.channel {
			enabled = true
		}
	}
	if !enabled {
		problem(w, 404, "not_found")
		return
	}
	var body struct{}
	if !decode(w, r, &body) {
		return
	}
	var result deskState
	err := g.repo.WithinTx(r.Context(), func(tx conversation.Transaction) error {
		c, err := tx.FindConversation(claims.conversation)
		if errors.Is(err, conversation.ErrConversationNotFound) {
			c, err = conversation.New(conversation.CreateParams{ID: claims.conversation, CustomerID: claims.customer, Channel: claims.channel, CreatedAt: g.now()})
			if err == nil {
				err = tx.InsertConversation(c)
			}
		}
		if err != nil {
			return err
		}
		if err = ownership(c, claims); err != nil {
			return err
		}
		if c.Status() == conversation.StatusClosed {
			return conversation.ErrConversationClosed
		}
		if c.Status() == conversation.StatusBotActive {
			if err = c.RequestHandoff(g.now()); err != nil {
				return err
			}
			if err = tx.UpdateConversation(c); err != nil {
				return err
			}
		}
		result = deskState{ID: c.ID(), Status: c.Status(), Revision: c.Revision()}
		return nil
	})
	if err != nil {
		applicationError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
func (g *Gateway) desk(w http.ResponseWriter, r *http.Request) {
	if len(g.agents) == 0 {
		problem(w, 404, "not_found")
		return
	}
	token, ok := bearer(r)
	if !ok {
		problem(w, 401, "unauthorized")
		return
	}
	digest := sha256.Sum256([]byte(token))
	var a *trustedAgent
	for i := range g.agents {
		if subtle.ConstantTimeCompare(digest[:], g.agents[i].digest[:]) == 1 {
			a = &g.agents[i]
		}
	}
	if a == nil {
		problem(w, 401, "unauthorized")
		return
	}
	if !a.limit.Allow() {
		problem(w, 429, "busy")
		return
	}
	g.mu.Lock()
	if g.active >= g.config.MaxConcurrent {
		g.mu.Unlock()
		problem(w, 429, "busy")
		return
	}
	g.active++
	g.mu.Unlock()
	defer func() { g.mu.Lock(); g.active--; g.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(r.Context(), g.config.RequestTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	if r.URL.Path == "/internal/chat/desk/queue" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, "GET")
			return
		}
		rows, err := g.repo.(conversation.HumanQueue).ListHumanQueue(ctx, a.channel, a.id)
		if err != nil {
			applicationError(w, err)
			return
		}
		out := []deskState{}
		for _, c := range rows {
			row := deskState{ID: c.ID(), Status: c.Status(), Revision: c.Revision()}
			if err := g.fillAttention(ctx, a, &row); err != nil {
				if errors.Is(err, conversation.ErrAttentionDenied) || errors.Is(err, conversation.ErrConversationNotFound) {
					continue
				}
				applicationError(w, err)
				return
			}
			out = append(out, row)
		}
		writeJSON(w, 200, map[string]any{"items": out, "limit": 100})
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var body struct {
		ID         string   `json:"conversation_id"`
		Action     string   `json:"action"`
		Revision   uint64   `json:"revision"`
		Event      string   `json:"event_id"`
		Content    string   `json:"content"`
		MessageIDs []string `json:"message_ids"`
		Before     string   `json:"before_message_id"`
		After      string   `json:"after_message_id"`
		From       string   `json:"from_message_id"`
	}
	if !decode(w, r, &body, "conversation_id", "action", "revision", "event_id", "content", "message_ids", "before_message_id", "after_message_id", "from_message_id") {
		return
	}
	if !validID(body.ID) || (body.Action != "read" && body.Action != "claim" && body.Action != "history" && body.Action != "reply" && body.Action != "close") {
		problem(w, 400, "invalid_request")
		return
	}
	count := 0
	for _, cursor := range []string{body.Before, body.After, body.From} {
		if cursor != "" {
			count++
			if !validID(cursor) {
				problem(w, 400, "invalid_request")
				return
			}
		}
	}
	if count > 1 || (count > 0 && body.Action != "history") {
		problem(w, 400, "invalid_request")
		return
	}
	if body.Action == "reply" && (!validID(body.Event) || strings.TrimSpace(body.Content) == "" || !utf8.ValidString(body.Content) || len(body.Content) > MaxContentBytes) {
		problem(w, 400, "invalid_request")
		return
	}
	if body.Action == "read" {
		port, ok := g.repo.(conversation.DeskAttention)
		if !ok {
			problem(w, 404, "not_found")
			return
		}
		if len(body.MessageIDs) == 0 || len(body.MessageIDs) > 100 {
			problem(w, 400, "invalid_request")
			return
		}
		_, err := port.DeskAttention(ctx, body.ID, a.channel, a.id, body.MessageIDs)
		if errors.Is(err, conversation.ErrAttentionDenied) {
			problem(w, 403, "forbidden")
			return
		}
		if errors.Is(err, conversation.ErrInvalidArgument) {
			problem(w, 400, "invalid_request")
			return
		}
		if err != nil {
			applicationError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"conversation_id": body.ID, "acknowledged": true})
		return
	}
	if len(body.MessageIDs) > 0 {
		problem(w, 400, "invalid_request")
		return
	}
	var result deskState
	err := g.repo.WithinTx(ctx, func(tx conversation.Transaction) error {
		c, err := tx.FindConversation(body.ID)
		if err != nil {
			return err
		}
		if c.Channel() != a.channel {
			return errDeskDenied
		}
		if c.Status() != conversation.StatusWaitingHuman && c.AgentID() != a.id {
			return errDeskDenied
		}
		if body.Action == "history" {
			if pager, ok := tx.(conversation.MessagePager); ok {
				page, err := pager.PageMessages(c.ID(), body.Before, body.After, body.From)
				if err != nil {
					return err
				}
				result.HasOlder = page.HasOlder
				result.HasNewer = page.HasNewer
				result.Messages = []Message{}
				for _, m := range page.Messages {
					result.Messages = append(result.Messages, Message{ID: m.ID(), Role: m.Sender(), Content: m.Content(), CreatedAt: m.CreatedAt(), ContentTruncated: page.Truncated[m.ID()]})
				}
			} else {
				all, err := tx.ListMessages(c.ID())
				if err != nil {
					return err
				}
				if err = deskHistory(all, body.Before, body.After, body.From, &result); err != nil {
					return err
				}
			}
		} else if body.Action == "reply" {
			if c.AgentID() != a.id {
				return errDeskDenied
			}
			event := mapped("agent-event", c.ID(), a.id, body.Event)
			old, err := tx.FindMessageByExternalID(c.Channel(), event)
			if err == nil {
				if old.Content() != body.Content {
					return errDeskConflict
				}
			} else {
				if !errors.Is(err, conversation.ErrMessageNotFound) {
					return err
				}
				if err = c.CanAcceptMessage(conversation.SenderHumanAgent); err != nil {
					return err
				}
				m, err := conversation.NewMessage(conversation.MessageParams{ID: uuid.NewString(), ConversationID: c.ID(), ExternalMessageID: event, Sender: conversation.SenderHumanAgent, SenderID: a.id, Content: body.Content, CreatedAt: g.now()})
				if err != nil {
					return err
				}
				if err = tx.InsertMessage(c.Channel(), m); err != nil {
					return err
				}
			}
		} else {
			// Same-state retries are safe; stale actions must not affect a later state.
			already := (body.Action == "claim" && c.Status() == conversation.StatusHumanActive && c.AgentID() == a.id) || (body.Action == "close" && c.Status() == conversation.StatusClosed && c.AgentID() == a.id)
			if !already {
				if body.Revision != c.Revision() {
					return errDeskConflict
				}
				if body.Action == "claim" {
					err = c.AssignAgent(a.id, g.now())
				} else {
					if c.Status() != conversation.StatusHumanActive || c.AgentID() != a.id {
						return errDeskDenied
					}
					err = c.Close(g.now())
				}
				if err != nil {
					return err
				}
				if err = tx.UpdateConversation(c); err != nil {
					return err
				}
			}
		}
		result.ID = c.ID()
		result.Status = c.Status()
		result.Revision = c.Revision()
		return nil
	})
	if errors.Is(err, errHistoryCursor) {
		problem(w, 400, "invalid_cursor")
		return
	}
	if errors.Is(err, errDeskDenied) {
		problem(w, 403, "forbidden")
		return
	}
	if errors.Is(err, errDeskConflict) || errors.Is(err, conversation.ErrInvalidTransition) {
		problem(w, 409, "state_conflict")
		return
	}
	if err != nil {
		applicationError(w, err)
		return
	}
	if err := g.fillAttention(ctx, a, &result); err != nil {
		if errors.Is(err, conversation.ErrAttentionDenied) {
			problem(w, 403, "forbidden")
			return
		}
		applicationError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func (g *Gateway) fillAttention(ctx context.Context, a *trustedAgent, result *deskState) error {
	port, ok := g.repo.(conversation.DeskAttention)
	if !ok {
		return nil
	}
	var info conversation.Attention
	var err error
	if reader, ok := g.repo.(conversation.DeskAttentionReader); ok {
		ids := make([]string, 0, len(result.Messages))
		for _, m := range result.Messages {
			if m.Role == conversation.SenderCustomer {
				ids = append(ids, m.ID)
			}
		}
		info, err = reader.ReadDeskAttention(ctx, result.ID, a.channel, a.id, ids)
	} else {
		info, err = port.DeskAttention(ctx, result.ID, a.channel, a.id, nil)
	}
	if err != nil {
		return err
	}
	result.AttentionEnabled = true
	result.Unread = info.Unread
	result.FirstUnreadID = info.FirstUnreadID
	for _, m := range result.Messages {
		if m.Role == conversation.SenderCustomer && !m.ContentTruncated && !info.Seen[m.ID] {
			result.UnreadIDs = append(result.UnreadIDs, m.ID)
		}
	}
	if info.HasLatest {
		m := info.Latest
		content := []rune(m.Content())
		truncated := len(content) > 160
		if truncated {
			content = content[:160]
		}
		result.Latest = &Message{ID: m.ID(), Role: m.Sender(), Content: string(content), CreatedAt: m.CreatedAt(), ContentTruncated: truncated}
	}
	return nil
}
