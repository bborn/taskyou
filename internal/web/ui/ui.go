// Package ui serves the embedded TaskYou web frontend — the same React app
// the desktop shell renders, so browser and desktop share one codebase.
//
// The assets are embedded only when building with the "ui" tag (the Makefile
// adds it automatically once desktop/dist has been built and copied here);
// plain `go build` stays green without Node and serves a pointer page instead.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

var (
	available bool
	assets    embed.FS
)

// Available reports whether the frontend was embedded at build time.
func Available() bool {
	return available
}

const placeholder = `<!doctype html><html><head><title>TaskYou</title></head>
<body style="font-family: system-ui; background:#16161e; color:#c0caf5; display:flex; align-items:center; justify-content:center; height:100vh; margin:0">
<div style="max-width:32rem">
<h1>TaskYou API</h1>
<p>This build of <code>ty</code> doesn't include the web UI.</p>
<p>Build it with <code>make build-ui build</code>, or use the desktop app in <code>desktop/</code>.
The JSON API is live under <code>/api/</code>.</p>
</div></body></html>`

// Handler serves the embedded single-page app, falling back to index.html for
// client-routed paths, or a pointer page when the UI wasn't embedded.
func Handler() http.Handler {
	if !available {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(placeholder))
		})
	}

	sub, err := fs.Sub(assets, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" {
			if f, err := sub.Open(name); err == nil {
				_ = f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// SPA fallback: unknown paths get the app shell.
		index, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}
