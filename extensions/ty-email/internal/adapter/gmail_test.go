package adapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// newTestGmailService returns a real *gmail.Service whose HTTP traffic is
// directed at srv (an httptest.Server acting as the Gmail API). Using a real
// service (rather than a struct zero value) means the racing pointer load in
// fetchMessage dispatches into a genuine HTTP round trip, the same way the
// production code traverses it.
func newTestGmailService(t *testing.T, srv *httptest.Server) *gmail.Service {
	t.Helper()
	svc, err := gmail.NewService(context.Background(),
		option.WithEndpoint(srv.URL+"/"),
		option.WithHTTPClient(http.DefaultClient))
	if err != nil {
		t.Fatalf("gmail.NewService: %v", err)
	}
	return svc
}

// newGmailAdapterForTest builds a GmailAdapter wired to a real *gmail.Service
// pointing at srv, with a default logger and an empty config.
func newGmailAdapterForTest(t *testing.T, srv *httptest.Server) *GmailAdapter {
	t.Helper()
	a := NewGmailAdapter(&GmailConfig{}, nil)
	a.service = newTestGmailService(t, srv)
	return a
}

// mustMarshal serializes v to JSON, failing the test on error. Using the gmail
// library's own struct types guarantees the canned responses match the wire
// format the client expects to decode.
func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(b)
}

// fakeGmailAPI routes Gmail API GET requests, returning canned list/get bodies.
type fakeGmailAPI struct {
	listBody string
	getBody  string
	delay    time.Duration // applied to Get before responding

	mu    sync.Mutex
	lists int
	gets  int
}

func (f *fakeGmailAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/messages"):
		f.mu.Lock()
		f.lists++
		f.mu.Unlock()
		_, _ = io.WriteString(w, f.listBody)
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/messages/"):
		if f.delay > 0 {
			time.Sleep(f.delay)
		}
		f.mu.Lock()
		f.gets++
		f.mu.Unlock()
		_, _ = io.WriteString(w, f.getBody)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeGmailAPI) counts() (lists, gets int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lists, f.gets
}

// minimalGetBody is a canned Gmail message used by the race tests.
func minimalGetBody(t *testing.T) string {
	t.Helper()
	return mustMarshal(t, &gmail.Message{
		Id:           "fake-id",
		ThreadId:     "t1",
		LabelIds:     []string{"INBOX"},
		InternalDate: 1,
		Payload: &gmail.MessagePart{
			Headers: []*gmail.MessagePartHeader{
				{Name: "From", Value: "a@b.c"},
				{Name: "Subject", Value: "s"},
			},
		},
	})
}

// TestGmailFetchMessageStopRace is the regression test for the data race fixed
// by threading the captured *gmail.Service into fetchMessage. Before the fix,
// fetchMessage re-read the shared a.service field without holding a.mu while
// Stop wrote a.service = nil under the mutex: a Go memory-model data race
// (flagged by -race) and a probable nil-pointer dereference panic.
//
// The test launches fetchMessage and Stop concurrently over many iterations.
// It must pass cleanly under `go test -race` (no race report, no panic).
func TestGmailFetchMessageStopRace(t *testing.T) {
	f := &fakeGmailAPI{
		listBody: mustMarshal(t, &gmail.ListMessagesResponse{
			Messages: []*gmail.Message{{Id: "fake-id", ThreadId: "t1"}},
		}),
		getBody: minimalGetBody(t),
	}
	srv := httptest.NewServer(f)
	defer srv.Close()

	for i := 0; i < 100; i++ {
		a := newGmailAdapterForTest(t, srv)
		// captured is read here (no concurrency yet) and passed explicitly into
		// fetchMessage. With the fix, fetchMessage must use this argument rather
		// than re-reading the shared a.service field. If it re-reads a.service
		// internally, the race detector flags it against Stop's nil store.
		captured := a.service

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = a.fetchMessage(context.Background(), captured, "fake-id")
		}()
		go func() {
			defer wg.Done()
			_ = a.Stop()
		}()
		wg.Wait()
	}
}

// TestGmailPollStopRace exercises the production shutdown path: a poll
// iteration in flight (mid-fetchMessage) while Stop runs on another goroutine.
// Before the fix, poll captured a.service under the mutex but fetchMessage
// re-read the shared field unsynchronized, racing with Stop's locked write and
// potentially panicking with a nil dereference at the .Users field access.
//
// A small server-side delay on Get widens the in-flight window so any
// unsynchronized access is more likely to be observed by the race detector.
func TestGmailPollStopRace(t *testing.T) {
	f := &fakeGmailAPI{
		listBody: mustMarshal(t, &gmail.ListMessagesResponse{
			Messages: []*gmail.Message{
				{Id: "m1", ThreadId: "t1"},
				{Id: "m2", ThreadId: "t2"},
			},
		}),
		getBody: minimalGetBody(t),
		delay:   5 * time.Millisecond,
	}
	srv := httptest.NewServer(f)
	defer srv.Close()

	for i := 0; i < 100; i++ {
		a := newGmailAdapterForTest(t, srv)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			a.poll(context.Background())
		}()
		go func() {
			defer wg.Done()
			_ = a.Stop()
		}()
		wg.Wait()
	}
}

// TestGmailPollLoopGracefulShutdown exercises the pollLoop + Stop integration
// path that mirrors serveCmd's shutdown: a running pollLoop (initial poll plus
// periodic ticks) is stopped via Stop, which closes stopCh. The pollLoop must
// exit without panic; the race detector must report nothing.
func TestGmailPollLoopGracefulShutdown(t *testing.T) {
	f := &fakeGmailAPI{
		listBody: mustMarshal(t, &gmail.ListMessagesResponse{
			Messages: []*gmail.Message{{Id: "m1", ThreadId: "t1"}},
		}),
		getBody: minimalGetBody(t),
	}
	srv := httptest.NewServer(f)
	defer srv.Close()

	a := newGmailAdapterForTest(t, srv)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		a.pollLoop(ctx, 50*time.Millisecond)
		close(done)
	}()

	// Allow an initial poll plus a couple of ticks to run.
	time.Sleep(120 * time.Millisecond)

	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pollLoop did not exit after Stop")
	}
}

// TestGmailPollStopBeforeCapture verifies the nil-check branch of poll: when
// Stop has run before poll captures a.service, poll observes nil and returns
// early without issuing any API requests.
func TestGmailPollStopBeforeCapture(t *testing.T) {
	f := &fakeGmailAPI{
		listBody: mustMarshal(t, &gmail.ListMessagesResponse{
			Messages: []*gmail.Message{{Id: "m1", ThreadId: "t1"}},
		}),
		getBody: minimalGetBody(t),
	}
	srv := httptest.NewServer(f)
	defer srv.Close()

	a := newGmailAdapterForTest(t, srv)

	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	a.poll(context.Background()) // must return early: a.service is nil

	if lists, gets := f.counts(); lists != 0 || gets != 0 {
		t.Fatalf("expected no API calls after Stop, got lists=%d gets=%d", lists, gets)
	}
}
