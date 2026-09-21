package adapter

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

const (
	testIMAPUser = "testuser"
	testIMAPPass = "testpass"
)

// startInMemoryIMAPServer spins up an in-tree imapmemserver reference server
// on a random localhost port with InsecureAuth enabled (plain TCP, no TLS).
// The reference server implements RFC 3501 §6.4.5 exactly — including the
// implicit \Seen-on-BODY[] behavior the loss bug depends on — so it is a
// faithful stand-in for a production IMAP server (Dovecot, Cyrus, Fastmail).
//
// It returns the server's listen address and a cleanup function that closes
// the server. Tests dial it with imapclient.DialInsecure.
func startInMemoryIMAPServer(t *testing.T) (string, func()) {
	t.Helper()

	memServer := imapmemserver.New()
	user := imapmemserver.NewUser(testIMAPUser, testIMAPPass)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatalf("create INBOX: %v", err)
	}
	memServer.AddUser(user)

	server := imapserver.New(&imapserver.Options{
		NewSession: func(conn *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return memServer.NewSession(), nil, nil
		},
		InsecureAuth: true,
		Caps: imap.CapSet{
			imap.CapIMAP4rev1: {},
			imap.CapIMAP4rev2: {},
		},
	})

	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(ln) }()

	cleanup := func() {
		_ = server.Close()
		if err := <-serveErr; err != nil && !strings.Contains(err.Error(), "closed") {
			t.Logf("server.Serve returned: %v", err)
		}
	}
	return ln.Addr().String(), cleanup
}

// dialTestClient connects + logs in to the in-memory server. The returned
// client must be closed by the caller (Logout + Close).
func dialTestClient(t *testing.T, addr string) *imapclient.Client {
	t.Helper()
	c, err := imapclient.DialInsecure(addr, nil)
	if err != nil {
		t.Fatalf("dial imap server: %v", err)
	}
	if err := c.Login(testIMAPUser, testIMAPPass).Wait(); err != nil {
		t.Fatalf("login: %v", err)
	}
	return c
}

// seedUnreadMessages appends n unread RFC 5322 messages into INBOX via a
// direct imapclient (using IMAP APPEND, which does not set \Seen). Each
// message has a unique Message-ID so MarkProcessed can find it again.
func seedUnreadMessages(t *testing.T, c *imapclient.Client, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		raw := fmt.Sprintf(
			"From: sender%d@example.com\r\n"+
				"To: recipient@example.com\r\n"+
				"Subject: Message %d\r\n"+
				"Message-ID: <msg-%d@example.com>\r\n"+
				"Date: 01 Jan 2024 12:00:%02d +0000\r\n"+
				"MIME-Version: 1.0\r\n"+
				"Content-Type: text/plain; charset=utf-8\r\n"+
				"\r\n"+
				"Body of message %d\r\n",
			i, i, i, i%60, i,
		)
		appendCmd := c.Append("INBOX", int64(len(raw)), nil)
		if _, err := appendCmd.Write([]byte(raw)); err != nil {
			t.Fatalf("append write %d: %v", i, err)
		}
		if err := appendCmd.Close(); err != nil {
			t.Fatalf("append close %d: %v", i, err)
		}
		if _, err := appendCmd.Wait(); err != nil {
			t.Fatalf("append wait %d: %v", i, err)
		}
	}
}

// newTestIMAPAdapter builds an IMAPAdapter with the production-channel cap
// and the supplied, already-connected imapclient injected via adp.client
// (bypassing the production DialTLS path, which our plain-TCP test server
// can't satisfy).
func newTestIMAPAdapter(t *testing.T, c *imapclient.Client, cap int) *IMAPAdapter {
	t.Helper()
	return &IMAPAdapter{
		config: &IMAPConfig{
			Folder: "INBOX",
		},
		logger:   slog.Default(),
		emailsCh: make(chan *Email, cap),
		stopCh:   make(chan struct{}),
		client:   c,
	}
}

