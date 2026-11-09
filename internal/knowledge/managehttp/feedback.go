package managehttp

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"

	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/feedback"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
)

func (h *Handler) feedback(w http.ResponseWriter, r *http.Request, ctx context.Context) {
	write := r.URL.Path == "/internal/knowledge/feedback/status"
	permission := manage.Read
	if write {
		permission = manage.Edit
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
		failure(w, 400, "invalid_request")
		return
	}
	if write && (r.Method != "POST" || r.Header.Get("Origin") != h.origin) || !write && r.Method != "GET" {
		failure(w, 403, "request_not_allowed")
		return
	}
	actor, err := h.auth(ctx, r, permission)
	if err != nil || actor == "" {
		failure(w, 403, "access_denied")
		return
	}
	repo, ok := h.repo.(feedback.Repository)
	if !ok {
		failure(w, 503, "storage_unavailable")
		return
	}
	if !write {
		items, err := repo.ListFeedback(ctx)
		if err != nil {
			respondError(w, err)
			return
		}
		if id, e := h.auth(ctx, r, permission); e != nil || id != actor {
			failure(w, 403, "access_denied")
			return
		}
		send(w, 200, map[string]any{"items": items})
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || len(r.Header.Values("Content-Type")) != 1 {
		failure(w, 415, "json_required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	defer r.Body.Close()
	var change feedback.Change
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&change) != nil || decoder.Decode(new(any)) != io.EOF {
		failure(w, 400, "invalid_request")
		return
	}
	if id, e := h.auth(ctx, r, permission); e != nil || id != actor {
		failure(w, 403, "access_denied")
		return
	}
	if err = repo.UpdateFeedback(ctx, change); err != nil {
		respondError(w, err)
		return
	}
	send(w, 200, map[string]bool{"updated": true})
}
