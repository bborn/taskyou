package executor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// APIError is a provider-side failure recorded in a Claude session transcript:
// a rate limit, an auth rejection, an overloaded upstream.
type APIError struct {
	Status    int    // HTTP status, when the transcript recorded one (e.g. 429)
	Transient bool   // the provider's own hint that a retry may succeed
	Message   string // the error text shown to the agent
}

// String renders the error for a task's activity log.
func (e *APIError) String() string {
	if e == nil {
		return ""
	}
	if e.Status > 0 && !strings.Contains(e.Message, fmt.Sprint(e.Status)) {
		return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
	}
	return e.Message
}

// maxAPIErrorMessage bounds what we copy into the activity log. Provider errors
// carry upgrade links and request refs that are useful, but a log line is not
// the place for an essay.
const maxAPIErrorMessage = 400

// LastAPIError returns the provider error a session ENDED on, or nil.
//
// Why this exists: when the model provider rejects a request, the agent's turn
// stops and ty parks the task with "Waiting for user input" — a message that
// reads as a question the agent never asked. The real cause is recorded only in
// the session transcript, so a step killed by a rate limit looks identical on
// the board to one genuinely waiting on a human. That cost a real debugging
// session: a workflow step ran three times, committed nothing each time, and
// looked like a bad prompt; the transcript said
//
//	API Error: Request rejected (429) · you have reached your session usage limit
//
// An error is only reported if the session ended on it. Claude Code retries
// transient failures, so an error followed by a successful assistant response is
// noise — reporting it would train the reader to ignore these lines.
func LastAPIError(transcriptPath string) *APIError {
	if strings.TrimSpace(transcriptPath) == "" {
		return nil
	}
	f, err := os.Open(transcriptPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var last *APIError
	scanner := bufio.NewScanner(f)
	// Transcript lines carry whole assistant turns and can be large; the default
	// 64KB limit would silently truncate and mis-parse them.
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var entry struct {
			Type      string `json:"type"`
			IsAPIErr  bool   `json:"isApiErrorMessage"`
			Status    int    `json:"apiErrorStatus"`
			Transient bool   `json:"apiErrorIsTransient"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry.Type != "assistant" {
			continue
		}
		if !entry.IsAPIErr {
			// The model answered after the failure, so the session recovered.
			last = nil
			continue
		}
		text := transcriptText(entry.Message.Content)
		if text == "" {
			continue
		}
		last = &APIError{Status: entry.Status, Transient: entry.Transient, Message: text}
	}
	return last
}

// transcriptText flattens a transcript entry's content to its text, which is
// either a plain string or the usual array of typed blocks.
func transcriptText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return truncateMessage(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, strings.TrimSpace(b.Text))
		}
	}
	return truncateMessage(strings.Join(parts, " "))
}

func truncateMessage(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > maxAPIErrorMessage {
		s = strings.TrimSpace(s[:maxAPIErrorMessage]) + "…"
	}
	return s
}
