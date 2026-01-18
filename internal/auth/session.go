package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/bborn/workflow/internal/hostdb"
)

const (
	// SessionCookieName is the name of the session cookie.
	SessionCookieName = "taskyou_session"

	// OAuthStateCookieName is the name of the OAuth state cookie.
	OAuthStateCookieName = "taskyou_oauth_state"
)

// SessionManager handles session creation and validation.
type SessionManager struct {
	db       *hostdb.DB
	secure   bool // Use secure cookies (HTTPS only)
	domain   string
	maxAge   time.Duration
}

// NewSessionManager creates a new session manager.
func NewSessionManager(db *hostdb.DB, secure bool, domain string) *SessionManager {
	return &SessionManager{
		db:     db,
		secure: secure,
		domain: domain,
		maxAge: hostdb.DefaultSessionDuration,
	}
}

// CreateSession creates a new session and sets the session cookie.
func (sm *SessionManager) CreateSession(w http.ResponseWriter, userID string) (*hostdb.Session, error) {
	session, err := sm.db.CreateSession(userID, sm.maxAge)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	sm.setSessionCookie(w, session.ID, session.ExpiresAt)
	return session, nil
}

// GetSession retrieves the session from the request cookie.
// Returns nil if no valid session exists.
func (sm *SessionManager) GetSession(r *http.Request) (*hostdb.Session, error) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return nil, nil // No cookie, no session
	}

	session, err := sm.db.GetSession(cookie.Value)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	return session, nil
}

// GetSessionWithUser retrieves the session and user from the request cookie.
// Returns nil for both if no valid session exists.
func (sm *SessionManager) GetSessionWithUser(r *http.Request) (*hostdb.Session, *hostdb.User, error) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return nil, nil, nil // No cookie, no session
	}

	session, user, err := sm.db.GetSessionWithUser(cookie.Value)
	if err != nil {
		return nil, nil, fmt.Errorf("get session with user: %w", err)
	}

	return session, user, nil
}

// DeleteSession deletes the session and clears the cookie.
func (sm *SessionManager) DeleteSession(w http.ResponseWriter, r *http.Request) error {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return nil // No cookie to delete
	}

	if err := sm.db.DeleteSession(cookie.Value); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}

	sm.clearSessionCookie(w)
	return nil
}

// setSessionCookie sets the session cookie.
func (sm *SessionManager) setSessionCookie(w http.ResponseWriter, sessionID string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sessionID,
		Path:     "/",
		Domain:   sm.domain,
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
		HttpOnly: true,
		Secure:   sm.secure,
		SameSite: http.SameSiteLaxMode, // Lax for OAuth redirects to work
	})
}

// clearSessionCookie clears the session cookie.
func (sm *SessionManager) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		Domain:   sm.domain,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   sm.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// GenerateOAuthState generates a random state string for OAuth.
func GenerateOAuthState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// SetOAuthStateCookie sets the OAuth state cookie.
func (sm *SessionManager) SetOAuthStateCookie(w http.ResponseWriter, state string) {
	http.SetCookie(w, &http.Cookie{
		Name:     OAuthStateCookieName,
		Value:    state,
		Path:     "/",
		Domain:   sm.domain,
		MaxAge:   600, // 10 minutes
		HttpOnly: true,
		Secure:   sm.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// GetOAuthStateCookie retrieves and clears the OAuth state cookie.
func (sm *SessionManager) GetOAuthStateCookie(w http.ResponseWriter, r *http.Request) string {
	cookie, err := r.Cookie(OAuthStateCookieName)
	if err != nil {
		return ""
	}

	// Clear the cookie
	http.SetCookie(w, &http.Cookie{
		Name:     OAuthStateCookieName,
		Value:    "",
		Path:     "/",
		Domain:   sm.domain,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   sm.secure,
		SameSite: http.SameSiteLaxMode,
	})

	return cookie.Value
}
