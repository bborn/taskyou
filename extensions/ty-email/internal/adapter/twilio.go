package adapter

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// TwilioConfig holds Twilio SMS configuration.
type TwilioConfig struct {
	AccountSID    string `yaml:"account_sid"`     // Twilio Account SID
	AccountSIDCmd string `yaml:"account_sid_cmd"` // Command to get Account SID
	AuthToken     string `yaml:"auth_token"`      // Twilio Auth Token
	AuthTokenCmd  string `yaml:"auth_token_cmd"`  // Command to get Auth Token
	PhoneNumber   string `yaml:"phone_number"`    // Your Twilio phone number (e.g., +15551234567)
	WebhookListen string `yaml:"webhook_listen"`  // Address to listen on (e.g., ":8080")
	WebhookPath   string `yaml:"webhook_path"`    // Path for webhook (e.g., "/sms")
	ValidateHMAC  bool   `yaml:"validate_hmac"`   // Validate Twilio request signatures (recommended for production)
	WebhookURL    string `yaml:"webhook_url"`     // Public URL for signature validation (e.g., https://example.com/sms)
}

// TwilioAdapter handles inbound/outbound SMS via Twilio.
type TwilioAdapter struct {
	config   *TwilioConfig
	logger   *slog.Logger
	emailsCh chan *Email

	mu         sync.Mutex
	server     *http.Server
	stopCh     chan struct{}
	stopped    bool
	accountSID string
	authToken  string
}

// NewTwilioAdapter creates a new Twilio SMS adapter.
func NewTwilioAdapter(cfg *TwilioConfig, logger *slog.Logger) *TwilioAdapter {
	if logger == nil {
		logger = slog.Default()
	}
	return &TwilioAdapter{
		config:   cfg,
		logger:   logger,
		emailsCh: make(chan *Email, 100),
		stopCh:   make(chan struct{}),
	}
}

func (a *TwilioAdapter) Name() string {
	return "twilio"
}

// resolveCredentials loads the Twilio Account SID and Auth Token.
func (a *TwilioAdapter) resolveCredentials() error {
	// Resolve Account SID
	sid := a.config.AccountSID
	if sid == "" && a.config.AccountSIDCmd != "" {
		out, err := exec.Command("sh", "-c", a.config.AccountSIDCmd).Output()
		if err != nil {
			return fmt.Errorf("failed to get account SID: %w", err)
		}
		sid = strings.TrimSpace(string(out))
	}
	if sid == "" {
		return fmt.Errorf("twilio account_sid or account_sid_cmd is required")
	}

	// Resolve Auth Token
	token := a.config.AuthToken
	if token == "" && a.config.AuthTokenCmd != "" {
		out, err := exec.Command("sh", "-c", a.config.AuthTokenCmd).Output()
		if err != nil {
			return fmt.Errorf("failed to get auth token: %w", err)
		}
		token = strings.TrimSpace(string(out))
	}
	if token == "" {
		return fmt.Errorf("twilio auth_token or auth_token_cmd is required")
	}

	a.accountSID = sid
	a.authToken = token
	return nil
}

func (a *TwilioAdapter) Start(ctx context.Context) error {
	if err := a.resolveCredentials(); err != nil {
		return err
	}

	listen := a.config.WebhookListen
	if listen == "" {
		listen = ":8080"
	}

	path := a.config.WebhookPath
	if path == "" {
		path = "/sms"
	}

	mux := http.NewServeMux()
	mux.HandleFunc(path, a.handleWebhook)

	a.server = &http.Server{
		Addr:    listen,
		Handler: mux,
	}

	go func() {
		a.logger.Info("starting Twilio webhook server", "listen", listen, "path", path)
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			a.logger.Error("webhook server error", "error", err)
		}
	}()

	return nil
}

func (a *TwilioAdapter) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.stopped {
		return nil
	}
	a.stopped = true
	close(a.stopCh)

	if a.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return a.server.Shutdown(ctx)
	}

	return nil
}

func (a *TwilioAdapter) Emails() <-chan *Email {
	return a.emailsCh
}

