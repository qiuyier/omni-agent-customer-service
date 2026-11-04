package managehttp

import (
	"context"
	"encoding/json"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/faq"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

type batchCase struct {
	ID       string          `json:"id"`
	Query    string          `json:"query"`
	Expected json.RawMessage `json:"expected_id"`
}
type batchOutcome struct {
	Status string   `json:"status"`
	IDs    []string `json:"matched_ids"`
}
type batchRow struct {
	ID         string       `json:"id"`
	Draft      batchOutcome `json:"draft"`
	Published  batchOutcome `json:"published"`
	Regression bool         `json:"regression"`
}

func validBatchID(s string, n int) bool {
	return s != "" && s == strings.TrimSpace(s) && utf8.ValidString(s) && utf8.RuneCountInString(s) <= n && strings.IndexFunc(s, unicode.IsControl) < 0
}
func batchRetriever(v *manage.Version) (*faq.Retriever, error) {
	if v == nil {
		return nil, nil
	}
	r, id, e := faq.ParseVersion(v.Data)
	if e != nil || id != v.ID {
		return nil, manage.ErrUnavailable
	}
	return r, nil
}
func batchEvaluate(ctx context.Context, r *faq.Retriever, c batchCase) (batchOutcome, error) {
	out := batchOutcome{Status: "unavailable", IDs: []string{}}
	if r == nil {
		return out, nil
	}
	matches, e := r.Retrieve(ctx, customerservice.KnowledgeQuery{Text: c.Query, Limit: customerservice.MaxKnowledgeSnippets})
	if e != nil {
		return out, e
	}
	var expected *string
	json.Unmarshal(c.Expected, &expected)
	pass := expected == nil && len(matches) == 0
	for _, m := range matches {
		out.IDs = append(out.IDs, m.ID)
		if expected != nil && m.ID == *expected {
			pass = true
		}
	}
	out.Status = "fail"
	if pass {
		out.Status = "pass"
	}
	return out, nil
}
func (h *Handler) batch(w http.ResponseWriter, r *http.Request, ctx context.Context, service *manage.Service, actor string) {
	media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || len(r.Header.Values("Content-Type")) != 1 {
		failure(w, 415, "json_required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 132<<10)
	defer r.Body.Close()
	var req struct {
		Revision *uint64     `json:"revision"`
		Cases    []batchCase `json:"cases"`
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil || d.Decode(new(any)) != io.EOF || req.Revision == nil || len(req.Cases) < 1 || len(req.Cases) > 50 {
		failure(w, 400, "invalid_cases")
		return
	}
	empty, _, _ := faq.ParseVersion([]byte("[]"))
	seen := map[string]bool{}
	for _, c := range req.Cases {
		var expected *string
		if !validBatchID(c.ID, 64) || seen[c.ID] || len(c.Expected) == 0 || json.Unmarshal(c.Expected, &expected) != nil || expected != nil && !validBatchID(*expected, 128) {
			failure(w, 400, "invalid_cases")
			return
		}
		seen[c.ID] = true
		if _, e := empty.Retrieve(ctx, customerservice.KnowledgeQuery{Text: c.Query, Limit: 3}); e != nil {
			failure(w, 400, "invalid_cases")
			return
		}
	}
	s, e := service.View(ctx)
	if e != nil {
		respondError(w, e)
		return
	}
	if s.Revision != *req.Revision {
		respondError(w, manage.ErrConflict)
		return
	}
	var active *manage.Version
	for i := range s.Versions {
		if s.Versions[i].ID == s.Active {
			active = &s.Versions[i]
			break
		}
	}
	if s.Active != "" && active == nil {
		respondError(w, manage.ErrUnavailable)
		return
	}
	draft, e := batchRetriever(s.Draft)
	if e != nil {
		respondError(w, e)
		return
	}
	published, e := batchRetriever(active)
	if e != nil {
		respondError(w, e)
		return
	}
	rows := make([]batchRow, 0, len(req.Cases))
	for _, c := range req.Cases {
		a, e := batchEvaluate(ctx, draft, c)
		if e != nil {
			respondError(w, e)
			return
		}
		b, e := batchEvaluate(ctx, published, c)
		if e != nil {
			respondError(w, e)
			return
		}
		rows = append(rows, batchRow{c.ID, a, b, b.Status == "pass" && a.Status == "fail"})
	}
	if id, e := h.auth(ctx, r, manage.Read); e != nil || id != actor {
		failure(w, 403, "access_denied")
		return
	}
	var dv, pv any
	if s.Draft != nil {
		dv = s.Draft.ID
	}
	if active != nil {
		pv = active.ID
	}
	send(w, 200, map[string]any{"revision": s.Revision, "draft_version": dv, "published_version": pv, "rows": rows})
}
