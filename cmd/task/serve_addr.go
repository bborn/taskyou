package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// serveListenAddr builds the address `ty serve` listens on from the --host and
// --port flags.
//
// An empty host preserves the historical behaviour of binding every interface
// (":8080"). A host is bracketed when needed, so IPv6 literals such as "::1"
// become "[::1]:8080". Hosts that net.Listen would only reject later with a
// confusing error are rejected here instead.
func serveListenAddr(host string, port int) (string, error) {
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("invalid port %d: must be between 1 and 65535", port)
	}

	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Sprintf(":%d", port), nil
	}

	// Accept a bracketed IPv6 literal ("[::1]") as well as a bare one.
	unbracketed := host
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		unbracketed = host[1 : len(host)-1]
	}

	if err := validateServeHost(unbracketed); err != nil {
		return "", err
	}

	return net.JoinHostPort(unbracketed, strconv.Itoa(port)), nil
}

// validateServeHost accepts an IP literal (with an optional zone) or a DNS
// hostname, and rejects anything else with an actionable message.
func validateServeHost(host string) error {
	if ip, _, found := strings.Cut(host, "%"); found {
		// IPv6 zone, e.g. "fe80::1%en0" — the address itself must still parse.
		if net.ParseIP(ip) == nil {
			return fmt.Errorf("invalid host %q: not a valid IP address or hostname", host)
		}
		return nil
	}

	if net.ParseIP(host) != nil {
		return nil
	}

	if isHostname(host) {
		return nil
	}

	return fmt.Errorf("invalid host %q: not a valid IP address or hostname", host)
}

// isHostname reports whether s looks like a DNS name (RFC 1123 labels).
func isHostname(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	s = strings.TrimSuffix(s, ".")
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-':
			default:
				return false
			}
		}
	}
	return true
}