// handleWebhook processes inbound SMS from Twilio.
// Twilio sends a POST with form fields: MessageSid, From, To, Body, etc.
func (a *TwilioAdapter) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		a.logger.Error("failed to parse webhook form", "error", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Validate Twilio signature if enabled
	if a.config.ValidateHMAC {
		if !a.validateSignature(r) {
			a.logger.Warn("invalid Twilio signature")
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	messageSid := r.FormValue("MessageSid")
	from := r.FormValue("From")
	to := r.FormValue("To")
	body := r.FormValue("Body")

	if messageSid == "" || from == "" {
		a.logger.Warn("missing required fields in webhook")
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Map SMS to Email struct.
	// Use the sender's phone number as the thread reference for conversation continuity.
	email := &Email{
		ID:         messageSid,
		From:       from,
		To:         []string{to},
		Subject:    smsSubject(body),
		Body:       body,
		InReplyTo:  "sms-thread-" + from, // Phone number as thread ID
		References: []string{"sms-thread-" + from},
		ReceivedAt: time.Now(),
	}

	select {
	case a.emailsCh <- email:
		a.logger.Info("received SMS", "from", from, "body_len", len(body))
	default:
		a.logger.Warn("SMS channel full, dropping message")
	}

	// Respond with empty TwiML to acknowledge receipt (no auto-reply from Twilio).
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "<?xml version=\"1.0\" encoding=\"UTF-8\"?><Response></Response>")
}

// validateSignature validates the Twilio request signature.
// See: https://www.twilio.com/docs/usage/security#validating-requests
func (a *TwilioAdapter) validateSignature(r *http.Request) bool {
	signature := r.Header.Get("X-Twilio-Signature")
	if signature == "" {
		return false
	}

	webhookURL := a.config.WebhookURL
	if webhookURL == "" {
		// Fall back to constructing from request
		scheme := "https"
		if r.TLS == nil {
			scheme = "http"
		}
		webhookURL = scheme + "://" + r.Host + r.URL.Path
	}

	return ValidateTwilioSignature(a.authToken, webhookURL, r.PostForm, signature)
}

// ValidateTwilioSignature checks that a request came from Twilio.
// It builds the validation string from the URL + sorted POST params,
// then computes HMAC-SHA1 with the auth token and compares to the signature.
func ValidateTwilioSignature(authToken, url string, params url.Values, expectedSig string) bool {
	// Build the string to sign: URL + sorted param key/value pairs
	data := url
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		data += k + params.Get(k)
	}

	// Compute HMAC-SHA1
	mac := hmac.New(sha1.New, []byte(authToken))
	mac.Write([]byte(data))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(expectedSig))
}

// Send sends an outbound SMS via the Twilio REST API.
func (a *TwilioAdapter) Send(ctx context.Context, email *OutboundEmail) error {
	if len(email.To) == 0 {
		return fmt.Errorf("no recipient specified")
	}

	// Truncate body for SMS friendliness.
	// Twilio handles segmentation, but keep replies concise.
	body := email.Body
	if len(body) > 1600 {
		body = body[:1597] + "..."
	}

	// Send to each recipient
	for _, to := range email.To {
		if err := a.sendSMS(ctx, to, body); err != nil {
			return fmt.Errorf("failed to send SMS to %s: %w", to, err)
		}
	}

	return nil
}

// sendSMS sends a single SMS via the Twilio REST API.
func (a *TwilioAdapter) sendSMS(ctx context.Context, to, body string) error {
	apiURL := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json", a.accountSID)

	data := url.Values{}
	data.Set("To", to)
	data.Set("From", a.config.PhoneNumber)
	data.Set("Body", body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}

	req.SetBasicAuth(a.accountSID, a.authToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("twilio API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	a.logger.Info("sent SMS", "to", to, "body_len", len(body))
	return nil
}

func (a *TwilioAdapter) MarkProcessed(ctx context.Context, emailID string) error {
	// No-op for SMS - there's no concept of marking SMS as read in Twilio.
	a.logger.Debug("marking SMS as processed", "id", emailID)
	return nil
}

// smsSubject generates a synthetic subject line from an SMS body.
// Since SMS has no subject, we derive one from the first line of the message.
func smsSubject(body string) string {
	line := body
	if idx := strings.IndexAny(line, "\n\r"); idx != -1 {
		line = line[:idx]
	}
	line = strings.TrimSpace(line)

	if len(line) > 60 {
		line = line[:57] + "..."
	}

	if line == "" {
		return "[SMS]"
	}

	return "[SMS] " + line
}
