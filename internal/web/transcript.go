package web

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
)

// Transcript lines carry whole tool outputs, so they routinely run past
// bufio.Scanner's 64KB default. Anything longer than this is a pathological
// blob we skip rather than fail the whole read for.
const maxTranscriptLine = 8 << 20

// Session files grow without bound -- 21MB for a single day-long task here, and
// the busiest project directory on this machine is over a gigabyte. Since the
// UI only ever wants the most recent turns, read backwards from the end in
// escalating windows instead of parsing the whole file. Only a conversation
// that is genuinely mostly silent tool calls ever needs the larger passes.
var tailBudgets = []int64{2 << 20, 8 << 20, 32 << 20, math.MaxInt64}

// chatMessage is one turn of the executor's conversation as the UI wants it:
// prose plus a summary of the machinery, never the machinery itself.
type chatMessage struct {
	ID        string   `json:"id"`
	Role      string   `json:"role"`
	Text      string   `json:"text"`
	Tools     []string `json:"tools"`
	Thinking  int      `json:"thinking"`
	CreatedAt string   `json:"created_at"`
}

// transcriptEntry is the subset of a Claude session JSONL line we care about.
// The file also carries attachment/last-prompt/bridge-session/ai-title records,
// which outnumber the conversation and are ignored.
type transcriptEntry struct {
	Type        string          `json:"type"`
	UUID        string          `json:"uuid"`
	Timestamp   string          `json:"timestamp"`
	IsSidechain bool            `json:"isSidechain"`
	Message     json.RawMessage `json:"message"`
}

type transcriptMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Name string `json:"name"`
}

// A running task is polled every few seconds, and nothing in the file changes
// between polls unless the agent wrote something. Keyed on the directory's
// (name, size, mtime) fingerprint so a stale entry is impossible.
type transcriptCacheEntry struct {
	fingerprint string
	limit       int
	messages    []chatMessage
}

var (
	transcriptCacheMu sync.Mutex
	transcriptCache   = map[string]transcriptCacheEntry{}
)

// handleTaskMessages returns the executor's real conversation.
//
// This does NOT come from task_logs: that table only ever received tool calls
// and system lines, so the assistant's prose was never persisted there. The
// conversation lives solely in Claude's session transcript on disk.
func (s *Server) handleTaskMessages(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}

	limit := 60
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 1000 {
		limit = 1000
	}

	var (
		messages []chatMessage
		err      error
	)
	if task.PlacementTarget != "" && task.PlacementTarget != "local" {
		workDir, _, dbErr := s.db.GetTaskRemoteWorktree(task.ID)
		if dbErr != nil {
			jsonErr(w, "failed to locate remote worktree", http.StatusInternalServerError)
			return
		}
		if workDir == "" {
			jsonErr(w, "remote worktree is not recorded yet", http.StatusConflict)
			return
		}
		messages, err = readRemoteTranscript(r.Context(), task.PlacementTarget, workDir, limit)
	} else {
		messages = readTranscriptDir(s.transcriptDir(task), limit)
	}
	if err != nil {
		jsonErr(w, fmt.Sprintf("cannot read conversation from %s: %v", task.PlacementTarget, err), http.StatusBadGateway)
		return
	}
	if len(messages) > limit {
		messages = messages[len(messages)-limit:]
	}
	jsonOK(w, messages)
}

// These scripts run through RemoteRunner, so the same outbound SSH path used
// to launch and inspect a placed task also supplies its transcript. Only file
// names and sizes are read on every poll; transcript bytes cross the wire only
// when that fingerprint changes.
const remoteTranscriptMetaScript = `# TY_TRANSCRIPT_META
set -eu
workdir=$1
slug=$(printf '%s' "$workdir" | sed 's#[/.]#-#g')
dir=${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects/$slug
[ -d "$dir" ] || exit 0
for file in "$dir"/*.jsonl; do
  [ -f "$file" ] || continue
  name=${file##*/}
  case "$name" in agent-*) continue ;; esac
  size=$(wc -c < "$file" | tr -d ' ')
  printf '%s\t%s\n' "$name" "$size"
done
`

