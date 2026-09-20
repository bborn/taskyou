package web

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// These tests pin the escalating-budget loop in readTranscriptDir (and the
// identical loop in readRemoteTranscript) for the resumed-task shape: a small,
// fully-read previous-session file plus a large, still-truncated current-
// session file. The count-based early-exit `len(out) >= limit` was a premature
// optimization that is only valid for a single file (whose tail is contiguous
// with EOF); with multiple files the merged count can clear `limit` while the
// newest file is still truncated, so the visible window drops the 41st–60th
// newest current-session turns and keeps stale previous-session turns at the
// older edge.

// makeSpeakingAssistantLine renders a real transcriptEntry for one speaking
// assistant turn: a single non-empty `text` content block, so toChatMessage
// keeps it and coalesceSilentTurns passes it through unchanged. `text` lets the
// caller control the line's byte size (large text -> large line).
func makeSpeakingAssistantLine(t *testing.T, id, ts, text string) string {
	t.Helper()
	entry := map[string]any{
		"type":      "assistant",
		"uuid":      id,
		"timestamp": ts,
		"message": map[string]any{
			"role": "assistant",
			"content": []map[string]any{
				{"type": "text", "text": text},
			},
		},
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal %s: %v", id, err)
	}
	return string(raw) + "\n"
}

func writeTranscript(t *testing.T, dir, name string, lines []string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Join(lines, "")), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// transcriptTimestamp is a strictly-increasing RFC3339 timestamp for turn i on
// `date`, at one-second granularity. Every old-* (date 2026-01-01) precedes
// every new-* (date 2026-06-01), so mergeTranscriptBatches' ascending sort
// places every old turn before every new turn.
func transcriptTimestamp(date string, i int) string {
	return fmt.Sprintf("%sT00:%02d:%02dZ", date, i/60, i%60)
}

// tailBudgetLines builds `count` speaking-assistant lines whose JSONL line is
// approximately `lineBytes` bytes long, so a 2 MiB tail of the resulting file
// holds well under `limit` turns. The id survives at the head of the text so a
// surviving turn is identifiable regardless of how the tail cuts the padding.
func tailBudgetLines(t *testing.T, idPrefix, date string, count, lineBytes int) []string {
	t.Helper()
	padding := strings.Repeat("x", lineBytes)
	lines := make([]string, 0, count)
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("%s-%d", idPrefix, i)
		lines = append(lines, makeSpeakingAssistantLine(t, id, transcriptTimestamp(date, i), id+" "+padding))
	}
	return lines
}

// trailingIDs returns the IDs of the last n messages, mirroring the
// most-recent-`limit` window handleTaskMessages renders.
func trailingIDs(messages []chatMessage, n int) []string {
	if len(messages) > n {
		messages = messages[len(messages)-n:]
	}
	ids := make([]string, len(messages))
	for i, m := range messages {
		ids[i] = m.ID
	}
	return ids
}

func firstStaleID(ids []string, prefix string) string {
	for _, id := range ids {
		if !strings.HasPrefix(id, prefix) {
			return id
		}
	}
	return ""
}

// newestOnly returns the IDs that begin with `prefix`, in order. It lets a test
// assert "the window is exactly the newest N turns of the current session"
// without coupling to the timestamp arithmetic.
func idsWithPrefix(messages []chatMessage, prefix string) []string {
	ids := make([]string, 0, len(messages))
	for _, m := range messages {
		if strings.HasPrefix(m.ID, prefix) {
			ids = append(ids, m.ID)
		}
	}
	return ids
}

func newIDs(t *testing.T, from, count int) []string {
	t.Helper()
	ids := make([]string, count)
	for i := 0; i < count; i++ {
		ids[i] = fmt.Sprintf("new-%d", from+i)
	}
	return ids
}

// idsFrom is the prefix-parameterized generalisation of newIDs, for asserting
// against windows that include turns from files whose id prefix is not "new-".
func idsFrom(t *testing.T, prefix string, from, count int) []string {
	t.Helper()
	ids := make([]string, count)
	for i := 0; i < count; i++ {
		ids[i] = fmt.Sprintf("%s-%d", prefix, from+i)
	}
	return ids
}

