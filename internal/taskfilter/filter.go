// Package taskfilter parses the structured token grammar shared by every
// surface that filters tasks: the TUI filter bar, saved views, `ty list --view`,
// and the HTTP API.
//
// The grammar is deliberately small and greppable:
//
//	status:blocked      is:blocked        only blocked tasks
//	status:in-progress  is:in-progress    queued + processing
//	status:open         is:open           anything not done/archived
//	is:pinned           is:unpinned       pin state
//	is:workflow         is:task           workflow steps vs standalone tasks
//	has:pr              no:pr             tasks with/without a pull request
//	tag:release                           tasks carrying a comma-separated tag
//	[project]                             project tag (OR'd across tags)
//
// Repeating a status token ORs the statuses together, so
// "status:in-progress status:blocked" is "the things I'm working on right now"
// — the view the list mode was built for.
//
// Everything the grammar does not recognise is left alone and handed back as
// the free-text remainder, so callers keep whatever keyword matching they
// already do (the TUI fuzzy-scores it; the CLI does a substring match).
package taskfilter

import (
	"strconv"
	"strings"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/pipeline"
)

// Kind values for the is:workflow / is:task token.
const (
	KindWorkflow = "workflow"
	KindTask     = "task"
)

// Query holds the structured predicates parsed out of a filter string. The
// zero value matches every task, so callers can apply it unconditionally.
type Query struct {
	// Statuses is the set of task statuses to keep. Empty means "any status".
	Statuses []string
	// Kind is KindWorkflow, KindTask, or "" for both.
	Kind string
	// Pinned, when set, requires the task's pin state to match.
	Pinned *bool
	// HasPR, when set, requires the task to have (or not have) a pull request.
	HasPR *bool
	// Tags are tag names the task must carry — all of them, so repeating
	// tag: narrows rather than widens (unlike status:, which ORs).
	Tags []string
	// Projects are the [project] tags to keep, OR'd together. Names are
	// lowercased; resolve aliases before matching (see ResolveProjects).
	Projects []string
	// PartialProject is a trailing unclosed "[proj" the user is still typing.
	// Only the TUI's autocomplete cares; Match ignores it.
	PartialProject string
	// Keyword is the free text left after every recognised token was removed.
	Keyword string
}

// statusAliases maps a token to the statuses it selects. One token can select
// several statuses ("in-progress" is queued + processing), which is why the
// value is a slice.
var statusAliases = map[string][]string{
	"backlog":     {db.StatusBacklog},
	"queued":      {db.StatusQueued},
	"processing":  {db.StatusProcessing},
	"running":     {db.StatusProcessing},
	"in-progress": {db.StatusQueued, db.StatusProcessing},
	"inprogress":  {db.StatusQueued, db.StatusProcessing},
	"progress":    {db.StatusQueued, db.StatusProcessing},
	"active":      {db.StatusQueued, db.StatusProcessing},
	"blocked":     {db.StatusBlocked},
	"done":        {db.StatusDone},
	"complete":    {db.StatusDone},
	"completed":   {db.StatusDone},
	"archived":    {db.StatusArchived},
	"open": {
		db.StatusBacklog, db.StatusQueued, db.StatusProcessing, db.StatusBlocked,
	},
}

// StatusTokens returns the accepted status:/is: status names in a stable order,
// for help text and shell completion.
func StatusTokens() []string {
	return []string{
		"backlog", "queued", "processing", "in-progress", "blocked", "done",
		"archived", "open",
	}
}

// Parse extracts every structured token from raw and returns the resulting
// Query. Unrecognised text — including [project] tags, which are parsed into
// Projects — ends up in Keyword.
//
// Tokens are matched case-insensitively; the keyword remainder keeps its
// original case so callers can decide how to fold it.
func Parse(raw string) Query {
	var q Query

	// Pull [project] tags out first: they can contain spaces, so they must not
	// be split into fields.
	rest, projects, partial := splitProjects(raw)
	q.Projects = projects
	q.PartialProject = partial

	kept := make([]string, 0, 8)
	for _, field := range strings.Fields(rest) {
		if q.consumeToken(field) {
			continue
		}
		kept = append(kept, field)
	}
	q.Keyword = strings.TrimSpace(strings.Join(kept, " "))
	return q
}

