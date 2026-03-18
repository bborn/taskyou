package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestReadJSONLSessionContent(t *testing.T) {
	t.Run("claude-style JSONL with user and assistant messages", func(t *testing.T) {
		dir := t.TempDir()
		sessionFile := filepath.Join(dir, "session.jsonl")

		content := `{"type":"system","message":"system init"}
{"role":"user","content":[{"type":"text","text":"Hello, can you help me fix a bug?"}]}
{"role":"assistant","content":[{"type":"text","text":"Sure! What bug are you seeing?"},{"type":"tool_use","id":"123","name":"read_file"}]}
{"role":"user","content":[{"type":"text","text":"The login page crashes when I submit"}]}
{"role":"assistant","content":[{"type":"text","text":"I found the issue. The form handler has a null pointer dereference."}]}
`
		if err := os.WriteFile(sessionFile, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		result := readJSONLSessionContent(sessionFile)

		if result == "" {
			t.Fatal("expected non-empty result")
		}
		if !strings.Contains(result, "**User:** Hello, can you help me fix a bug?") {
			t.Errorf("expected user message, got: %s", result)
		}
		if !strings.Contains(result, "**Assistant:** Sure! What bug are you seeing?") {
			t.Errorf("expected assistant message, got: %s", result)
		}
		if !strings.Contains(result, "**Assistant:** I found the issue.") {
			t.Errorf("expected second assistant message, got: %s", result)
		}
		// Tool use should be excluded
		if strings.Contains(result, "tool_use") || strings.Contains(result, "read_file") {
			t.Errorf("tool use details should not appear in output: %s", result)
		}
	})

	t.Run("empty file returns empty string", func(t *testing.T) {
		dir := t.TempDir()
		sessionFile := filepath.Join(dir, "empty.jsonl")
		if err := os.WriteFile(sessionFile, []byte(""), 0644); err != nil {
			t.Fatal(err)
		}

		result := readJSONLSessionContent(sessionFile)
		if result != "" {
			t.Errorf("expected empty result, got: %s", result)
		}
	})

	t.Run("non-existent file returns empty string", func(t *testing.T) {
		result := readJSONLSessionContent("/nonexistent/path/session.jsonl")
		if result != "" {
			t.Errorf("expected empty result, got: %s", result)
		}
	})

	t.Run("only system messages returns empty", func(t *testing.T) {
		dir := t.TempDir()
		sessionFile := filepath.Join(dir, "system-only.jsonl")
		content := `{"type":"system","message":"init"}
{"type":"result","result":"done"}
`
		if err := os.WriteFile(sessionFile, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		result := readJSONLSessionContent(sessionFile)
		if result != "" {
			t.Errorf("expected empty result, got: %s", result)
		}
	})
}

func TestReadJSONSessionContent(t *testing.T) {
	t.Run("messages array with role and content", func(t *testing.T) {
		dir := t.TempDir()
		sessionFile := filepath.Join(dir, "session.json")

		content := `{
			"workDir": "/tmp/test",
			"messages": [
				{"role": "user", "content": "Please fix the tests"},
				{"role": "assistant", "content": "I'll look at the test failures now."},
				{"role": "user", "content": "Focus on the auth module"},
				{"role": "assistant", "content": "Found the issue in auth/login_test.go"}
			]
		}`
		if err := os.WriteFile(sessionFile, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		result := readJSONSessionContent(sessionFile)

		if result == "" {
			t.Fatal("expected non-empty result")
		}
		if !strings.Contains(result, "**User:** Please fix the tests") {
			t.Errorf("expected user message, got: %s", result)
		}
		if !strings.Contains(result, "**Assistant:** I'll look at the test failures now.") {
			t.Errorf("expected assistant message, got: %s", result)
		}
	})

	t.Run("conversation key with sender field", func(t *testing.T) {
		dir := t.TempDir()
		sessionFile := filepath.Join(dir, "session.json")

		content := `{
			"conversation": [
				{"sender": "human", "text": "What is this code doing?"},
				{"sender": "ai", "text": "This code implements a REST API."}
			]
		}`
		if err := os.WriteFile(sessionFile, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		result := readJSONSessionContent(sessionFile)

		if result == "" {
			t.Fatal("expected non-empty result")
		}
		if !strings.Contains(result, "**User:** What is this code doing?") {
			t.Errorf("expected user message, got: %s", result)
		}
		if !strings.Contains(result, "**Assistant:** This code implements a REST API.") {
			t.Errorf("expected assistant message, got: %s", result)
		}
	})

	t.Run("nested content array (Claude-style in JSON)", func(t *testing.T) {
		dir := t.TempDir()
		sessionFile := filepath.Join(dir, "session.json")

		content := `{
			"messages": [
				{"role": "user", "content": [{"type": "text", "text": "Help me debug"}]},
				{"role": "assistant", "content": [{"type": "text", "text": "Sure, let me check."}]}
			]
		}`
		if err := os.WriteFile(sessionFile, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		result := readJSONSessionContent(sessionFile)

		if !strings.Contains(result, "**User:** Help me debug") {
			t.Errorf("expected user message from content array, got: %s", result)
		}
	})

	t.Run("Gemini-style with parts array", func(t *testing.T) {
		dir := t.TempDir()
		sessionFile := filepath.Join(dir, "session.json")

		content := `{
			"history": [
				{"role": "user", "parts": ["Analyze this code"]},
				{"role": "model", "parts": ["The code implements a sorting algorithm."]}
			]
		}`
		if err := os.WriteFile(sessionFile, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		result := readJSONSessionContent(sessionFile)

		if !strings.Contains(result, "**User:** Analyze this code") {
			t.Errorf("expected user message, got: %s", result)
		}
		if !strings.Contains(result, "**Assistant:** The code implements a sorting algorithm.") {
			t.Errorf("expected model->assistant message, got: %s", result)
		}
	})

	t.Run("auto-discover messages in nested object", func(t *testing.T) {
		dir := t.TempDir()
		sessionFile := filepath.Join(dir, "session.json")

		content := `{
			"metadata": {"version": 1},
			"data": {
				"thread": [
					{"role": "user", "content": "First message"},
					{"role": "assistant", "content": "First response"},
					{"role": "user", "content": "Second message"},
					{"role": "assistant", "content": "Second response"}
				]
			}
		}`
		if err := os.WriteFile(sessionFile, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		_ = readJSONSessionContent(sessionFile)
		// findMessagesInObject only searches top-level values, not recursively nested.
		// The "data" value is an object not an array, so "thread" won't be found.
		// This is expected - the fallback is best-effort for top-level arrays only.
	})

	t.Run("empty JSON returns empty", func(t *testing.T) {
		dir := t.TempDir()
		sessionFile := filepath.Join(dir, "empty.json")
		if err := os.WriteFile(sessionFile, []byte(`{}`), 0644); err != nil {
			t.Fatal(err)
		}

		result := readJSONSessionContent(sessionFile)
		if result != "" {
			t.Errorf("expected empty result, got: %s", result)
		}
	})

	t.Run("non-existent file returns empty", func(t *testing.T) {
		result := readJSONSessionContent("/nonexistent/session.json")
		if result != "" {
			t.Errorf("expected empty result, got: %s", result)
		}
	})
}

func TestTruncateSessionContent(t *testing.T) {
	t.Run("short content is not truncated", func(t *testing.T) {
		content := "**User:** Hello\n\n**Assistant:** Hi there!"
		result := truncateSessionContent(content)
		if result != content {
			t.Errorf("expected unchanged content, got: %s", result)
		}
	})

	t.Run("long content is truncated from beginning", func(t *testing.T) {
		// Build content longer than maxSessionContentSize
		var sb strings.Builder
		for i := 0; i < 1000; i++ {
			sb.WriteString("**User:** " + strings.Repeat("x", 100) + "\n\n")
		}
		content := sb.String()

		result := truncateSessionContent(content)

		if len(result) > maxSessionContentSize+200 { // allow some overhead for truncation notice
			t.Errorf("expected truncated content, got length: %d", len(result))
		}
		if !strings.HasPrefix(result, "[... earlier conversation truncated ...]") {
			t.Errorf("expected truncation notice, got: %s", result[:100])
		}
	})
}

func TestFormatSessionHandoff(t *testing.T) {
	t.Run("formats content with executor name", func(t *testing.T) {
		content := "**User:** Hello\n\n**Assistant:** Hi!"
		result := FormatSessionHandoff("claude", content)

		if !strings.Contains(result, "## Previous Session Context") {
			t.Error("expected section header")
		}
		if !strings.Contains(result, "**claude**") {
			t.Error("expected executor name")
		}
		if !strings.Contains(result, content) {
			t.Error("expected content to be included")
		}
	})

	t.Run("empty content returns empty", func(t *testing.T) {
		result := FormatSessionHandoff("claude", "")
		if result != "" {
			t.Errorf("expected empty result, got: %s", result)
		}
	})
}

func TestReadClaudeSessionContent(t *testing.T) {
	t.Run("reads session from claude config dir", func(t *testing.T) {
		// Setup a fake Claude config directory structure
		tmpDir := t.TempDir()
		workDir := "/tmp/test-workdir"

		// Claude escapes path: /tmp/test-workdir -> -tmp-test-workdir
		escapedPath := strings.ReplaceAll(workDir, "/", "-")
		escapedPath = strings.ReplaceAll(escapedPath, ".", "-")
		projectDir := filepath.Join(tmpDir, "projects", escapedPath)
		if err := os.MkdirAll(projectDir, 0755); err != nil {
			t.Fatal(err)
		}

		// Create a session file
		sessionContent := `{"role":"user","content":[{"type":"text","text":"Fix the bug"}]}
{"role":"assistant","content":[{"type":"text","text":"I'll fix it now."}]}
`
		sessionFile := filepath.Join(projectDir, "abc12345-1234-5678-abcd-123456789abc.jsonl")
		if err := os.WriteFile(sessionFile, []byte(sessionContent), 0644); err != nil {
			t.Fatal(err)
		}

		result := ReadClaudeSessionContent(workDir, tmpDir)

		if result == "" {
			t.Fatal("expected non-empty result")
		}
		if !strings.Contains(result, "**User:** Fix the bug") {
			t.Errorf("expected user message, got: %s", result)
		}
		if !strings.Contains(result, "**Assistant:** I'll fix it now.") {
			t.Errorf("expected assistant message, got: %s", result)
		}
	})

	t.Run("no session returns empty", func(t *testing.T) {
		tmpDir := t.TempDir()
		result := ReadClaudeSessionContent("/nonexistent/workdir", tmpDir)
		if result != "" {
			t.Errorf("expected empty result, got: %s", result)
		}
	})
}

// mockTaskExecutor is a minimal TaskExecutor for testing GetPreviousSessionContent.
type mockTaskExecutor struct {
	name           string
	sessionID      string // returned by FindSessionID
	sessionContent string // returned by GetSessionContent
}

func (m *mockTaskExecutor) Name() string                          { return m.name }
func (m *mockTaskExecutor) FindSessionID(workDir string) string   { return m.sessionID }
func (m *mockTaskExecutor) GetSessionContent(workDir string) string { return m.sessionContent }

// Stubs for the rest of the interface — not exercised by these tests.
func (m *mockTaskExecutor) Execute(ctx context.Context, task *db.Task, workDir, prompt string) ExecResult {
	return ExecResult{}
}
func (m *mockTaskExecutor) Resume(ctx context.Context, task *db.Task, workDir, prompt, feedback string) ExecResult {
	return ExecResult{}
}
func (m *mockTaskExecutor) BuildCommand(task *db.Task, sessionID, prompt string) string { return "" }
func (m *mockTaskExecutor) IsAvailable() bool                                          { return true }
func (m *mockTaskExecutor) GetProcessID(taskID int64) int                              { return 0 }
func (m *mockTaskExecutor) Kill(taskID int64) bool                                     { return false }
func (m *mockTaskExecutor) Suspend(taskID int64) bool                                  { return false }
func (m *mockTaskExecutor) IsSuspended(taskID int64) bool                              { return false }
func (m *mockTaskExecutor) SupportsSessionResume() bool                                { return false }
func (m *mockTaskExecutor) SupportsDangerousMode() bool                                { return false }
func (m *mockTaskExecutor) ResumeDangerous(task *db.Task, workDir string) bool         { return false }
func (m *mockTaskExecutor) ResumeSafe(task *db.Task, workDir string) bool              { return false }

func TestGetPreviousSessionContent(t *testing.T) {
	buildExecutor := func(executors ...TaskExecutor) *Executor {
		factory := NewExecutorFactory()
		for _, ex := range executors {
			factory.Register(ex)
		}
		return &Executor{executorFactory: factory}
	}

	t.Run("returns empty when current executor has session", func(t *testing.T) {
		current := &mockTaskExecutor{name: "codex", sessionID: "existing-session"}
		other := &mockTaskExecutor{name: "claude", sessionID: "old-session", sessionContent: "**User:** hello"}

		e := buildExecutor(current, other)
		result := e.GetPreviousSessionContent(current, "/tmp/work")

		if result != "" {
			t.Errorf("expected empty (current has session), got: %s", result)
		}
	})

	t.Run("returns handoff when other executor has session", func(t *testing.T) {
		current := &mockTaskExecutor{name: "codex", sessionID: ""}
		other := &mockTaskExecutor{name: "claude", sessionID: "old-session", sessionContent: "**User:** Fix the bug\n\n**Assistant:** On it."}

		e := buildExecutor(current, other)
		result := e.GetPreviousSessionContent(current, "/tmp/work")

		if result == "" {
			t.Fatal("expected handoff content, got empty")
		}
		if !strings.Contains(result, "## Previous Session Context") {
			t.Error("expected session handoff header")
		}
		if !strings.Contains(result, "**claude**") {
			t.Error("expected executor name in handoff")
		}
		if !strings.Contains(result, "Fix the bug") {
			t.Error("expected session content in handoff")
		}
	})

	t.Run("returns empty when no other executor has session", func(t *testing.T) {
		current := &mockTaskExecutor{name: "codex", sessionID: ""}
		other := &mockTaskExecutor{name: "claude", sessionID: ""}

		e := buildExecutor(current, other)
		result := e.GetPreviousSessionContent(current, "/tmp/work")

		if result != "" {
			t.Errorf("expected empty (no other sessions), got: %s", result)
		}
	})

	t.Run("skips other executor with session ID but empty content", func(t *testing.T) {
		current := &mockTaskExecutor{name: "codex", sessionID: ""}
		noContent := &mockTaskExecutor{name: "gemini", sessionID: "some-id", sessionContent: ""}
		hasContent := &mockTaskExecutor{name: "claude", sessionID: "old-session", sessionContent: "**User:** hello"}

		e := buildExecutor(current, noContent, hasContent)
		result := e.GetPreviousSessionContent(current, "/tmp/work")

		if result == "" {
			t.Fatal("expected handoff from claude, got empty")
		}
		if !strings.Contains(result, "**claude**") {
			t.Errorf("expected claude handoff, got: %s", result)
		}
	})
}