const remoteTranscriptArchiveScript = `# TY_TRANSCRIPT_ARCHIVE
set -eu
workdir=$1
budget=$2
slug=$(printf '%s' "$workdir" | sed 's#[/.]#-#g')
dir=${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects/$slug
[ -d "$dir" ] || exit 0
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
for file in "$dir"/*.jsonl; do
  [ -f "$file" ] || continue
  name=${file##*/}
  case "$name" in agent-*) continue ;; esac
  size=$(wc -c < "$file" | tr -d ' ')
  if [ "$size" -gt "$budget" ]; then
    tail -c "$budget" < "$file" > "$tmp/$name"
  else
    cp "$file" "$tmp/$name"
  fi
done
tar -cf - -C "$tmp" .
`

func readRemoteTranscript(ctx context.Context, host, workDir string, limit int) ([]chatMessage, error) {
	runner := executor.RemoteRunner{Host: host}
	metaCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	meta, err := runner.Command(metaCtx, "", "sh", "-c", remoteTranscriptMetaScript, "ty-transcript", workDir).Output()
	cancel()
	if err != nil {
		return nil, err
	}
	sizes, err := parseRemoteTranscriptMeta(meta)
	if err != nil {
		return nil, err
	}
	if len(sizes) == 0 {
		return []chatMessage{}, nil
	}

	key := "remote\x00" + host + "\x00" + workDir
	fp := string(meta)
	transcriptCacheMu.Lock()
	if cached, ok := transcriptCache[key]; ok && cached.fingerprint == fp && cached.limit >= limit {
		messages := cached.messages
		transcriptCacheMu.Unlock()
		return messages, nil
	}
	transcriptCacheMu.Unlock()

	var messages []chatMessage
	for _, budget := range tailBudgets {
		fetchCtx, fetchCancel := context.WithTimeout(ctx, 30*time.Second)
		archive, fetchErr := runner.Command(fetchCtx, "", "sh", "-c", remoteTranscriptArchiveScript,
			"ty-transcript", workDir, strconv.FormatInt(budget, 10)).Output()
		fetchCancel()
		if fetchErr != nil {
			return nil, fetchErr
		}
		var truncated bool
		messages, truncated, fetchErr = readRemoteTranscriptArchive(archive, sizes)
		if fetchErr != nil {
			return nil, fetchErr
		}
		if len(messages) >= limit || !truncated {
			break
		}
	}

	transcriptCacheMu.Lock()
	transcriptCache[key] = transcriptCacheEntry{fingerprint: fp, limit: limit, messages: messages}
	transcriptCacheMu.Unlock()
	return messages, nil
}

func parseRemoteTranscriptMeta(data []byte) (map[string]int64, error) {
	files := make(map[string]int64)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		name, rawSize, ok := strings.Cut(line, "\t")
		if !ok || filepath.Base(name) != name || !strings.HasSuffix(name, ".jsonl") {
			return nil, fmt.Errorf("invalid transcript metadata")
		}
		size, err := strconv.ParseInt(strings.TrimSpace(rawSize), 10, 64)
		if err != nil || size < 0 {
			return nil, fmt.Errorf("invalid transcript size for %s", name)
		}
		files[name] = size
	}
	return files, nil
}

func readRemoteTranscriptArchive(data []byte, sizes map[string]int64) ([]chatMessage, bool, error) {
	reader := tar.NewReader(bytes.NewReader(data))
	var batches [][]chatMessage
	truncated := false
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, false, fmt.Errorf("read remote transcript archive: %w", err)
		}
		name := filepath.Base(header.Name)
		originalSize, ok := sizes[name]
		if !ok || header.Typeflag != tar.TypeReg {
			continue
		}
		fileTruncated := header.Size < originalSize
		if fileTruncated {
			truncated = true
		}
		batches = append(batches, readTranscript(reader, fileTruncated))
	}
	return mergeTranscriptBatches(batches), truncated, nil
}

