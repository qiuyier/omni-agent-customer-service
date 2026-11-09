package managehttp

import (
	"context"
	"encoding/json"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/feedback"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
	"io"
	"mime"
	"net/http"
	"time"
	"unicode/utf8"
)

// Read-only verification uses stored sanitized text, never caller-supplied
// questions. Results are a versioned snapshot, not proof of answer quality.
func (h *Handler) checkFeedback(w http.ResponseWriter, r *http.Request, ctx context.Context, repo feedback.Repository, actor string) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || len(r.Header.Values("Content-Type")) != 1 {
		failure(w, 415, "json_required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	defer r.Body.Close()
	var req struct {
		ID           string  `json:"id"`
		Revision     *uint64 `json:"revision"`
		ItemRevision int64   `json:"item_revision"`
		Expected     string  `json:"expected_id"`
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil || d.Decode(new(any)) != io.EOF || req.Revision == nil || req.ItemRevision < 1 || req.ID == "" || len(req.ID) > 128 || req.Expected == "" || !utf8.ValidString(req.Expected) || utf8.RuneCountInString(req.Expected) > 128 {
		failure(w, 400, "invalid_request")
		return
	}
	items, err := repo.ListFeedback(ctx)
	if err != nil {
		respondError(w, err)
		return
	}
	var item *feedback.Item
	for i := range items {
		if items[i].ID == req.ID && items[i].ExpiresAt.After(time.Now()) {
			item = &items[i]
			break
		}
	}
	if item == nil {
		failure(w, 404, "feedback_unavailable")
		return
	}
	if item.Revision != req.ItemRevision {
		respondError(w, manage.ErrConflict)
		return
	}
	state, err := h.repo.Load(ctx)
	if err != nil {
		respondError(w, err)
		return
	}
	if state.Revision != *req.Revision {
		respondError(w, manage.ErrConflict)
		return
	}
	var active *manage.Version
	for i := range state.Versions {
		if state.Versions[i].ID == state.Active {
			active = &state.Versions[i]
			break
		}
	}
	if active == nil {
		failure(w, 409, "published_unavailable")
		return
	}
	var entries []struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(active.Data, &entries) != nil {
		respondError(w, manage.ErrUnavailable)
		return
	}
	found := false
	for _, e := range entries {
		if e.ID == req.Expected {
			found = true
		}
	}
	if !found {
		failure(w, 400, "expected_faq_unavailable")
		return
	}
	result, err := explainVersion(ctx, active, customerservice.KnowledgeQuery{Text: item.Question, Limit: customerservice.MaxKnowledgeSnippets})
	if err != nil {
		respondError(w, err)
		return
	}
	outcome := "miss"
	if len(result.Matches) > 0 {
		outcome = "other"
	}
	for _, m := range result.Matches {
		if m.ID == req.Expected {
			outcome = "matched"
		}
	}
	latest, err := h.repo.Load(ctx)
	if err != nil {
		respondError(w, err)
		return
	}
	if latest.Revision != state.Revision {
		respondError(w, manage.ErrConflict)
		return
	}
	latestItems, e := repo.ListFeedback(ctx)
	if e != nil {
		respondError(w, e)
		return
	}
	unchanged := false
	for _, candidate := range latestItems {
		if candidate.ID == item.ID && candidate.Revision == item.Revision && candidate.ExpiresAt.After(time.Now()) {
			unchanged = true
		}
	}
	if !unchanged {
		respondError(w, manage.ErrConflict)
		return
	}
	if !item.ExpiresAt.After(time.Now()) {
		failure(w, 404, "feedback_unavailable")
		return
	}
	if id, e := h.auth(ctx, r, manage.Read); e != nil || id != actor {
		failure(w, 403, "access_denied")
		return
	}
	send(w, 200, map[string]any{"id": item.ID, "item_revision": item.Revision, "revision": state.Revision, "expected_id": req.Expected, "outcome": outcome, "published": result})
}
