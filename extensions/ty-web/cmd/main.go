// Command ty-web serves a web kanban board that talks to the ty serve API.
package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

//go:embed all:static
var staticFiles embed.FS

func main() {
	port := flag.Int("port", 3000, "port for the web UI")
	apiURL := flag.String("api", "http://localhost:8080", "ty serve API base URL")
	flag.Parse()

	target, err := url.Parse(*apiURL)
	if err != nil {
		log.Fatalf("invalid --api URL: %v", err)
	}

	mux := http.NewServeMux()

	// Reverse proxy: /api/* -> ty serve backend
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		originalDirector(r)
		r.Host = target.Host
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		// Strip any double slashes, pass through as-is
		r.URL.Path = strings.TrimRight(r.URL.Path, "/")
		if r.URL.Path == "/api" {
			r.URL.Path = "/api/"
		}
		proxy.ServeHTTP(w, r)
	})

	// Static files from embedded filesystem
	staticSub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("embedded static files: %v", err)
	}
	fileServer := http.FileServer(http.FS(staticSub))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// SPA fallback: serve index.html for non-file paths
		if r.URL.Path != "/" {
			// Try to open the file first
			f, err := staticSub.(fs.ReadFileFS).ReadFile(strings.TrimPrefix(r.URL.Path, "/"))
			if err != nil {
				// Not a real file, serve index.html
				r.URL.Path = "/"
			} else {
				_ = f
			}
		}
		fileServer.ServeHTTP(w, r)
	})

	addr := fmt.Sprintf(":%d", *port)
	fmt.Printf("ty-web listening on http://localhost%s\n", addr)
	fmt.Printf("Proxying /api/* to %s\n", target.String())
	log.Fatal(http.ListenAndServe(addr, mux))
}
