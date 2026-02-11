package adapter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// GmailAdapter connects to Gmail via OAuth2.
type GmailAdapter struct {
	config   *GmailConfig
	logger   *slog.Logger
	emailsCh chan *Email

	mu      sync.Mutex
	service *gmail.Service
	stopCh  chan struct{}
	stopped bool
}

// NewGmailAdapter creates a new Gmail adapter.
func NewGmailAdapter(cfg *GmailConfig, logger *slog.Logger) *GmailAdapter {
	if logger == nil {
		logger = slog.Default()
	}
	return &GmailAdapter{
		config:   cfg,
		logger:   logger,
		emailsCh: make(chan *Email, 100),
		stopCh:   make(chan struct{}),
	}
}

func (a *GmailAdapter) Name() string {
	return "gmail"
}

// Authenticate performs OAuth2 flow and returns the Gmail service.
// If tokenFile exists and is valid, uses cached token.
// Otherwise, opens browser for user consent.
func (a *GmailAdapter) Authenticate(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.service != nil {
		return nil
	}

	// Read credentials
	credBytes, err := os.ReadFile(a.config.CredentialsFile)
	if err != nil {
		return fmt.Errorf("failed to read credentials file: %w", err)
	}

	config, err := google.ConfigFromJSON(credBytes, gmail.GmailReadonlyScope, gmail.GmailSendScope, gmail.GmailModifyScope)
	if err != nil {
		return fmt.Errorf("failed to parse credentials: %w", err)
	}

	// Try to load existing token
	token, err := a.loadToken()
	if err != nil {
		// Need to get new token via OAuth flow
		token, err = a.getTokenFromWeb(ctx, config)
		if err != nil {
			return fmt.Errorf("failed to get token: %w", err)
		}
		if err := a.saveToken(token); err != nil {
			a.logger.Warn("failed to save token", "error", err)
		}
	}

	// Create client and service
	client := config.Client(ctx, token)
	service, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("failed to create Gmail service: %w", err)
	}

	a.service = service
	a.logger.Info("connected to Gmail")
	return nil
}

func (a *GmailAdapter) loadToken() (*oauth2.Token, error) {
	f, err := os.Open(a.config.TokenFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var token oauth2.Token
	if err := json.NewDecoder(f).Decode(&token); err != nil {
		return nil, err
	}

	// Check if token is expired and can't be refreshed
	if token.Expiry.Before(time.Now()) && token.RefreshToken == "" {
		return nil, fmt.Errorf("token expired")
	}

	return &token, nil
}

func (a *GmailAdapter) saveToken(token *oauth2.Token) error {
	// Ensure directory exists
	dir := filepath.Dir(a.config.TokenFile)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	f, err := os.OpenFile(a.config.TokenFile, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	return json.NewEncoder(f).Encode(token)
}

func (a *GmailAdapter) getTokenFromWeb(ctx context.Context, config *oauth2.Config) (*oauth2.Token, error) {
	// Start local server to receive callback
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	// Use a random available port
	server := &http.Server{Addr: "localhost:8089"}

	http.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no code in callback")
			fmt.Fprintf(w, "Error: no authorization code received")
			return
		}
		codeCh <- code
		fmt.Fprintf(w, "Authorization successful! You can close this window.")
	})

	go func() {
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Set redirect URL to local server
	config.RedirectURL = "http://localhost:8089/callback"

	// Generate auth URL
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)

	fmt.Printf("\nOpen this URL in your browser to authorize ty-email:\n\n%s\n\nWaiting for authorization...\n", authURL)

	// Wait for code or error
	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		server.Shutdown(ctx)
		return nil, err
	case <-time.After(5 * time.Minute):
		server.Shutdown(ctx)
		return nil, fmt.Errorf("authorization timeout")
	case <-ctx.Done():
		server.Shutdown(ctx)
		return nil, ctx.Err()
	}

	server.Shutdown(ctx)

	// Exchange code for token
	token, err := config.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}

	return token, nil
}

func (a *GmailAdapter) Start(ctx context.Context) error {
	if err := a.Authenticate(ctx); err != nil {
		return err
	}

	// Parse poll interval
	pollInterval := 30 * time.Second
	if a.config.PollInterval != "" {
		d, err := time.ParseDuration(a.config.PollInterval)
		if err == nil {
			pollInterval = d
		}
	}

	// Start poll loop
	go a.pollLoop(ctx, pollInterval)

	return nil
}

func (a *GmailAdapter) pollLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial poll
	a.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopCh:
			return
		case <-ticker.C:
			a.poll(ctx)
		}
	}
}