// drainEmails returns all emails currently buffered in ch.
func drainEmails(ch <-chan *Email) []*Email {
	var out []*Email
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}

// TestPEEKFetchDoesNotMarkSeen verifies the core PEEK fix mechanism: a
// fetch cycle does NOT set \Seen server-side, so a second poll (with no
// MarkProcessed in between) re-fetches every message. Before the fix, the
// zero-value FetchItemBodySection{} produced BODY[] (non-PEEK), the in-tree
// server set \Seen during Collect(), and the second poll's
// Search(NotFlag:\Seen) returned nothing.
func TestPEEKFetchDoesNotMarkSeen(t *testing.T) {
	addr, cleanup := startInMemoryIMAPServer(t)
	defer cleanup()
	c := dialTestClient(t, addr)
	defer c.Close()
	seedUnreadMessages(t, c, 5)

	adp := newTestIMAPAdapter(t, c, 10)

	ctx := context.Background()
	if err := adp.PollOnce(ctx); err != nil {
		t.Fatalf("first PollOnce: %v", err)
	}
	first := drainEmails(adp.emailsCh)
	if len(first) != 5 {
		t.Fatalf("first poll delivered %d emails, want 5", len(first))
	}

	// Without MarkProcessed, PEEK must leave the messages unseen so the next
	// poll re-fetches them (this is the property that makes dropped or
	// deferred emails recoverable).
	if err := adp.PollOnce(ctx); err != nil {
		t.Fatalf("second PollOnce: %v", err)
	}
	second := drainEmails(adp.emailsCh)
	if len(second) != 5 {
		t.Fatalf("second poll delivered %d emails, want 5 (PEEK must not mark seen)", len(second))
	}
}

// TestPollBackpressureNoDrop verifies the blocking-send + concurrent-drain
// path that processCmd now relies on: with a >cap backlog (150 unread) and
// a channel cap of 100, the producer must block-and-wait rather than
// dropping overflow, and the consumer receives all 150 messages with no
// loss. Before the fix, the non-blocking select/default push dropped
// messages 101..150 (the reproducer for the original overflow-drop loss).
func TestPollBackpressureNoDrop(t *testing.T) {
	addr, cleanup := startInMemoryIMAPServer(t)
	defer cleanup()
	c := dialTestClient(t, addr)
	defer c.Close()
	const total = 150
	seedUnreadMessages(t, c, total)

	adp := newTestIMAPAdapter(t, c, 100)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Concurrent consumer: count until we have every message. Because the
	// producer blocks on a full channel, it only completes once the consumer
	// has drained enough; the producer's Push loop finishing (PollOnce
	// returning) thus implies all `total` messages were pushed.
	received := make(chan int, 1)
	var consumerWg sync.WaitGroup
	consumerWg.Add(1)
	go func() {
		defer consumerWg.Done()
		count := 0
		deadline := time.After(30 * time.Second)
		for count < total {
			select {
			case <-adp.Emails():
				count++
			case <-deadline:
				received <- count
				return
			}
		}
		received <- count
	}()

	if err := adp.PollOnce(ctx); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	consumerWg.Wait()
	n := <-received
	if n != total {
		t.Fatalf("consumer received %d emails, want %d (backpressure must not drop)", n, total)
	}
}

