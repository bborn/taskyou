package adapter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/smtp"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/mail"
)

// IMAPAdapter connects to an IMAP server to receive emails.
type IMAPAdapter struct {
	config   *IMAPConfig
	smtp     *SMTPConfig // For sending replies
	logger   *slog.Logger
	emailsCh chan *Email

	mu       sync.Mutex
	client   *imapclient.Client
	stopCh   chan struct{}
	stopped  bool
}

// SMTPConfig holds SMTP configuration for sending.
type SMTPConfig struct {
	Server      string `yaml:"server"`
	Username    string `yaml:"username"`
	PasswordCmd string `yaml:"password_cmd"`
	From        string `yaml:"from"`
}

// NewIMAPAdapter creates a new IMAP adapter.
func NewIMAPAdapter(cfg *IMAPConfig, smtp *SMTPConfig, logger *slog.Logger) *IMAPAdapter {
	if logger == nil {
		logger = slog.Default()
	}
	return &IMAPAdapter{
		config:   cfg,
		smtp:     smtp,
		logger:   logger,
		emailsCh: make(chan *Email, 100),
		stopCh:   make(chan struct{}),
	}
}

func (a *IMAPAdapter) Name() string {
	return "imap"
}

func (a *IMAPAdapter) connect() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.client != nil {
		return nil
	}

	// Get password
	password := ""
	if a.config.PasswordCmd != "" {
		out, err := exec.Command("sh", "-c", a.config.PasswordCmd).Output()
		if err != nil {
			return fmt.Errorf("failed to get password: %w", err)
		}
		password = strings.TrimSpace(string(out))
	}

	// Connect
	client, err := imapclient.DialTLS(a.config.Server, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to IMAP server: %w", err)
	}

	// Login
	if err := client.Login(a.config.Username, password).Wait(); err != nil {
		client.Close()
		return fmt.Errorf("failed to login: %w", err)
	}

	a.client = client
	a.logger.Info("connected to IMAP server", "server", a.config.Server)
	return nil
}

