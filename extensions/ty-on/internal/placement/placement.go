// Package placement decides which host a TaskYou task should run on.
//
// It is deliberately conservative: every path that cannot confidently name a
// host resolves to a local placement (empty target) with a reason explaining
// why. Nothing in here returns an error to the caller — the caller is in the
// task spawn path and must never be failed by a policy decision.
package placement

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Event is the only event this resolver answers.
const Event = "task.placement"

// Request is the JSON document ty writes to the resolver's stdin.
type Request struct {
	Event string `json:"event"`
	Task  Task   `json:"task"`
}

// Task describes the task being placed.
type Task struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Project  string `json:"project"`
	RepoPath string `json:"repo_path"`
	Executor string `json:"executor"`
}

// Response is the JSON document the resolver writes to stdout.
//
// An empty Target means "run locally". Reason is always populated and is shown
// to the user, so it should be specific enough to explain a surprising choice.
type Response struct {
	Target  string `json:"target"`
	Workdir string `json:"workdir"`
	Reason  string `json:"reason"`
}

// Local builds a "run here" response with the given reason.
func Local(format string, args ...any) Response {
	return Response{Reason: fmt.Sprintf(format, args...)}
}

// DefaultTimeout bounds the `on ls` probe. Ranking several hosts costs an SSH
// round trip per host; past this we prefer a local placement to a slow answer.
const DefaultTimeout = 3 * time.Second

// Resolver holds the knobs the resolution rules depend on. The zero value is
// usable: it reads the inventory `on` would read and probes with the default
// timeout.
type Resolver struct {
	// InventoryPath overrides the inventory location. Empty means "ask the
	// same environment `on` asks" (ON_HOSTS, XDG_CONFIG_HOME, ~/.config).
	InventoryPath string

	// Timeout bounds the `on ls` probe. Zero means DefaultTimeout.
	Timeout time.Duration

	// Prober ranks hosts by free memory. Nil means shell out to `on ls`.
	Prober Prober
}

// Resolve applies the placement rules to req and always returns a usable
// response.
func (r Resolver) Resolve(ctx context.Context, req Request) Response {
	if req.Event != "" && req.Event != Event {
		return Local("unsupported event %q, expected %q", req.Event, Event)
	}
	project := req.Task.Project
	if project == "" {
		return Local("task has no project, nothing to match against the host inventory")
	}

	path := r.InventoryPath
	if path == "" {
		path = InventoryPath()
	}

	inv, err := LoadInventory(path)
	if err != nil {
		return Local("%s", err)
	}

	candidates := inv.Serving(project)
	switch len(candidates) {
	case 0:
		return Local("no host in %s serves %s (%s)", path, project, hostSummary(inv))
	case 1:
		c := candidates[0]
		return Response{
			Target:  c.Name,
			Workdir: c.Checkout,
			Reason:  fmt.Sprintf("only host serving %s", project),
		}
	}

	return r.rank(ctx, project, path, candidates)
}

// rank picks between two or more hosts that all serve the project.
func (r Resolver) rank(ctx context.Context, project, path string, candidates []Candidate) Response {
	prober := r.Prober
	if prober == nil {
		prober = OnProber{InventoryPath: path}
	}

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stats, err := prober.Probe(ctx)
	if err != nil {
		// A late answer is worth less than a local one: the caller is waiting
		// to spawn the executor.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Local("%d hosts serve %s but comparing them took longer than %s, so this task stays local",
				len(candidates), project, timeout)
		}
		return Local("%d hosts serve %s but they could not be compared: %s",
			len(candidates), project, err)
	}

	// Keep only candidates the probe could actually reach and measure.
	type ranked struct {
		Candidate
		freeKB int64
	}
	var reachable []ranked
	for _, c := range candidates {
		s, ok := stats[c.Name]
		if !ok || !s.Reachable {
			continue
		}
		reachable = append(reachable, ranked{Candidate: c, freeKB: s.FreeKB})
	}

	switch len(reachable) {
	case 0:
		return Local("%d hosts serve %s but none of them are reachable (%s)",
			len(candidates), project, names(candidates))
	case 1:
		only := reachable[0]
		return Response{
			Target:  only.Name,
			Workdir: only.Checkout,
			Reason: fmt.Sprintf("only reachable host of %d serving %s (%s)",
				len(candidates), project, names(candidates)),
		}
	}

	// Most free memory wins; ties break on host name so the answer is stable.
	sort.SliceStable(reachable, func(i, j int) bool {
		if reachable[i].freeKB != reachable[j].freeKB {
			return reachable[i].freeKB > reachable[j].freeKB
		}
		return reachable[i].Name < reachable[j].Name
	})

	// Spell out what each contender had, so a surprising winner is explicable.
	detail := make([]string, 0, len(reachable))
	for _, h := range reachable {
		detail = append(detail, h.Name+" "+humanKB(h.freeKB))
	}
	reason := fmt.Sprintf("most free memory of %d hosts serving %s (%s)",
		len(reachable), project, strings.Join(detail, ", "))
	if skipped := len(candidates) - len(reachable); skipped > 0 {
		reason += fmt.Sprintf("; %d unreachable", skipped)
	}

	best := reachable[0]
	return Response{Target: best.Name, Workdir: best.Checkout, Reason: reason}
}

func names(candidates []Candidate) string {
	out := make([]string, len(candidates))
	for i, c := range candidates {
		out[i] = c.Name
	}
	return strings.Join(out, ", ")
}

func hostSummary(inv *Inventory) string {
	switch n := len(inv.Hosts); n {
	case 0:
		return "inventory lists no hosts"
	case 1:
		return "1 host in inventory"
	default:
		return fmt.Sprintf("%d hosts in inventory", n)
	}
}

// humanKB renders kilobytes the way `on ls` does, so a reason string can be
// checked against `on ls` output by eye.
func humanKB(kb int64) string {
	switch {
	case kb >= 1<<20:
		return fmt.Sprintf("%.1fG", float64(kb)/(1<<20))
	case kb >= 1<<10:
		return fmt.Sprintf("%dM", kb/(1<<10))
	default:
		return fmt.Sprintf("%dK", kb)
	}
}