// TestPollStopsAbandonedMessagesRePolled verifies the recovery path when the
// adapter is stopped mid-push. Without PEEK the FETCH had already set \Seen
// on every fetched message, so the messages that were fetched-but-never-
// delivered to the consumer were permanently lost. With PEEK, only
// MarkProcessed sets \Seen: the 100 delivered emails are marked seen by the
// processor; the 50 fetched-but-abandoned emails stay unseen and are
// re-fetched by a later poll.
func TestPollStopsAbandonedMessagesRePolled(t *testing.T) {
	addr, cleanup := startInMemoryIMAPServer(t)
	defer cleanup()
	c := dialTestClient(t, addr)
	defer c.Close()
	const total = 150
	seedUnreadMessages(t, c, total)

	// First adapter: cap-100 channel and NO concurrent consumer. The
	// producer fills the channel to 100, then blocks on message 101.
	adp1 := newTestIMAPAdapter(t, c, 100)

	pollDone := make(chan error, 1)
	go func() { pollDone <- adp1.PollOnce(context.Background()) }()

	// Wait until the channel is full (producer is blocked at message 101).
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(adp1.emailsCh) >= 100 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := len(adp1.emailsCh); got != 100 {
		t.Fatalf("channel did not fill before stop: got %d, want 100", got)
	}

	// Stop the adapter. The producer's select picks <-stopCh and abandons
	// the remaining 50 messages. Stop also closes the shared client, so a
	// fresh client/adapter is needed for the MarkProcessed + re-poll below.
	if err := adp1.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	select {
	case <-pollDone:
	case <-time.After(5 * time.Second):
		t.Fatal("PollOnce did not return after Stop")
	}

	delivered := drainEmails(adp1.emailsCh)
	if len(delivered) != 100 {
		t.Fatalf("first adapter delivered %d, want 100", len(delivered))
	}

	// Mark the 100 delivered emails as processed — this is what the
	// processor does for successfully handled mail, and the only thing that
	// sets \Seen under PEEK. Use a fresh client because Stop closed the first.
	c2 := dialTestClient(t, addr)
	defer c2.Close()
	adp2 := newTestIMAPAdapter(t, c2, 100)
	ctx := context.Background()
	for _, e := range delivered {
		if err := adp2.MarkProcessed(ctx, e.ID); err != nil {
			t.Fatalf("MarkProcessed(%q): %v", e.ID, err)
		}
	}

	// Second poll: the 100 handled emails are \Seen via MarkProcessed. The
	// 50 fetched-but-abandoned emails were never MarkProcessed, and because
	// of PEEK the FETCH did not set \Seen on them either, so they must be
	// re-fetched. Before the fix (non-PEEK), the FETCH had set \Seen on all
	// 150 and this poll would return 0 — permanent loss.
	if err := adp2.PollOnce(ctx); err != nil {
		t.Fatalf("second PollOnce: %v", err)
	}
	second := drainEmails(adp2.emailsCh)
	if len(second) != total-100 {
		t.Fatalf("second poll re-fetched %d, want %d (PEEK must preserve unseen for abandoned messages)",
			len(second), total-100)
	}
}

// TestMarkProcessedAfterPEEK verifies the post-PEEK contract: an email that
// is successfully handled is marked \Seen via MarkProcessed (which is
// exactly what the processor calls). After MarkProcessed, a subsequent poll
// must NOT re-fetch the message — this is what prevents re-processing loops.
// (Pre-PEEK, FETCH already set \Seen, so MarkProcessed was a redundant no-op
// and the "stays unseen until processed" guarantee was hollow.)
func TestMarkProcessedAfterPEEK(t *testing.T) {
	addr, cleanup := startInMemoryIMAPServer(t)
	defer cleanup()
	c := dialTestClient(t, addr)
	defer c.Close()
	seedUnreadMessages(t, c, 3)

	adp := newTestIMAPAdapter(t, c, 10)
	ctx := context.Background()

	if err := adp.PollOnce(ctx); err != nil {
		t.Fatalf("first PollOnce: %v", err)
	}
	got := drainEmails(adp.emailsCh)
	if len(got) != 3 {
		t.Fatalf("first poll delivered %d, want 3", len(got))
	}

	// Mark each delivered email as processed (this is what the processor
	// does after successful handling).
	for _, e := range got {
		if err := adp.MarkProcessed(ctx, e.ID); err != nil {
			t.Fatalf("MarkProcessed(%q): %v", e.ID, err)
		}
	}

	// Second poll: all three are now \Seen via MarkProcessed, so the unseen
	// search must return nothing.
	if err := adp.PollOnce(ctx); err != nil {
		t.Fatalf("second PollOnce: %v", err)
	}
	if n := len(drainEmails(adp.emailsCh)); n != 0 {
		t.Fatalf("after MarkProcessed, second poll delivered %d, want 0", n)
	}
}

