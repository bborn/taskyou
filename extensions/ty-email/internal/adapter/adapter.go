// Package adapter provides email input/output adapters.
package adapter

import (
	"context"
	"time"
)

// Email represents an inbound email message.
type Email struct {
	ID          string       // Unique identifier (Message-ID header)
	From        string       // Sender address
	To          []string     // Recipient addresses
	Subject     string       // Email subject
	Body        string       // Plain text body
	HTML        string       // HTML body (if available)
	Attachments []Attachment // File attachments
	InReplyTo   string       // Message-ID this is replying to
	References  []string     // Thread reference chain
	ReceivedAt  time.Time    // When the email was received
	Raw         []byte       // Raw email data for archival
}

// Attachment represents an email attachment.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// OutboundEmail represents an email to send.
type OutboundEmail struct {
	To        []string
	Subject   string
	Body      string
	HTML      string
	InReplyTo string   // Message-ID to reply to (for threading)
	TaskID    int64    // Associated task (for tracking)
}

// Adapter is the interface for email providers.
// Implementations handle the specifics of Gmail, IMAP, webhooks, etc.
type Adapter interface {
	// Name returns the adapter name (e.g., "gmail", "imap").
	Name() string

	// Start begins listening for emails (for push-based adapters).
	// For polling adapters, this starts the poll loop.
	Start(ctx context.Context) error

	// Stop gracefully shuts down the adapter.
	Stop() error

	// Emails returns a channel that receives inbound emails.
	Emails() <-chan *Email

	// Send sends an outbound email.
	Send(ctx context.Context, email *OutboundEmail) error

	// MarkProcessed marks an email as handled (e.g., move to folder, add label).
	MarkProcessed(ctx context.Context, emailID string) error
}

// Config holds adapter configuration.
type Config struct {
	Type string `yaml:"type"` // "gmail", "imap", "webhook", "twilio"

	Gmail   *GmailConfig   `yaml:"gmail,omitempty"`
	IMAP    *IMAPConfig    `yaml:"imap,omitempty"`
	Webhook *WebhookConfig `yaml:"webhook,omitempty"`
	Twilio  *TwilioConfig  `yaml:"twilio,omitempty"`
}

// GmailConfig holds Gmail OAuth configuration.
type GmailConfig struct {
	CredentialsFile string `yaml:"credentials_file"`
	TokenFile       string `yaml:"token_file"`
	PollInterval    string `yaml:"poll_interval"` // e.g., "30s"
	Label           string `yaml:"label"`         // Optional label to filter (e.g., "ty-email")
}

// IMAPConfig holds IMAP configuration.
type IMAPConfig struct {
	Server       string `yaml:"server"`        // e.g., "imap.fastmail.com:993"
	Username     string `yaml:"username"`
	PasswordCmd  string `yaml:"password_cmd"`  // Command to get password
	Folder       string `yaml:"folder"`        // e.g., "INBOX"
	PollInterval string `yaml:"poll_interval"` // e.g., "30s"
}

// WebhookConfig holds webhook configuration.
type WebhookConfig struct {
	Listen string `yaml:"listen"` // e.g., ":8080"
	Path   string `yaml:"path"`   // e.g., "/webhook/email"
	Secret string `yaml:"secret"` // For signature verification
}
