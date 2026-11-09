package managehttp

import (
	"context"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/feedback"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
	"net/http"
	"testing"
)

type feedbackMemory struct {
	memory
	reads, writes int
}

func (m *feedbackMemory) RecordFeedback(context.Context, string) error { return nil }
func (m *feedbackMemory) PurgeFeedback(context.Context) error          { return nil }
func (m *feedbackMemory) ListFeedback(context.Context) ([]feedback.Item, error) {
	m.reads++
	return []feedback.Item{}, nil
}
func (m *feedbackMemory) UpdateFeedback(context.Context, feedback.Change) error {
	m.writes++
	return nil
}
func TestFeedbackHTTPBoundary(t *testing.T) {
	repo := &feedbackMemory{}
	h, _ := New(repo, func(_ context.Context, r *http.Request, p manage.Permission) (string, error) {
		role := r.Header.Get("X-Test-Session")
		if role == "editor" || role == "reader" && p == manage.Read {
			return role, nil
		}
		return "", manage.ErrDenied
	}, "https://admin.example")
	for _, role := range []string{"", "customer", "staff"} {
		if w := call(h, "/internal/knowledge/feedback", "", "https://admin.example", role); w.Code != 403 {
			t.Fatal(role, w.Code)
		}
	}
	if repo.reads != 0 {
		t.Fatal("unauthorized read")
	}
	if w := call(h, "/internal/knowledge/feedback", "", "https://admin.example", "reader"); w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code)
	}
	body := `{"id":"test","revision":1,"status":"ignored","faq_id":""}`
	for _, c := range []struct{ origin, role string }{{"https://foreign.example", "editor"}, {"", "editor"}, {"https://admin.example", "reader"}} {
		if w := call(h, "/internal/knowledge/feedback/status", body, c.origin, c.role); w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
	if repo.writes != 0 {
		t.Fatal("unauthorized write")
	}
	if w := call(h, "/internal/knowledge/feedback/status", body, "https://admin.example", "editor"); w.Code != 200 || repo.writes != 1 {
		t.Fatal(w.Code)
	}
	if w := call(h, "/internal/knowledge/feedback?", "", "https://admin.example", "reader"); w.Code != 400 {
		t.Fatal("query accepted")
	}
	if w := call(h, "/internal/knowledge/feedback/status", `{"unknown":1}`, "https://admin.example", "editor"); w.Code != 400 {
		t.Fatal("unknown field accepted")
	}
}
