// Package adminui serves only public application assets, never knowledge data.
package adminui

import (
	"embed"
	"net/http"
)

//go:embed index.html assets/*
var files embed.FS

func ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; font-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	w.Header().Set("X-Frame-Options", "DENY")
	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(w, "method not allowed", 405)
		return
	}
	name, media := "", ""
	switch r.URL.Path {
	case "/":
		name, media = "index.html", "text/html; charset=utf-8"
	case "/assets/admin.js":
		name, media = "assets/admin.js", "text/javascript; charset=utf-8"
	case "/assets/admin.css":
		name, media = "assets/admin.css", "text/css; charset=utf-8"
	case "/assets/admin.js.LEGAL.txt":
		name, media = "assets/admin.js.LEGAL.txt", "text/plain; charset=utf-8"
	default:
		http.NotFound(w, r)
		return
	}
	if r.URL.RawQuery != "" || r.URL.RawPath != "" || r.URL.ForceQuery {
		http.NotFound(w, r)
		return
	}
	b, e := files.ReadFile(name)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", media)
	if r.Method == "GET" {
		_, _ = w.Write(b)
	}
}
