package adminauth

import (
	"context"
	"encoding/json"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const password = "fictional-test-password-only"

type memory struct {
	mu sync.Mutex
	s  manage.State
}

func (m *memory) Load(context.Context) (manage.State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, _ := json.Marshal(m.s)
	var s manage.State
	json.Unmarshal(b, &s)
	return s, nil
}
func (m *memory) CompareAndSwap(_ context.Context, n uint64, s manage.State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.s.Revision != n {
		return manage.ErrConflict
	}
	m.s = s
	return nil
}
func setup(t *testing.T) (*Directory, http.Handler, *memory) {
	t.Helper()
	hash, e := HashPassword([]byte(password))
	if e != nil {
		t.Fatal(e)
	}
	d, e := New([]Account{{"admin", hash, []manage.Permission{manage.Read, manage.Edit, manage.Publish}}, {"editor", hash, []manage.Permission{manage.Read, manage.Edit}}}, "https://admin.example")
	if e != nil {
		t.Fatal(e)
	}
	r := &memory{}
	h, e := d.Handler(r)
	if e != nil {
		t.Fatal(e)
	}
	return d, h, r
}
func request(h http.Handler, path, body, cookie string) *httptest.ResponseRecorder {
	method := "POST"
	if body == "" {
		method = "GET"
	}
	r := httptest.NewRequest(method, "https://admin.example"+path, strings.NewReader(body))
	r.Header.Set("Origin", "https://admin.example")
	r.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		r.Header.Set("Cookie", cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func login(t *testing.T, h http.Handler, id, old string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"account": id, "password": password})
	w := request(h, "/auth/login", string(b), old)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe cookie")
	}
	if strings.Contains(w.Body.String(), cookies[0].Value) {
		t.Fatal("token exposed in JSON")
	}
	return cookies[0].Name + "=" + cookies[0].Value
}
func TestLoginToPublishAndLogout(t *testing.T) {
	_, h, r := setup(t)
	if w := request(h, "/auth/me", "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request(h, "/auth/login", `{"account":"admin","password":"wrong"}`, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	cookie := login(t, h, "admin", "")
	if w := request(h, "/auth/me", "", cookie); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(h, "/internal/knowledge/draft", `{"revision":0,"entries":[]}`, cookie); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := request(h, "/internal/knowledge/publish", `{"revision":1,"allow_empty":true}`, cookie); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if r.s.Revision != 2 || r.s.Events[1].Actor != "admin" {
		t.Fatal("wrong authenticated actor")
	}
	if w := request(h, "/auth/logout", `{}`, cookie); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := request(h, "/internal/knowledge/state", "", cookie); w.Code != 403 {
		t.Fatal("logout did not revoke", w.Code)
	}
}
func TestSessionRotationPermissionsExpiryAndRevoke(t *testing.T) {
	d, h, _ := setup(t)
	cookie := login(t, h, "admin", "")
	newCookie := login(t, h, "editor", cookie)
	if w := request(h, "/auth/me", "", cookie); w.Code != 401 {
		t.Fatal("old session alive")
	}
	if w := request(h, "/internal/knowledge/publish", `{"revision":0}`, newCookie); w.Code != 403 {
		t.Fatal("editor gained publish")
	}
	if w := request(h, "/auth/me", "", newCookie+"; "+newCookie); w.Code != 401 {
		t.Fatal("duplicate cookie accepted")
	}
	// Customer/staff cookies and renamed administrator tokens cannot authenticate.
	if w := request(h, "/auth/me", "", "office_operator="+strings.SplitN(newCookie, "=", 2)[1]); w.Code != 401 {
		t.Fatal("foreign cookie accepted")
	}
	now := time.Now().Add(31 * time.Minute)
	d.now = func() time.Time { return now }
	if w := request(h, "/auth/me", "", newCookie); w.Code != 401 {
		t.Fatal("expired cookie accepted")
	}
	cookie = login(t, h, "admin", "")
	d.Revoke("admin")
	if w := request(h, "/auth/me", "", cookie); w.Code != 401 {
		t.Fatal("revoked account alive")
	}
}
func TestCSRFAndCredentialClaimsFailClosed(t *testing.T) {
	_, h, _ := setup(t)
	for _, origin := range []string{"", "https://foreign.example"} {
		r := httptest.NewRequest("POST", "https://admin.example/auth/login", strings.NewReader(`{"account":"admin","password":"`+password+`"}`))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
	if w := request(h, "/auth/login", `{"account":"admin","password":"`+password+`","permissions":["knowledge.publish"]}`, ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
}
func TestLoginRateLimitAndRestartInvalidateSessions(t *testing.T) {
	d, h, _ := setup(t)
	for i := 0; i < 10; i++ {
		if w := request(h, "/auth/login", `{"account":"missing","password":"wrong"}`, ""); w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	if w := request(h, "/auth/login", `{"account":"missing","password":"wrong"}`, ""); w.Code != 429 {
		t.Fatal(w.Code)
	}
	cookie := login(t, h, "admin", "")
	_, other, _ := setup(t)
	if w := request(other, "/auth/me", "", cookie); w.Code != 401 {
		t.Fatal("session crossed process")
	}
	if len(d.tickets) != 1 {
		t.Fatal("failed logins created sessions")
	}
}
func TestHashAndConfigurationValidation(t *testing.T) {
	for _, p := range [][]byte{[]byte("short"), []byte(strings.Repeat("a", 513)), []byte("sixteen-characters\n")} {
		if _, e := HashPassword(p); e == nil {
			t.Fatal("unsafe password accepted")
		}
	}
	if _, e := New(nil, "https://admin.example"); e == nil {
		t.Fatal("empty accounts accepted")
	}
	if _, e := New([]Account{{"admin", "plaintext", []manage.Permission{manage.Read}}}, "https://admin.example"); e == nil {
		t.Fatal("plaintext config accepted")
	}
}
