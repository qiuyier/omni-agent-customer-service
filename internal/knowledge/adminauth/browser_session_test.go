package adminauth

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserSessionBinding(t *testing.T) {
	_, h, repo := setup(t)
	cookie := login(t, h, "admin", "")
	var me struct {
		Version string `json:"version"`
	}
	json.Unmarshal(request(h, "/auth/me", "", cookie).Body.Bytes(), &me)
	next := login(t, h, "editor", cookie)
	for _, path := range []string{"/internal/knowledge/draft", "/auth/logout"} {
		body := `{}`
		if strings.Contains(path, "draft") {
			body = `{"revision":0,"entries":[]}`
		}
		r := httptest.NewRequest("POST", "https://admin.example"+path, strings.NewReader(body))
		r.Header.Set("Cookie", next)
		r.Header.Set("Origin", "https://admin.example")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Knowledge-Session", me.Version)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("stale browser accepted", path, w.Code)
		}
	}
	if repo.s.Revision != 0 || request(h, "/auth/me", "", next).Code != 200 {
		t.Fatal("new account mutated or logged out")
	}
	json.Unmarshal(request(h, "/auth/me", "", next).Body.Bytes(), &me)
	r := httptest.NewRequest("GET", "https://admin.example/internal/knowledge/state", nil)
	r.Header.Set("Cookie", next)
	r.Header.Set("X-Knowledge-Session", me.Version)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	r.Header.Add("X-Knowledge-Session", me.Version)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("duplicate accepted")
	}
	w = request(h, "/", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "/assets/admin.js") {
		t.Fatal("login UI unavailable")
	}
	r = httptest.NewRequest("GET", "https://foreign.example/", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("foreign host accepted")
	}
}
