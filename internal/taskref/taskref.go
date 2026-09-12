// Package taskref resolves the ways people name a task: "5187", "#5187", a
// task branch such as "task/5187-fix-login", or a GitHub PR URL. These are the
// references the TUI's go-to-task palette recognizes exactly. Anything looser,
// like words from a title, is a search and belongs to the palette.
package taskref

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/bborn/workflow/internal/db"
)

var (
	// Matches branch names like "task/1068-description" or "task/1068".
	branchTaskIDPattern = regexp.MustCompile(`(?:^|/)(\d+)(?:-|$)`)
	// Matches GitHub PR URLs like "https://github.com/org/repo/pull/123".
	prURLPattern = regexp.MustCompile(`github\.com/([^/]+/[^/]+)/pull/(\d+)`)
)

// TaskIDFromBranch returns the task ID in a branch name such as
// "task/1068-description", or 0.
func TaskIDFromBranch(s string) int64 {
	if m := branchTaskIDPattern.FindStringSubmatch(s); m != nil {
		if id, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			return id
		}
	}
	return 0
}

// PRNumberFromURL returns the PR number in a GitHub PR URL, or 0.
func PRNumberFromURL(s string) int {
	_, n := parsePRURL(s)
	return n
}

// parsePRURL returns the lowercased "org/repo" and number of a GitHub PR URL.
func parsePRURL(s string) (repo string, number int) {
	m := prURLPattern.FindStringSubmatch(s)
	if m == nil {
		return "", 0
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0
	}
	return strings.ToLower(m[1]), n
}

// Store is what Resolve reads; *db.DB satisfies it.
type Store interface {
	GetTask(id int64) (*db.Task, error)
	SearchTasks(query string, limit int) ([]*db.Task, error)
}

// Resolve returns the one task ref names, or nil when it names none or more
// than one: an ID with no task, a PR several tasks share, or search text.
func Resolve(store Store, ref string) (*db.Task, error) {
	ref = strings.TrimSpace(ref)
	if repo, n := parsePRURL(ref); n > 0 {
		return byPR(store, repo, n)
	}
	if id, err := strconv.ParseInt(strings.TrimPrefix(ref, "#"), 10, 64); err == nil {
		return store.GetTask(id)
	}
	// A branch is one word; "fix 12-hour clock" is a search.
	if !strings.ContainsAny(ref, " \t") {
		if id := TaskIDFromBranch(ref); id > 0 {
			return store.GetTask(id)
		}
	}
	return nil, nil
}

// SearchText is what to hand the palette for ref: a PR URL cut down to
// github.com/org/repo/pull/N, so a pasted ".../pull/N/files" still finds the
// task, and anything else as typed.
func SearchText(ref string) string {
	ref = strings.TrimSpace(ref)
	if repo, n := parsePRURL(ref); n > 0 {
		return canonicalPRURL(repo, n)
	}
	return ref
}

func canonicalPRURL(repo string, number int) string {
	return fmt.Sprintf("github.com/%s/pull/%d", repo, number)
}

// byPR finds the task for one repo's PR. SearchTasks matches pr_url as a
// substring, so pull/36 also finds pull/365; compare exactly here.
func byPR(store Store, repo string, number int) (*db.Task, error) {
	candidates, err := store.SearchTasks(canonicalPRURL(repo, number), 50)
	if err != nil {
		return nil, err
	}
	var found *db.Task
	for _, t := range candidates {
		if r, n := parsePRURL(t.PRURL); r == repo && n == number {
			if found != nil {
				return nil, nil // several tasks share this PR; the palette lists them
			}
			found = t
		}
	}
	return found, nil
}