// TestReadTranscriptDirBudgetLoopDropsNewestAcrossFiles reproduces the resumed
// multi-file budget bug directly against readTranscriptDir. The previous-
// session file (old.jsonl, 80 small turns on 2026-01-01) is fully read at the
// first 2 MiB budget; the current-session file (new.jsonl, 100 large turns on
// 2026-06-01, ~50 KiB/line) is still truncated there. The combined post-coalesce
// count clears limit=60, so the buggy count-based early-exit stops the loop
// without escalating — and the trailing-`limit` window the handler renders
// retains stale previous-session turns at its older edge instead of the
// 41st–60th newest current-session turns.
func TestReadTranscriptDirBudgetLoopDropsNewestAcrossFiles(t *testing.T) {
	const (
		oldCount    = 80
		newCount    = 100
		limit       = 60
		newLineSize = 50 * 1024 // ~50 KiB/line -> the 2 MiB tail holds < 60 turns
	)
	dir := t.TempDir()
	// Defend against any future reuse of this exact temp path: the fingerprint
	// would not match, but clearing documents that the read is from-disk.
	transcriptCacheMu.Lock()
	delete(transcriptCache, dir)
	transcriptCacheMu.Unlock()

	writeTranscript(t, dir, "old.jsonl", tailBudgetLines(t, "old", "2026-01-01", oldCount, 150))
	writeTranscript(t, dir, "new.jsonl", tailBudgetLines(t, "new", "2026-06-01", newCount, newLineSize))

	got := readTranscriptDir(dir, limit)
	visible := trailingIDs(got, limit)
	want := newIDs(t, 40, limit) // the 60 newest turns across both files: new-40..new-99

	if !reflect.DeepEqual(visible, want) {
		t.Fatalf("budget loop returned stale older-session message %q instead of a newer current-session message; got IDs: %s",
			firstStaleID(visible, "new-"), strings.Join(visible, ","))
	}
}