func (a *GmailAdapter) poll(ctx context.Context) {
	a.mu.Lock()
	service := a.service
	a.mu.Unlock()

	if service == nil {
		return
	}

	// List unread messages
	query := "is:unread"
	if a.config.Label != "" {
		query += " label:" + a.config.Label
	}

	resp, err := service.Users.Messages.List("me").Q(query).MaxResults(20).Do()
	if err != nil {
		a.logger.Error("failed to list messages", "error", err)
		return
	}

	for _, msg := range resp.Messages {
		email, err := a.fetchMessage(ctx, msg.Id)
		if err != nil {
			a.logger.Warn("failed to fetch message", "id", msg.Id, "error", err)
			continue
		}

		select {
		case a.emailsCh <- email:
			a.logger.Info("received email", "from", email.From, "subject", email.Subject)
		default:
			a.logger.Warn("email channel full, dropping message")
		}
	}
}

func (a *GmailAdapter) fetchMessage(ctx context.Context, id string) (*Email, error) {
	msg, err := a.service.Users.Messages.Get("me", id).Format("full").Do()
	if err != nil {
		return nil, err
	}

	email := &Email{
		ID:         id,
		ReceivedAt: time.UnixMilli(msg.InternalDate),
	}

	// Parse headers
	for _, header := range msg.Payload.Headers {
		switch strings.ToLower(header.Name) {
		case "from":
			email.From = header.Value
		case "to":
			email.To = strings.Split(header.Value, ",")
		case "subject":
			email.Subject = header.Value
		case "message-id":
			email.ID = header.Value
		case "in-reply-to":
			email.InReplyTo = header.Value
		case "references":
			email.References = strings.Fields(header.Value)
		}
	}

	// Parse body
	email.Body = a.extractBody(msg.Payload, "text/plain")
	if email.Body == "" {
		email.HTML = a.extractBody(msg.Payload, "text/html")
	}

	// Parse attachments
	email.Attachments = a.extractAttachments(msg.Payload)

	return email, nil
}

func (a *GmailAdapter) extractBody(payload *gmail.MessagePart, mimeType string) string {
	if payload.MimeType == mimeType && payload.Body != nil && payload.Body.Data != "" {
		data, err := base64.URLEncoding.DecodeString(payload.Body.Data)
		if err == nil {
			return string(data)
		}
	}

	for _, part := range payload.Parts {
		if body := a.extractBody(part, mimeType); body != "" {
			return body
		}
	}

	return ""
}

func (a *GmailAdapter) extractAttachments(payload *gmail.MessagePart) []Attachment {
	var attachments []Attachment

	if payload.Filename != "" && payload.Body != nil && payload.Body.AttachmentId != "" {
		// Would need to fetch attachment data separately
		attachments = append(attachments, Attachment{
			Filename:    payload.Filename,
			ContentType: payload.MimeType,
		})
	}

	for _, part := range payload.Parts {
		attachments = append(attachments, a.extractAttachments(part)...)
	}

	return attachments
}

func (a *GmailAdapter) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.stopped {
		return nil
	}
	a.stopped = true

	close(a.stopCh)
	a.service = nil

	return nil
}

func (a *GmailAdapter) Emails() <-chan *Email {
	return a.emailsCh
}

func (a *GmailAdapter) Send(ctx context.Context, email *OutboundEmail) error {
	a.mu.Lock()
	service := a.service
	a.mu.Unlock()

	if service == nil {
		return fmt.Errorf("not connected")
	}

	// Build raw email
	var msg strings.Builder
	if email.From != "" {
		msg.WriteString(fmt.Sprintf("From: %s\r\n", email.From))
	}
	msg.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(email.To, ", ")))
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", email.Subject))
	if email.InReplyTo != "" {
		msg.WriteString(fmt.Sprintf("In-Reply-To: %s\r\n", email.InReplyTo))
		msg.WriteString(fmt.Sprintf("References: %s\r\n", email.InReplyTo))
	}
	msg.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(email.Body)

	raw := base64.URLEncoding.EncodeToString([]byte(msg.String()))

	_, err := service.Users.Messages.Send("me", &gmail.Message{
		Raw: raw,
	}).Do()

	return err
}

func (a *GmailAdapter) MarkProcessed(ctx context.Context, emailID string) error {
	a.mu.Lock()
	service := a.service
	a.mu.Unlock()

	if service == nil {
		return fmt.Errorf("not connected")
	}

	// Remove UNREAD label
	_, err := service.Users.Messages.Modify("me", emailID, &gmail.ModifyMessageRequest{
		RemoveLabelIds: []string{"UNREAD"},
	}).Do()

	return err
}
