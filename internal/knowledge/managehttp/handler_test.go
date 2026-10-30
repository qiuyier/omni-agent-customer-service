package managehttp

import (
	"context"
	"encoding/json"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type memory struct {
	mu    sync.Mutex
	state manage.State
}

func copyState(s manage.State) manage.State {
	b, _ := json.Marshal(s)
	var v manage.State
	json.Unmarshal(b, &v)
	return v
}
func (m *memory) Load(context.Context) (manage.State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return copyState(m.state), nil
}
func (m *memory) CompareAndSwap(_ context.Context, n uint64, s manage.State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Revision != n {
		return manage.ErrConflict
	}
	m.state = copyState(s)
	return nil
}
func call(h http.Handler, path, body, origin, role string) *httptest.ResponseRecorder {
	method := "POST"
	if body == "" {
		method = "GET"
	}
	r := httptest.NewRequest(method, "https://admin.example"+path, strings.NewReader(body))
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Test-Session", role)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestAdminBoundaryLifecycleAndDeniedRoles(t *testing.T) {
	store := &memory{}
	auth := func(_ context.Context, r *http.Request, p manage.Permission) (string, error) {
		role := r.Header.Get("X-Test-Session")
		if role == "admin" || role == "editor" && p != manage.Publish {
			return role, nil
		}
		return "", manage.ErrDenied
	}
	h, e := New(store, auth, "https://admin.example")
	if e != nil {
		t.Fatal(e)
	}
	draft := `{"revision":0,"entries":[{"id":"hours","title":"Hours","source":"fictional","content":"test only","keywords":["hours"]}]}`
	for _, role := range []string{"", "customer", "staff"} {
		if w := call(h, "/internal/knowledge/draft", draft, "https://admin.example", role); w.Code != 403 {
			t.Fatal(role, w.Code)
		}
	}
	if w := call(h, "/internal/knowledge/draft", draft, "https://foreign.example", "admin"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := call(h, "/internal/knowledge/draft", draft, "", "admin"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := call(h, "/internal/knowledge/draft", draft, "https://admin.example", "editor"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(h, "/internal/knowledge/publish", `{"revision":1}`, "https://admin.example", "editor"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := call(h, "/internal/knowledge/publish", `{"revision":1,"actor":"admin"}`, "https://admin.example", "admin"); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := call(h, "/internal/knowledge/publish", `{"revision":1}`, "https://admin.example", "admin"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(h, "/internal/knowledge/draft", draft, "https://admin.example", "admin"); w.Code != 409 {
		t.Fatal(w.Code)
	}
	w := call(h, "/internal/knowledge/state", "", "https://admin.example", "admin")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "test only") {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(h, "/internal/knowledge/state", "", "https://admin.example", "revoked"); w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestRevokedDuringReadCannotReturnSnapshot(t *testing.T) {
	n := 0
	h, _ := New(&memory{}, func(context.Context, *http.Request, manage.Permission) (string, error) {
		n++
		if n == 3 {
			return "", manage.ErrDenied
		}
		return "admin", nil
	}, "https://admin.example")
	if w := call(h, "/internal/knowledge/state", "", "https://admin.example", "admin"); w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestUnsafeConfigurationAndErrorSanitization(t *testing.T) {
	auth := func(context.Context, *http.Request, manage.Permission) (string, error) { return "admin", nil }
	for _, origin := range []string{"http://remote.example", "https://admin.example/path", "https://user:password@admin.example"} {
		if _, e := New(&memory{}, auth, origin); e == nil {
			t.Fatal(origin)
		}
	}
	for _, e := range []error{manage.ErrOutcomeUnknown, manage.ErrUnavailable} {
		w := httptest.NewRecorder()
		respondError(w, e)
		if w.Code != 503 {
			t.Fatal(w.Code)
		}
		if e == manage.ErrOutcomeUnknown && !strings.Contains(w.Body.String(), "reload_required") {
			t.Fatal(w.Body)
		}
	}
}
