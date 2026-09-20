package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedRoutes(t *testing.T) {
	for _, path := range []string{"/", "/chat/", "/chat", "/assets/app.js", "/assets/app.css"} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusOK || response.Body.Len() == 0 {
				t.Fatalf("route %q: status %d, bytes %d", path, response.Code, response.Body.Len())
			}
			if response.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(response.Header().Get("Content-Security-Policy"), "connect-src 'self'") {
				t.Fatal("missing browser security headers")
			}
		})
	}
}

func TestUIRejectsUnknownRoutesAndWrites(t *testing.T) {
	for _, path := range []string{"/unknown", "/assets/", "/assets/../index.html", "/chat/private", "/host/me"} {
		response := httptest.NewRecorder()
		Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("route %q: got %d, want 404", path, response.Code)
		}
	}
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /: got %d, want 405", response.Code)
	}
}

func TestHeadHasNoBody(t *testing.T) {
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/chat/", nil))
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("HEAD /chat/: got %d with %d bytes", response.Code, response.Body.Len())
	}
}
