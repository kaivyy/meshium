package main

import (
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
)

//go:embed all:web/build
var webFS embed.FS

var inlineScriptRe = regexp.MustCompile(`(?s)<script(?:\s[^>]*)?>(.*?)</script>`)

// inlineScriptHashes scans the embedded index.html for inline <script> blocks
// (those without a src attribute) and returns their CSP sha256 source
// expressions. SvelteKit's static build bootstraps the SPA via an inline
// script whose content changes per build, so the CSP script-src must allow
// these exact hashes or the app never starts. Computing them from the embedded
// HTML at startup keeps the CSP correct across rebuilds without hardcoding.
func inlineScriptHashes() []string {
	data, err := webFS.ReadFile("web/build/index.html")
	if err != nil {
		return nil
	}

	html := string(data)
	var hashes []string
	seen := make(map[string]bool)

	for _, m := range inlineScriptRe.FindAllStringSubmatch(html, -1) {
		openTag := m[0][:strings.Index(m[0], ">")+1]
		if strings.Contains(strings.ToLower(openTag), "src=") {
			continue
		}
		sum := sha256.Sum256([]byte(m[1]))
		expr := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
		if !seen[expr] {
			seen[expr] = true
			hashes = append(hashes, expr)
		}
	}
	return hashes
}

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
