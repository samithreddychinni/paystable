package ui

import (
	"embed"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"

	"github.com/IDEA-Amrita/paystable/internal/localonly"
)

//go:embed dist
var dist embed.FS

// Register mounts the dashboard SPA at /dashboard/ on mux.
// Real files (assets) are served directly; any other path under /dashboard/
// falls back to index.html so client-side routes like /dashboard/transactions
// work on a hard refresh or a pasted deep link. Local operators only.
func Register(mux *http.ServeMux, allowed []net.IP) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic("ui: failed to sub dashboard dist: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(sub))

	indexHTML, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		panic("ui: failed to read index.html: " + err.Error())
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/dashboard")
		p = strings.TrimPrefix(p, "/")

		// Serve real file when it exists (assets, favicon, etc.).
		if p != "" {
			if f, err := sub.Open(p); err == nil {
				f.Close() //nolint:errcheck
				http.StripPrefix("/dashboard", fileServer).ServeHTTP(w, r)
				return
			}
		}

		// SPA fallback: everything else gets index.html.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.Copy(w, strings.NewReader(string(indexHTML)))
	}

	mux.Handle("/dashboard/", localonly.Wrap(allowed, http.HandlerFunc(handler)))
	mux.Handle("/dashboard", localonly.Wrap(allowed, http.RedirectHandler("/dashboard/", http.StatusMovedPermanently)))
}
