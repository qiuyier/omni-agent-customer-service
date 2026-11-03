package managehttp

import (
	"context"
	"encoding/json"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/faq"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func trialFixture(t *testing.T) (*Handler, *memory) {
	t.Helper()
	old := []byte(`[{"id":"old","title":"Old","content":"old answer","source":"fixture","keywords":["hours"]}]`)
	draft := []byte(`[{"id":"new","title":"New","content":"new answer","source":"fixture","keywords":["hours","时间"]}]`)
	_, oldID, _ := faq.ParseVersion(old)
	_, newID, _ := faq.ParseVersion(draft)
	repo := &memory{state: manage.State{Revision: 7, Active: oldID, Draft: &manage.Version{ID: newID, Data: draft}, Versions: []manage.Version{{ID: oldID, Data: old}}}}
	h, _ := New(repo, func(_ context.Context, r *http.Request, p manage.Permission) (string, error) {
		if r.Header.Get("X-Test-Session") == "reader" && p == manage.Read {
			return "reader", nil
		}
		return "", manage.ErrDenied
	}, "https://admin.example")
	return h, repo
}
func TestTrialReadOnlyComparison(t *testing.T) {
	h, repo := trialFixture(t)
	before := copyState(repo.state)
	w := call(h, "/internal/knowledge/trial", `{"revision":7,"query":"HOURS 时间"}`, "https://admin.example", "reader")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var result struct {
		Revision         uint64
		Draft, Published *trialVersion
	}
	if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if result.Revision != 7 || result.Draft.Matches[0].ID != "new" || result.Published.Matches[0].ID != "old" || len(result.Draft.Matches[0].Keywords) != 2 {
		t.Fatal(result)
	}
	if !reflect.DeepEqual(repo.state, before) {
		t.Fatal("trial wrote state")
	}
	if strings.Contains(w.Body.String(), "HOURS 时间") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("query echoed or response cached")
	}
}
func TestTrialRejectsUnsafeOrStaleRequests(t *testing.T) {
	h, repo := trialFixture(t)
	before := copyState(repo.state)
	for _, tc := range []struct {
		body, origin, role string
		code               int
	}{
		{`{"revision":7,"query":"hours"}`, "", "reader", 403},
		{`{"revision":7,"query":"hours"}`, "https://foreign.example", "reader", 403},
		{`{"revision":7,"query":"hours"}`, "https://admin.example", "customer", 403},
		{`{"revision":6,"query":"hours"}`, "https://admin.example", "reader", 409},
		{`{"revision":7,"query":""}`, "https://admin.example", "reader", 400},
		{`{"revision":7,"query":"hours","extra":true}`, "https://admin.example", "reader", 400},
		{`{"revision":7,"query":"` + strings.Repeat("问", 513) + `"}`, "https://admin.example", "reader", 400},
		{`{"revision":7,"query":"hours"} {}`, "https://admin.example", "reader", 400},
		{`{"revision":7,"query":"hours"}` + strings.Repeat(" ", 8192), "https://admin.example", "reader", 400},
	} {
		w := call(h, "/internal/knowledge/trial", tc.body, tc.origin, tc.role)
		if w.Code != tc.code {
			t.Fatalf("%d want %d: %s", w.Code, tc.code, w.Body)
		}
	}
	if w := call(h, "/internal/knowledge/trial", "", "https://admin.example", "reader"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if !reflect.DeepEqual(repo.state, before) {
		t.Fatal("invalid request wrote state")
	}
}
func TestTrialAbsentEmptyAndRevocation(t *testing.T) {
	h, repo := trialFixture(t)
	repo.state = manage.State{}
	w := call(h, "/internal/knowledge/trial", `{"revision":0,"query":"hours"}`, "https://admin.example", "reader")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"draft":null`) {
		t.Fatal(w.Code, w.Body)
	}
	_, id, _ := faq.ParseVersion([]byte("[]"))
	repo.state.Draft = &manage.Version{ID: id, Data: []byte("[]")}
	w = call(h, "/internal/knowledge/trial", `{"revision":0,"query":"hours"}`, "https://admin.example", "reader")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"matches":[]`) {
		t.Fatal(w.Code, w.Body)
	}
	n := 0
	h.auth = func(context.Context, *http.Request, manage.Permission) (string, error) {
		n++
		if n == 3 {
			return "", manage.ErrDenied
		}
		return "reader", nil
	}
	if w = call(h, "/internal/knowledge/trial", `{"revision":0,"query":"hours"}`, "https://admin.example", "reader"); w.Code != 403 {
		t.Fatal(w.Code, w.Body)
	}
}
