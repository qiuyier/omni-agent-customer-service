// Package managehttp is an opt-in host-composed admin boundary. It is never
// mounted automatically on the customer or staff gateway.
package managehttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
)

type Authorize func(context.Context, *http.Request, manage.Permission) (string, error)
type Handler struct {
	repo         manage.Repository
	auth         Authorize
	origin, host string
	gate         chan struct{}
}

// Authorize must verify the real session, expiry/revocation and explicit
// knowledge permission; never infer rights from request body/header role claims.
func New(repo manage.Repository, auth Authorize, origin string) (*Handler, error) {
	u, e := url.Parse(origin)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Scheme != "https" && !(u.Scheme == "http" && net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback()) || repo == nil || auth == nil {
		return nil, manage.ErrInvalid
	}
	return &Handler{repo: repo, auth: auth, origin: origin, host: u.Host, gate: make(chan struct{}, 8)}, nil
}
func send(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, status int, code string) {
	send(w, status, map[string]string{"code": code})
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Host != h.host || len(r.Header.Values("Origin")) > 1 || r.Header.Get("Sec-Fetch-Site") == "cross-site" || r.Header.Get("Origin") != "" && r.Header.Get("Origin") != h.origin {
		failure(w, 403, "request_not_allowed")
		return
	}
	select {
	case h.gate <- struct{}{}:
		defer func() { <-h.gate }()
	default:
		failure(w, 429, "busy")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	permission := manage.Publish
	switch r.URL.Path {
	case "/internal/knowledge/state":
		permission = manage.Read
		if r.Method != "GET" {
			failure(w, 405, "method_not_allowed")
			return
		}
	case "/internal/knowledge/draft":
		permission = manage.Edit
	case "/internal/knowledge/publish", "/internal/knowledge/rollback":
	default:
		failure(w, 404, "not_found")
		return
	}
	if r.URL.RawQuery != "" || r.URL.RawPath != "" {
		failure(w, 400, "invalid_request")
		return
	}
	if permission != manage.Read && (r.Method != "POST" || r.Header.Get("Origin") != h.origin) {
		failure(w, 403, "request_not_allowed")
		return
	}
	actor, e := h.auth(ctx, r, permission)
	if e != nil || actor == "" {
		failure(w, 403, "access_denied")
		return
	}
	service, _ := manage.New(h.repo, func(c context.Context, p manage.Permission) (string, error) {
		id, e := h.auth(c, r, p)
		if e != nil || id != actor {
			return "", manage.ErrDenied
		}
		return id, nil
	})
	if permission == manage.Read {
		s, e := service.View(ctx)
		if e != nil {
			respondError(w, e)
			return
		}
		if id, e := h.auth(ctx, r, permission); e != nil || id != actor {
			failure(w, 403, "access_denied")
			return
		}
		// A bounded presentation DTO, not the persistence snapshot or base64 blobs.
		ids := make([]string, 0, len(s.Versions))
		for _, v := range s.Versions {
			ids = append(ids, v.ID)
		}
		var draft any
		if s.Draft != nil {
			draft = struct {
				Version string          `json:"version"`
				Entries json.RawMessage `json:"entries"`
			}{s.Draft.ID, s.Draft.Data}
		}
		send(w, 200, map[string]any{"revision": s.Revision, "active": s.Active, "draft": draft, "versions": ids, "events": s.Events})
		return
	}
	media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || len(r.Header.Values("Content-Type")) != 1 {
		failure(w, 415, "json_required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, (1<<20)+4096)
	defer r.Body.Close()
	var request struct {
		Revision   *uint64         `json:"revision"`
		Entries    json.RawMessage `json:"entries,omitempty"`
		Version    string          `json:"version,omitempty"`
		AllowEmpty bool            `json:"allow_empty,omitempty"`
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&request) != nil || d.Decode(new(any)) != io.EOF || request.Revision == nil {
		failure(w, 400, "invalid_request")
		return
	}
	var revision uint64
	switch r.URL.Path {
	case "/internal/knowledge/draft":
		if request.Version != "" || request.AllowEmpty {
			failure(w, 400, "invalid_request")
			return
		}
		revision, e = service.SaveDraft(ctx, *request.Revision, request.Entries)
	case "/internal/knowledge/publish":
		if len(request.Entries) != 0 || request.Version != "" {
			failure(w, 400, "invalid_request")
			return
		}
		revision, e = service.PublishDraft(ctx, *request.Revision, request.AllowEmpty)
	case "/internal/knowledge/rollback":
		if len(request.Entries) != 0 || strings.TrimSpace(request.Version) == "" {
			failure(w, 400, "invalid_request")
			return
		}
		revision, e = service.Rollback(ctx, *request.Revision, request.Version, request.AllowEmpty)
	}
	if e != nil {
		respondError(w, e)
		return
	}
	send(w, 200, map[string]uint64{"revision": revision})
}
func respondError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, manage.ErrDenied):
		failure(w, 403, "access_denied")
	case errors.Is(e, manage.ErrConflict):
		failure(w, 409, "revision_conflict")
	case errors.Is(e, manage.ErrOutcomeUnknown):
		failure(w, 503, "outcome_unknown_reload_required")
	case errors.Is(e, manage.ErrEmpty):
		failure(w, 422, "empty_confirmation_required")
	case errors.Is(e, manage.ErrLimit):
		failure(w, 409, "history_limit")
	case errors.Is(e, manage.ErrNotFound):
		failure(w, 404, "version_not_found")
	case errors.Is(e, manage.ErrInvalid):
		failure(w, 400, "invalid_request")
	default:
		failure(w, 503, "storage_unavailable")
	}
}
