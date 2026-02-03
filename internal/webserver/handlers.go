package webserver

import (
	"fmt"
	"net/http"

	"github.com/bborn/workflow/internal/auth"
)

// handleGoogleAuth initiates Google OAuth flow.
func (s *Server) handleGoogleAuth(w http.ResponseWriter, r *http.Request) {
	s.handleOAuthStart(w, r, auth.ProviderGoogle)
}

// handleGitHubAuth initiates GitHub OAuth flow.
func (s *Server) handleGitHubAuth(w http.ResponseWriter, r *http.Request) {
	s.handleOAuthStart(w, r, auth.ProviderGitHub)
}

// handleOAuthStart initiates an OAuth flow for the given provider.
func (s *Server) handleOAuthStart(w http.ResponseWriter, r *http.Request, provider auth.Provider) {
	if !auth.IsConfigured(provider) {
		jsonError(w, fmt.Sprintf("%s OAuth not configured", provider), http.StatusServiceUnavailable)
		return
	}

	// Generate state
	state, err := auth.GenerateOAuthState()
	if err != nil {
		s.logger.Error("generate oauth state failed", "error", err)
		jsonError(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Store state in cookie with provider info
	stateWithProvider := fmt.Sprintf("%s:%s", provider, state)
	s.sessionMgr.SetOAuthStateCookie(w, stateWithProvider)

	// Get OAuth config
	redirectURL := fmt.Sprintf("%s/api/auth/callback", s.baseURL)
	config := auth.GetConfig(provider, redirectURL)

	// Redirect to provider
	url := config.OAuth2.AuthCodeURL(state)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

// handleOAuthCallback handles the OAuth callback from the provider.
func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	// Get state from cookie
	stateWithProvider := s.sessionMgr.GetOAuthStateCookie(w, r)
	if stateWithProvider == "" {
		jsonError(w, "Invalid OAuth state", http.StatusBadRequest)
		return
	}

	// Parse provider from state
	var provider auth.Provider
	var expectedState string
	if n, _ := fmt.Sscanf(stateWithProvider, "%s:%s", &provider, &expectedState); n != 2 {
		// Fallback to parsing with string operations
		for _, p := range []auth.Provider{auth.ProviderGoogle, auth.ProviderGitHub} {
			prefix := string(p) + ":"
			if len(stateWithProvider) > len(prefix) && stateWithProvider[:len(prefix)] == prefix {
				provider = p
				expectedState = stateWithProvider[len(prefix):]
				break
			}
		}
	}

	if provider == "" {
		jsonError(w, "Invalid OAuth state format", http.StatusBadRequest)
		return
	}

	// Verify state
	state := r.URL.Query().Get("state")
	if state != expectedState {
		jsonError(w, "OAuth state mismatch", http.StatusBadRequest)
		return
	}

	// Check for error from provider
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		errDesc := r.URL.Query().Get("error_description")
		s.logger.Error("oauth error", "provider", provider, "error", errMsg, "description", errDesc)
		http.Redirect(w, r, fmt.Sprintf("/?error=%s", errMsg), http.StatusTemporaryRedirect)
		return
	}

	// Exchange code for tokens
	code := r.URL.Query().Get("code")
	if code == "" {
		jsonError(w, "Missing authorization code", http.StatusBadRequest)
		return
	}

	redirectURL := fmt.Sprintf("%s/api/auth/callback", s.baseURL)
	config := auth.GetConfig(provider, redirectURL)

	token, err := auth.ExchangeCode(r.Context(), config, code)
	if err != nil {
		s.logger.Error("exchange code failed", "error", err)
		jsonError(w, "Failed to exchange authorization code", http.StatusInternalServerError)
		return
	}

	// Get user info from provider
	userInfo, err := auth.GetUserInfo(r.Context(), config, token)
	if err != nil {
		s.logger.Error("get user info failed", "error", err)
		jsonError(w, "Failed to get user information", http.StatusInternalServerError)
		return
	}

	// Get or create user
	expiresAt := auth.TokenExpiry(token)
	user, isNew, err := s.db.GetOrCreateUserByOAuth(
		string(provider),
		userInfo.ProviderAccountID,
		userInfo.Email,
		userInfo.Name,
		userInfo.AvatarURL,
		token.AccessToken,
		token.RefreshToken,
		expiresAt,
	)
	if err != nil {
		s.logger.Error("get or create user failed", "error", err)
		jsonError(w, "Failed to create user", http.StatusInternalServerError)
		return
	}

	s.logger.Info("user authenticated",
		"user_id", user.ID,
		"email", user.Email,
		"provider", provider,
		"new_user", isNew,
	)

	// Create session
	if _, err := s.sessionMgr.CreateSession(w, user.ID); err != nil {
		s.logger.Error("create session failed", "error", err)
		jsonError(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	// Provision sprite for new users
	if isNew && s.spriteMgr != nil {
		go func() {
			if _, err := s.spriteMgr.ProvisionSprite(r.Context(), user.ID); err != nil {
				s.logger.Error("provision sprite failed", "user_id", user.ID, "error", err)
			}
		}()
	}

	// Redirect to dashboard
	http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
}

// handleLogout logs the user out.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.sessionMgr.DeleteSession(w, r); err != nil {
		s.logger.Error("delete session failed", "error", err)
	}
	jsonResponse(w, map[string]bool{"success": true}, http.StatusOK)
}

// handleGetMe returns the current user's information.
func (s *Server) handleGetMe(w http.ResponseWriter, r *http.Request) {
	_, user, err := s.sessionMgr.GetSessionWithUser(r)
	if err != nil {
		s.logger.Error("get session failed", "error", err)
		jsonError(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if user == nil {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Get user's sprite status
	var spriteStatus interface{}
	if s.spriteMgr != nil {
		sprite, err := s.spriteMgr.GetUserSprite(r.Context(), user.ID)
		if err == nil && sprite != nil {
			spriteStatus = map[string]interface{}{
				"id":         sprite.ID,
				"name":       sprite.SpriteName,
				"status":     sprite.Status,
				"region":     sprite.Region,
				"created_at": sprite.CreatedAt,
			}
		}
	}

	jsonResponse(w, map[string]interface{}{
		"id":         user.ID,
		"email":      user.Email,
		"name":       user.Name,
		"avatar_url": user.AvatarURL,
		"sprite":     spriteStatus,
	}, http.StatusOK)
}

// handleGetSprite returns the user's sprite status.
func (s *Server) handleGetSprite(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r)
	if user == nil {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if s.spriteMgr == nil {
		jsonError(w, "Sprites not configured", http.StatusServiceUnavailable)
		return
	}

	sprite, err := s.spriteMgr.GetUserSprite(r.Context(), user.ID)
	if err != nil {
		s.logger.Error("get user sprite failed", "error", err)
		jsonError(w, "Failed to get sprite", http.StatusInternalServerError)
		return
	}

	if sprite == nil {
		jsonResponse(w, map[string]interface{}{"sprite": nil}, http.StatusOK)
		return
	}

	// Get current status from Fly
	status, _ := s.spriteMgr.GetSpriteStatus(r.Context(), sprite)

	jsonResponse(w, map[string]interface{}{
		"id":         sprite.ID,
		"name":       sprite.SpriteName,
		"status":     status,
		"region":     sprite.Region,
		"created_at": sprite.CreatedAt,
	}, http.StatusOK)
}

// handleCreateSprite creates a sprite for the user.
func (s *Server) handleCreateSprite(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r)
	if user == nil {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if s.spriteMgr == nil {
		jsonError(w, "Sprites not configured", http.StatusServiceUnavailable)
		return
	}

	sprite, err := s.spriteMgr.ProvisionSprite(r.Context(), user.ID)
	if err != nil {
		s.logger.Error("provision sprite failed", "error", err)
		jsonError(w, fmt.Sprintf("Failed to create sprite: %v", err), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, map[string]interface{}{
		"id":         sprite.ID,
		"name":       sprite.SpriteName,
		"status":     sprite.Status,
		"region":     sprite.Region,
		"created_at": sprite.CreatedAt,
	}, http.StatusCreated)
}

// handleStartSprite starts the user's sprite.
func (s *Server) handleStartSprite(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r)
	if user == nil {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if s.spriteMgr == nil {
		jsonError(w, "Sprites not configured", http.StatusServiceUnavailable)
		return
	}

	sprite, err := s.spriteMgr.GetUserSprite(r.Context(), user.ID)
	if err != nil {
		jsonError(w, "Failed to get sprite", http.StatusInternalServerError)
		return
	}

	if sprite == nil {
		jsonError(w, "No sprite found", http.StatusNotFound)
		return
	}

	if err := s.spriteMgr.StartSprite(r.Context(), sprite); err != nil {
		s.logger.Error("start sprite failed", "error", err)
		jsonError(w, fmt.Sprintf("Failed to start sprite: %v", err), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, map[string]bool{"success": true}, http.StatusOK)
}

// handleStopSprite stops the user's sprite.
func (s *Server) handleStopSprite(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r)
	if user == nil {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if s.spriteMgr == nil {
		jsonError(w, "Sprites not configured", http.StatusServiceUnavailable)
		return
	}

	sprite, err := s.spriteMgr.GetUserSprite(r.Context(), user.ID)
	if err != nil {
		jsonError(w, "Failed to get sprite", http.StatusInternalServerError)
		return
	}

	if sprite == nil {
		jsonError(w, "No sprite found", http.StatusNotFound)
		return
	}

	if err := s.spriteMgr.StopSprite(r.Context(), sprite); err != nil {
		s.logger.Error("stop sprite failed", "error", err)
		jsonError(w, fmt.Sprintf("Failed to stop sprite: %v", err), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, map[string]bool{"success": true}, http.StatusOK)
}

// handleDestroySprite destroys the user's sprite.
func (s *Server) handleDestroySprite(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r)
	if user == nil {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if s.spriteMgr == nil {
		jsonError(w, "Sprites not configured", http.StatusServiceUnavailable)
		return
	}

	sprite, err := s.spriteMgr.GetUserSprite(r.Context(), user.ID)
	if err != nil {
		jsonError(w, "Failed to get sprite", http.StatusInternalServerError)
		return
	}

	if sprite == nil {
		jsonError(w, "No sprite found", http.StatusNotFound)
		return
	}

	if err := s.spriteMgr.DestroySprite(r.Context(), sprite); err != nil {
		s.logger.Error("destroy sprite failed", "error", err)
		jsonError(w, fmt.Sprintf("Failed to destroy sprite: %v", err), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, map[string]bool{"success": true}, http.StatusOK)
}

// handleGetSSHConfig returns SSH config for connecting to the user's sprite.
func (s *Server) handleGetSSHConfig(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r)
	if user == nil {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if s.spriteMgr == nil {
		jsonError(w, "Sprites not configured", http.StatusServiceUnavailable)
		return
	}

	sprite, err := s.spriteMgr.GetUserSprite(r.Context(), user.ID)
	if err != nil {
		jsonError(w, "Failed to get sprite", http.StatusInternalServerError)
		return
	}

	if sprite == nil {
		jsonError(w, "No sprite found", http.StatusNotFound)
		return
	}

	config, err := s.spriteMgr.GetSSHConfig(r.Context(), sprite)
	if err != nil {
		jsonError(w, "Failed to get SSH config", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(config))
}

// handleExport exports the user's task database.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r)
	if user == nil {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if s.spriteMgr == nil {
		jsonError(w, "Sprites not configured", http.StatusServiceUnavailable)
		return
	}

	sprite, err := s.spriteMgr.GetUserSprite(r.Context(), user.ID)
	if err != nil {
		jsonError(w, "Failed to get sprite", http.StatusInternalServerError)
		return
	}

	if sprite == nil {
		jsonError(w, "No sprite found", http.StatusNotFound)
		return
	}

	data, err := s.spriteMgr.ExportDatabase(r.Context(), sprite)
	if err != nil {
		s.logger.Error("export database failed", "error", err)
		jsonError(w, "Failed to export database", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=tasks-export.db")
	w.Write(data)
}

// handleProxyTasks proxies requests to the user's sprite.
func (s *Server) handleProxyTasks(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r)
	if user == nil {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if s.spriteProxy == nil {
		jsonError(w, "Sprites not configured", http.StatusServiceUnavailable)
		return
	}

	sprite, err := s.spriteMgr.GetUserSprite(r.Context(), user.ID)
	if err != nil {
		jsonError(w, "Failed to get sprite", http.StatusInternalServerError)
		return
	}

	if sprite == nil {
		jsonError(w, "No sprite found. Please create a sprite first.", http.StatusNotFound)
		return
	}

	if err := s.spriteProxy.ProxyRequest(r.Context(), sprite, w, r); err != nil {
		s.logger.Error("proxy request failed", "error", err)
		jsonError(w, "Sprite unavailable", http.StatusBadGateway)
	}
}

// handleWebSocket handles WebSocket connections.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r)
	if user == nil {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if s.spriteProxy == nil {
		jsonError(w, "Sprites not configured", http.StatusServiceUnavailable)
		return
	}

	sprite, err := s.spriteMgr.GetUserSprite(r.Context(), user.ID)
	if err != nil {
		jsonError(w, "Failed to get sprite", http.StatusInternalServerError)
		return
	}

	if sprite == nil {
		jsonError(w, "No sprite found", http.StatusNotFound)
		return
	}

	if err := s.spriteProxy.ProxyWebSocket(r.Context(), sprite, w, r); err != nil {
		s.logger.Error("websocket proxy failed", "error", err)
	}
}
