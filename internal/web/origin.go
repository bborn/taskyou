package web

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// The API has no authentication, so a web page open in the user's browser
// must not be able to drive it. Two checks close that:
//
//   - For browser requests, the Host header must name a local or tailnet
//     address. This defeats DNS rebinding, where evil.example resolves to
//     127.0.0.1 and the browser treats the API as same-origin with the
//     attacker's page. Browsers mark every request, same-origin GETs
//     included, with Origin or Sec-Fetch-* headers.
//   - A browser Origin, when present, must be one of our own clients: the
//     daemon's own web UI, the desktop shell, a localhost dev server, or a
//     browser extension. Non-browser clients (the TUI, bb plugin, ty-email,
//     curl) send no Origin and are unaffected.

// trustedHost reports whether host (no port) is a name a browser could only
// reach on this machine or the tailnet. Attacker-registrable public domains
// always contain a dot and end in a public suffix, so they fail.
func trustedHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if !strings.Contains(host, ".") {
		return true // localhost, or a MagicDNS short name like "mona"
	}
	for _, suffix := range []string{".localhost", ".local", ".lan", ".home", ".internal", ".home.arpa", ".ts.net"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

// allowedOrigin reports whether a request carrying this Origin header may
// use the API.
func allowedOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "tauri", "chrome-extension", "moz-extension":
		return true
	case "http", "https":
		return trustedHost(u.Hostname())
	}
	return false
}

// checkRequestOrigin is the gate shared by the HTTP middleware and the
// terminal WebSocket upgrader.
func checkRequestOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	fromBrowser := origin != "" || r.Header.Get("Sec-Fetch-Site") != ""
	if fromBrowser && !trustedHost(hostOnly(r.Host)) {
		return false
	}
	return allowedOrigin(origin)
}

// originGuard rejects requests from untrusted hosts or browser origins and
// answers CORS only for trusted ones.
func originGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !checkRequestOrigin(r) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