// transcriptDir locates the Claude session directory for a task's worktree.
// Claude names it after the working directory with BOTH "/" and "." replaced
// by "-" — dropping the second replacement silently points at a directory that
// does not exist for any project whose path contains a dot.
func (s *Server) transcriptDir(task *db.Task) string {
	workDir := task.WorktreePath
	if workDir == "" {
		return ""
	}

	// A task may pin its own config dir; otherwise the project's wins, and
	// failing that the process default. Mirrors how the executor resolves it
	// when it spawns Claude in the first place.
	configDir := task.ClaudeConfigDir
	if strings.TrimSpace(configDir) == "" && task.Project != "" {
		if p, err := s.db.GetProjectByName(task.Project); err == nil && p != nil {
			configDir = p.ClaudeConfigDir
		}
	}

	slug := strings.ReplaceAll(workDir, "/", "-")
	slug = strings.ReplaceAll(slug, ".", "-")
	return filepath.Join(executor.ResolveClaudeConfigDir(configDir), "projects", slug)
}

// sessionFiles lists the conversation transcripts in a directory, newest last.
// agent-*.jsonl are subagent transcripts, not this conversation.
func sessionFiles(dir string) ([]os.FileInfo, []string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	var infos []os.FileInfo
	var paths []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".jsonl") || strings.HasPrefix(name, "agent-") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		infos = append(infos, info)
		paths = append(paths, filepath.Join(dir, name))
	}
	return infos, paths
}

func fingerprint(infos []os.FileInfo) string {
	var b strings.Builder
	for _, info := range infos {
		b.WriteString(info.Name())
		b.WriteByte(':')
		b.WriteString(strconv.FormatInt(info.Size(), 10))
		b.WriteByte(':')
		b.WriteString(strconv.FormatInt(info.ModTime().UnixNano(), 10))
		b.WriteByte('|')
	}
	return b.String()
}

// readTranscriptDir merges every session file in a transcript directory. A
// resumed task gets a fresh UUID.jsonl each time, so the conversation is spread
// across several files and has to be re-interleaved by timestamp. A missing
// directory is normal (task never ran) and yields no messages.
func readTranscriptDir(dir string, limit int) []chatMessage {
	if dir == "" {
		return []chatMessage{}
	}
	infos, paths := sessionFiles(dir)
	if len(paths) == 0 {
		return []chatMessage{}
	}

	fp := fingerprint(infos)
	transcriptCacheMu.Lock()
	if cached, ok := transcriptCache[dir]; ok && cached.fingerprint == fp && cached.limit >= limit {
		msgs := cached.messages
		transcriptCacheMu.Unlock()
		return msgs
	}
	transcriptCacheMu.Unlock()

	var out []chatMessage
	for _, budget := range tailBudgets {
		merged, truncated := readFilesWithBudget(paths, budget)
		out = merged
		// Enough turns, or we already read everything there is.
		if len(out) >= limit || !truncated {
			break
		}
	}

	transcriptCacheMu.Lock()
	transcriptCache[dir] = transcriptCacheEntry{fingerprint: fp, limit: limit, messages: out}
	transcriptCacheMu.Unlock()
	return out
}

func readFilesWithBudget(paths []string, budget int64) ([]chatMessage, bool) {
	var batches [][]chatMessage
	anyTruncated := false

	for _, path := range paths {
		msgs, truncated := readFileTail(path, budget)
		if truncated {
			anyTruncated = true
		}
		batches = append(batches, msgs)
	}
	return mergeTranscriptBatches(batches), anyTruncated
}

func mergeTranscriptBatches(batches [][]chatMessage) []chatMessage {
	type timed struct {
		at  time.Time
		msg chatMessage
	}
	var collected []timed
	seen := make(map[string]bool)
	for _, messages := range batches {
		for _, message := range messages {
			if seen[message.ID] {
				continue
			}
			seen[message.ID] = true
			at, _ := time.Parse(time.RFC3339, message.CreatedAt)
			collected = append(collected, timed{at: at, msg: message})
		}
	}

	sort.SliceStable(collected, func(i, j int) bool { return collected[i].at.Before(collected[j].at) })

	out := make([]chatMessage, 0, len(collected))
	for _, c := range collected {
		out = append(out, c.msg)
	}
	return coalesceSilentTurns(out)
}