// consumeToken applies one whitespace-delimited field to the query and reports
// whether it was a recognised token (and so must not reach the keyword).
func (q *Query) consumeToken(field string) bool {
	prefix, value, ok := strings.Cut(strings.ToLower(field), ":")
	if !ok || value == "" {
		return false
	}

	switch prefix {
	case "status":
		return q.addStatus(value)

	case "is":
		switch value {
		case "pinned":
			q.Pinned = boolPtr(true)
			return true
		case "unpinned", "unpin":
			q.Pinned = boolPtr(false)
			return true
		case "workflow", "wf", "pipeline":
			q.Kind = KindWorkflow
			return true
		case "task", "normal":
			q.Kind = KindTask
			return true
		}
		// `is:blocked` is a friendlier spelling of `status:blocked`.
		return q.addStatus(value)

	case "has":
		if value == "pr" {
			q.HasPR = boolPtr(true)
			return true
		}

	case "no":
		if value == "pr" {
			q.HasPR = boolPtr(false)
			return true
		}

	case "tag":
		q.Tags = append(q.Tags, value)
		return true
	}

	return false
}

// addStatus records the statuses a status token selects, reporting false for an
// unknown name so it falls through to the keyword rather than silently matching
// nothing.
func (q *Query) addStatus(value string) bool {
	statuses, ok := statusAliases[value]
	if !ok {
		return false
	}
	for _, s := range statuses {
		if !contains(q.Statuses, s) {
			q.Statuses = append(q.Statuses, s)
		}
	}
	return true
}

// splitProjects removes [project] tags from the query, returning the remaining
// text, the completed tag names (lowercased), and any trailing unclosed tag the
// user is still typing.
func splitProjects(query string) (rest string, projects []string, partial string) {
	remaining := query
	for {
		start := strings.Index(remaining, "[")
		if start == -1 {
			break
		}
		end := strings.Index(remaining[start:], "]")
		if end == -1 {
			// Unclosed bracket — a project name still being typed.
			partial = strings.ToLower(remaining[start+1:])
			remaining = remaining[:start]
			break
		}
		name := strings.TrimSpace(remaining[start+1 : start+end])
		if name != "" {
			projects = append(projects, strings.ToLower(name))
		}
		remaining = remaining[:start] + remaining[start+end+1:]
	}
	return remaining, projects, partial
}

// Rest returns the query with every structured token removed: the [project]
// tags followed by the free-text keyword, followed by any half-typed tag.
//
// It exists so a caller with its own matcher for the unstructured part — the
// TUI, which fuzzy-ranks rather than substring-matches — can hand that matcher
// exactly the text it understands, without having to know which tokens this
// package consumed. The partial tag is emitted last because an unclosed "["
// swallows the remainder of the string by definition.
func (q Query) Rest() string {
	parts := make([]string, 0, len(q.Projects)+2)
	for _, p := range q.Projects {
		parts = append(parts, "["+p+"]")
	}
	if q.Keyword != "" {
		parts = append(parts, q.Keyword)
	}
	if q.PartialProject != "" {
		parts = append(parts, "["+q.PartialProject)
	}
	return strings.Join(parts, " ")
}

// IsZero reports whether the query constrains nothing at all — no structured
// predicate, no project, no keyword. Callers use it to skip filtering entirely.
func (q Query) IsZero() bool {
	return len(q.Statuses) == 0 && q.Kind == "" && q.Pinned == nil &&
		q.HasPR == nil && len(q.Tags) == 0 && len(q.Projects) == 0 &&
		q.PartialProject == "" && q.Keyword == ""
}

