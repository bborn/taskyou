// Package webserver provides the HTTP server for the web UI.
package webserver

import (
	"context"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/auth"
	"github.com/bborn/workflow/internal/hostdb"
	"github.com/bborn/workflow/internal/sprite"
	"github.com/charmbracelet/log"
)

// Server is the web server for taskyou.
type Server struct {
	addr           string
	db             *hostdb.DB
	sessionMgr     *auth.SessionManager
	spriteClient   *sprite.Client
	spriteMgr      *sprite.Manager
	spriteProxy    *sprite.ProxyHandler
	baseURL        string
	staticFS       fs.FS
	logger         *log.Logger
}

// Config holds server configuration.
type Config struct {
	Addr     string
	DB       *hostdb.DB
	BaseURL  string
	Secure   bool   // Use secure cookies
	Domain   string // Cookie domain
	StaticFS fs.FS  // Embedded static files (optional)
}

// New creates a new server.
func New(cfg Config) (*Server, error) {
	sessionMgr := auth.NewSessionManager(cfg.DB, cfg.Secure, cfg.Domain)

	spriteClient, err := sprite.NewClient()
	if err != nil {
		// Sprites client is optional for development
		log.Warn("sprites client not configured", "error", err)
	}

	var spriteMgr *sprite.Manager
	var spriteProxy *sprite.ProxyHandler
	if spriteClient != nil {
		spriteMgr = sprite.NewManager(spriteClient, cfg.DB)
		spriteProxy = sprite.NewProxyHandler(spriteMgr)
	}

	return &Server{
		addr:         cfg.Addr,
		db:           cfg.DB,
		sessionMgr:   sessionMgr,
		spriteClient: spriteClient,
		spriteMgr:    spriteMgr,
		spriteProxy:  spriteProxy,
		baseURL:      cfg.BaseURL,
		staticFS:     cfg.StaticFS,
		logger:       log.NewWithOptions(os.Stderr, log.Options{Prefix: "webserver"}),
	}, nil
}

// Start starts the server.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// Auth routes
	mux.HandleFunc("GET /api/auth/google", s.handleGoogleAuth)
	mux.HandleFunc("GET /api/auth/github", s.handleGitHubAuth)
	mux.HandleFunc("GET /api/auth/callback", s.handleOAuthCallback)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/auth/me", s.handleGetMe)

	// Sprite management routes
	mux.HandleFunc("GET /api/sprite", s.requireAuth(s.handleGetSprite))
	mux.HandleFunc("POST /api/sprite", s.requireAuth(s.handleCreateSprite))
	mux.HandleFunc("POST /api/sprite/start", s.requireAuth(s.handleStartSprite))
	mux.HandleFunc("POST /api/sprite/stop", s.requireAuth(s.handleStopSprite))
	mux.HandleFunc("DELETE /api/sprite", s.requireAuth(s.handleDestroySprite))
	mux.HandleFunc("GET /api/sprite/ssh-config", s.requireAuth(s.handleGetSSHConfig))
	mux.HandleFunc("GET /api/export", s.requireAuth(s.handleExport))

	// Proxied routes to sprite
	mux.HandleFunc("GET /api/tasks", s.requireAuth(s.handleProxyTasks))
	mux.HandleFunc("POST /api/tasks", s.requireAuth(s.handleProxyTasks))
	mux.HandleFunc("GET /api/tasks/{id}", s.requireAuth(s.handleProxyTasks))
	mux.HandleFunc("PUT /api/tasks/{id}", s.requireAuth(s.handleProxyTasks))
	mux.HandleFunc("DELETE /api/tasks/{id}", s.requireAuth(s.handleProxyTasks))
	mux.HandleFunc("POST /api/tasks/{id}/queue", s.requireAuth(s.handleProxyTasks))
	mux.HandleFunc("POST /api/tasks/{id}/retry", s.requireAuth(s.handleProxyTasks))
	mux.HandleFunc("POST /api/tasks/{id}/close", s.requireAuth(s.handleProxyTasks))

	mux.HandleFunc("GET /api/projects", s.requireAuth(s.handleProxyTasks))
	mux.HandleFunc("POST /api/projects", s.requireAuth(s.handleProxyTasks))
	mux.HandleFunc("GET /api/projects/{id}", s.requireAuth(s.handleProxyTasks))
	mux.HandleFunc("PUT /api/projects/{id}", s.requireAuth(s.handleProxyTasks))
	mux.HandleFunc("DELETE /api/projects/{id}", s.requireAuth(s.handleProxyTasks))

	mux.HandleFunc("GET /api/settings", s.requireAuth(s.handleProxyTasks))
	mux.HandleFunc("PUT /api/settings", s.requireAuth(s.handleProxyTasks))

	// WebSocket endpoint
	mux.HandleFunc("GET /api/ws", s.requireAuth(s.handleWebSocket))

	// Health check
	mux.HandleFunc("GET /health", s.handleHealth)

	// Static files (React app)
	if s.staticFS != nil {
		mux.Handle("/", s.staticFileHandler())
	}

	// Apply middleware
	handler := s.corsMiddleware(s.loggingMiddleware(mux))

	server := &http.Server{
		Addr:         s.addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	s.logger.Info("starting server", "addr", s.addr)

	// Start server in goroutine
	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Wait for context cancellation or error
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// staticFileHandler returns a handler for serving static files with SPA support.
func (s *Server) staticFileHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Clean the path
		p := path.Clean(r.URL.Path)
		if p == "/" {
			p = "/index.html"
		}

		// Try to open the file
		filePath := strings.TrimPrefix(p, "/")
		f, err := s.staticFS.Open(filePath)
		if err != nil {
			// File not found, serve index.html for SPA routing
			f, err = s.staticFS.Open("index.html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			filePath = "index.html"
		}
		defer f.Close()

		// Get file info
		stat, err := f.Stat()
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		// If it's a directory, serve index.html
		if stat.IsDir() {
			f.Close()
			indexPath := path.Join(filePath, "index.html")
			f, err = s.staticFS.Open(indexPath)
			if err != nil {
				// Fallback to root index.html for SPA
				f, err = s.staticFS.Open("index.html")
				if err != nil {
					http.NotFound(w, r)
					return
				}
			}
			stat, _ = f.Stat()
		}

		// Set content type based on extension
		contentType := getContentType(p)
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}

		// Read file content and serve
		content, err := io.ReadAll(f)
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		w.Write(content)
	})
}

// getContentType returns the content type for a file extension.
func getContentType(path string) string {
	switch {
	case strings.HasSuffix(path, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(path, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(path, ".js"):
		return "application/javascript"
	case strings.HasSuffix(path, ".json"):
		return "application/json"
	case strings.HasSuffix(path, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(path, ".png"):
		return "image/png"
	case strings.HasSuffix(path, ".ico"):
		return "image/x-icon"
	default:
		return ""
	}
}

// handleHealth handles health check requests.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}