func (a *IMAPAdapter) Start(ctx context.Context) error {
	if err := a.connect(); err != nil {
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

func (a *IMAPAdapter) pollLoop(ctx context.Context, interval time.Duration) {
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

func (a *IMAPAdapter) poll(ctx context.Context) {
	a.mu.Lock()
	client := a.client
	a.mu.Unlock()

	if client == nil {
		if err := a.connect(); err != nil {
			a.logger.Error("failed to reconnect", "error", err)
			return
		}
		a.mu.Lock()
		client = a.client
		a.mu.Unlock()
	}

	folder := a.config.Folder
	if folder == "" {
		folder = "INBOX"
	}

	// Select folder
	_, err := client.Select(folder, nil).Wait()
	if err != nil {
		a.logger.Error("failed to select folder", "folder", folder, "error", err)
		return
	}

	// Search for unseen messages
	criteria := &imap.SearchCriteria{
		NotFlag: []imap.Flag{imap.FlagSeen},
	}
	searchData, err := client.Search(criteria, nil).Wait()
	if err != nil {
		a.logger.Error("failed to search emails", "error", err)
		return
	}

	// Get sequence numbers from search results
	seqNums := searchData.AllSeqNums()
	if len(seqNums) == 0 {
		return
	}

	// Build sequence set
	seqSet := imap.SeqSetNum(seqNums...)

	// Fetch messages
	fetchOptions := &imap.FetchOptions{
		Envelope:    true,
		BodySection: []*imap.FetchItemBodySection{{}},
	}

	messages, err := client.Fetch(seqSet, fetchOptions).Collect()
	if err != nil {
		a.logger.Error("failed to fetch emails", "error", err)
		return
	}

	for _, msg := range messages {
		email, err := a.parseMessage(msg)
		if err != nil {
			a.logger.Warn("failed to parse message", "error", err)
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

func (a *IMAPAdapter) parseMessage(msg *imapclient.FetchMessageBuffer) (*Email, error) {
	env := msg.Envelope
	if env == nil {
		return nil, fmt.Errorf("no envelope")
	}

	email := &Email{
		ID:         env.MessageID,
		Subject:    env.Subject,
		ReceivedAt: env.Date,
	}

	// InReplyTo may have multiple values, take the first
	if len(env.InReplyTo) > 0 {
		email.InReplyTo = env.InReplyTo[0]
	}
	// Store all references
	email.References = env.InReplyTo

	// Parse From
	if len(env.From) > 0 {
		email.From = env.From[0].Addr()
	}

	// Parse To
	for _, addr := range env.To {
		email.To = append(email.To, addr.Addr())
	}

	// Parse body sections
	for _, section := range msg.BodySection {
		mr, err := mail.CreateReader(bytes.NewReader(section))
		if err != nil {
			continue
		}

		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				continue
			}

			switch h := part.Header.(type) {
			case *mail.InlineHeader:
				ct, _, _ := h.ContentType()
				body, _ := io.ReadAll(part.Body)

				if strings.HasPrefix(ct, "text/plain") {
					email.Body = string(body)
				} else if strings.HasPrefix(ct, "text/html") {
					email.HTML = string(body)
				}

			case *mail.AttachmentHeader:
				filename, _ := h.Filename()
				ct, _, _ := h.ContentType()
				data, _ := io.ReadAll(part.Body)

				email.Attachments = append(email.Attachments, Attachment{
					Filename:    filename,
					ContentType: ct,
					Data:        data,
				})
			}
		}
	}

	// Store raw for archival
	for _, section := range msg.BodySection {
		email.Raw = section
		break
	}

	return email, nil
}

func (a *IMAPAdapter) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.stopped {
		return nil
	}
	a.stopped = true

	close(a.stopCh)

	if a.client != nil {
		a.client.Logout()
		a.client.Close()
		a.client = nil
	}

	return nil
}

func (a *IMAPAdapter) Emails() <-chan *Email {
	return a.emailsCh
}

func (a *IMAPAdapter) Send(ctx context.Context, email *OutboundEmail) error {
	if a.smtp == nil {
		a.logger.Warn("SMTP not configured, cannot send email")
		return fmt.Errorf("SMTP not configured")
	}

	// Get password
	password := ""
	if a.smtp.PasswordCmd != "" {
		out, err := exec.Command("sh", "-c", a.smtp.PasswordCmd).Output()
		if err != nil {
			return fmt.Errorf("failed to get SMTP password: %w", err)
		}
		password = strings.TrimSpace(string(out))
	}

	// Parse server address
	host := a.smtp.Server
	if !strings.Contains(host, ":") {
		host = host + ":587"
	}
	hostOnly := strings.Split(host, ":")[0]

	// Use the From override if provided, otherwise fall back to SMTP config.
	// This ensures replies come from the +ty alias address so that
	// when the user replies, the reply routes back to ty-email.
	fromAddr := a.smtp.From
	if email.From != "" {
		fromAddr = email.From
	}

	// Build message
	var msg bytes.Buffer
	msg.WriteString(fmt.Sprintf("From: %s\r\n", fromAddr))
	msg.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(email.To, ", ")))
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", email.Subject))
	if email.InReplyTo != "" {
		msg.WriteString(fmt.Sprintf("In-Reply-To: %s\r\n", email.InReplyTo))
		msg.WriteString(fmt.Sprintf("References: %s\r\n", email.InReplyTo))
	}
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(email.Body)

	// Send via SMTP
	auth := smtp.PlainAuth("", a.smtp.Username, password, hostOnly)
	if err := smtp.SendMail(host, auth, fromAddr, email.To, msg.Bytes()); err != nil {
		return fmt.Errorf("failed to send email: %w", err)
	}

	a.logger.Info("sent email via SMTP", "to", email.To, "subject", email.Subject)
	return nil
}

func (a *IMAPAdapter) MarkProcessed(ctx context.Context, emailID string) error {
	// Mark as seen in IMAP
	// This is a simplified implementation - in practice you'd need to
	// look up the message by Message-ID and mark it
	a.logger.Debug("marking email as processed", "id", emailID)
	return nil
}
