package webchat

import (
	"net/http"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
)

func (g *Gateway) searchDesk(w http.ResponseWriter, r *http.Request, a *trustedAgent) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var q conversation.DeskSearchQuery
	if !decode(w, r, &q, "keyword", "conversation_id", "status", "after") {
		return
	}
	if !q.Valid() {
		problem(w, 400, "invalid_request")
		return
	}
	reader, ok := g.repo.(conversation.DeskSearcher)
	if !ok {
		problem(w, 404, "not_found")
		return
	}
	page, err := reader.SearchDesk(r.Context(), a.channel, a.id, q)
	if err != nil {
		applicationError(w, err)
		return
	}
	writeJSON(w, 200, page)
}
