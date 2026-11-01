package adminui

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAssetsAndSecurity(t *testing.T) {
	for _, path := range []string{"/", "/assets/admin.js", "/assets/admin.css", "/assets/admin.js.LEGAL.txt"} {
		w := httptest.NewRecorder()
		ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatalf("%s: %d", path, w.Code)
		}
		if w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatal("missing protection")
		}
	}
	for _, path := range []string{"/assets/", "/assets/../index.html", "/auth/me", "/private", "/?x=1"} {
		w := httptest.NewRecorder()
		ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatal(path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	ServeHTTP(w, httptest.NewRequest("POST", "/", nil))
	if w.Code != 405 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	ServeHTTP(w, httptest.NewRequest("HEAD", "/", nil))
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("HEAD body")
	}
}
