package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/executor"
)

// ProjectMetadata is the structured result of inferring a project's identity.
type ProjectMetadata struct {
	Name        string `json:"name"`
	Alias       string `json:"alias"`
	Description string `json:"description"`
}

// inferenceTimeout caps how long we wait on `claude -p`.
const inferenceTimeout = 12 * time.Second

// InferProjectMetadata shells out to `claude -p` (print mode) to infer a clean
// project name, short alias, and one-line description from the folder. It reuses
// the user's Claude CLI auth via CLAUDE_CONFIG_DIR (no API key), mirroring
// executor.RenameClaudeSession. Returns an error when claude is unavailable,
// times out, or returns unparseable output; callers MUST degrade gracefully.
func InferProjectMetadata(dir, configDir string) (ProjectMetadata, error) {
	ctx, cancel := context.WithTimeout(context.Background(), inferenceTimeout)
	defer cancel()

	prompt := buildInferencePrompt(dir)
	cmd := exec.CommandContext(ctx, "claude", "-p", prompt)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), fmt.Sprintf("CLAUDE_CONFIG_DIR=%s", executor.ResolveClaudeConfigDir(configDir)))
	out, err := cmd.Output()
	if err != nil {
		return ProjectMetadata{}, fmt.Errorf("claude -p inference failed: %w", err)
	}
	return parseInferenceJSON(string(out))
}

func buildInferencePrompt(dir string) string {
	var sb strings.Builder
	sb.WriteString("You are naming a software project for a task manager. ")
	sb.WriteString("Given a folder, respond with ONLY a JSON object: ")
	sb.WriteString(`{"name": "...", "alias": "...", "description": "..."}`)
	sb.WriteString(".\n- name: a clean human project name (kebab or title case), not a file path.\n")
	sb.WriteString("- alias: a short lowercase handle (3-12 chars), no spaces.\n")
	sb.WriteString("- description: one sentence (<= 12 words) describing what the project is.\n")
	sb.WriteString("Output JSON only, no prose, no code fences.\n\n")
	sb.WriteString("Folder name: " + filepath.Base(filepath.Clean(dir)) + "\n\n")
	sb.WriteString("Files:\n" + shallowFileListing(dir) + "\n")
	if snippet := readmeSnippet(dir); snippet != "" {
		sb.WriteString("\nREADME/AGENTS excerpt:\n" + snippet + "\n")
	}
	return sb.String()
}

// shallowFileListing returns up to 40 top-level entry names, dirs marked with /.
func shallowFileListing(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "(unreadable)"
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() {
			names = append(names, e.Name()+"/")
		} else {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) > 40 {
		names = names[:40]
	}
	return strings.Join(names, "\n")
}

// readmeSnippet returns the first ~1200 chars of the best available doc file.
func readmeSnippet(dir string) string {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "README.md"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		s := strings.TrimSpace(string(data))
		if s == "" {
			continue
		}
		if len(s) > 1200 {
			s = s[:1200]
		}
		return s
	}
	return ""
}

// parseInferenceJSON extracts a ProjectMetadata from claude's output, tolerating
// surrounding prose or fences by scanning for the first {...} block.
func parseInferenceJSON(raw string) (ProjectMetadata, error) {
	s := strings.TrimSpace(raw)
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < 0 || end <= start {
		return ProjectMetadata{}, fmt.Errorf("no JSON object found in inference output")
	}
	var meta ProjectMetadata
	if err := json.Unmarshal([]byte(s[start:end+1]), &meta); err != nil {
		return ProjectMetadata{}, fmt.Errorf("parse inference JSON: %w", err)
	}
	if strings.TrimSpace(meta.Name) == "" {
		return ProjectMetadata{}, fmt.Errorf("inference returned empty name")
	}
	return meta, nil
}