// TestPollEmptyMailbox sanity-checks that a poll on an empty mailbox
// succeeds and enqueues nothing — the push loop's blocking send must not
// run when there are no messages.
func TestPollEmptyMailbox(t *testing.T) {
	addr, cleanup := startInMemoryIMAPServer(t)
	defer cleanup()
	c := dialTestClient(t, addr)
	defer c.Close()

	adp := newTestIMAPAdapter(t, c, 10)
	if err := adp.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce on empty mailbox: %v", err)
	}
	if n := len(adp.emailsCh); n != 0 {
		t.Fatalf("empty mailbox enqueued %d, want 0", n)
	}
}

// TestPollContextCancelAbandonsUnpushed verifies that when the context is
// cancelled while the producer is blocked on a full channel, the producer
// exits cleanly (returning ctx.Err()) and — because of PEEK — the
// unpushed messages stay unseen and are re-fetched by a later poll once
// the delivered ones have been MarkProcessed. This is the recovery path for
// processCmd hitting its 5-minute timeout while a large backlog is in
// flight. Before the fix, the non-PEEK FETCH had already marked every
// fetched message \Seen, so the cancelled poll permanently lost the
// unpushed tail.
func TestPollContextCancelAbandonsUnpushed(t *testing.T) {
	addr, cleanup := startInMemoryIMAPServer(t)
	defer cleanup()
	c := dialTestClient(t, addr)
	defer c.Close()
	const total = 150
	seedUnreadMessages(t, c, total)

	adp1 := newTestIMAPAdapter(t, c, 100)

	ctx, cancel := context.WithCancel(context.Background())
	pollDone := make(chan error, 1)
	go func() { pollDone <- adp1.PollOnce(ctx) }()

	// Wait for the channel to fill, then cancel the context.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(adp1.emailsCh) >= 100 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := len(adp1.emailsCh); got != 100 {
		t.Fatalf("channel did not fill before cancel: got %d, want 100", got)
	}
	cancel()

	select {
	case err := <-pollDone:
		if err == nil {
			t.Fatal("PollOnce returned nil after context cancel, want ctx.Err()")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PollOnce did not return after context cancel")
	}

	// Drain what was delivered (the 100 that made it into the channel
	// before the cancel aborted the push loop).
	delivered := drainEmails(adp1.emailsCh)
	if len(delivered) != 100 {
		t.Fatalf("delivered %d before cancel, want 100", len(delivered))
	}

	// Mark the 100 delivered emails as processed so the re-poll only
	// surfaces the 50 that were fetched-but-abandoned. adp1's client is
	// still open (cancel does not close the connection), but use a fresh
	// adapter anyway to mirror the realistic "next run" shape.
	c2 := dialTestClient(t, addr)
	defer c2.Close()
	adp2 := newTestIMAPAdapter(t, c2, 100)
	markCtx := context.Background()
	for _, e := range delivered {
		if err := adp2.MarkProcessed(markCtx, e.ID); err != nil {
			t.Fatalf("MarkProcessed(%q): %v", e.ID, err)
		}
	}

	// Second poll must re-fetch the 50 abandoned messages: PEEK left them
	// unseen and they were never MarkProcessed. Before the fix, the non-PEEK
	// FETCH had marked all 150 \Seen, so this poll would return 0.
	if err := adp2.PollOnce(markCtx); err != nil {
		t.Fatalf("second PollOnce: %v", err)
	}
	second := drainEmails(adp2.emailsCh)
	if len(second) != total-100 {
		t.Fatalf("second poll re-fetched %d, want %d (PEEK must preserve unseen for cancelled polls)",
			len(second), total-100)
	}
}
