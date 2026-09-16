package ui

import (
	"os"
	"strings"
	"sync"

	"github.com/bborn/workflow/internal/db"
)

// Filtering the board by the machine a task ran on.
//
// Task cards already wear an "@host" badge once a placement hook starts sending
// work to other machines, but the badge was only readable, never actionable:
// with a fleet of four hosts there was no way to ask "what is mona doing?" or to
// pull up the tasks that ran here. `@mona` in the filter bar answers both, and
// the syntax is the badge, so nothing new has to be learned.

// filterHostLocalName is the token that means "this machine" — the word the
// autocomplete offers and `ty place` already accepts.
const filterHostLocalName = "local"

// filterHostLocalAliases are all the ways to say "not placed anywhere": the
// resolver spells local as an empty target, which is untypeable.
var filterHostLocalAliases = map[string]bool{
	filterHostLocalName: true,
	"here":              true,
	"localhost":         true,
}

// filterHostAny is the token a bare "@" parses to: every task that ran on some
// other machine. It keeps the board sensible mid-keystroke, the same way a bare
// "[" narrows to tasks that have a project.
const filterHostAny = ""

// parseFilterHosts extracts `@host` tokens from a filter query and returns them
// alongside the query minus those tokens, so the host never pollutes the fuzzy
// keyword scoring. Several tokens are an OR: `@mona @bruce` is both machines.
func parseFilterHosts(query string) (hosts []string, rest string) {
	fields := strings.Fields(query)
	kept := make([]string, 0, len(fields))
	for _, f := range fields {
		if !strings.HasPrefix(f, "@") {
			kept = append(kept, f)
			continue
		}
		hosts = append(hosts, strings.ToLower(strings.TrimPrefix(f, "@")))
	}
	return hosts, strings.TrimSpace(strings.Join(kept, " "))
}

// filterTasksByHost keeps only the tasks that ran on one of the named hosts. An
// empty host list is a no-op so callers can pass it through unconditionally.
//
// localHost is this machine's name, and is what makes `@mona` mean the same
// thing on mona as it does anywhere else: a task that ran here carries no
// placement target at all, so without it the local machine would be the one host
// in the fleet you could not name.
func filterTasksByHost(tasks []*db.Task, hosts []string, localHost string) []*db.Task {
	if len(hosts) == 0 {
		return tasks
	}
	out := make([]*db.Task, 0, len(tasks))
	for _, t := range tasks {
		for _, h := range hosts {
			if taskRanOnHost(t, h, localHost) {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

// taskRanOnHost reports whether a task satisfies one `@host` token.
func taskRanOnHost(task *db.Task, host, localHost string) bool {
	if task == nil {
		return false
	}
	target := strings.ToLower(strings.TrimSpace(task.PlacementTarget))
	switch {
	case host == filterHostAny:
		return target != ""
	case filterHostLocalAliases[host]:
		return target == ""
	case target == "":
		// Ran here, so it belongs to this machine's name.
		return localHost != "" && hostNameMatches(localHost, host)
	default:
		return hostNameMatches(target, host)
	}
}

// hostNameMatches matches a host name against a typed token by prefix, so the
// board narrows as the name is typed and "@mona" still finds "mona.local".
func hostNameMatches(host, token string) bool {
	return strings.HasPrefix(strings.ToLower(host), token)
}

var (
	localHostOnce sync.Once
	localHostName string
)

// localHostname is this machine's short name, lowercased — "mona" for
// "mona.local". Empty when the OS will not say, in which case `@mona` simply
// matches nothing locally rather than guessing.
func localHostname() string {
	localHostOnce.Do(func() {
		name, err := os.Hostname()
		if err != nil {
			return
		}
		localHostName = strings.ToLower(strings.TrimSpace(name))
		if i := strings.Index(localHostName, "."); i > 0 {
			localHostName = localHostName[:i]
		}
	})
	return localHostName
}
