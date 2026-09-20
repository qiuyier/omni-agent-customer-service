// Package ui serves the self-contained browser demo. Generated assets are
// checked in so running the Go demo does not require Node.js or a CDN.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed index.html assets/*
var content embed.FS

// Handler serves the example host page, independent chat page, and assets.
// API routes remain the responsibility of the enclosing host application.
func Handler() http.Handler {
	assets, err := fs.Sub(content, "assets")
	if err != nil {
		panic(err)
	}
	files := http.StripPrefix("/assets/", http.FileServer(http.FS(assets)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/", "/chat", "/chat/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			if r.Method == http.MethodHead {
				return
			}
			page, _ := content.ReadFile("index.html")
			_, _ = w.Write(page)
		case "/assets/app.js", "/assets/app.css", "/assets/app.js.LEGAL.txt":
			w.Header().Set("Cache-Control", "no-cache")
			files.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}
