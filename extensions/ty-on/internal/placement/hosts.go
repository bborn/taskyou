package placement

import (
	"fmt"
	"strings"
)

// HostsEvent is the second question ty asks this resolver: not "where does this
// task go" but "where COULD it go".
//
// Resolve answers with one host, chosen by policy. That is the right default and
// the wrong thing when a person already knows which machine they want — so ty's
// new-task form asks for the candidates and records the chosen one as the task's
// placement, which stops the resolver from being asked at all.
//
// The rules are the eligibility half of Resolve, without the ranking: a host is
// offered when it has a checkout of the project and its executor capabilities
// allow this task. No host is probed — the list is answered from the inventory
// file alone, because a form is open and waiting, and an unreachable host is
// something ty reports when it tries to launch there.
const HostsEvent = "task.hosts"

// Choice is one machine ty may offer the user. It is a projection of an
// inventory Host: only what a person needs to pick one, plus the destination ty
// records if they do.
type Choice struct {
	// Name is the inventory key: what the user sees and what `on` accepts.
	Name string `json:"name"`
	// Target is the SSH destination ty records as the placement.
	Target string `json:"target"`
	// Workdir is the project's checkout on that host.
	Workdir string `json:"workdir"`
	// Detail is a one-liner shown beside the name.
	Detail string `json:"detail,omitempty"`
}

// HostsResponse is the JSON document written to stdout for a hosts request.
// An empty list is a normal answer: no inventory, or no host serving this
// project. ty then offers no choice, which is what a machine with no fleet has
// always looked like.
type HostsResponse struct {
	Hosts []Choice `json:"hosts"`
}

// Hosts lists the hosts eligible to run this task.
func (r Resolver) Hosts(req Request) HostsResponse {
	out := HostsResponse{Hosts: []Choice{}}

	executor := req.Task.Executor
	if executor == "" {
		executor = "claude"
	}
	// Remote adapters exist for Claude and Codex only; offering a host for any
	// other executor would be offering a choice ty cannot honour.
	if executor != "claude" && executor != "codex" {
		return out
	}
	if req.Task.Project == "" {
		return out
	}

	path := r.InventoryPath
	if path == "" {
		path = InventoryPath()
	}
	inv, err := LoadInventory(path)
	if err != nil {
		return out
	}

	for _, c := range eligible(inv, req.Task.Project, executor) {
		out.Hosts = append(out.Hosts, Choice{
			Name:    c.Name,
			Target:  c.Destination(),
			Workdir: c.Checkout,
			Detail:  hostDetail(c, req.Task.Project),
		})
	}
	return out
}

// hostDetail describes a candidate in one line: what it is provisioned for, or
// failing that, the fact that it serves the project at all.
func hostDetail(c Candidate, project string) string {
	if len(c.Host.Capabilities) > 0 {
		return strings.Join(c.Host.Capabilities, ", ")
	}
	return fmt.Sprintf("serves %s", project)
}
