package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// EventTaskHosts is the second question ty asks a placement plugin: not "where
// does this task go" but "where COULD it go".
//
// task.placement is answered once, at spawn, by policy. That is the right
// default and the wrong thing when a person already knows which machine they
// want — and until now the only way to say so was to create the task, let the
// resolver choose, then move it with `ty place`. Asking the same plugin for its
// candidates lets the new-task form offer them up front, so a deliberate choice
// costs a keystroke instead of a move.
//
// The request is the placement request with no task id (nothing has been created
// yet); the answer is a list of hosts:
//
//	{"hosts":[{"name":"ol-agents","target":"ol-agents",
//	           "workdir":"~/projects/engineering","detail":"serves taskyou"}]}
//
// Core still never learns what a host IS. It asks for names it can show, and
// hands whichever one the user picked straight back as a placement decision.
const EventTaskHosts = "task.hosts"

// DefaultHostsTimeout bounds a task.hosts handler. This is answered while a
// human waits on a form rather than in the spawn path, but the answer is only
// useful while the form is open: past this budget the picker is simply not
// offered, which is exactly the behaviour of having no plugin at all.
const DefaultHostsTimeout = 5 * time.Second

// Host is one machine a task could be placed on. Everything but Target is for
// the human reading the list.
type Host struct {
	// Name is what the user sees — the inventory's name for the machine.
	Name string `json:"name"`
	// Target is the SSH destination to record as the placement. Empty means
	// "same as Name", which is what an inventory without an ssh field implies.
	Target string `json:"target"`
	// WorkDir is the project's checkout on that host, which a placement needs
	// and a user should not have to look up.
	WorkDir string `json:"workdir"`
	// Detail is an optional one-liner shown beside the name (why this host is a
	// candidate, what it is provisioned for).
	Detail string `json:"detail"`
}

// hostsRequest is the JSON written to a handler's stdin. It reuses the placement
// task shape so a handler can share one decoder for both events.
type hostsRequest struct {
	Event string            `json:"event"`
	Task  PlacementTaskInfo `json:"task"`
}

// hostsResponse is what a handler writes to stdout.
type hostsResponse struct {
	Hosts []Host `json:"hosts"`
}

// HasHostsHandler reports whether any loaded plugin answers task.hosts. The
// forms check this before asking, so a user with no placement plugin pays
// nothing and is shown no host picker.
func (r *Runner) HasHostsHandler() bool {
	for _, p := range r.plugins {
		if _, ok := p.ScriptFor(EventTaskHosts); ok {
			return true
		}
	}
	return false
}

// ListHosts asks the installed handlers which machines could run this task.
//
// Unlike ResolvePlacement, every handler is consulted and the answers are
// merged: two fleets installed side by side are two sets of candidates, not a
// race between them. Duplicates (same target) keep the first answer, so plugin
// name order decides which description wins.
//
// A handler that fails, times out or answers unusably contributes nothing. An
// empty result is the honest answer to "nothing is offering a choice here" and
// means the surfaces show no picker at all.
func (r *Runner) ListHosts(ctx context.Context, info PlacementTaskInfo) []Host {
	if !r.HasHostsHandler() {
		return nil
	}
	req, err := json.Marshal(hostsRequest{Event: EventTaskHosts, Task: info})
	if err != nil {
		r.logger.Error("hosts: could not build request", "error", err)
		return nil
	}

	var hosts []Host
	seen := map[string]bool{}
	r.consult(ctx, EventTaskHosts, r.hostsTimeout(), req,
		func(p Plugin) []string {
			return append(os.Environ(),
				"TASK_EVENT="+EventTaskHosts,
				"TASK_PLUGIN_NAME="+p.Name,
				"TASK_PLUGIN_DIR="+p.Dir,
			)
		},
		func(a handlerAnswer) bool {
			got, err := parseHostsOutput(a.Stdout)
			if err != nil {
				r.logger.Error("hosts handler answered unusably; offering no hosts from it",
					"plugin", a.Plugin, "error", err)
				return false
			}
			for _, h := range got {
				if seen[h.Target] {
					continue
				}
				seen[h.Target] = true
				hosts = append(hosts, h)
			}
			// Never decisive: every handler gets asked, and the lists add up.
			return false
		})
	return hosts
}

// parseHostsOutput reads a hosts handler's stdout. Silence means "no
// candidates", which is a normal answer for a project no host serves.
func parseHostsOutput(stdout string) ([]Host, error) {
	out := bytes.TrimSpace([]byte(stdout))
	if len(out) == 0 {
		return nil, nil
	}
	var resp hostsResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, err
	}
	hosts := make([]Host, 0, len(resp.Hosts))
	for _, h := range resp.Hosts {
		h.Name = strings.TrimSpace(h.Name)
		h.Target = strings.TrimSpace(h.Target)
		h.WorkDir = strings.TrimSpace(h.WorkDir)
		h.Detail = strings.TrimSpace(h.Detail)
		// A host is identified by its ssh destination; the name is what a person
		// reads. Either one alone is enough to be usable, so fill the other in.
		if h.Target == "" {
			h.Target = h.Name
		}
		if h.Name == "" {
			h.Name = h.Target
		}
		if h.Target == "" {
			continue // nothing to place onto
		}
		hosts = append(hosts, h)
	}
	return hosts, nil
}

// hostsTimeout returns the handler budget, defaulting when unset (tests
// shorten it; nothing user-facing configures it).
func (r *Runner) hostsTimeout() time.Duration {
	if r.hostsTimeoutOverride > 0 {
		return r.hostsTimeoutOverride
	}
	return DefaultHostsTimeout
}
