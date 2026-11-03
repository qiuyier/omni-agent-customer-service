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
)

type trialVersion struct {
	Version string               `json:"version"`
	Matches []faq.ExplainedMatch `json:"matches"`
}

func explainVersion(ctx context.Context, v *manage.Version, q customerservice.KnowledgeQuery) (*trialVersion, error) {
	if v == nil {
		return nil, nil
	}
	retriever, hash, err := faq.ParseVersion(v.Data)
	if err != nil || hash != v.ID {
		return nil, manage.ErrUnavailable
	}
	matches, err := retriever.Explain(ctx, q)
	if err != nil {
		return nil, err
	}
	return &trialVersion{v.ID, matches}, nil
}
func (h *Handler) trial(w http.ResponseWriter, r *http.Request, ctx context.Context, service *manage.Service, actor string) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || len(r.Header.Values("Content-Type")) != 1 {
		failure(w, 415, "json_required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	defer r.Body.Close()
	var request struct {
		Revision *uint64 `json:"revision"`
		Query    string  `json:"query"`
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&request) != nil || d.Decode(new(any)) != io.EOF || request.Revision == nil {
		failure(w, 400, "invalid_query")
		return
	}
	q := customerservice.KnowledgeQuery{Text: request.Query, Limit: customerservice.MaxKnowledgeSnippets}
	empty, _, _ := faq.ParseVersion([]byte("[]"))
	if _, err = empty.Retrieve(ctx, q); err != nil {
		failure(w, 400, "invalid_query")
		return
	}
	s, err := service.View(ctx)
	if err != nil {
		respondError(w, err)
		return
	}
	if s.Revision != *request.Revision {
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
	draft, err := explainVersion(ctx, s.Draft, q)
	if err != nil {
		respondError(w, err)
		return
	}
	published, err := explainVersion(ctx, active, q)
	if err != nil {
		respondError(w, err)
		return
	}
	if id, err := h.auth(ctx, r, manage.Read); err != nil || id != actor {
		failure(w, 403, "access_denied")
		return
	}
	send(w, 200, map[string]any{"revision": s.Revision, "draft": draft, "published": published})
}