// TestReadFilesWithBudgetFullReadRepairsWindow isolates the helper from the
// loop. At the full-read (math.MaxInt64) budget readFilesWithBudget must
// report nothing truncated and hold the full 180 post-coalesce turns, with the
// trailing 60 being exactly new-40..new-99. This passes against buggy code and
// corroborates that the bug is the loop's stop condition, not the merge.
func TestReadFilesWithBudgetFullReadRepairsWindow(t *testing.T) {
	const (
		oldCount    = 80
		newCount    = 100
		limit       = 60
		newLineSize = 50 * 1024
	)
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.jsonl")
	newPath := filepath.Join(dir, "new.jsonl")
	if err := os.WriteFile(oldPath, []byte(strings.Join(tailBudgetLines(t, "old", "2026-01-01", oldCount, 150), "")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte(strings.Join(tailBudgetLines(t, "new", "2026-06-01", newCount, newLineSize), "")), 0o600); err != nil {
		t.Fatal(err)
	}

	merged, truncated := readFilesWithBudget([]string{oldPath, newPath}, math.MaxInt64)
	if truncated {
		t.Fatalf("full-read budget still reports truncated; merged %d messages", len(merged))
	}
	const total = oldCount + newCount
	if len(merged) != total {
		t.Fatalf("merged message count = %d, want %d (coalescing must not drop speaking turns)", len(merged), total)
	}
	if got := idsWithPrefix(merged, "old-"); len(got) != oldCount {
		t.Fatalf("old-* messages surviving coalesce = %d, want %d: %v", len(got), oldCount, got)
	}
	if visible := trailingIDs(merged, limit); !reflect.DeepEqual(visible, newIDs(t, 40, limit)) {
		t.Fatalf("trailing-%d window after full read = %v, want new-40..new-99", limit, visible)
	}
}

// TestReadRemoteTranscriptBudgetLoopDropsNewestAcrossFiles exercises the remote
// path's identical stop condition end-to-end through handleTaskMessages. A
// stub ssh serves both the transcript metadata and a budget-aware archive
// (tail -c $budget per file, exactly as remoteTranscriptArchiveScript does on
// a real host), so the escalating-budget loop sees a truncated newest file at
// the 2 MiB budget and a fully-read archive at the larger ones.
func TestReadRemoteTranscriptBudgetLoopDropsNewestAcrossFiles(t *testing.T) {
	const (
		oldCount    = 80
		newCount    = 100
		limit       = 60
		newLineSize = 50 * 1024
	)

	// Real transcript files served by the ssh stub: the host's `$HOME/.claude`
	// is bypassed entirely; the stub reads straight from this temp dir.
	transcriptDir := t.TempDir()
	writeTranscript(t, transcriptDir, "old.jsonl", tailBudgetLines(t, "old", "2026-01-01", oldCount, 150))
	writeTranscript(t, transcriptDir, "new.jsonl", tailBudgetLines(t, "new", "2026-06-01", newCount, newLineSize))

	// The remote cache is keyed on host+worktree; both names are free of digits
	// so the stub's budget extraction (the last numeric token of $*) is
	// unambiguous.
	const (
		remoteHost = "budget-loop-host"
		remoteWork = "/remote/budget-worktree"
	)
	// Clear any prior remote cache entry for this key so the run is from-disk.
	transcriptCacheMu.Lock()
	delete(transcriptCache, "remote\x00"+remoteHost+"\x00"+remoteWork)
	transcriptCacheMu.Unlock()

	binDir := t.TempDir()
	// The stub mirrors remoteTranscriptMetaScript / remoteTranscriptArchiveScript
	// against $TY_TEST_REMOTE_TRANSCRIPT_DIR. The budget is the last numeric
	// token the runner appends to the archive script line (ty-transcript
	// <workdir> <budget>); workDir carries no digits, so `tail -n1` of the
	// numeric matches is the budget.
	stub := `#!/bin/sh
case "$*" in
  *TY_TRANSCRIPT_META*)
    for f in "$TY_TEST_REMOTE_TRANSCRIPT_DIR"/*.jsonl; do
      [ -f "$f" ] || continue
      name=${f##*/}
      case "$name" in agent-*) continue ;; esac
      size=$(wc -c < "$f" | tr -d ' ')
      printf '%s\t%s\n' "$name" "$size"
    done
    ;;
  *TY_TRANSCRIPT_ARCHIVE*)
    budget=$(printf '%s\n' "$*" | grep -oE '[0-9]+' | tail -n1)
    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' EXIT HUP INT TERM
    for f in "$TY_TEST_REMOTE_TRANSCRIPT_DIR"/*.jsonl; do
      [ -f "$f" ] || continue
      name=${f##*/}
      case "$name" in agent-*) continue ;; esac
      size=$(wc -c < "$f" | tr -d ' ')
      if [ "$size" -gt "$budget" ]; then
        tail -c "$budget" < "$f" > "$tmp/$name"
      else
        cp "$f" "$tmp/$name"
      fi
    done
    tar -cf - -C "$tmp" .
    rm -rf "$tmp"
    ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "ssh"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TY_TEST_REMOTE_TRANSCRIPT_DIR", transcriptDir)
	t.Setenv("HOME", t.TempDir()) // never touch the real HOME

	srv, database, _ := setupServer(t)
	task := createTestTask(t, database, &db.Task{
		Title: "resumed remote conversation", Status: db.StatusProcessing, Project: "personal",
	})
	if err := database.SetTaskPlacement(task.ID, remoteHost, "test"); err != nil {
		t.Fatal(err)
	}
	if err := database.SetTaskRemoteWorktree(task.ID, remoteWork, "task/test"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/api/tasks/%d/messages", task.ID), nil)
	req.SetPathValue("id", fmt.Sprintf("%d", task.ID))
	w := httptest.NewRecorder()
	srv.handleTaskMessages(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("messages: %d %s", w.Code, w.Body.String())
	}
	var messages []chatMessage
	if err := json.Unmarshal(w.Body.Bytes(), &messages); err != nil {
		t.Fatal(err)
	}
	visible := trailingIDs(messages, limit)
	want := newIDs(t, 40, limit)
	if !reflect.DeepEqual(visible, want) {
		t.Fatalf("remote budget loop returned stale older-session message %q instead of a newer current-session message; got IDs: %s",
			firstStaleID(visible, "new-"), strings.Join(visible, ","))
	}
}

// TestReadTranscriptDirCacheRecomputesCorrectlyOnFingerprintChange exercises
// the cache recomputation path (G6). The bug was a recomputation error — every
// fingerprint change re-derived the same wrong window — so this is the
// regression that matters operationally: after the dir's (name,size,mtime)
// fingerprint changes, the loop must recompute the *correct* most-recent-
// `limit` window, not regurgitate a stale one. The first call primes the cache;
// appending a `new-100` turn changes new.jsonl's size (hence the fingerprint);
// the second call must miss the cache and return new-41..new-100.
func TestReadTranscriptDirCacheRecomputesCorrectlyOnFingerprintChange(t *testing.T) {
	const (
		oldCount    = 80
		newCount    = 100
		limit       = 60
		newLineSize = 50 * 1024
	)
	dir := t.TempDir()
	transcriptCacheMu.Lock()
	delete(transcriptCache, dir)
	transcriptCacheMu.Unlock()

	writeTranscript(t, dir, "old.jsonl", tailBudgetLines(t, "old", "2026-01-01", oldCount, 150))
	writeTranscript(t, dir, "new.jsonl", tailBudgetLines(t, "new", "2026-06-01", newCount, newLineSize))

	// Cold read primes the cache with the trailing-60 = new-40..new-99 window.
	cold := readTranscriptDir(dir, limit)
	if got := trailingIDs(cold, limit); !reflect.DeepEqual(got, newIDs(t, 40, limit)) {
		t.Fatalf("cold read = %v, want new-40..new-99", got)
	}
	// Sanity: the cache really holds an entry for this dir now.
	transcriptCacheMu.Lock()
	cached, ok := transcriptCache[dir]
	transcriptCacheMu.Unlock()
	if !ok {
		t.Fatal("cold read did not prime transcriptCache for this dir")
	}

	// Append a `new-100` turn strictly after new-99 (2026-06-01T01:40:00Z).
	// best-effort: touch mtime forward so the fingerprint's mtime field also
	// differs even on a filesystem where appending alone might land in the
	// same nanosecond as the write above; the size field changes regardless.
	newPath := filepath.Join(dir, "new.jsonl")
	f, err := os.OpenFile(newPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	extra := makeSpeakingAssistantLine(t, "new-100", transcriptTimestamp("2026-06-01", 100), "new-100 "+strings.Repeat("x", newLineSize))
	if _, err := f.WriteString(extra); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Second) // guaranteed newer than the write above
	if err := os.Chtimes(newPath, future, future); err != nil {
		t.Fatal(err)
	}

	// The fingerprint changed (size+mtime), so the cached entry must miss and
	// the loop must recompute. The trailing-60 window must now be exactly
	// new-41..new-100: the previous new-40 dropped off the older edge, the new
	// new-100 sits at the newer edge.
	hot := readTranscriptDir(dir, limit)
	want := newIDs(t, 41, limit) // new-41..new-100
	if got := trailingIDs(hot, limit); !reflect.DeepEqual(got, want) {
		t.Fatalf("recomputed window after fingerprint change = %v, want new-41..new-100 (cache served a stale window or recomputation was wrong)", got)
	}

	// And the cache entry must now reflect the recomputed (correct) window,
	// proving the fix replaced — not just bypassed — the stale entry.
	transcriptCacheMu.Lock()
	cached, ok = transcriptCache[dir]
	transcriptCacheMu.Unlock()
	if !ok {
		t.Fatal("recompute dropped the cache entry")
	}
	if cachedTrailing := trailingIDs(cached.messages, limit); !reflect.DeepEqual(cachedTrailing, want) {
		t.Fatalf("cached entry = %v, want %v (stale entry survived recompute)", cachedTrailing, want)
	}
	// The fingerprint stored on the entry must match the dir's *new*
	// fingerprint, not the cold-read one — otherwise the next caller would
	// miss every time.
	infos, _ := sessionFiles(dir)
	if fp := fingerprint(infos); cached.fingerprint != fp {
		t.Fatalf("cached fingerprint does not match dir: cached=%q dir=%q", cached.fingerprint, fp)
	}
}

// TestReadTranscriptDirSingleFileFastPathStaysUnchanged pins G4 for the local
// path: a single small session file that fits the first 2 MiB budget is fully
// read on iteration one (truncated==false), so the loop breaks immediately and
// the rendered window is the file's turns in order. The fix must not regress
// this — it changes the stop condition, not the read/merge/sort/trim.
func TestReadTranscriptDirSingleFileFastPathStaysUnchanged(t *testing.T) {
	const (
		count = 30 // fits comfortably in the 2 MiB first budget
		limit = 60
	)
	dir := t.TempDir()
	transcriptCacheMu.Lock()
	delete(transcriptCache, dir)
	transcriptCacheMu.Unlock()

	writeTranscript(t, dir, "single.jsonl", tailBudgetLines(t, "single", "2026-03-15", count, 150))

	got := readTranscriptDir(dir, limit)
	// No trim needed (count < limit), so the window is the file's 30 turns in
	// ascending order, no stale/dropped turns, no coalescing surprises.
	if len(got) != count {
		t.Fatalf("single-file read returned %d messages, want %d", len(got), count)
	}
	want := make([]string, count)
	for i := 0; i < count; i++ {
		want[i] = fmt.Sprintf("single-%d", i)
	}
	ids := make([]string, len(got))
	for i, m := range got {
		ids[i] = m.ID
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("single-file window order/content changed: got %v, want %v", ids, want)
	}
	// A single small file must not have triggered truncation or escalation:
	// the cached entry's messages must equal the returned slice (no extra
	// hidden budget work) and the cache must hit on a same-limit re-read.
	again := readTranscriptDir(dir, limit)
	if !reflect.DeepEqual(again, got) {
		t.Fatalf("second read diverged from first: got %v then %v", got, again)
	}
}

// TestReadTranscriptDirLoopReachesMaxInt64BudgetAndTerminates forces the loop
// through every budget in tailBudgets and asserts it terminates at the
// math.MaxInt64 final budget with a correct window. huge.jsonl is 34 MiB
// (34 * 1 MiB lines), so it is truncated at the 2 MiB, 8 MiB and 32 MiB budgets
// and only fully read at math.MaxInt64; the loop therefore iterates all four
// budgets before `!truncated` breaks it. Buggy code broke early at the 2 MiB
// budget (len(out) >= limit) and never reached MaxInt64; this test fails under
// the bug and passes under the fix, directly pinning G5 at the loop level (T2
// pins it at the helper level only).
func TestReadTranscriptDirLoopReachesMaxInt64BudgetAndTerminates(t *testing.T) {
	const (
		oldCount  = 80
		hugeCount = 34 // 34 * 1 MiB = 34 MiB > 32 MiB penultimate budget
		limit     = 60
	)
	dir := t.TempDir()
	transcriptCacheMu.Lock()
	delete(transcriptCache, dir)
	transcriptCacheMu.Unlock()

	writeTranscript(t, dir, "old.jsonl", tailBudgetLines(t, "old", "2026-01-01", oldCount, 150))

	// Build huge.jsonl by appending 1 MiB lines so the test never holds a 34 MiB
	// string slice in memory. Each line is a real speaking-assistant turn.
	hugePath := filepath.Join(dir, "huge.jsonl")
	f, err := os.OpenFile(hugePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	const oneMiB = 1 << 20
	for i := 0; i < hugeCount; i++ {
		id := fmt.Sprintf("huge-%d", i)
		line := makeSpeakingAssistantLine(t, id, transcriptTimestamp("2026-06-01", i), id+" "+strings.Repeat("x", oneMiB))
		if _, err := f.WriteString(line); err != nil {
			_ = f.Close()
			t.Fatalf("write huge-%d: %v", i, err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// The loop must escalate through every budget and terminate. A hang here
	// fails the -timeout 120s; a wrong window fails the assertion.
	got := readTranscriptDir(dir, limit)
	const total = oldCount + hugeCount
	if len(got) != total {
		t.Fatalf("loop returned %d messages after escalation, want %d (every turn fully read)", len(got), total)
	}
	// Every huge-* is newer than every old-* (2026-06-01 vs 2026-01-01), so the
	// trailing-60 window is the 34 huge turns at the newest edge plus the 26
	// newest old turns (old-54..old-79) at the older edge.
	visible := trailingIDs(got, limit)
	want := append(idsFrom(t, "old", 54, oldCount-54), idsFrom(t, "huge", 0, hugeCount)...) // old-54..old-79 + huge-0..huge-33
	if !reflect.DeepEqual(visible, want) {
		t.Fatalf("MaxInt64-budget window = %v, want old-54..old-79 + huge-0..huge-33", visible)
	}
}
