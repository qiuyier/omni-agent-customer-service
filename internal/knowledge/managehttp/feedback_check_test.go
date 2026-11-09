package managehttp

import (
	"context"
	"encoding/json"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/feedback"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
	"net/http"
	"testing"
	"time"
)

type checkStore struct {
	feedbackMemory
	items []feedback.Item
}

func (m *checkStore) ListFeedback(context.Context) ([]feedback.Item, error) { return m.items, nil }
func TestFeedbackCheckReadOnly(t *testing.T) {
	for _, c := range []struct{ query, outcome string }{{"hours", "matched"}, {"delivery", "other"}, {"unknown", "miss"}} {
		t.Run(c.outcome, func(t *testing.T) {
			repo := &checkStore{items: []feedback.Item{{ID: "question", Question: c.query, Revision: 1, ExpiresAt: time.Now().Add(time.Hour)}}}
			service, _ := manage.New(repo, func(context.Context, manage.Permission) (string, error) { return "fixture", nil })
			data := []byte(`[{"id":"hours","title":"Hours","content":"Opening times","source":"fictional","keywords":["hours"]},{"id":"delivery","title":"Delivery","content":"Delivery guide","source":"fictional","keywords":["delivery"]}]`)
			if _, e := service.SaveDraft(context.Background(), 0, data); e != nil {
				t.Fatal(e)
			}
			if _, e := service.PublishDraft(context.Background(), 1, false); e != nil {
				t.Fatal(e)
			}
			h, _ := New(repo, func(_ context.Context, r *http.Request, p manage.Permission) (string, error) {
				if r.Header.Get("X-Test-Session") == "reader" && p == manage.Read {
					return "reader", nil
				}
				return "", manage.ErrDenied
			}, "https://admin.example")
			body := `{"id":"question","revision":2,"item_revision":1,"expected_id":"hours"}`
			w := call(h, "/internal/knowledge/feedback/check", body, "https://admin.example", "reader")
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			var result struct {
				Outcome string `json:"outcome"`
			}
			if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Outcome != c.outcome {
				t.Fatal(w.Body)
			}
			if repo.writes != 0 || repo.state.Revision != 2 {
				t.Fatal("verification wrote state")
			}
			for _, role := range []string{"customer", "staff", ""} {
				if call(h, "/internal/knowledge/feedback/check", body, "https://admin.example", role).Code != 403 {
					t.Fatal("unauthorized")
				}
			}
			if call(h, "/internal/knowledge/feedback/check", body, "", "reader").Code != 403 {
				t.Fatal("missing origin")
			}
			if call(h, "/internal/knowledge/feedback/check", `{"id":"question","revision":1,"item_revision":1,"expected_id":"hours"}`, "https://admin.example", "reader").Code != 409 {
				t.Fatal("stale knowledge")
			}
			if call(h, "/internal/knowledge/feedback/check", `{"id":"question","revision":2,"item_revision":2,"expected_id":"hours"}`, "https://admin.example", "reader").Code != 409 {
				t.Fatal("stale item")
			}
			if call(h, "/internal/knowledge/feedback/check", `{"id":"question","revision":2,"item_revision":1,"expected_id":"absent"}`, "https://admin.example", "reader").Code != 400 {
				t.Fatal("unknown FAQ")
			}
			repo.items[0].ExpiresAt = time.Now().Add(-time.Hour)
			if call(h, "/internal/knowledge/feedback/check", body, "https://admin.example", "reader").Code != 404 {
				t.Fatal("expired item")
			}
		})
	}
}
