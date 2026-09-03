package executor

import "strings"

// The remote poll infers a placed agent's fate from its screen, because a
// remotely placed agent's MCP server talks to its own host's database and so
// cannot signal this one (see remote_poll.go). That inference has one failure
// mode worth naming: every stop looks the same. A finished agent, an agent
// sitting at a dialog nobody will ever answer, and an agent whose turn died on
// a provider error all stop repainting, and all three used to park two minutes
// later as "Task needs review" — a message that says only that ty gave up.
//
// The screen said exactly what was wrong in each case. These detectors read it,
// the same way DetectAuthPrompt already reads it for a logged-out session, so
// the poll can say which of the three happened and act differently for each.
//
// The needles are multi-word and specific for the reason auth_check.go gives:
// ordinary task output — a diff, a test name, this file — must not trip them.

// blockingPromptPatterns are things an executor paints when it has stopped to
// ask the operator a question and will not proceed until a key is pressed.
//
// The first entry does most of the work: Claude Code renders that footer under
// every modal choice it offers, whatever the question is, so a dialog this list
// has never seen still gets caught. The named entries below it exist to give a
// better reason than "a dialog is open" for the ones we have actually hit.
var blockingPromptPatterns = []authPattern{
	{"enter to confirm · esc to cancel", "Executor is waiting on a dialog — answer it, or re-run with the prompt disabled"},
	{"teach auto mode about your environment", "Claude is asking to learn the environment (auto-mode onboarding) — answer it to let the task start"},
	{"do you trust the files in this folder", "Claude is asking whether to trust this folder — answer it to let the task start"},
}

// busyPatterns are things an executor paints only while a turn is genuinely in
// flight. They matter because the idle tracker fingerprints the pane, and a
// screen can be both perfectly still and perfectly busy: a provider retry loop
// repaints the same frame between attempts, and a long tool call may not
// repaint at all. Parking either one throws away live work.
var busyPatterns = []string{
	"esc to interrupt", // Claude renders this in the footer only during an active turn
	"retrying in",      // "API error · Retrying in 8s · attempt 2/10"
	"esc to cancel generation",
}

// stallNoticePatterns explain why a turn ended without the agent finishing.
// These do not decide anything on their own — the agent really has stopped, and
// parking it is right — but they turn the park message from "Task needs review"
// into the reason, which is the difference between a board and a mystery.
var stallNoticePatterns = []authPattern{
	{"api error: 529", "the model API was overloaded (529) and the turn did not complete"},
	{"529 overloaded", "the model API was overloaded (529) and the turn did not complete"},
	{"api error: 500", "the model API returned a server error (500) and the turn did not complete"},
	{"api error: 503", "the model API was unavailable (503) and the turn did not complete"},
	{"context low", "the executor ran low on context"},
	{"usage limit reached", "the account's usage limit was reached"},
}

// DetectBlockingPrompt reports whether the executor is parked on a question
// nobody is there to answer, and what it is asking.
//
// A blocking prompt is not a finished agent and must not wait out the idle
// window to be noticed: the agent has done no work and will do none, so every
// tick spent confirming that is a tick wasted.
func DetectBlockingPrompt(content string) (string, bool) {
	return matchPromptLine(content, blockingPromptPatterns)
}

// DetectStallNotice reports a visible explanation for why the agent stopped.
func DetectStallNotice(content string) (string, bool) {
	return matchPattern(content, stallNoticePatterns)
}

// DetectBusy reports whether the screen shows a turn still in flight, and so
// must not be counted toward the idle threshold however still it looks.
//
// A blocking prompt wins over a busy marker. Claude leaves footer text on
// screen while a modal is open, so a dialog raised mid-turn can show both at
// once, and of the two only the dialog is true: nothing is progressing behind
// it. Answering it is what unsticks the task, so that is what we say.
func DetectBusy(content string) bool {
	if content == "" {
		return false
	}
	if _, blocked := DetectBlockingPrompt(content); blocked {
		return false
	}
	lower := strings.ToLower(content)
	for _, needle := range busyPatterns {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// matchPromptLine matches a pattern only where a terminal would actually draw a
// prompt: as its own line.
//
// Plain substring matching is wrong here, and its own test caught it — a task
// editing this file painted the pattern list into its pane and the detector
// parked it for "waiting on a dialog". Any task whose screen quotes a prompt
// (a diff, a test fixture, a log) would do the same, and being wrong parks live
// work. A rendered dialog puts its question and its footer on lines of their
// own, so requiring the line to BEGIN with the needle — after stripping the box
// drawing and selection markers a TUI puts in front of it — keeps the real
// thing and drops the quotation, which always has code before it.
func matchPromptLine(content string, patterns []authPattern) (string, bool) {
	if content == "" {
		return "", false
	}
	for _, raw := range strings.Split(content, "\n") {
		line := strings.ToLower(strings.TrimLeft(strings.TrimSpace(raw), "│|>❯●✻*-+#[( \t"))
		for _, p := range patterns {
			if strings.HasPrefix(line, p.needle) {
				return p.reason, true
			}
		}
	}
	return "", false
}

// matchPattern returns the reason for the first pattern present in content.
func matchPattern(content string, patterns []authPattern) (string, bool) {
	if content == "" {
		return "", false
	}
	lower := strings.ToLower(content)
	for _, p := range patterns {
		if strings.Contains(lower, p.needle) {
			return p.reason, true
		}
	}
	return "", false
}
