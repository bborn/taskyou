package executor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxSessionContentSize is the maximum number of characters to include from a previous session.
// This prevents extremely long sessions from blowing up the prompt.
// ~50K chars ≈ ~12.5K tokens.
const maxSessionContentSize = 50000

// sessionMessage represents an extracted message from any executor's session file.
type sessionMessage struct {
	Role string // "user" or "assistant"
	Text string
}

// ReadClaudeSessionContent reads a Claude session JSONL file and extracts
// a human-readable conversation transcript.
func ReadClaudeSessionContent(workDir, configDir string) string {
	sessionID := findClaudeSessionIDImpl(workDir, configDir)
	if sessionID == "" {
		return ""
	}

	baseDir := ResolveClaudeConfigDir(configDir)
	escapedPath := strings.ReplaceAll(workDir, "/", "-")
	escapedPath = strings.ReplaceAll(escapedPath, ".", "-")
	sessionFile := filepath.Join(baseDir, "projects", escapedPath, sessionID+".jsonl")

	return readJSONLSessionContent(sessionFile)
}

// ReadCodexSessionContent reads a Codex session JSON file and extracts conversation content.
func ReadCodexSessionContent(workDir string) string {
	sessionID := findCodexSessionID(workDir)
	if sessionID == "" {
		return ""
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	sessionFile := filepath.Join(home, ".codex", "sessions", sessionID+".json")
	return readJSONSessionContent(sessionFile)
}

// ReadGeminiSessionContent reads a Gemini session JSON file and extracts conversation content.
func ReadGeminiSessionContent(workDir string) string {
	sessionID := findGeminiSessionID(workDir)
	if sessionID == "" {
		return ""
	}

	// Find the actual file path (it's in a hash-named subdirectory)
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	geminiTmpDir := filepath.Join(home, ".gemini", "tmp")
	sessionFile := sessionID + ".json"
	var foundPath string

	filepath.Walk(geminiTmpDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.Contains(path, "/chats/") && info.Name() == sessionFile {
			foundPath = path
			return filepath.SkipAll
		}
		return nil
	})

	if foundPath == "" {
		return ""
	}
	return readJSONSessionContent(foundPath)
}

// ReadPiSessionContent reads a Pi session JSONL file and extracts conversation content.
func ReadPiSessionContent(workDir string) string {
	sessionPath := findPiSessionID(workDir)
	if sessionPath == "" {
		return ""
	}
	return readJSONLSessionContent(sessionPath)
}

// readJSONLSessionContent parses a JSONL session file (used by Claude and Pi).
// Each line is a JSON object. We extract user/assistant text messages.
func readJSONLSessionContent(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	var messages []sessionMessage
	scanner := bufio.NewScanner(file)
	// Increase buffer for large messages (1MB per line)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		// Extract role-based messages
		role, _ := entry["role"].(string)
		if role != "user" && role != "assistant" {
			continue
		}

		// Extract text from content array
		content, ok := entry["content"].([]interface{})
		if !ok {
			continue
		}

		for _, item := range content {
			itemMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if itemMap["type"] == "text" {
				text, _ := itemMap["text"].(string)
				if text != "" {
					messages = append(messages, sessionMessage{Role: role, Text: text})
				}
			}
		}
	}

	return formatMessages(messages)
}

// readJSONSessionContent parses a JSON session file (used by Codex and Gemini).
// These tools store sessions as JSON objects with varying structures.
// We try common patterns to extract conversation messages.
func readJSONSessionContent(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	// Try parsing as a JSON object
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return ""
	}

	var messages []sessionMessage

	// Try common conversation keys
	for _, key := range []string{"messages", "conversation", "history", "entries", "chat", "turns"} {
		if arr, ok := obj[key].([]interface{}); ok {
			messages = extractMessagesFromArray(arr)
			if len(messages) > 0 {
				break
			}
		}
	}

	// If no messages found from known keys, try to find any array of message-like objects
	if len(messages) == 0 {
		messages = findMessagesInObject(obj)
	}

	return formatMessages(messages)
}

