package web

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// These tests pin the fix for the once-per-minute SSE disconnect.
//
// Go's http.Server.WriteTimeout is set once per request and never reset for
// streaming responses, so the 60s value at server.go:202 capped every SSE
// connection at 60s. The two SSE handlers in sse.go now extend the deadline
// per response via http.NewResponseController(w).SetWriteDeadline, matching
// the pattern already used by placement.go / handlers.go / handlers_gui.go.
//
// To keep the test fast we drive the *production* handlers through a real
// *http.Server whose WriteTimeout is compressed from 60s to 400ms — the
// mechanism is identical, only the timescale changes. With the fix the
// stream survives past the deadline; without it the next post-deadline write
// fails and the server tears down the connection (client sees io.EOF).

// startHTTPServer wraps s.Handler() in a real *http.Server with a compressed
// WriteTimeout, reproducing the production mechanism at server.go:198 in a
// test-friendly timescale. The base URL of the listener is returned.
func startHTTPServer(t *testing.T, s *Server, writeTimeout time.Duration) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	hs := &http.Server{
		Handler:      s.Handler(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: writeTimeout,
		IdleTimeout:  120 * time.Second,
	}
	serverErr := make(chan error, 1)
	go func() { serverErr <- hs.Serve(ln) }()
	t.Cleanup(func() {
		_ = hs.Close()
		<-serverErr
	})
	return "http://" + ln.Addr().String()
}

// readSSEFrame reads one SSE frame (up to and including the terminating blank
// line) from br. The frame text and any read error are returned together.
func readSSEFrame(br *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			b.WriteString(line)
		}
		if err == nil && (line == "\n" || line == "\r\n") {
			return b.String(), nil
		}
		if err != nil {
			return b.String(), err
		}
	}
}

// pumpSSE continuously reads SSE frames from r and sends them on ch. When a
// read returns an error (io.EOF when the server closes the connection) the
// error is sent on errCh and pumping stops.
func pumpSSE(r io.Reader, ch chan<- string, errCh chan<- error) {
	br := bufio.NewReader(r)
	for {
		frame, err := readSSEFrame(br)
		if frame != "" {
			ch <- frame
		}
		if err != nil {
			errCh <- err
			return
		}
	}
}

// TestSSEBoardStreamSurvivesWriteTimeout asserts the board stream keeps
// delivering events after the server's WriteTimeout has fired. A mutation
// (CreateTask -> event_log row) made after the deadline must still reach the
// client; with the buggy handler the post-deadline write fails and the client
// reads io.EOF instead.
func TestSSEBoardStreamSurvivesWriteTimeout(t *testing.T) {
	database := setupTestDB(t)
	srv := New(Config{Addr: ":0", DB: database, CmdRunner: &mockRunner{}})
	base := startHTTPServer(t, srv, 400*time.Millisecond)

	resp, err := http.Get(base + "/api/board/stream?signal=true")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()

	frames := make(chan string, 8)
	errCh := make(chan error, 1)
	go pumpSSE(resp.Body, frames, errCh)

	// Initial snapshot, pushed immediately at connection open.
	select {
	case f := <-frames:
		if !strings.Contains(f, "event: board") {
			t.Fatalf("expected initial board event, got: %q", f)
		}
	case err := <-errCh:
		t.Fatalf("initial snapshot read failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial board snapshot")
	}

	// Wait until the 400ms WriteTimeout has elapsed. Without the per-handler
	// SetWriteDeadline any subsequent write fails and the server closes the
	// connection.
	time.Sleep(700 * time.Millisecond)

	// CreateTask records a genesis event_log row; the next 1s board poll
	// emits a fresh board event. This is the write the deadline would kill.
	if err := database.CreateTask(&db.Task{Title: "post-deadline task", Status: db.StatusBacklog}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	// The board polls every 1s; the event should arrive well inside 4s. If
	// the connection was torn down at the deadline we get io.EOF instead.
	select {
	case f := <-frames:
		if !strings.Contains(f, "event: board") {
			t.Fatalf("expected board event after deadline, got: %q", f)
		}
	case err := <-errCh:
		t.Fatalf("stream died at the WriteTimeout (the ~60s-disconnect bug): %v", err)
	case <-time.After(4 * time.Second):
		t.Fatal("stream stalled after the deadline; no board event arrived")
	}
}

// TestSSETaskLogStreamSurvivesWriteTimeout is the analogous pin for the task
// log stream: a log line appended after the WriteTimeout has fired must still
// reach the client. Without the fix the write fails and the client sees
// io.EOF.
func TestSSETaskLogStreamSurvivesWriteTimeout(t *testing.T) {
	database := setupTestDB(t)
	task := &db.Task{Title: "log stream task", Status: db.StatusProcessing}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	srv := New(Config{Addr: ":0", DB: database, CmdRunner: &mockRunner{}})
	base := startHTTPServer(t, srv, 400*time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("%s/api/tasks/%d/stream", base, task.ID))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()

	frames := make(chan string, 8)
	errCh := make(chan error, 1)
	go pumpSSE(resp.Body, frames, errCh)

	// The task log stream flushes only headers at open; the first frame is
	// the first log line. Wait until the WriteTimeout has elapsed so the
	// subsequent write is the one the fix protects.
	time.Sleep(700 * time.Millisecond)

	if err := database.AppendTaskLog(task.ID, "output", "post-deadline log line"); err != nil {
		t.Fatalf("append log: %v", err)
	}

	select {
	case f := <-frames:
		if !strings.Contains(f, "event: log") {
			t.Fatalf("expected log event after deadline, got: %q", f)
		}
	case err := <-errCh:
		t.Fatalf("stream died at the WriteTimeout (the ~60s-disconnect bug): %v", err)
	case <-time.After(4 * time.Second):
		t.Fatal("stream stalled after the deadline; no log event arrived")
	}
}
