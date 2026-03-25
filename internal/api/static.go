package api

import (
	"io/fs"
	"net/http"
)

// staticHandler returns an http.Handler that serves files from fsys.
// All unmatched paths return index.html (for SPA client-side routing).
func staticHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the file; fall back to index.html for the SPA router.
		if _, err := fs.Stat(fsys, r.URL.Path[1:]); err != nil {
			// Serve index.html for all unknown paths.
			r2 := *r
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, &r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
