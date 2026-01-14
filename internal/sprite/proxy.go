package sprite

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/bborn/workflow/internal/hostdb"
)

// ProxyHandler handles proxying requests to user sprites.
type ProxyHandler struct {
	manager *Manager
}

// NewProxyHandler creates a new proxy handler.
func NewProxyHandler(manager *Manager) *ProxyHandler {
	return &ProxyHandler{manager: manager}
}

// GetSpriteURL returns the internal URL for a sprite's web API.
func (p *ProxyHandler) GetSpriteURL(sprite *hostdb.Sprite) string {
	// Sprites expose their web API on port 8080
	// The URL format for accessing sprites via the Fly network
	return fmt.Sprintf("http://%s.internal:8080", sprite.SpriteName)
}

// ProxyRequest proxies an HTTP request to a user's sprite.
func (p *ProxyHandler) ProxyRequest(ctx context.Context, sprite *hostdb.Sprite, w http.ResponseWriter, r *http.Request) error {
	target, err := url.Parse(p.GetSpriteURL(sprite))
	if err != nil {
		return fmt.Errorf("parse sprite url: %w", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	// Modify the director to strip the /api prefix and adjust headers
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)

		// Strip /api prefix from path (the sprite API doesn't use /api prefix)
		req.URL.Path = strings.TrimPrefix(req.URL.Path, "/api")
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}

		// Set forwarding headers
		req.Header.Set("X-Forwarded-For", r.RemoteAddr)
		req.Header.Set("X-Forwarded-Host", r.Host)
		req.Header.Set("X-Forwarded-Proto", "https")

		// Remove cookies and auth headers from proxied request
		// (sprite API doesn't need them - we've already authenticated)
		req.Header.Del("Cookie")
		req.Header.Del("Authorization")
	}

	// Custom error handler
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, fmt.Sprintf("Sprite unavailable: %v", err), http.StatusBadGateway)
	}

	proxy.ServeHTTP(w, r)
	return nil
}

// ProxyWebSocket proxies a WebSocket connection to a user's sprite.
func (p *ProxyHandler) ProxyWebSocket(ctx context.Context, sprite *hostdb.Sprite, w http.ResponseWriter, r *http.Request) error {
	// Get sprite internal URL
	spriteURL := p.GetSpriteURL(sprite)
	target, err := url.Parse(spriteURL)
	if err != nil {
		return fmt.Errorf("parse sprite url: %w", err)
	}

	// Change scheme to ws
	target.Scheme = "ws"
	target.Path = "/ws"

	// Hijack the connection for WebSocket proxying
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return fmt.Errorf("response writer does not support hijacking")
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		return fmt.Errorf("hijack connection: %w", err)
	}
	defer clientConn.Close()

	// Connect to sprite WebSocket
	// This is a simplified implementation - in production you'd use gorilla/websocket
	// or nhooyr.io/websocket for proper WebSocket handling
	spriteConn, err := dialWebSocket(ctx, target.String())
	if err != nil {
		return fmt.Errorf("dial sprite websocket: %w", err)
	}
	defer spriteConn.Close()

	// Bidirectional copy
	errCh := make(chan error, 2)
	go func() {
		_, err := io.Copy(clientConn, spriteConn)
		errCh <- err
	}()
	go func() {
		_, err := io.Copy(spriteConn, clientConn)
		errCh <- err
	}()

	return <-errCh
}

// dialWebSocket establishes a WebSocket connection.
// This is a placeholder - in production, use a proper WebSocket library.
func dialWebSocket(ctx context.Context, url string) (io.ReadWriteCloser, error) {
	// In production, use gorilla/websocket or nhooyr.io/websocket
	return nil, fmt.Errorf("websocket not implemented - use gorilla/websocket")
}
