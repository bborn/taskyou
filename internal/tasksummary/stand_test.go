package tasksummary

import (
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

func TestIsStandLine(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"   ", false},
		{"Merge email ingest PR", true},
		{"Which webhook auth?", true},
		{"Merge or close PR #12 — email ingest is ready", true}, // long one-liner still counts; display clamps
		{"- User asked for email ingest\n- Agent opened a PR\n- Next: merge it", false},
		{"- User asked for email ingest", false},
		{"* bullet recap", false},
		{"1. numbered recap", false},
		{"line one\nline two", false},
	}
	for _, c := range cases {
		if got := IsStandLine(c.in); got != c.want {
			t.Errorf("IsStandLine(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestDisplayStandRejectsFossils(t *testing.T) {
	fossil := "- Built the IMAP poller\n- Opened PR #12\n- Waiting on merge"
	if got := DisplayStand(fossil); got != "" {
		t.Errorf("fossil recap must not render as stand, got %q", got)
	}
	want := "Waiting on APNs key"
	if got := DisplayStand(want); got != want {
		t.Errorf("DisplayStand(%q) = %q", want, got)
	}
	if got := DisplayStand("  " + want + "  "); got != want {
		t.Errorf("DisplayStand should trim, got %q", got)
	}
}

func TestDisplayStandClampsToFiveWords(t *testing.T) {
	long := "Waiting on the APNs key before we can test pushes"
	got := DisplayStand(long)
	if got != "Waiting on the APNs key" {
		t.Errorf("DisplayStand(%q) = %q, want five words", long, got)
	}
}

func TestDisplayStandTruncatesLongToken(t *testing.T) {
	long := strings.Repeat("a", StandMaxChars+20)
	got := DisplayStand(long)
	if !IsStandLine(long) {
		t.Fatal("a long single line is still a stand")
	}
	if got == long {
		t.Fatal("expected truncation")
	}
	if n := len([]rune(got)); n > StandMaxChars {
		t.Errorf("truncated length = %d, want <= %d", n, StandMaxChars)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated stand should end with ellipsis, got %q", got)
	}
}

func TestNormalizeStand(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"  Merge PR #12  ", "Merge PR #12"},
		{"\"Which port?\"", "Which port?"},
		{"- first bullet\n- second", "first bullet"},
		{"line one\nline two", "line one"},
		{"Waiting on the APNs key before we can test pushes", "Waiting on the APNs key"},
	}
	for _, c := range cases {
		if got := NormalizeStand(c.in); got != c.want {
			t.Errorf("NormalizeStand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFallbackStand(t *testing.T) {
	q := &db.TaskLog{LineType: "question", Content: "Which auth scheme should the webhook use?"}
	if got := FallbackStand(q); got != "Which auth scheme should the" {
		t.Errorf("question fallback = %q", got)
	}
	textQ := &db.TaskLog{LineType: "text", Content: "Need the destination folder?"}
	if got := FallbackStand(textQ); got != textQ.Content {
		t.Errorf("text question fallback = %q", got)
	}
	reconnect := &db.TaskLog{LineType: "system", Content: "Reconnecting to claude session abc"}
	if got := FallbackStand(reconnect); got != "" {
		t.Errorf("reconnect must not be fallback, got %q", got)
	}
	cont := &db.TaskLog{LineType: "system", Content: "--- Continuation ---"}
	if got := FallbackStand(cont); got != "" {
		t.Errorf("continuation marker must not be fallback, got %q", got)
	}
	tool := &db.TaskLog{LineType: "tool", Content: "Editing store.go"}
	if got := FallbackStand(tool); got != "" {
		t.Errorf("non-question tool line must not be fallback, got %q", got)
	}
	if got := FallbackStand(nil); got != "" {
		t.Errorf("nil log = %q", got)
	}
}

func TestNeedsRefresh(t *testing.T) {
	now := time.Now()
	distilled := db.LocalTime{Time: now.Add(-time.Hour)}
	freshLog := &db.TaskLog{CreatedAt: db.LocalTime{Time: now}}
	oldLog := &db.TaskLog{CreatedAt: db.LocalTime{Time: now.Add(-2 * time.Hour)}}
	stand := "Merge email ingest PR"
	fossil := "- did a thing\n- another"

	blockedStand := &db.Task{Status: db.StatusBlocked, Summary: stand, LastDistilledAt: &distilled}
	if NeedsRefresh(blockedStand, oldLog) {
		t.Error("valid stand with no new logs should not refresh")
	}
	if !NeedsRefresh(blockedStand, freshLog) {
		t.Error("new logs after last_distilled should refresh")
	}
	longStand := &db.Task{
		Status:          db.StatusBlocked,
		Summary:         "Waiting on the APNs key before we can test pushes",
		LastDistilledAt: &distilled,
	}
	if !NeedsRefresh(longStand, oldLog) {
		t.Error("oversized stand should refresh even without new logs")
	}

	blockedFossil := &db.Task{Status: db.StatusBlocked, Summary: fossil, LastDistilledAt: &distilled}
	if !NeedsRefresh(blockedFossil, oldLog) {
		t.Error("fossil recap on blocked should refresh")
	}
	emptyBlocked := &db.Task{Status: db.StatusBlocked}
	if !NeedsRefresh(emptyBlocked, nil) {
		t.Error("empty stand on blocked should refresh")
	}

	doneFossil := &db.Task{Status: db.StatusDone, Summary: fossil, LastDistilledAt: &distilled}
	if NeedsRefresh(doneFossil, freshLog) {
		t.Error("done tasks freeze — no refresh")
	}
	processing := &db.Task{Status: db.StatusProcessing, Summary: ""}
	if NeedsRefresh(processing, freshLog) {
		t.Error("processing uses live crumb, not a rewrite")
	}
	queued := &db.Task{Status: db.StatusQueued, Summary: fossil}
	if NeedsRefresh(queued, nil) {
		t.Error("queued should not rewrite")
	}
	if NeedsRefresh(nil, nil) {
		t.Error("nil task")
	}
}

func TestShouldRewriteOnStatus(t *testing.T) {
	if !ShouldRewriteOnStatus(db.StatusProcessing, db.StatusBlocked) {
		t.Error("processing → blocked should rewrite")
	}
	if ShouldRewriteOnStatus(db.StatusBlocked, db.StatusBlocked) {
		t.Error("already blocked should not rewrite")
	}
	if ShouldRewriteOnStatus(db.StatusBlocked, db.StatusDone) {
		t.Error("blocked → done freezes")
	}
	if ShouldRewriteOnStatus(db.StatusProcessing, db.StatusDone) {
		t.Error("processing → done does not force-rewrite")
	}
}

func TestBuildSummaryPromptIsOneLine(t *testing.T) {
	p := buildSummaryPrompt(&db.Task{Title: "Ship email ingest", Status: db.StatusBlocked}, nil)
	if strings.Contains(p, "2-4") {
		t.Errorf("prompt still asks for a recap:\n%s", p)
	}
	if !strings.Contains(strings.ToLower(p), "five word") {
		t.Errorf("prompt should ask for five words:\n%s", p)
	}
}
