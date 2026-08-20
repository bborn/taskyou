package tasksummary

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bborn/workflow/internal/db"
)

const rewriteTimeout = 20 * time.Second

// KickoffRewrite starts a background Force rewrite of the stand line.
func KickoffRewrite(database *db.DB, taskID int64) {
	if database == nil || taskID <= 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rewriteTimeout)
		defer cancel()
		_, _ = GenerateAndStoreForce(ctx, database, taskID)
	}()
}

// KickoffGenerate starts a background skip-if-exists generation (freeze-on-done).
func KickoffGenerate(database *db.DB, taskID int64) {
	if database == nil || taskID <= 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rewriteTimeout)
		defer cancel()
		_, _ = GenerateAndStore(ctx, database, taskID)
	}()
}

// KickoffOnStatusChange rewrites the stand when a task first enters blocked.
func KickoffOnStatusChange(database *db.DB, oldStatus, newStatus string, taskID int64) {
	if ShouldRewriteOnStatus(oldStatus, newStatus) {
		KickoffRewrite(database, taskID)
	}
}

// StandMaxWords is the sticky-note length: enough to queue the brain, short
// enough to fit a kanban card without ellipsis.
const StandMaxWords = 5

// StandMaxChars is a hard cap after the word clamp (very long tokens).
const StandMaxChars = 40

// IsStandLine reports whether summary is a one-line stand (queue-the-brain),
// not an old 2–4 bullet recap. Fossils must not render as the stand.
func IsStandLine(summary string) bool {
	s := strings.TrimSpace(summary)
	if s == "" {
		return false
	}
	if strings.ContainsAny(s, "\n\r") {
		return false
	}
	if isBulletLine(s) {
		return false
	}
	return true
}

// DisplayStand returns the stand to show, or empty when summary is a fossil
// recap / empty. Long valid stands are clamped to StandMaxWords.
func DisplayStand(summary string) string {
	if !IsStandLine(summary) {
		return ""
	}
	return clampStand(strings.TrimSpace(summary))
}

// FallbackStand is the card/header fallback when there is no stand: the
// agent's question. Reconnect / continuation noise is never shown.
func FallbackStand(log *db.TaskLog) string {
	if log == nil {
		return ""
	}
	if IsNoiseLog(log.Content) {
		return ""
	}
	line := firstLine(log.Content)
	if line == "" {
		return ""
	}
	if log.LineType == "question" || strings.HasSuffix(line, "?") {
		return clampStand(line)
	}
	return ""
}

// IsNoiseLog reports log lines that must never appear as a stand fallback
// (session reconnects, continuation markers, empty).
func IsNoiseLog(content string) bool {
	s := strings.TrimSpace(content)
	if s == "" {
		return true
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, "reconnecting to") {
		return true
	}
	if strings.HasPrefix(s, "---") {
		return true
	}
	return false
}

// NeedsRefresh reports whether a stand should be rewritten for this task.
// Frozen on done/archived. Not while running (live crumb is enough).
// On blocked: rewrite fossils, or when new logs arrived after last distill.
func NeedsRefresh(task *db.Task, latest *db.TaskLog) bool {
	if task == nil {
		return false
	}
	switch task.Status {
	case db.StatusDone, db.StatusArchived, db.StatusProcessing, db.StatusQueued:
		return false
	}
	if task.Status != db.StatusBlocked {
		return false
	}
	if !IsStandLine(task.Summary) || oversizedStand(task.Summary) {
		return true
	}
	if latest == nil || task.LastDistilledAt == nil {
		return false
	}
	return latest.CreatedAt.Time.After(task.LastDistilledAt.Time)
}

// ShouldRewriteOnStatus is true on the transition into blocked. That is the
// attention-change rewrite: the stand is the question now in front of you.
func ShouldRewriteOnStatus(oldStatus, newStatus string) bool {
	return newStatus == db.StatusBlocked && oldStatus != db.StatusBlocked
}

// NormalizeStand flattens model output into a single stored stand line.
func NormalizeStand(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"'`)
	s = strings.TrimSpace(s)
	if idx := strings.IndexAny(s, "\n\r"); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	s = strings.Trim(s, `"'`)
	s = strings.TrimSpace(s)
	if isBulletLine(s) {
		s = stripBulletPrefix(s)
	}
	return clampStand(s)
}

func oversizedStand(s string) bool {
	return len(strings.Fields(strings.TrimSpace(s))) > StandMaxWords
}

func clampStand(s string) string {
	fields := strings.Fields(s)
	if len(fields) > StandMaxWords {
		s = strings.Join(fields[:StandMaxWords], " ")
	}
	return truncateRunes(s, StandMaxChars)
}

func isBulletLine(s string) bool {
	s = strings.TrimSpace(s)
	for _, p := range []string{"- ", "* ", "• ", "– ", "— "} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	if len(s) >= 3 && s[0] >= '1' && s[0] <= '9' && s[1] == '.' && s[2] == ' ' {
		return true
	}
	return false
}

func stripBulletPrefix(s string) string {
	s = strings.TrimSpace(s)
	for _, p := range []string{"- ", "* ", "• ", "– ", "— "} {
		if strings.HasPrefix(s, p) {
			return strings.TrimSpace(s[len(p):])
		}
	}
	if len(s) >= 3 && s[0] >= '1' && s[0] <= '9' && s[1] == '.' && s[2] == ' ' {
		return strings.TrimSpace(s[3:])
	}
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexAny(s, "\n\r"); idx >= 0 {
		s = s[:idx]
	}
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.TrimSpace(s)
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	if max == 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}
