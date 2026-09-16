package web

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestHandleTaskMessagesReadsRemoteClaudeTranscript(t *testing.T) {
	srv, database, local := setupServer(t)
	task := createTestTask(t, database, &db.Task{
		Title: "remote conversation", Status: db.StatusProcessing, Project: "personal",
	})
	if err := database.SetTaskPlacement(task.ID, "test-remote-host", "test"); err != nil {
		t.Fatal(err)
	}
	if err := database.SetTaskRemoteWorktree(task.ID, "/remote/worktree", "task/test"); err != nil {
		t.Fatal(err)
	}

	line := `{"type":"assistant","uuid":"remote-answer","timestamp":"2026-09-15T12:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Answer from the remote agent"}]}}` + "\n"
	archive := remoteTranscriptArchive(t, "session.jsonl", []byte(line))
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "transcript.tar")
	if err := os.WriteFile(archivePath, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	commandsPath := filepath.Join(dir, "commands")
	stub := `#!/bin/sh
printf '%s\n' "$*" >> "$TY_TEST_REMOTE_COMMANDS"
case "$*" in
  *TY_TRANSCRIPT_META*) printf 'session.jsonl\t%s\n' "$(wc -c < "$TY_TEST_REMOTE_ARCHIVE_LINE")" ;;
  *TY_TRANSCRIPT_ARCHIVE*) cat "$TY_TEST_REMOTE_ARCHIVE" ;;
  *) exit 1 ;;
esac
`
	linePath := filepath.Join(dir, "line")
	if err := os.WriteFile(linePath, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TY_TEST_REMOTE_ARCHIVE", archivePath)
	t.Setenv("TY_TEST_REMOTE_ARCHIVE_LINE", linePath)
	t.Setenv("TY_TEST_REMOTE_COMMANDS", commandsPath)

	req := httptest.NewRequest("GET", fmt.Sprintf("/api/tasks/%d/messages", task.ID), nil)
	req.SetPathValue("id", fmt.Sprint(task.ID))
	w := httptest.NewRecorder()
	srv.handleTaskMessages(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("messages: %d %s", w.Code, w.Body.String())
	}
	var messages []chatMessage
	if err := json.Unmarshal(w.Body.Bytes(), &messages); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].ID != "remote-answer" || messages[0].Text != "Answer from the remote agent" {
		t.Fatalf("wrong remote messages: %+v", messages)
	}
	second := httptest.NewRecorder()
	srv.handleTaskMessages(second, req)
	if second.Code != http.StatusOK {
		t.Fatalf("cached messages: %d %s", second.Code, second.Body.String())
	}
	commands, _ := os.ReadFile(commandsPath)
	if !strings.Contains(string(commands), "test-remote-host") || !strings.Contains(string(commands), "/remote/worktree") {
		t.Fatalf("transcript was not requested from the placed host/worktree: %s", commands)
	}
	if got := strings.Count(string(commands), "TY_TRANSCRIPT_ARCHIVE"); got != 1 {
		t.Fatalf("unchanged transcript was transferred %d times, want 1: %s", got, commands)
	}
	if len(local.snapshot()) != 0 {
		t.Fatalf("remote transcript touched the local command runner: %v", local.snapshot())
	}
}

func TestHandleTaskMessagesReportsUnreachableRemoteHost(t *testing.T) {
	srv, database, _ := setupServer(t)
	task := createTestTask(t, database, &db.Task{
		Title: "unreachable conversation", Status: db.StatusProcessing, Project: "personal",
	})
	if err := database.SetTaskPlacement(task.ID, "offline-host", "test"); err != nil {
		t.Fatal(err)
	}
	if err := database.SetTaskRemoteWorktree(task.ID, "/remote/worktree", "task/test"); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	stub := "#!/bin/sh\necho 'ssh: connect to host offline-host: No route to host' >&2\nexit 255\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	req := httptest.NewRequest("GET", fmt.Sprintf("/api/tasks/%d/messages", task.ID), nil)
	req.SetPathValue("id", fmt.Sprint(task.ID))
	w := httptest.NewRecorder()
	srv.handleTaskMessages(w, req)

	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "offline-host") {
		t.Fatalf("unreachable host response = %d %s, want 502 naming host", w.Code, w.Body.String())
	}
}

func remoteTranscriptArchive(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
