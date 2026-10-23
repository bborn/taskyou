package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOriginGuard(t *testing.T) {
	srv, _, _ := setupServer(t)
	h := srv.Handler()

	cases := []struct {
		name   string
		method string
		host   string
		origin string
		want   int
	}{
		{"non-browser client", "GET", "localhost:8080", "", http.StatusOK},
		{"own web UI", "GET", "localhost:8080", "http://localhost:8080", http.StatusOK},
		{"tailnet MagicDNS", "GET", "mona.taila2a10c.ts.net", "https://mona.taila2a10c.ts.net", http.StatusOK},
		{"tailnet IP", "GET", "100.91.243.95:8080", "http://100.91.243.95:8080", http.StatusOK},
		{"short host name", "GET", "mona:8080", "http://mona:8080", http.StatusOK},
		{"desktop shell", "GET", "127.0.0.1:8080", "tauri://localhost", http.StatusOK},
		{"vite dev server", "GET", "localhost:8080", "http://localhost:1420", http.StatusOK},
		{"chrome extension", "POST", "localhost:8080", "chrome-extension://abcdefg", http.StatusNoContent},
		{"foreign site", "GET", "localhost:8080", "https://evil.example", http.StatusForbidden},
		{"foreign site preflight", "OPTIONS", "localhost:8080", "https://evil.example", http.StatusForbidden},
		{"DNS rebinding", "POST", "evil.example:8080", "http://evil.example:8080", http.StatusForbidden},
		{"non-browser client via public proxy", "GET", "ty.example.com", "", http.StatusOK},
		{"LAN hostname", "GET", "mac.lan:8080", "http://mac.lan:8080", http.StatusOK},
		{"null origin", "POST", "localhost:8080", "null", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			method, path := tc.method, "/api/board"
			if method == "POST" {
				method = "OPTIONS" // preflight; avoids side effects
			}
			req := httptest.NewRequest(method, path, nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("got %d, want %d", w.Code, tc.want)
			}
			if tc.want != http.StatusForbidden && tc.origin != "" {
				if got := w.Header().Get("Access-Control-Allow-Origin"); got != tc.origin {
					t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tc.origin)
				}
			}
		})
	}
}

func TestOriginGuardBlocksRebindingSameOriginGET(t *testing.T) {
	srv, _, _ := setupServer(t)
	// A same-origin GET carries no Origin header, only Sec-Fetch-*.
	req := httptest.NewRequest("GET", "/api/board", nil)
	req.Host = "evil.example:8080"
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403", w.Code)
	}
}

func TestTerminalUpgraderUsesOriginGuard(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/tasks/1/terminal", nil)
	req.Host = "localhost:8080"
	req.Header.Set("Origin", "https://evil.example")
	if upgrader.CheckOrigin(req) {
		t.Fatal("terminal WebSocket accepted a foreign origin")
	}
	req.Header.Set("Origin", "http://localhost:8080")
	if !upgrader.CheckOrigin(req) {
		t.Fatal("terminal WebSocket rejected its own web UI")
	}
}
