package ui

import (
	"os"
	"strings"
	"sync"
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
//
// The `@host` grammar itself (parsing and matching) lives in
// internal/taskfilter, so the filter bar, saved views, `ty list --filter` and
// the HTTP API all agree on what `@mona` means. Only the lookup of this
// machine's own name is UI-side, because it reads the OS.
const filterHostLocalName = "local"

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
