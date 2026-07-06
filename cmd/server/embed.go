package main

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:web/build
var webFS embed.FS

func staticHandler() http.Handler {
	sub, err := fs.Sub(webFS, "web/build")
	if err != nil {
		return http.NotFoundHandler()
	}

	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")

		// Set cache headers based on file type
		// HTML files (index.html, SPA routes): no-cache — must always revalidate
		// Hashed assets (_app/immutable/): long cache — filename changes on rebuild
		if path == "" || path == "index.html" || !strings.Contains(path, ".") {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
		} else if strings.HasPrefix(path, "_app/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		}

		if path == "" {
			fileServer.ServeHTTP(w, r)
			return
		}

		if _, err := fs.Stat(sub, path); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}

		if strings.HasPrefix(path, "_app/") || strings.Contains(path, ".") {
			http.NotFound(w, r)
			return
		}

		// SPA fallback: serve index.html for client-side routing
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
