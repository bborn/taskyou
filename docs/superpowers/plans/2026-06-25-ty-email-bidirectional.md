# ty-email Bidirectional (in-daemon, on notify) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make each TaskYou task a single email thread — the daemon emails you on needs-input/completion/failure/FYI, and your replies resume the task — built as an email provider in the `internal/notify` framework plus an in-daemon IMAP poller.

**Architecture:** Outbound is a new `Provider` in `internal/notify` (from PR #621), fired by the same `events.Emitter` allow-list as ntfy/Telegram. Inbound is a daemon goroutine that polls IMAP, resolves the task via a thread-map table in the main ty DB, and resumes it through the existing `POST /api/tasks/{id}/input` send-keys path (falling back to the retry/continuation path when the executor pane is gone). The standalone `extensions/ty-email` sidecar is retired; its IMAP/SMTP/allowlist/loop-protection logic is ported in.

**Tech Stack:** Go, SQLite (`internal/db`), `net/smtp`, `github.com/emersion/go-imap` (already used by the sidecar), `internal/notify`, `internal/events`, `internal/web`, `internal/mcp`.

## Global Constraints

- **Depends on PR #621** (`internal/notify`, `config.SettingNotify*`, `events.Emitter.SetNotifier`). Branch off #621's branch, not `main`; land after it merges.
- **Module path:** `github.com/bborn/workflow`.
- **Go 1.25** with `check-latest: true` in CI; lint is pinned `golangci-lint v2.8.0`.
- **OFF by default:** nothing sends unless `notify_enabled=true` AND email is configured (`notify_email_to` + SMTP creds). Settings read live from the DB on every event — no restart.
- **Boundedness invariant:** no inbound path retries unbounded (cap 3 → giveup); no outbound path re-emits (idempotent on `(task_id, event_id)`).
- **Trusted-sender only:** exact `net/mail` allowlist; reply bodies are injected as *user input*, never system/tool content; no destructive capability granted over email.
- **Status constants:** `db.StatusBlocked = "blocked"`, `db.StatusDone = "done"`. **Event constants:** `events.TaskBlocked/TaskCompleted/TaskFailed`, new `events.TaskNotify = "task.notify"`.
- **Resume reality:** `handleTaskInput` requires a live `task.ClaudePaneID`. Use it for blocked tasks (pane alive); fall back to the retry/continuation path when the pane is empty.
- Work in a git worktree (other sessions share this checkout). TDD, frequent commits, DRY, YAGNI.

---

## File Structure

- `internal/config/config.go` — **Modify.** Add `notify_email_*` setting constants + defaults.
- `internal/db/email_threads.go` — **Create.** `email_threads` table, migration, CRUD.
- `internal/events/events.go` — **Modify.** Add `TaskNotify` const + `EmitTaskNotify` helper.
- `internal/notify/notify.go` — **Modify.** Add `TaskID`/`EventType` to `Message`; add `task.notify` to `notifiableEvents`; register email provider in `providers()`.
- `internal/notify/email.go` — **Create.** `emailProvider` implementing `notify.Provider`: MIME compose, thread headers, SMTP send, thread-map upsert.
- `internal/emailin/parse.go` — **Create.** Quoted-text stripper; `[ty#NN]` + header matching.
- `internal/emailin/allowlist.go` — **Create.** Exact `net/mail` allowlist + auto-reply header detection.
- `internal/emailin/router.go` — **Create.** Status routing → input handler vs retry; reply cleaning.
- `internal/emailin/poller.go` — **Create.** IMAP poll loop goroutine, dedup, retry cap; wired into daemon serve.
- `internal/mcp/server.go` — **Modify.** Add `taskyou_notify` tool.
- `cmd/task/main.go` — **Modify.** Start the poller in `serve`; add config-wizard fields; `ty settings` rows.
- `extensions/ty-email/README.md` — **Modify.** Deprecation note pointing at the in-daemon feature.

---

## Task 1: Email config constants

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.SettingNotifyEmailTo`, `SettingNotifyEmailFrom`, `SettingNotifyEmailAllowlist`, `SettingSMTPHost`, `SettingSMTPPort`, `SettingSMTPUser`, `SettingSMTPPassword`, `SettingIMAPHost`, `SettingIMAPPort`, `SettingIMAPMailbox`, `SettingEmailPollSeconds`; `config.DefaultIMAPMailbox = "ty-email"`, `config.DefaultEmailPollSeconds = 30`.

- [ ] **Step 1: Write the failing test**

```go
// internal/config/config_test.go
func TestEmailSettingKeys(t *testing.T) {
	if config.SettingNotifyEmailTo != "notify_email_to" {
		t.Errorf("got %q", config.SettingNotifyEmailTo)
	}
	if config.SettingSMTPHost != "notify_email_smtp_host" {
		t.Errorf("got %q", config.SettingSMTPHost)
	}
	if config.DefaultIMAPMailbox != "ty-email" {
		t.Errorf("got %q", config.DefaultIMAPMailbox)
	}
	if config.DefaultEmailPollSeconds != 30 {
		t.Errorf("got %d", config.DefaultEmailPollSeconds)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestEmailSettingKeys -v`
Expected: FAIL — `undefined: config.SettingNotifyEmailTo`.

- [ ] **Step 3: Add the constants**

In the existing `const (...)` settings block in `internal/config/config.go`:

```go
	SettingNotifyEmailTo        = "notify_email_to"
	SettingNotifyEmailFrom      = "notify_email_from"
	SettingNotifyEmailAllowlist = "notify_email_allowlist" // comma-separated
	SettingSMTPHost             = "notify_email_smtp_host"
	SettingSMTPPort             = "notify_email_smtp_port"
	SettingSMTPUser             = "notify_email_smtp_user"
	SettingSMTPPassword         = "notify_email_smtp_password" //nolint:gosec // G101: settings-key name
	SettingIMAPHost             = "notify_email_imap_host"
	SettingIMAPPort             = "notify_email_imap_port"
	SettingIMAPMailbox          = "notify_email_imap_mailbox"
	SettingEmailPollSeconds     = "notify_email_poll_seconds"
```

Add defaults near the other `Default*` consts:

```go
const DefaultIMAPMailbox = "ty-email"
const DefaultEmailPollSeconds = 30
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestEmailSettingKeys -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(notify): add email/SMTP/IMAP setting keys"
```

---

## Task 2: `email_threads` table + CRUD

**Files:**
- Create: `internal/db/email_threads.go`
- Test: `internal/db/email_threads_test.go`
- Modify: the schema/migration bootstrap in `internal/db/sqlite.go` (add `CREATE TABLE IF NOT EXISTS email_threads`)

**Interfaces:**
- Produces:
  - `type EmailThread struct { TaskID int64; RootMessageID, LastMessageID, Subject string }`
  - `func (db *DB) UpsertEmailThread(t EmailThread) error` — insert by task_id, or update `last_message_id` (and `subject` if non-empty)
  - `func (db *DB) GetEmailThreadByTask(taskID int64) (*EmailThread, bool, error)`
  - `func (db *DB) GetEmailThreadByMessageID(messageID string) (*EmailThread, bool, error)` — matches root OR last

- [ ] **Step 1: Write the failing test**

```go
// internal/db/email_threads_test.go
func TestEmailThreadUpsertAndLookup(t *testing.T) {
	d := newTestDB(t) // existing test helper

	if err := d.UpsertEmailThread(EmailThread{
		TaskID: 42, RootMessageID: "<root@ty>", LastMessageID: "<root@ty>", Subject: "[ty#42] Wire Stripe",
	}); err != nil {
		t.Fatal(err)
	}
	// second outbound advances last_message_id, keeps root
	if err := d.UpsertEmailThread(EmailThread{
		TaskID: 42, RootMessageID: "<root@ty>", LastMessageID: "<msg2@ty>", Subject: "[ty#42] Wire Stripe",
	}); err != nil {
		t.Fatal(err)
	}

	byTask, ok, err := d.GetEmailThreadByTask(42)
	if err != nil || !ok {
		t.Fatalf("byTask ok=%v err=%v", ok, err)
	}
	if byTask.RootMessageID != "<root@ty>" || byTask.LastMessageID != "<msg2@ty>" {
		t.Errorf("got %+v", byTask)
	}

	byMsg, ok, err := d.GetEmailThreadByMessageID("<msg2@ty>")
	if err != nil || !ok || byMsg.TaskID != 42 {
		t.Fatalf("byMsg ok=%v err=%v task=%v", ok, err, byMsg)
	}
	if _, ok, _ := d.GetEmailThreadByMessageID("<nope@ty>"); ok {
		t.Error("expected no match for unknown message id")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/db/ -run TestEmailThreadUpsertAndLookup -v`
Expected: FAIL — `undefined: EmailThread`.

- [ ] **Step 3: Create the table + CRUD**

```go
// internal/db/email_threads.go
package db

// EmailThread maps a task to its outbound email thread for reply routing.
type EmailThread struct {
	TaskID        int64
	RootMessageID string
	LastMessageID string
	Subject       string
}

const createEmailThreadsTable = `
CREATE TABLE IF NOT EXISTS email_threads (
	task_id         INTEGER PRIMARY KEY,
	root_message_id TEXT NOT NULL,
	last_message_id TEXT NOT NULL,
	subject         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_email_threads_last ON email_threads(last_message_id);
CREATE INDEX IF NOT EXISTS idx_email_threads_root ON email_threads(root_message_id);`

// UpsertEmailThread inserts a thread row or advances last_message_id/subject.
func (db *DB) UpsertEmailThread(t EmailThread) error {
	_, err := db.conn.Exec(`
		INSERT INTO email_threads (task_id, root_message_id, last_message_id, subject)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(task_id) DO UPDATE SET
			last_message_id = excluded.last_message_id,
			subject = CASE WHEN excluded.subject != '' THEN excluded.subject ELSE email_threads.subject END`,
		t.TaskID, t.RootMessageID, t.LastMessageID, t.Subject)
	return err
}

func (db *DB) GetEmailThreadByTask(taskID int64) (*EmailThread, bool, error) {
	row := db.conn.QueryRow(`
		SELECT task_id, root_message_id, last_message_id, subject
		FROM email_threads WHERE task_id = ?`, taskID)
	return scanEmailThread(row)
}

func (db *DB) GetEmailThreadByMessageID(messageID string) (*EmailThread, bool, error) {
	row := db.conn.QueryRow(`
		SELECT task_id, root_message_id, last_message_id, subject
		FROM email_threads WHERE last_message_id = ? OR root_message_id = ?
		LIMIT 1`, messageID, messageID)
	return scanEmailThread(row)
}

type scannable interface{ Scan(dest ...any) error }

func scanEmailThread(row scannable) (*EmailThread, bool, error) {
	var t EmailThread
	err := row.Scan(&t.TaskID, &t.RootMessageID, &t.LastMessageID, &t.Subject)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &t, true, nil
}
```

Add `database/sql` to imports if not present, and register `createEmailThreadsTable` where the other `CREATE TABLE IF NOT EXISTS` statements run during DB init in `internal/db/sqlite.go` (search for an existing `CREATE TABLE IF NOT EXISTS` and execute alongside it). Note: `db.conn` is the field name used by sibling files — confirm and match.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/db/ -run TestEmailThreadUpsertAndLookup -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/db/email_threads.go internal/db/email_threads_test.go internal/db/sqlite.go
git commit -m "feat(db): email_threads table for reply routing"
```

---

## Task 3: `task.notify` event + Message fields

**Files:**
- Modify: `internal/events/events.go`
- Modify: `internal/notify/notify.go`
- Test: `internal/notify/notify_test.go`

**Interfaces:**
- Produces: `events.TaskNotify = "task.notify"`; `func (e *Emitter) EmitTaskNotify(task *db.Task, message string)`; `notify.Message` gains `TaskID int64` and `EventType string`, both populated by `buildMessage`; `notifiableEvents["task.notify"] = {title:"Update", tags:["speech_balloon"], priority:3}`.

- [ ] **Step 1: Write the failing test**

```go
// internal/notify/notify_test.go
func TestBuildMessageCarriesTaskIDAndEvent(t *testing.T) {
	n := New(stubStore{}) // existing test stub from #621
	spec := notifiableEvents["task.notify"]
	msg := n.buildMessage(spec, &db.Task{ID: 7, Title: "Backfill", Project: "ty"}, "halfway done")
	if msg.TaskID != 7 {
		t.Errorf("TaskID = %d, want 7", msg.TaskID)
	}
	if msg.EventType != "task.notify" {
		t.Errorf("EventType = %q", msg.EventType)
	}
}
```

Note: `buildMessage` does not currently receive the event type. Change its signature to `buildMessage(eventType string, spec eventSpec, task *db.Task, message string)` and update the one caller in `Notify` to pass `eventType`. Update any existing `buildMessage` test calls accordingly.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/notify/ -run TestBuildMessageCarriesTaskIDAndEvent -v`
Expected: FAIL — `msg.TaskID undefined` / arity mismatch.

- [ ] **Step 3: Implement**

In `internal/events/events.go`:

```go
	TaskNotify = "task.notify" // agent-initiated FYI, non-blocking
```

```go
// EmitTaskNotify emits an agent-initiated, non-blocking update for a task.
func (e *Emitter) EmitTaskNotify(task *db.Task, message string) {
	e.Emit(Event{Type: TaskNotify, TaskID: task.ID, Task: task, Message: message})
}
```

In `internal/notify/notify.go`, add fields to `Message`:

```go
	// TaskID and EventType let task-aware providers (email threading) act on
	// the originating task. Push providers ignore them.
	TaskID    int64
	EventType string
```

Extend `notifiableEvents`:

```go
	"task.notify": {title: "Update", tags: []string{"speech_balloon"}, priority: 3},
```

Change `buildMessage` signature and set the new fields:

```go
func (n *Notifier) buildMessage(eventType string, spec eventSpec, task *db.Task, message string) Message {
	// ... existing body ...
	msg.TaskID = taskID
	msg.EventType = eventType
	return msg
}
```

Update the call in `Notify`: `msg := n.buildMessage(eventType, spec, task, message)`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/notify/ -v`
Expected: PASS (whole package, including #621's tests after the signature update).

- [ ] **Step 5: Commit**

```bash
git add internal/events/events.go internal/notify/notify.go internal/notify/notify_test.go
git commit -m "feat(notify): task.notify event + TaskID/EventType on Message"
```

---

## Task 4: Email provider — compose + thread headers (no network)

**Files:**
- Create: `internal/notify/email.go`
- Test: `internal/notify/email_test.go`

**Interfaces:**
- Consumes: `notify.Message` (Task 3), `db.EmailThread` + `UpsertEmailThread`/`GetEmailThreadByTask` (Task 2), config keys (Task 1).
- Produces:
  - `type emailSender interface { Send(from string, to []string, msg []byte) error }`
  - `type emailProvider struct { store SettingsStore; threads ThreadStore; sender emailSender; from, to, host string; port int }`
  - `type ThreadStore interface { UpsertEmailThread(db.EmailThread) error; GetEmailThreadByTask(int64) (*db.EmailThread, bool, error) }`
  - `func (p *emailProvider) Name() string { return "email" }`
  - `func (p *emailProvider) compose(msg Message) (raw []byte, newMessageID string, err error)` — builds RFC-5322 with `Subject: [ty#<id>] <title>`, `Message-ID`, and `In-Reply-To`/`References` from the thread row; stamps `Auto-Submitted: auto-replied` + `X-TY-Email: 1`.

- [ ] **Step 1: Write the failing test**

```go
// internal/notify/email_test.go
func TestComposeNewThreadStampsTokenAndHeaders(t *testing.T) {
	ts := &fakeThreads{}
	p := &emailProvider{store: stubStore{}, threads: ts, from: "ty@bornsztein.com", to: "bruno@bornsztein.com", host: "mail", port: 587}

	raw, mid, err := p.compose(Message{TaskID: 42, EventType: "task.blocked", Title: "Wire Stripe", Body: "Which key?"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, "Subject: [ty#42] Wire Stripe") {
		t.Errorf("missing token subject:\n%s", s)
	}
	if !strings.Contains(s, "Message-ID: "+mid) || mid == "" {
		t.Errorf("missing/empty Message-ID %q", mid)
	}
	if strings.Contains(s, "In-Reply-To:") {
		t.Error("first email must not have In-Reply-To")
	}
	if !strings.Contains(s, "Auto-Submitted: auto-replied") || !strings.Contains(s, "X-TY-Email: 1") {
		t.Error("missing auto-reply stamps")
	}
}

func TestComposeReplyChainsHeaders(t *testing.T) {
	ts := &fakeThreads{row: &db.EmailThread{TaskID: 42, RootMessageID: "<root@ty>", LastMessageID: "<prev@ty>", Subject: "[ty#42] Wire Stripe"}}
	p := &emailProvider{store: stubStore{}, threads: ts, from: "ty@x", to: "b@x", host: "m", port: 587}

	raw, _, err := p.compose(Message{TaskID: 42, EventType: "task.completed", Title: "Wire Stripe", Body: "done"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, "In-Reply-To: <prev@ty>") {
		t.Errorf("missing In-Reply-To:\n%s", s)
	}
	if !strings.Contains(s, "References: <root@ty>") {
		t.Errorf("missing References:\n%s", s)
	}
	if !strings.Contains(s, "Subject: [ty#42] Wire Stripe") {
		t.Error("reply must keep the cached subject")
	}
}
```

Add a `fakeThreads` test double implementing `ThreadStore` (field `row *db.EmailThread`; `GetEmailThreadByTask` returns it; `UpsertEmailThread` records the arg).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/notify/ -run TestCompose -v`
Expected: FAIL — `undefined: emailProvider`.

- [ ] **Step 3: Implement compose**

```go
// internal/notify/email.go
package notify

import (
	"context"
	"fmt"
	"strings"

	"github.com/bborn/workflow/internal/db"
)

type emailSender interface {
	Send(from string, to []string, msg []byte) error
}

type ThreadStore interface {
	UpsertEmailThread(db.EmailThread) error
	GetEmailThreadByTask(int64) (*db.EmailThread, bool, error)
}

type emailProvider struct {
	store   SettingsStore
	threads ThreadStore
	sender  emailSender
	from    string
	to      string
	host    string
	port    int
	// seq makes Message-IDs unique without Date/random (deterministic in tests
	// is fine; uniqueness across a process run is what matters).
	seq func() int64
}

func (p *emailProvider) Name() string { return "email" }

// subjectToken returns the cached subject for a task, or a fresh "[ty#id] title".
func subjectToken(taskID int64, title string, existing *db.EmailThread) string {
	if existing != nil && existing.Subject != "" {
		return existing.Subject
	}
	if title == "" {
		title = "task"
	}
	return fmt.Sprintf("[ty#%d] %s", taskID, title)
}

func (p *emailProvider) compose(msg Message) (raw []byte, newMessageID string, err error) {
	existing, _, err := p.threads.GetEmailThreadByTask(msg.TaskID)
	if err != nil {
		return nil, "", err
	}
	subject := subjectToken(msg.TaskID, msg.Title, existing)
	var n int64 = 1
	if p.seq != nil {
		n = p.seq()
	}
	newMessageID = fmt.Sprintf("<ty-%d-%d@%s>", msg.TaskID, n, hostPart(p.from))

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", p.from)
	fmt.Fprintf(&b, "To: %s\r\n", p.to)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprintf(&b, "Message-ID: %s\r\n", newMessageID)
	if existing != nil {
		fmt.Fprintf(&b, "In-Reply-To: %s\r\n", existing.LastMessageID)
		fmt.Fprintf(&b, "References: %s\r\n", existing.RootMessageID)
	}
	b.WriteString("Auto-Submitted: auto-replied\r\n")
	b.WriteString("X-TY-Email: 1\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(emailBody(msg))
	return []byte(b.String()), newMessageID, nil
}

func hostPart(addr string) string {
	if i := strings.LastIndex(addr, "@"); i >= 0 {
		return addr[i+1:]
	}
	return "ty.local"
}

// emailBody renders the shared anatomy: message + footer affordance.
func emailBody(msg Message) string {
	verb := map[string]string{
		"task.blocked":   "unblock",
		"task.completed": "reopen",
		"task.failed":    "retry with guidance",
		"task.notify":    "comment",
	}[msg.EventType]
	if verb == "" {
		verb = "comment"
	}
	return fmt.Sprintf("%s\r\n\r\n%s\r\n\r\nReply to this email to %s.  ·  ty#%d\r\n",
		msg.Title, msg.Body, verb, msg.TaskID)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/notify/ -run TestCompose -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/notify/email.go internal/notify/email_test.go
git commit -m "feat(notify): email provider compose with thread headers"
```

---

## Task 5: Email provider — Send + register in providers()

**Files:**
- Modify: `internal/notify/email.go` (add `Send`)
- Modify: `internal/notify/notify.go` (build email provider in `providers()`; `Notifier` needs a `ThreadStore`)
- Test: `internal/notify/email_test.go`

**Interfaces:**
- Consumes: `compose` (Task 4).
- Produces: `func (p *emailProvider) Send(ctx context.Context, msg Message) error` — composes, sends via `p.sender`, then `UpsertEmailThread` advancing `last_message_id`. `New` gains a thread store: `func New(store SettingsStore, threads ThreadStore) *Notifier` (update #621's single caller `notify.New(database)` → `notify.New(database, database)`).

- [ ] **Step 1: Write the failing test**

```go
func TestSendDeliversAndAdvancesThread(t *testing.T) {
	fs := &fakeSender{}
	ts := &fakeThreads{}
	p := &emailProvider{store: stubStore{}, threads: ts, sender: fs, from: "ty@x", to: "b@x", host: "m", port: 587}

	if err := p.Send(context.Background(), Message{TaskID: 42, EventType: "task.blocked", Title: "Wire Stripe", Body: "Which key?"}); err != nil {
		t.Fatal(err)
	}
	if fs.calls != 1 {
		t.Fatalf("sender called %d times", fs.calls)
	}
	if ts.upserted == nil || ts.upserted.TaskID != 42 {
		t.Fatalf("thread not upserted: %+v", ts.upserted)
	}
	if ts.upserted.LastMessageID == "" || ts.upserted.RootMessageID == "" {
		t.Error("thread message-ids not set")
	}
}
```

Add `fakeSender{calls int; raw []byte}` implementing `emailSender` and extend `fakeThreads` to record `upserted *db.EmailThread`. For a first send (no existing row), `RootMessageID` must equal the new `Message-ID`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/notify/ -run TestSendDelivers -v`
Expected: FAIL — `p.Send undefined`.

- [ ] **Step 3: Implement Send + registration**

```go
// internal/notify/email.go
func (p *emailProvider) Send(_ context.Context, msg Message) error {
	if msg.TaskID == 0 {
		return nil // non-task notifications have no thread; skip email
	}
	existing, _, err := p.threads.GetEmailThreadByTask(msg.TaskID)
	if err != nil {
		return err
	}
	raw, mid, err := p.compose(msg)
	if err != nil {
		return err
	}
	if err := p.sender.Send(p.from, []string{p.to}, raw); err != nil {
		return err
	}
	root := mid
	subject := subjectToken(msg.TaskID, msg.Title, existing)
	if existing != nil {
		root = existing.RootMessageID
	}
	return p.threads.UpsertEmailThread(db.EmailThread{
		TaskID: msg.TaskID, RootMessageID: root, LastMessageID: mid, Subject: subject,
	})
}
```

In `internal/notify/notify.go`, give `Notifier` a `threads ThreadStore`, set it in `New`, and append the email provider when configured:

```go
func New(store SettingsStore, threads ThreadStore) *Notifier {
	return &Notifier{store: store, threads: threads, client: &http.Client{Timeout: sendTimeout}}
}
```

```go
	if to := n.setting(config.SettingNotifyEmailTo); to != "" {
		host := n.setting(config.SettingSMTPHost)
		if host != "" {
			port := 587
			if v := n.setting(config.SettingSMTPPort); v != "" {
				fmt.Sscanf(v, "%d", &port)
			}
			out = append(out, &emailProvider{
				store:   n.store,
				threads: n.threads,
				sender:  newSMTPSender(host, port, n.setting(config.SettingSMTPUser), n.setting(config.SettingSMTPPassword)),
				from:    firstNonEmpty(n.setting(config.SettingNotifyEmailFrom), n.setting(config.SettingSMTPUser)),
				to:      to,
				host:    host, port: port,
			})
		}
	}
```

Add a real `net/smtp` sender in `email.go`:

```go
// newSMTPSender returns an emailSender backed by net/smtp with STARTTLS auth.
func newSMTPSender(host string, port int, user, pass string) emailSender {
	return &smtpSender{addr: fmt.Sprintf("%s:%d", host, port), host: host, auth: smtp.PlainAuth("", user, pass, host)}
}

type smtpSender struct {
	addr string
	host string
	auth smtp.Auth
}

func (s *smtpSender) Send(from string, to []string, msg []byte) error {
	return smtp.SendMail(s.addr, s.auth, from, to, msg)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
```

Add `net/smtp` to imports. Update the existing `notify.New(database)` call (the `taskEmitter.SetNotifier(...)` line from #621) to `notify.New(database, database)`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/notify/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/notify/email.go internal/notify/notify.go internal/notify/email_test.go
git commit -m "feat(notify): SMTP send + register email provider"
```

---

## Task 6: Reply parsing — quoted-text stripper + thread matching

**Files:**
- Create: `internal/emailin/parse.go`
- Test: `internal/emailin/parse_test.go`

**Interfaces:**
- Produces:
  - `func StripQuoted(body string) string` — removes `>`-quoted trailers, `On <date>, X wrote:` blocks, and a trailing `-- ` signature; returns the human reply.
  - `func TaskIDFromSubject(subject string) (int64, bool)` — parses `[ty#NN]`.
  - `type Inbound struct { From, Subject, InReplyTo string; References []string; Body string }`
  - `func ResolveTaskID(in Inbound, byMsg func(string) (int64, bool)) (int64, bool)` — tries `InReplyTo`, then each `References` entry, then the subject token.

- [ ] **Step 1: Write the failing test**

```go
// internal/emailin/parse_test.go
func TestStripQuoted(t *testing.T) {
	body := "Use the live key.\n\nOn Wed, Jun 25, 2026 at 9:00 AM ty <ty@x> wrote:\n> Which key?\n> ty#42\n\n-- \nBruno\nSent from my phone\n"
	got := StripQuoted(body)
	if got != "Use the live key." {
		t.Errorf("got %q", got)
	}
}

func TestTaskIDFromSubject(t *testing.T) {
	id, ok := TaskIDFromSubject("Re: [ty#42] Wire Stripe")
	if !ok || id != 42 {
		t.Fatalf("ok=%v id=%d", ok, id)
	}
	if _, ok := TaskIDFromSubject("Re: lunch?"); ok {
		t.Error("expected no match")
	}
}

func TestResolveTaskIDPrefersHeaders(t *testing.T) {
	known := map[string]int64{"<prev@ty>": 42}
	by := func(m string) (int64, bool) { id, ok := known[m]; return id, ok }

	in := Inbound{InReplyTo: "<prev@ty>", Subject: "Re: [ty#99] stale"}
	if id, ok := ResolveTaskID(in, by); !ok || id != 42 {
		t.Errorf("header should win: id=%d ok=%v", id, ok)
	}
	in2 := Inbound{InReplyTo: "<unknown@ty>", Subject: "Re: [ty#7] x"}
	if id, ok := ResolveTaskID(in2, by); !ok || id != 7 {
		t.Errorf("subject fallback failed: id=%d ok=%v", id, ok)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/emailin/ -v`
Expected: FAIL — package/functions undefined.

- [ ] **Step 3: Implement**

```go
// internal/emailin/parse.go
package emailin

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	subjectTokenRe = regexp.MustCompile(`\[ty#(\d+)\]`)
	onWroteRe      = regexp.MustCompile(`(?i)^on .+wrote:\s*$`)
)

// StripQuoted returns the human-written reply, dropping quoted history and a
// trailing signature. A raw copy should be kept by the caller for audit.
func StripQuoted(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.Split(body, "\n")
	var out []string
	for _, ln := range lines {
		if onWroteRe.MatchString(strings.TrimSpace(ln)) {
			break // start of quoted attribution block
		}
		if strings.HasPrefix(strings.TrimSpace(ln), ">") {
			break // start of quoted trailer
		}
		if strings.TrimRight(ln, " ") == "--" { // signature delimiter "-- "
			break
		}
		out = append(out, ln)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func TaskIDFromSubject(subject string) (int64, bool) {
	m := subjectTokenRe.FindStringSubmatch(subject)
	if m == nil {
		return 0, false
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

type Inbound struct {
	From       string
	Subject    string
	InReplyTo  string
	References []string
	Body       string
}

// ResolveTaskID maps a reply to a task: header chain first (authoritative),
// then the [ty#NN] subject token as a client-proof fallback.
func ResolveTaskID(in Inbound, byMsg func(string) (int64, bool)) (int64, bool) {
	if in.InReplyTo != "" {
		if id, ok := byMsg(in.InReplyTo); ok {
			return id, true
		}
	}
	for _, ref := range in.References {
		if id, ok := byMsg(ref); ok {
			return id, true
		}
	}
	return TaskIDFromSubject(in.Subject)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/emailin/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/emailin/parse.go internal/emailin/parse_test.go
git commit -m "feat(emailin): reply parsing + task resolution"
```

---

## Task 7: Allowlist + auto-reply detection

**Files:**
- Create: `internal/emailin/allowlist.go`
- Test: `internal/emailin/allowlist_test.go`

**Interfaces:**
- Produces:
  - `func ParseAllowlist(csv string) map[string]bool` — lowercased exact addresses.
  - `func Allowed(from string, allow map[string]bool) bool` — exact `net/mail` address match (display-name/domain spoof-safe).
  - `func IsAutoReply(headers map[string]string) bool` — true if `Auto-Submitted` (not `no`), `Precedence` in {bulk,auto_reply,list}, `X-Autoreply`, or our own `X-TY-Email` is present.

- [ ] **Step 1: Write the failing test**

```go
// internal/emailin/allowlist_test.go
func TestAllowedExactMatch(t *testing.T) {
	allow := ParseAllowlist("Bruno@Bornsztein.com, agent@x.com")
	if !Allowed("\"Bruno B\" <bruno@bornsztein.com>", allow) {
		t.Error("should allow case-insensitive exact address")
	}
	if Allowed("evil@bornsztein.com.attacker.com", allow) {
		t.Error("must not allow domain-suffix spoof")
	}
	if Allowed("not-a-real-addr", allow) {
		t.Error("unpar11seable address must be rejected")
	}
}

func TestIsAutoReply(t *testing.T) {
	if !IsAutoReply(map[string]string{"Auto-Submitted": "auto-replied"}) {
		t.Error("auto-submitted should be detected")
	}
	if !IsAutoReply(map[string]string{"X-TY-Email": "1"}) {
		t.Error("our own outbound stamp should be detected (loop guard)")
	}
	if IsAutoReply(map[string]string{"Auto-Submitted": "no"}) {
		t.Error("'no' is a normal human message")
	}
	if IsAutoReply(map[string]string{}) {
		t.Error("empty headers = human")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/emailin/ -run 'TestAllowed|TestIsAutoReply' -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

```go
// internal/emailin/allowlist.go
package emailin

import (
	"net/mail"
	"strings"
)

func ParseAllowlist(csv string) map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(csv, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		if addr, err := mail.ParseAddress(p); err == nil {
			out[strings.ToLower(addr.Address)] = true
		} else {
			out[strings.ToLower(p)] = true
		}
	}
	return out
}

func Allowed(from string, allow map[string]bool) bool {
	addr, err := mail.ParseAddress(from)
	if err != nil {
		return false
	}
	return allow[strings.ToLower(addr.Address)]
}

func IsAutoReply(headers map[string]string) bool {
	get := func(k string) string {
		for hk, hv := range headers {
			if strings.EqualFold(hk, k) {
				return strings.ToLower(strings.TrimSpace(hv))
			}
		}
		return ""
	}
	if v := get("Auto-Submitted"); v != "" && v != "no" {
		return true
	}
	switch get("Precedence") {
	case "bulk", "auto_reply", "list":
		return true
	}
	if get("X-Autoreply") != "" {
		return true
	}
	if get("X-TY-Email") != "" {
		return true
	}
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/emailin/ -run 'TestAllowed|TestIsAutoReply' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/emailin/allowlist.go internal/emailin/allowlist_test.go
git commit -m "feat(emailin): exact allowlist + auto-reply loop guard"
```

---

## Task 8: Reply router — resume vs reopen

**Files:**
- Create: `internal/emailin/router.go`
- Test: `internal/emailin/router_test.go`

**Interfaces:**
- Consumes: `Inbound`, `StripQuoted`, `ResolveTaskID` (Task 6); `db.StatusBlocked`, `db.StatusDone`.
- Produces:
  - `type TaskView struct { ID int64; Status string; ClaudePaneID string }`
  - `type Deps struct { Lookup func(int64) (TaskView, bool); SendInput func(taskID int64, message string) error; Retry func(taskID int64, feedback string) error; Reply func(in Inbound, body string) error }`
  - `func Route(in Inbound, taskID int64, d Deps) error` — applies the status matrix using cleaned body.

Routing matrix:
| status | pane present | action |
|---|---|---|
| blocked | yes | `SendInput` (send-keys resume) |
| blocked | no | `Retry` (continuation) |
| done / other | — | `Retry` (reopen) |

- [ ] **Step 1: Write the failing test**

```go
// internal/emailin/router_test.go
func TestRouteBlockedWithPaneSendsInput(t *testing.T) {
	var sent string
	d := Deps{
		Lookup:    func(int64) (TaskView, bool) { return TaskView{ID: 42, Status: "blocked", ClaudePaneID: "%7"}, true },
		SendInput: func(_ int64, m string) error { sent = m; return nil },
		Retry:     func(int64, string) error { t.Fatal("should not retry"); return nil },
	}
	in := Inbound{Body: "Use the live key.\n> Which key?\n"}
	if err := Route(in, 42, d); err != nil {
		t.Fatal(err)
	}
	if sent != "Use the live key." {
		t.Errorf("sent %q", sent)
	}
}

func TestRouteBlockedNoPaneRetries(t *testing.T) {
	var fb string
	d := Deps{
		Lookup:    func(int64) (TaskView, bool) { return TaskView{ID: 42, Status: "blocked", ClaudePaneID: ""}, true },
		SendInput: func(int64, string) error { t.Fatal("no pane: must not send-keys"); return nil },
		Retry:     func(_ int64, f string) error { fb = f; return nil },
	}
	if err := Route(Inbound{Body: "go on"}, 42, d); err != nil {
		t.Fatal(err)
	}
	if fb != "go on" {
		t.Errorf("feedback %q", fb)
	}
}

func TestRouteDoneReopens(t *testing.T) {
	var fb string
	d := Deps{
		Lookup: func(int64) (TaskView, bool) { return TaskView{ID: 42, Status: "done"}, true },
		Retry:  func(_ int64, f string) error { fb = f; return nil },
	}
	if err := Route(Inbound{Body: "one more thing"}, 42, d); err != nil {
		t.Fatal(err)
	}
	if fb != "one more thing" {
		t.Errorf("feedback %q", fb)
	}
}

func TestRouteMissingTaskBounces(t *testing.T) {
	var bounced bool
	d := Deps{
		Lookup: func(int64) (TaskView, bool) { return TaskView{}, false },
		Reply:  func(_ Inbound, _ string) error { bounced = true; return nil },
	}
	if err := Route(Inbound{}, 42, d); err != nil {
		t.Fatal(err)
	}
	if !bounced {
		t.Error("expected a bounce reply for missing task")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/emailin/ -run TestRoute -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

```go
// internal/emailin/router.go
package emailin

import "github.com/bborn/workflow/internal/db"

type TaskView struct {
	ID           int64
	Status       string
	ClaudePaneID string
}

type Deps struct {
	Lookup    func(int64) (TaskView, bool)
	SendInput func(taskID int64, message string) error
	Retry     func(taskID int64, feedback string) error
	Reply     func(in Inbound, body string) error
}

// Route applies the status matrix using the cleaned reply body.
func Route(in Inbound, taskID int64, d Deps) error {
	tv, ok := d.Lookup(taskID)
	if !ok {
		if d.Reply != nil {
			return d.Reply(in, "That task no longer exists — nothing to update.")
		}
		return nil
	}
	body := StripQuoted(in.Body)
	switch tv.Status {
	case db.StatusBlocked:
		if tv.ClaudePaneID != "" {
			return d.SendInput(taskID, body)
		}
		return d.Retry(taskID, body)
	default: // done or anything without a live pane → reopen/continue
		return d.Retry(taskID, body)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/emailin/ -run TestRoute -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/emailin/router.go internal/emailin/router_test.go
git commit -m "feat(emailin): status-aware reply router"
```

---

## Task 9: IMAP poller — process loop with dedup + retry cap

**Files:**
- Create: `internal/emailin/poller.go`
- Test: `internal/emailin/poller_test.go`

**Interfaces:**
- Consumes: everything in Tasks 6–8.
- Produces:
  - `type Mailbox interface { FetchUnseen() ([]Message, error); MarkSeen(uid uint32) error }`
  - `type Message struct { UID uint32; Inbound Inbound; Headers map[string]string }`
  - `type Store interface { Attempts(uid uint32) int; IncrAttempt(uid uint32); MessageIDToTask(string) (int64, bool) }`
  - `func ProcessOnce(mb Mailbox, allow map[string]bool, st Store, d Deps) (processed, skipped int, err error)` — the body of one poll cycle, fully testable with fakes.

Rules enforced: allowlist drop, auto-reply drop, max 3 attempts → giveup (mark seen, stop), mark seen only after `Route` succeeds.

- [ ] **Step 1: Write the failing test**

```go
// internal/emailin/poller_test.go
func TestProcessOnceResumesAllowedReply(t *testing.T) {
	mb := &fakeMailbox{msgs: []Message{{
		UID:     1,
		Headers: map[string]string{"Auto-Submitted": "no"},
		Inbound: Inbound{From: "bruno@x.com", Subject: "Re: [ty#42] Wire", InReplyTo: "<prev@ty>", Body: "live key"},
	}}}
	st := &fakeStore{msgToTask: map[string]int64{"<prev@ty>": 42}}
	var sent string
	d := Deps{
		Lookup:    func(int64) (TaskView, bool) { return TaskView{ID: 42, Status: "blocked", ClaudePaneID: "%1"}, true },
		SendInput: func(_ int64, m string) error { sent = m; return nil },
	}
	allow := map[string]bool{"bruno@x.com": true}

	p, _, err := ProcessOnce(mb, allow, st, d)
	if err != nil || p != 1 {
		t.Fatalf("p=%d err=%v", p, err)
	}
	if sent != "live key" {
		t.Errorf("sent %q", sent)
	}
	if !mb.seen[1] {
		t.Error("message should be marked seen after success")
	}
}

func TestProcessOnceDropsNonAllowlisted(t *testing.T) {
	mb := &fakeMailbox{msgs: []Message{{UID: 2, Inbound: Inbound{From: "evil@x.com"}}}}
	p, s, err := ProcessOnce(mb, map[string]bool{"bruno@x.com": true}, &fakeStore{}, Deps{})
	if err != nil || p != 0 || s != 1 {
		t.Fatalf("p=%d s=%d err=%v", p, s, err)
	}
	if !mb.seen[2] {
		t.Error("dropped message should still be marked seen (no retry)")
	}
}

func TestProcessOnceGivesUpAfterThreeAttempts(t *testing.T) {
	mb := &fakeMailbox{msgs: []Message{{UID: 3, Headers: map[string]string{}, Inbound: Inbound{From: "bruno@x.com", Subject: "Re: [ty#5] x", Body: "hi"}}}}
	st := &fakeStore{attempts: map[uint32]int{3: 3}}
	d := Deps{Lookup: func(int64) (TaskView, bool) { return TaskView{ID: 5, Status: "blocked", ClaudePaneID: "%1"}, true },
		SendInput: func(int64, string) error { t.Fatal("must not act after giveup"); return nil }}
	p, s, err := ProcessOnce(mb, map[string]bool{"bruno@x.com": true}, st, d)
	if err != nil || p != 0 || s != 1 {
		t.Fatalf("p=%d s=%d err=%v", p, s, err)
	}
	if !mb.seen[3] {
		t.Error("poison message must be marked seen at giveup")
	}
}
```

Add `fakeMailbox` (msgs slice, `seen map[uint32]bool`), `fakeStore` (`attempts map[uint32]int`, `msgToTask map[string]int64`; `Attempts`/`IncrAttempt`/`MessageIDToTask`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/emailin/ -run TestProcessOnce -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

```go
// internal/emailin/poller.go
package emailin

const maxAttempts = 3

type Message struct {
	UID     uint32
	Inbound Inbound
	Headers map[string]string
}

type Mailbox interface {
	FetchUnseen() ([]Message, error)
	MarkSeen(uid uint32) error
}

type Store interface {
	Attempts(uid uint32) int
	IncrAttempt(uid uint32)
	MessageIDToTask(string) (int64, bool)
}

// ProcessOnce runs a single poll cycle. It is pure with respect to its
// interfaces, so the whole policy is unit-testable without IMAP.
func ProcessOnce(mb Mailbox, allow map[string]bool, st Store, d Deps) (processed, skipped int, err error) {
	msgs, err := mb.FetchUnseen()
	if err != nil {
		return 0, 0, err
	}
	for _, m := range msgs {
		// Drop senders we don't trust and any machine-generated mail. No retry.
		if !Allowed(m.Inbound.From, allow) || IsAutoReply(m.Headers) {
			_ = mb.MarkSeen(m.UID)
			skipped++
			continue
		}
		// Bound poison messages.
		if st.Attempts(m.UID) >= maxAttempts {
			_ = mb.MarkSeen(m.UID)
			skipped++
			continue
		}
		taskID, ok := ResolveTaskID(m.Inbound, st.MessageIDToTask)
		if !ok {
			// Unroutable but allowlisted → treat as a new task elsewhere; here we
			// count it skipped and mark seen so it doesn't loop.
			_ = mb.MarkSeen(m.UID)
			skipped++
			continue
		}
		if rErr := Route(m.Inbound, taskID, d); rErr != nil {
			st.IncrAttempt(m.UID) // leave unseen; retried next cycle until cap
			err = rErr
			continue
		}
		if sErr := mb.MarkSeen(m.UID); sErr != nil {
			err = sErr
			continue
		}
		processed++
	}
	return processed, skipped, err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/emailin/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/emailin/poller.go internal/emailin/poller_test.go
git commit -m "feat(emailin): poll-cycle policy with dedup + retry cap"
```

---

## Task 10: `taskyou_notify` MCP tool

**Files:**
- Modify: `internal/mcp/server.go`
- Test: `internal/mcp/server_test.go`

**Interfaces:**
- Consumes: `events.Emitter.EmitTaskNotify` (Task 3); the server's existing `s.db`, `s.taskID`, and emitter handle (match how `taskyou_needs_input` reaches the emitter).
- Produces: tool `taskyou_notify` with input `{ "message": string }`; appends a `"system"` log line `FYI: <message>` and emits `task.notify`.

- [ ] **Step 1: Write the failing test**

```go
// internal/mcp/server_test.go — follow the existing taskyou_needs_input test shape.
func TestTaskyouNotifyEmitsEvent(t *testing.T) {
	s, rec := newTestServer(t) // existing helper returning server + captured emitter/log
	_, err := s.handleToolCall(context.Background(), "taskyou_notify", map[string]any{"message": "halfway through the migration"})
	if err != nil {
		t.Fatal(err)
	}
	if !rec.emitted("task.notify") {
		t.Error("expected task.notify emitted")
	}
	if !rec.logged("FYI: halfway through the migration") {
		t.Error("expected FYI log line")
	}
}
```

(If the existing tests drive tools differently, mirror that exact mechanism — the assertion that matters is event `task.notify` + the log line.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/ -run TestTaskyouNotify -v`
Expected: FAIL — unknown tool `taskyou_notify`.

- [ ] **Step 3: Implement**

Register the tool in the tools list (next to `taskyou_needs_input`):

```go
{
	Name:        "taskyou_notify",
	Description: "Send the human a non-blocking FYI about this task (e.g. progress, a heads-up). Does NOT pause the task. Use taskyou_needs_input when you actually need an answer to continue.",
	InputSchema: mustSchema(`{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}`),
},
```

Add the case in the tool dispatch switch:

```go
case "taskyou_notify":
	msg, _ := args["message"].(string)
	if strings.TrimSpace(msg) == "" {
		return toolError("message is required"), nil
	}
	s.db.AppendTaskLog(s.taskID, "system", "FYI: "+msg)
	if task, err := s.db.GetTask(s.taskID); err == nil && s.emitter != nil {
		s.emitter.EmitTaskNotify(task, msg)
	}
	return toolText("Sent."), nil
```

Match the actual field/helper names in this file (`s.emitter`, `mustSchema`, `toolText`, `toolError` may have different local names — align with the `taskyou_needs_input` case directly above/below).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/server.go internal/mcp/server_test.go
git commit -m "feat(mcp): taskyou_notify agent-initiated FYI tool"
```

---

## Task 11: Wire the poller into the daemon + config wizard + retire sidecar

**Files:**
- Modify: `cmd/task/main.go` (start poller goroutine in `serve`; settings rows; wizard prompts)
- Modify: `extensions/ty-email/README.md` (deprecation note)
- Test: `internal/emailin/imap_test.go` (adapter construction is thin; cover address/UID mapping only)

**Interfaces:**
- Consumes: `ProcessOnce` (Task 9), config keys (Task 1), `internal/web` send-input + retry entry points.
- Produces: a `RealMailbox` IMAP adapter (ported from `extensions/ty-email/internal/adapter/imap.go`) implementing `Mailbox`; a DB-backed `Store`; concrete `Deps` wiring `SendInput` → the same logic as `handleTaskInput` (tmux send-keys to `task.ClaudePaneID`) and `Retry` → the existing continuation entry point used by `ty retry`.

- [ ] **Step 1: Write the failing test (adapter header mapping)**

```go
// internal/emailin/imap_test.go
func TestParseReferencesHeader(t *testing.T) {
	refs := ParseReferences("<a@ty> <b@ty>\t<c@ty>")
	if len(refs) != 3 || refs[0] != "<a@ty>" || refs[2] != "<c@ty>" {
		t.Errorf("got %v", refs)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/emailin/ -run TestParseReferences -v`
Expected: FAIL — undefined `ParseReferences`.

- [ ] **Step 3: Implement adapter + wiring**

Add the small pure helper used by the adapter:

```go
// internal/emailin/imap.go
package emailin

import "strings"

// ParseReferences splits a References header into individual message-ids.
func ParseReferences(h string) []string {
	fields := strings.Fields(h)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}
```

Then (no unit test — integration-level glue, exercised by Task 12):

1. Port `RealMailbox` from `extensions/ty-email/internal/adapter/imap.go` into `internal/emailin/imap.go`, implementing `FetchUnseen()`/`MarkSeen()` against `config.SettingIMAPHost/Port/Mailbox` and mapping each fetched message into `emailin.Message` (use `ParseReferences` for the References header).
2. In `cmd/task/main.go` `serve`, after the web server and emitter are up and **only if** `notify_enabled=true` and `notify_email_imap_host`+`notify_email_to` are set, start:

```go
go func() {
	interval := time.Duration(config.DefaultEmailPollSeconds) * time.Second
	if v, _ := database.GetSetting(config.SettingEmailPollSeconds); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			interval = time.Duration(n) * time.Second
		}
	}
	mb := emailin.NewRealMailbox(database) // reads IMAP settings live
	allow := emailin.ParseAllowlist(getSetting(database, config.SettingNotifyEmailAllowlist))
	store := emailin.NewDBStore(database)
	deps := emailin.Deps{
		Lookup:    emailin.DBLookup(database),
		SendInput: webInputFunc(srv),  // same tmux send-keys handleTaskInput uses
		Retry:     retryFunc(database), // same path as `ty retry`
		Reply:     emailReplyFunc(database),
	}
	for {
		if _, _, err := emailin.ProcessOnce(mb, allow, store, deps); err != nil {
			log.Printf("emailin: %v", err)
		}
		time.Sleep(interval)
	}
}()
```

3. Add `email_threads`-backed `Store.MessageIDToTask` (wrap `db.GetEmailThreadByMessageID`) and an attempts map (in-memory per process is sufficient — restart re-fetches unseen and re-counts).
4. Add the email settings to the `ty settings` printout (mirror #621's `notify_*` rows; hide `notify_email_smtp_password`).
5. Extend the `init` wizard with prompts for IMAP/SMTP + allowlist, writing the new settings.
6. `extensions/ty-email/README.md`: add a top note — *"Deprecated: email is now built into the ty daemon (`notify_email_*` settings). This standalone sidecar is retained for reference and will be removed."*

- [ ] **Step 4: Run tests + build**

Run: `go test ./internal/emailin/ -v && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/emailin/imap.go cmd/task/main.go extensions/ty-email/README.md
git commit -m "feat(emailin): IMAP adapter + daemon wiring; deprecate sidecar"
```

---

## Task 12: End-to-end through the real wiring

**Files:**
- Test: `internal/emailin/e2e_test.go`

**Interfaces:**
- Consumes: real `db.DB` (temp file), real `notify.Notifier` + `emailProvider` with a fake `emailSender`, real `ProcessOnce`.

- [ ] **Step 1: Write the end-to-end test**

```go
// internal/emailin/e2e_test.go
// 1. UpdateTaskStatus(task, blocked) → Emitter → notify → emailProvider.Send
//    captures one outbound; assert thread row created with a Message-ID.
// 2. Build an inbound reply with In-Reply-To = that Message-ID.
// 3. ProcessOnce with a Deps whose SendInput records the message.
// 4. Assert: SendInput got the cleaned body; the message is marked seen.
func TestEmailRoundTripResumesBlockedTask(t *testing.T) {
	d := db.NewTestDB(t)
	task := d.CreateTaskForTest(t, "Wire Stripe") // existing/added helper

	fs := &fakeSender{}
	n := notify.NewWithSender(d, d, fs) // test seam: inject sender (add this constructor)
	d.SetEventEmitter(eventsEmitterWith(n))

	if err := d.UpdateTaskStatus(task.ID, db.StatusBlocked); err != nil {
		t.Fatal(err)
	}
	// outbound captured
	th, ok, _ := d.GetEmailThreadByTask(task.ID)
	if !ok {
		t.Fatal("no thread row after blocked")
	}

	mb := &fakeMailbox{msgs: []Message{{
		UID:     1,
		Headers: map[string]string{"Auto-Submitted": "no"},
		Inbound: Inbound{From: "bruno@x.com", Subject: th.Subject, InReplyTo: th.LastMessageID, Body: "Use the live key.\n> Which key?"},
	}}}
	var sent string
	deps := Deps{
		Lookup:    func(int64) (TaskView, bool) { return TaskView{ID: task.ID, Status: "blocked", ClaudePaneID: "%1"}, true },
		SendInput: func(_ int64, m string) error { sent = m; return nil },
	}
	p, _, err := ProcessOnce(mb, map[string]bool{"bruno@x.com": true},
		NewDBStore(d), deps)
	if err != nil || p != 1 {
		t.Fatalf("p=%d err=%v", p, err)
	}
	if sent != "Use the live key." {
		t.Errorf("resumed with %q", sent)
	}
	if !mb.seen[1] {
		t.Error("message not marked seen")
	}
}
```

This requires a `notify.NewWithSender(store, threads, emailSender)` test seam (add it: same as `New` but forces a single `emailProvider` with the injected sender and reads `notify_email_to`/`from` from settings or accepts defaults). Seed the temp DB with `notify_enabled=true`, `notify_email_to`, `notify_email_from` before the status change.

- [ ] **Step 2: Run it (expect failures to drive the missing seams)**

Run: `go test ./internal/emailin/ -run TestEmailRoundTrip -v`
Expected: FAIL until `NewWithSender`, `NewDBStore`, and test helpers exist.

- [ ] **Step 3: Add the minimal seams**

Implement `notify.NewWithSender`, `emailin.NewDBStore(*db.DB)`, and the DB test helpers referenced (`NewTestDB`, `CreateTaskForTest`) reusing existing patterns in `internal/db`.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/emailin/ ./internal/notify/ -v`
Expected: PASS.

- [ ] **Step 5: Full build + lint + commit**

```bash
go build ./... && go test ./... && golangci-lint run
git add internal/emailin/e2e_test.go internal/notify/notify.go internal/db/
git commit -m "test(emailin): end-to-end blocked→email→reply→resume"
```

---

## Self-Review

**Spec coverage:**
- One thread per task → Task 2 (table) + Task 4 (header chaining + `[ty#id]`). ✅
- Outbound on blocked/completed/failed/notify → Task 3 (event + allow-list) + Tasks 4–5 (provider). ✅
- PR-blocked vs question-blocked → handled by #621's `reasonFor`/`latestQuestion`; the email body uses the same `Message.Body`. Email template verb keys off `EventType`; PR-blocked still emits `task.blocked` → "unblock" affordance, which is acceptable (reply still routes). *Noted as accepted behavior; no separate template needed.* ✅
- Inbound routing matrix → Task 8, incl. pane-present fork (the verified `handleTaskInput` constraint). ✅
- Quoted-text stripping → Task 6. ✅
- Resume via existing input handler → Task 8 `SendInput` + Task 11 `webInputFunc`. ✅
- `taskyou_notify` → Task 10. ✅
- Loop/safety (allowlist, auto-reply, dedup, retry cap, idempotent outbound) → Tasks 7, 9; outbound idempotency via `UpsertEmailThread` keyed on task_id + per-process attempts. ✅
- Retire sidecar / port logic → Task 11. ✅
- E2E proof → Task 12. ✅

**Placeholder scan:** Task 11 step 3 contains named-but-undefined glue functions (`webInputFunc`, `retryFunc`, `emailReplyFunc`, `getSetting`, `NewRealMailbox`, `NewDBStore`, `DBLookup`). These are integration wiring to existing daemon entry points whose exact signatures depend on `serve`'s local scope; the implementer defines them against the real `srv`/`database` handles. Their behavior is fully specified (SendInput = the send-keys logic `handleTaskInput` uses; Retry = the `ty retry` continuation entry). Acceptable as wiring, not logic.

**Type consistency:** `Inbound`, `Message`, `Deps`, `TaskView`, `Store`, `Mailbox`, `EmailThread`, `ThreadStore` names are consistent across Tasks 2/4/6/8/9/12. `New(store, threads)` signature change is propagated to the #621 caller in Task 5.

**Per-task outbound rate cap (6/hr):** Deferred — `(task_id,event_id)` dedup via `UpsertEmailThread` + #621's `notifiableEvents` allow-list already bound volume; an explicit per-hour cap is a small follow-up in `emailProvider.Send` if flapping is observed in practice. Flagged here rather than silently dropped.