// HasStructured reports whether any non-keyword predicate is set. The TUI uses
// this to decide whether the structured pass can change the result at all.
func (q Query) HasStructured() bool {
	return len(q.Statuses) > 0 || q.Kind != "" || q.Pinned != nil ||
		q.HasPR != nil || len(q.Tags) > 0
}

// Match reports whether a task satisfies the structured predicates. It
// deliberately ignores Projects and Keyword: those are matched (and, in the
// TUI, fuzzy-ranked) by the caller, which owns alias resolution and scoring.
func (q Query) Match(t *db.Task) bool {
	if t == nil {
		return false
	}
	if len(q.Statuses) > 0 && !contains(q.Statuses, t.Status) {
		return false
	}
	if q.Kind != "" && pipeline.IsWorkflowTask(t) != (q.Kind == KindWorkflow) {
		return false
	}
	if q.Pinned != nil && t.Pinned != *q.Pinned {
		return false
	}
	if q.HasPR != nil && hasPR(t) != *q.HasPR {
		return false
	}
	for _, tag := range q.Tags {
		if !hasTag(t.Tags, tag) {
			return false
		}
	}
	return true
}

// MatchProject reports whether a task's project satisfies the [project] tags.
// Names are compared case-insensitively; pass already-resolved project names
// (see ResolveProjects) so aliases like "ol" work.
func (q Query) MatchProject(t *db.Task) bool {
	if len(q.Projects) == 0 || t == nil {
		return true
	}
	for _, p := range q.Projects {
		if strings.EqualFold(t.Project, p) {
			return true
		}
	}
	return false
}

// MatchKeyword reports whether a task matches the free-text remainder by plain
// substring, across the fields a person would search: id, title, body, summary
// and project. The TUI replaces this with fuzzy ranking; the CLI and API use it
// as-is so a saved view means the same thing everywhere.
func (q Query) MatchKeyword(t *db.Task) bool {
	if q.Keyword == "" || t == nil {
		return true
	}
	needle := strings.ToLower(strings.TrimPrefix(q.Keyword, "#"))
	for _, hay := range []string{
		strconv.FormatInt(t.ID, 10), t.Title, t.Body, t.Summary, t.Project, t.Tags,
	} {
		if strings.Contains(strings.ToLower(hay), needle) {
			return true
		}
	}
	return false
}

// MatchAll applies every predicate — structured, project, and keyword. This is
// the whole meaning of a saved view outside the TUI.
func (q Query) MatchAll(t *db.Task) bool {
	return q.Match(t) && q.MatchProject(t) && q.MatchKeyword(t)
}

// Filter returns the tasks matching the whole query, preserving input order.
func (q Query) Filter(tasks []*db.Task) []*db.Task {
	out := make([]*db.Task, 0, len(tasks))
	for _, t := range tasks {
		if q.MatchAll(t) {
			out = append(out, t)
		}
	}
	return out
}

// ResolveProjects rewrites project tags through resolve, which maps a name or
// alias to the canonical project name. A resolver that returns "" leaves the
// name untouched, so an unknown project still filters (to nothing) rather than
// silently widening the view to everything.
func (q Query) ResolveProjects(resolve func(string) string) Query {
	if resolve == nil || len(q.Projects) == 0 {
		return q
	}
	resolved := make([]string, len(q.Projects))
	for i, name := range q.Projects {
		if canonical := resolve(name); canonical != "" {
			resolved[i] = strings.ToLower(canonical)
			continue
		}
		resolved[i] = name
	}
	q.Projects = resolved
	return q
}

func hasPR(t *db.Task) bool {
	return t.PRURL != "" || t.PRNumber > 0
}

func hasTag(tags, want string) bool {
	for _, tag := range strings.Split(tags, ",") {
		if strings.EqualFold(strings.TrimSpace(tag), want) {
			return true
		}
	}
	return false
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func boolPtr(b bool) *bool { return &b }