// extractMessagesFromArray extracts messages from a JSON array of message objects.
func extractMessagesFromArray(arr []interface{}) []sessionMessage {
	var messages []sessionMessage
	for _, item := range arr {
		msg, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		role := extractRole(msg)
		if role != "user" && role != "assistant" {
			continue
		}

		text := extractText(msg)
		if text != "" {
			messages = append(messages, sessionMessage{Role: role, Text: text})
		}
	}
	return messages
}

// extractRole gets the role from a message object, checking common field names.
func extractRole(msg map[string]interface{}) string {
	for _, key := range []string{"role", "sender", "author", "from", "type"} {
		if v, ok := msg[key].(string); ok {
			// Normalize role names
			switch strings.ToLower(v) {
			case "user", "human":
				return "user"
			case "assistant", "ai", "bot", "model":
				return "assistant"
			}
		}
	}
	return ""
}

// extractText gets text content from a message object.
func extractText(msg map[string]interface{}) string {
	// Direct text/content string
	for _, key := range []string{"text", "content", "message", "body"} {
		if v, ok := msg[key].(string); ok && v != "" {
			return v
		}
	}

	// Content array (Claude-style)
	for _, key := range []string{"content", "parts"} {
		if arr, ok := msg[key].([]interface{}); ok {
			var texts []string
			for _, item := range arr {
				switch v := item.(type) {
				case string:
					if v != "" {
						texts = append(texts, v)
					}
				case map[string]interface{}:
					if v["type"] == "text" {
						if t, ok := v["text"].(string); ok && t != "" {
							texts = append(texts, t)
						}
					}
				}
			}
			if len(texts) > 0 {
				return strings.Join(texts, "\n")
			}
		}
	}

	return ""
}

// findMessagesInObject recursively looks for arrays of message-like objects in a JSON object.
// This is a best-effort fallback for unknown session formats.
func findMessagesInObject(obj map[string]interface{}) []sessionMessage {
	for _, v := range obj {
		arr, ok := v.([]interface{})
		if !ok || len(arr) == 0 {
			continue
		}

		// Check if this looks like an array of messages (has role/content fields)
		messages := extractMessagesFromArray(arr)
		if len(messages) >= 2 { // Need at least 2 messages to be a conversation
			return messages
		}
	}
	return nil
}

// formatMessages converts extracted messages to a readable string with truncation.
func formatMessages(messages []sessionMessage) string {
	if len(messages) == 0 {
		return ""
	}

	var parts []string
	for _, msg := range messages {
		prefix := "User"
		if msg.Role == "assistant" {
			prefix = "Assistant"
		}
		parts = append(parts, fmt.Sprintf("**%s:** %s", prefix, msg.Text))
	}

	content := strings.Join(parts, "\n\n")
	return truncateSessionContent(content)
}

// truncateSessionContent truncates content to maxSessionContentSize,
// keeping the most recent messages and adding a truncation notice.
func truncateSessionContent(content string) string {
	if len(content) <= maxSessionContentSize {
		return content
	}

	content = content[len(content)-maxSessionContentSize:]
	// Find the first complete message boundary
	if idx := strings.Index(content, "\n\n**"); idx != -1 {
		content = content[idx+2:]
	}
	return "[... earlier conversation truncated ...]\n\n" + content
}

// FormatSessionHandoff wraps session content in a section header for inclusion in prompts.
func FormatSessionHandoff(executorName, content string) string {
	if content == "" {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Previous Session Context\n\n")
	sb.WriteString(fmt.Sprintf("This task was previously worked on using **%s**. Below is the conversation history from that session.\n", executorName))
	sb.WriteString("Use this context to continue the work seamlessly.\n\n")
	sb.WriteString(content)
	sb.WriteString("\n\n---\n\n")
	return sb.String()
}