// readFileTail parses at most the final `budget` bytes of a session file. The
// bool reports whether anything was skipped, so the caller knows a bigger
// window might find more. The first line after a seek is almost always a
// fragment and is discarded.
func readFileTail(path string, budget int64) ([]chatMessage, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, false
	}

	truncated := false
	if budget > 0 && info.Size() > budget {
		if _, err := f.Seek(info.Size()-budget, io.SeekStart); err != nil {
			return nil, false
		}
		truncated = true
	}

	return readTranscript(f, truncated), truncated
}

func readTranscript(reader io.Reader, truncated bool) []chatMessage {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), maxTranscriptLine)

	var out []chatMessage
	skipFragment := truncated
	for scanner.Scan() {
		if skipFragment {
			skipFragment = false
			continue
		}
		var entry transcriptEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		// Sidechains are subagent turns; they'd interleave a second
		// conversation into this one.
		if entry.IsSidechain || (entry.Type != "user" && entry.Type != "assistant") {
			continue
		}
		if msg, ok := toChatMessage(entry); ok {
			out = append(out, msg)
		}
	}
	return out
}

func toChatMessage(entry transcriptEntry) (chatMessage, bool) {
	var message transcriptMessage
	if err := json.Unmarshal(entry.Message, &message); err != nil {
		return chatMessage{}, false
	}

	msg := chatMessage{
		ID:        entry.UUID,
		Role:      entry.Type,
		Tools:     []string{},
		CreatedAt: entry.Timestamp,
	}

	// A user turn is either a plain string (what the human actually typed) or
	// an array that is usually just tool_result payloads echoed back to the
	// model. The latter is machinery and must not surface as "You said".
	var text string
	if err := json.Unmarshal(message.Content, &text); err == nil {
		msg.Text = strings.TrimSpace(text)
		return msg, msg.Text != ""
	}

	var blocks []contentBlock
	if err := json.Unmarshal(message.Content, &blocks); err != nil {
		return chatMessage{}, false
	}

	var prose []string
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if t := strings.TrimSpace(block.Text); t != "" {
				prose = append(prose, t)
			}
		case "thinking":
			msg.Thinking++
		case "tool_use":
			if block.Name != "" {
				msg.Tools = append(msg.Tools, block.Name)
			}
		}
	}
	msg.Text = strings.Join(prose, "\n\n")

	// Keep a turn that did work silently (tools but no commentary); drop one
	// that carried nothing but tool_result echoes.
	return msg, msg.Text != "" || len(msg.Tools) > 0 || msg.Thinking > 0
}

// coalesceSilentTurns folds tool-only assistant turns into the next turn that
// actually says something.
//
// A real task runs ~1100 silent tool turns against ~120 speaking ones. Emitted
// one-per-bubble they bury the conversation exactly the way the raw log did,
// so each run of silent work becomes a tool summary on the message that
// follows it.
func coalesceSilentTurns(in []chatMessage) []chatMessage {
	out := make([]chatMessage, 0, len(in))

	var pendingTools []string
	pendingThinking := 0
	pendingID := ""
	pendingAt := ""

	reset := func() {
		pendingTools = nil
		pendingThinking = 0
		pendingID = ""
		pendingAt = ""
	}

	for _, m := range in {
		if m.Role == "assistant" && m.Text == "" {
			if pendingID == "" {
				pendingID, pendingAt = m.ID, m.CreatedAt
			}
			pendingTools = append(pendingTools, m.Tools...)
			pendingThinking += m.Thinking
			continue
		}

		// Pending work belongs to the agent. If the next speaker is the human,
		// give that work its own row rather than captioning their message with
		// tools they did not run.
		if pendingID != "" && m.Role != "assistant" {
			out = append(out, chatMessage{
				ID: pendingID, Role: "assistant", Tools: pendingTools,
				Thinking: pendingThinking, CreatedAt: pendingAt,
			})
			reset()
		}

		msg := m
		if pendingID != "" {
			msg.Tools = append(append([]string{}, pendingTools...), msg.Tools...)
			msg.Thinking += pendingThinking
			reset()
		}
		out = append(out, msg)
	}

	// Work that finished without a closing remark still deserves a row.
	if pendingID != "" {
		out = append(out, chatMessage{
			ID: pendingID, Role: "assistant", Tools: pendingTools,
			Thinking: pendingThinking, CreatedAt: pendingAt,
		})
	}
	return out
}
