package placement

import (
	"errors"
	"testing"
)

// errAllHostsUnreachable stands in for a probe that could not reach anything.
var errAllHostsUnreachable = errors.New("no host answered")

func hostsRequest(project, executor string) Request {
	return Request{Event: HostsEvent, Task: Task{Project: project, Executor: executor}}
}

// Every host serving the project is offered, in inventory-name order, with the
// checkout ty will need if one is picked. Ranking belongs to Resolve: a person
// choosing by hand is allowed to want the busier machine.
func TestHostsOffersEveryHostServingTheProject(t *testing.T) {
	path := writeInventory(t, fleet)

	got := Resolver{InventoryPath: path}.Hosts(hostsRequest("taskyou", "claude"))

	if len(got.Hosts) != 2 {
		t.Fatalf("hosts = %+v, want the two hosts serving taskyou", got.Hosts)
	}
	if got.Hosts[0].Name != "mona" || got.Hosts[1].Name != "rex" {
		t.Errorf("hosts = %+v, want mona then rex (inventory order)", got.Hosts)
	}
	if got.Hosts[0].Workdir != "~/Projects/taskyou" {
		t.Errorf("workdir = %q, want mona's checkout of taskyou", got.Hosts[0].Workdir)
	}
	if got.Hosts[0].Detail == "" {
		t.Error("detail is empty: the list is read by a person choosing between machines")
	}
}

// No probe is run, so a host that is currently unreachable is still offered.
// Offering it and failing at launch is more useful than hiding it and leaving
// the user wondering where their machine went.
func TestHostsDoesNotProbe(t *testing.T) {
	path := writeInventory(t, fleet)
	prober := &stubProber{err: errAllHostsUnreachable}

	got := Resolver{InventoryPath: path, Prober: prober}.Hosts(hostsRequest("taskyou", "claude"))

	if len(got.Hosts) != 2 {
		t.Fatalf("hosts = %+v, want both hosts regardless of reachability", got.Hosts)
	}
	if prober.calls != 0 {
		t.Errorf("prober was called %d times; listing candidates must not cost an SSH round trip", prober.calls)
	}
}

// The same executor rule Resolve applies: a host that declares what it can run
// is only offered for those executors.
func TestHostsRespectsExecutorCapabilities(t *testing.T) {
	path := writeInventory(t, `
hosts:
  claude-only:
    ssh: claude-only
    capabilities: ["executor:claude"]
    repos:
      taskyou: ~/src/taskyou
  either:
    ssh: either
    repos:
      taskyou: ~/src/taskyou
`)

	got := Resolver{InventoryPath: path}.Hosts(hostsRequest("taskyou", "codex"))

	if len(got.Hosts) != 1 || got.Hosts[0].Name != "either" {
		t.Fatalf("hosts = %+v, want only the host that has not ruled codex out", got.Hosts)
	}
}

// The questions ty cannot use an answer to are answered with no candidates:
// an executor that cannot run remotely, a task with no project, and an
// inventory that is not there.
func TestHostsAnswersNothingWhenThereIsNothingToOffer(t *testing.T) {
	path := writeInventory(t, fleet)

	cases := []struct {
		name string
		req  Request
		path string
	}{
		{"executor cannot run remotely", hostsRequest("taskyou", "gemini"), path},
		{"no project to match on", hostsRequest("", "claude"), path},
		{"no inventory", hostsRequest("taskyou", "claude"), "/nonexistent/hosts.yaml"},
		{"project nothing serves", hostsRequest("unknown-project", "claude"), path},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolver{InventoryPath: tc.path}.Hosts(tc.req)
			if len(got.Hosts) != 0 {
				t.Errorf("hosts = %+v, want none", got.Hosts)
			}
			if got.Hosts == nil {
				t.Error("hosts is nil; it must encode as [] rather than null")
			}
		})
	}
}

// An inventory that omits ssh is addressed by its inventory name, the same way
// Resolve addresses it.
func TestHostsFallsBackToTheInventoryName(t *testing.T) {
	path := writeInventory(t, `
hosts:
  bare:
    repos:
      taskyou: ~/src/taskyou
`)

	got := Resolver{InventoryPath: path}.Hosts(hostsRequest("taskyou", "claude"))

	if len(got.Hosts) != 1 || got.Hosts[0].Target != "bare" {
		t.Fatalf("hosts = %+v, want the inventory name as the destination", got.Hosts)
	}
}
