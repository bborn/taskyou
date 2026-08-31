package main

import "testing"

func TestServeListenAddr(t *testing.T) {
	tests := []struct {
		name string
		host string
		port int
		want string
	}{
		{"empty host binds all interfaces", "", 8080, ":8080"},
		{"whitespace host binds all interfaces", "  ", 8080, ":8080"},
		{"ipv4 host", "127.0.0.1", 8080, "127.0.0.1:8080"},
		{"tailscale ip", "100.107.3.120", 8899, "100.107.3.120:8899"},
		{"hostname", "my-box.tail1234.ts.net", 8080, "my-box.tail1234.ts.net:8080"},
		{"ipv6 literal is bracketed", "::1", 8080, "[::1]:8080"},
		{"bracketed ipv6 stays single-bracketed", "[::1]", 8080, "[::1]:8080"},
		{"ipv6 unspecified", "::", 8080, "[::]:8080"},
		{"ipv6 with zone", "fe80::1%en0", 8080, "[fe80::1%en0]:8080"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := serveListenAddr(tt.host, tt.port)
			if err != nil {
				t.Fatalf("serveListenAddr(%q, %d) returned error: %v", tt.host, tt.port, err)
			}
			if got != tt.want {
				t.Errorf("serveListenAddr(%q, %d) = %q, want %q", tt.host, tt.port, got, tt.want)
			}
		})
	}
}

func TestServeListenAddrInvalid(t *testing.T) {
	tests := []struct {
		name string
		host string
		port int
	}{
		{"host with scheme", "http://127.0.0.1", 8080},
		{"host with port", "127.0.0.1:8080", 8080},
		{"host with path", "127.0.0.1/board", 8080},
		{"host with space", "not a host", 8080},
		{"underscore label", "bad_host", 8080},
		{"leading dash label", "-nope", 8080},
		{"port zero", "127.0.0.1", 0},
		{"port too large", "127.0.0.1", 70000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := serveListenAddr(tt.host, tt.port)
			if err == nil {
				t.Fatalf("serveListenAddr(%q, %d) = %q, want error", tt.host, tt.port, got)
			}
		})
	}
}
