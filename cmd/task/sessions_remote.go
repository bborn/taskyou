package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
)

// `ty sessions list` (and its `ty claudes` alias) enumerates the tmux server on
// THIS machine. Once a task.placement plugin started putting tasks on other
// hosts, that made the command quietly incomplete: a remotely placed agent never
// appeared in it, and on a coordinator that runs nothing locally the command
// printed "No agent sessions running" while a fleet of agents worked.
//
// This file is the other half of the answer — ask the hosts tasks were actually
// placed on — plus the rule that keeps the list honest: a host ty could not
// reach is NAMED, never silently dropped, so an empty list means "nothing is
// running" rather than "ty did not look".

// remoteWindowListFormat asks tmux for one line per window on the host.
//
// window_activity comes FIRST so the two fields that can contain surprising
// characters are the ones at the end: a foreign window whose name holds a colon
// simply fails to match "task-<id>" and is skipped, rather than shifting the
// fields of every other line.
const remoteWindowListFormat = "#{window_activity}:#{window_name}:#{session_name}"

// remoteHostProblem is a placed host ty asked about and could not reach. An
// empty host means the lookup of which hosts to ask failed.
type remoteHostProblem struct {
	host string
	// tasks is how many placed tasks are therefore unaccounted for.
	tasks int
	err   error
}

// remoteAgentSessions asks every host that currently holds placed tasks which of
// their agent windows are alive.
//
// The database says where to look; only tmux on the far side says what is
// actually running, so a task row is never reported as a live session on the
// strength of its columns alone.
func remoteAgentSessions(ctx context.Context, database *db.DB) ([]agentSession, []remoteHostProblem) {
	if database == nil {
		return nil, nil
	}
	placed, err := database.ListRemoteAgentTasks()
	if err != nil {
		// An empty host means "the lookup itself failed", so the listing says it
		// could not check rather than implying a specific host was down.
		return nil, []remoteHostProblem{{err: err}}
	}
	if len(placed) == 0 {
		return nil, nil
	}

	byHost := make(map[string][]db.RemoteAgentTask)
	var hosts []string
	for _, t := range placed {
		if _, seen := byHost[t.Host]; !seen {
			hosts = append(hosts, t.Host)
		}
		byHost[t.Host] = append(byHost[t.Host], t)
	}

	// One ssh round trip per host, all of them at once: a fleet of five hosts
	// should cost one slow handshake, not five in series.
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		sessions []agentSession
		problems []remoteHostProblem
	)
	for _, host := range hosts {
		wg.Add(1)
		go func(host string) {
			defer wg.Done()
			found, err := hostAgentSessions(ctx, host, byHost[host])
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				problems = append(problems, remoteHostProblem{host: host, tasks: len(byHost[host]), err: err})
				return
			}
			sessions = append(sessions, found...)
		}(host)
	}
	wg.Wait()

	// Goroutines finish in whatever order the network allows; the listing must
	// not.
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].host != sessions[j].host {
			return sessions[i].host < sessions[j].host
		}
		return sessions[i].taskID < sessions[j].taskID
	})
	sort.Slice(problems, func(i, j int) bool { return problems[i].host < problems[j].host })
	return sessions, problems
}

// hostAgentSessions returns the live agent windows on one host.
//
// An error means ty could not LOOK — the host is unreachable. tmux answering
// "no server running" is not an error but an answer, and comes back as no
// sessions, because a host with no tmux server is running none of our agents.
func hostAgentSessions(ctx context.Context, host string, tasks []db.RemoteAgentTask) ([]agentSession, error) {
	out, err := remoteTmuxRunner{ctx: ctx, host: host}.Output("tmux", "list-windows", "-a", "-F", remoteWindowListFormat)
	if err != nil {
		if executor.RemoteCommandUnreachable(ctx.Err(), err) {
			return nil, err
		}
		return nil, nil
	}

	// Only windows belonging to a task THIS coordinator placed on THIS host, in
	// the session its run recorded, are ours to report: window "task-42" on a
	// host two coordinators share does not necessarily mean our task 42, and
	// pairing id with recorded session is the same identity check the rest of
	// the remote code path (InspectRemoteTerminal) makes.
	// NUL joins the pair, not ":": a session name may legally contain a colon, and
	// with a colon separator "a:task-1" + "task-2" and "a" + "task-1:task-2" are
	// the same key.
	want := make(map[string]db.RemoteAgentTask, len(tasks))
	for _, t := range tasks {
		want[t.DaemonSession+"\x00"+executor.TmuxWindowName(t.ID)] = t
	}

	var sessions []agentSession
	for _, line := range strings.Split(string(out), "\n") {
		activity, window, session, ok := splitRemoteWindowLine(line)
		if !ok {
			continue
		}
		task, mine := want[session+"\x00"+window]
		if !mine {
			continue
		}
		sessions = append(sessions, agentSession{
			taskID:    int(task.ID),
			taskTitle: task.Title,
			executor:  task.Executor,
			model:     task.Model,
			effort:    task.EffortLevel,
			host:      host,
			// Memory is deliberately absent rather than guessed: reading it means
			// a second round trip per host, and a wrong number is worse than a
			// blank column.
			info: remoteSessionInfo(host, session, activity),
		})
	}
	return sessions, nil
}

// splitRemoteWindowLine parses one remoteWindowListFormat line.
func splitRemoteWindowLine(line string) (activity, window, session string, ok bool) {
	parts := strings.SplitN(strings.TrimSpace(line), ":", 3)
	if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// remoteSessionInfo renders the "where" column: the host first, because that is
// the fact the local listing was missing.
func remoteSessionInfo(host, session, activity string) string {
	info := host + ": " + session
	var epoch int64
	if _, err := fmt.Sscanf(activity, "%d", &epoch); err == nil && epoch > 0 {
		info += fmt.Sprintf(", last activity %s", time.Unix(epoch, 0).Format("15:04:05"))
	}
	return info
}
