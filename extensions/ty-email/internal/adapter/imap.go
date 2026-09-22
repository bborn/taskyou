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

	mu      sync.Mutex
	client  *imapclient.Client
	stopCh  chan struct{}
	stopped bool
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
	//
	// poll returns an error but already logs every failure path itself, so
	// callers can safely drop it here: continued polling is desired even after
	// a transient failure (the next ticker tick will retry).
	_ = a.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopCh:
			return
		case <-ticker.C:
			_ = a.poll(ctx)
		}
	}
}

// poll runs a single fetch cycle. It returns an error (already logged via
// a.logger) so one-shot callers like PollOnce can propagate failures.
//
// The fetch uses BODY.PEEK[] (Peek: true) so the IMAP server does NOT
// implicitly set \Seen during the FETCH (RFC 3501 §6.4.5). Messages are
// only marked \Seen by MarkProcessed, which runs after the email has been
// successfully handled. Without PEEK, any email that is fetched but then
// dropped (e.g. the consumer is slow, the adapter is stopped, or the email
// is deferred by the rate limiter) would be permanently skipped by the next
// poll's NotFlag:\Seen search, causing silent permanent mail loss.
//
// The push into emailsCh blocks (with select on stopCh/ctx.Done) rather than
// dropping on a full channel: a slow consumer must exert backpressure instead
// of causing loss. The drop-on-full arm previously combined with the
// non-PEEK FETCH to make every dropped message permanently invisible.
func (a *IMAPAdapter) poll(ctx context.Context) error {
	a.mu.Lock()
	client := a.client
	a.mu.Unlock()

	if client == nil {
		if err := a.connect(); err != nil {
			a.logger.Error("failed to reconnect", "error", err)
			return err
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
		// A failed SELECT usually means the connection died. Drop it so the
		// next poll reconnects instead of failing forever.
		a.resetConnection()
		return err
	}

	// Search for unseen messages
	criteria := &imap.SearchCriteria{
		NotFlag: []imap.Flag{imap.FlagSeen},
	}
	searchData, err := client.Search(criteria, nil).Wait()
	if err != nil {
		a.logger.Error("failed to search emails", "error", err)
		a.resetConnection()
		return err
	}

	// Get sequence numbers from search results
	seqNums := searchData.AllSeqNums()
	if len(seqNums) == 0 {
		return nil
	}

	// Build sequence set
	seqSet := imap.SeqSetNum(seqNums...)

	// Fetch messages with PEEK so \Seen is only set by MarkProcessed after
	// successful processing. See the poll doc comment for why this matters.
	fetchOptions := &imap.FetchOptions{
		Envelope:    true,
		BodySection: []*imap.FetchItemBodySection{{Peek: true}},
	}

	messages, err := client.Fetch(seqSet, fetchOptions).Collect()
	if err != nil {
		a.logger.Error("failed to fetch emails", "error", err)
		a.resetConnection()
		return err
	}

	for _, msg := range messages {
		email, err := a.parseMessage(msg)
		if err != nil {
			a.logger.Warn("failed to parse message", "error", err)
			continue
		}

		// Block until the consumer drains, the adapter is stopped, or the
		// context is cancelled. Blocking (rather than dropping on a full
		// channel) turns a >cap backlog into backpressure instead of loss;
		// combined with PEEK, even a cancelled/abandoned poll leaves the
		// unfetched-or-unpushed messages unseen and re-eligible for the next
		// poll.
		select {
		case a.emailsCh <- email:
			a.logger.Info("received email", "from", email.From, "subject", email.Subject)
		case <-a.stopCh:
			a.logger.Warn("adapter stopped, abandoning pending messages")
			return nil
		case <-ctx.Done():
			a.logger.Warn("context done, abandoning pending messages", "error", ctx.Err())
			return ctx.Err()
		}
	}
	return nil
}

// PollOnce runs a single poll synchronously and returns once the poll's push
// loop has completed (or errored). It is used by one-shot callers (processCmd)
// so that "the channel is empty" genuinely means "no more emails this run",
// rather than the momentary-empty race produced by a sleep-then-drain pattern.
// PollOnce connects on demand like poll does.
func (a *IMAPAdapter) PollOnce(ctx context.Context) error {
	return a.poll(ctx)
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

		// Detect auto-generated mail (vacation responders, bounces, our own
		// outbound) from the message headers for loop protection.
		if isAutoGenerated(mr.Header.Get) {
			email.AutoReply = true
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

// resetConnection drops the current IMAP connection so the next poll
// reconnects. Called when a command fails (dead/stale connection).
func (a *IMAPAdapter) resetConnection() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client != nil {
		a.client.Close()
		a.client = nil
	}
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
	// Mark as auto-generated so other auto-responders don't reply to us,
	// and so we never process our own messages (loop protection).
	msg.WriteString("Auto-Submitted: auto-replied\r\n")
	msg.WriteString("X-Auto-Response-Suppress: All\r\n")
	msg.WriteString(fmt.Sprintf("%s: %s\r\n", LoopHeader, loopHeaderValue))
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
	a.mu.Lock()
	client := a.client
	a.mu.Unlock()

	if client == nil {
		return fmt.Errorf("not connected")
	}

	folder := a.config.Folder
	if folder == "" {
		folder = "INBOX"
	}

	// Select folder
	if _, err := client.Select(folder, nil).Wait(); err != nil {
		return fmt.Errorf("failed to select folder: %w", err)
	}

	// Search for the message by Message-ID header
	criteria := &imap.SearchCriteria{
		Header: []imap.SearchCriteriaHeaderField{
			{Key: "Message-ID", Value: emailID},
		},
	}
	searchData, err := client.Search(criteria, nil).Wait()
	if err != nil {
		return fmt.Errorf("failed to search for message: %w", err)
	}

	seqNums := searchData.AllSeqNums()
	if len(seqNums) == 0 {
		a.logger.Debug("message not found for marking processed", "id", emailID)
		return nil
	}

	// Add \Seen flag
	seqSet := imap.SeqSetNum(seqNums...)
	storeFlags := &imap.StoreFlags{
		Op:    imap.StoreFlagsAdd,
		Flags: []imap.Flag{imap.FlagSeen},
	}
	if err := client.Store(seqSet, storeFlags, nil).Close(); err != nil {
		return fmt.Errorf("failed to mark message as seen: %w", err)
	}

	a.logger.Debug("marked email as processed", "id", emailID)
	return nil
}
