package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTranscript writes JSONL lines to a temp transcript file.
func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const (
	// The real shape Claude Code writes, from the ollama 429 that killed a
	// workflow step three runs in a row.
	rateLimited = `{"type":"assistant","isApiErrorMessage":true,"apiErrorStatus":429,"apiErrorIsTransient":false,"message":{"content":[{"type":"text","text":"API Error: Request rejected (429) · you (someone) have reached your session usage limit, upgrade for higher limits: https://ollama.com/upgrade"}]}}`
	overloaded  = `{"type":"assistant","isApiErrorMessage":true,"apiErrorStatus":529,"apiErrorIsTransient":true,"message":{"content":[{"type":"text","text":"API Error: Overloaded"}]}}`
	goodReply   = `{"type":"assistant","message":{"content":[{"type":"text","text":"Done — pushed the review."}]}}`
	userTurn    = `{"type":"user","message":{"content":"go on"}}`
)

func TestLastAPIErrorReportsTheFailureASessionEndedOn(t *testing.T) {
	got := LastAPIError(writeTranscript(t, userTurn, goodReply, rateLimited))
	if got == nil {
		t.Fatal("no error reported; the session ended on a 429")
	}
	if got.Status != 429 {
		t.Errorf("Status = %d, want 429", got.Status)
	}
	if got.Transient {
		t.Error("Transient = true; a usage-limit rejection is not transient")
	}
	if want := "usage limit"; !strings.Contains(got.Message, want) {
		t.Errorf("Message = %q, want it to mention %q", got.Message, want)
	}
}

// Claude Code retries transient failures. An error the session recovered from is
// noise — reporting it would train the reader to ignore these lines.
func TestLastAPIErrorIgnoresAFailureTheSessionRecoveredFrom(t *testing.T) {
	if got := LastAPIError(writeTranscript(t, overloaded, goodReply)); got != nil {
		t.Errorf("reported %q, want nil — the model answered after the error", got.String())
	}
}

func TestLastAPIErrorKeepsTheTransientHint(t *testing.T) {
	got := LastAPIError(writeTranscript(t, overloaded))
	if got == nil {
		t.Fatal("no error reported")
	}
	if !got.Transient {
		t.Error("Transient = false, want true so the log can suggest a retry")
	}
}

func TestLastAPIErrorOnCleanOrMissingTranscript(t *testing.T) {
	if got := LastAPIError(writeTranscript(t, userTurn, goodReply)); got != nil {
		t.Errorf("reported %q on a clean session, want nil", got.String())
	}
	if got := LastAPIError(filepath.Join(t.TempDir(), "nope.jsonl")); got != nil {
		t.Errorf("reported %q for a missing transcript, want nil", got.String())
	}
	if got := LastAPIError(""); got != nil {
		t.Errorf("reported %q for an empty path, want nil", got.String())
	}
}

// A status the message doesn't already state is appended, so the log line is
// self-contained; one it does state is not duplicated.
func TestAPIErrorString(t *testing.T) {
	e := &APIError{Status: 500, Message: "API Error: internal"}
	if want := "API Error: internal (HTTP 500)"; e.String() != want {
		t.Errorf("String() = %q, want %q", e.String(), want)
	}
	e = &APIError{Status: 429, Message: "API Error: Request rejected (429)"}
	if want := "API Error: Request rejected (429)"; e.String() != want {
		t.Errorf("String() = %q, want %q", e.String(), want)
	}
}
