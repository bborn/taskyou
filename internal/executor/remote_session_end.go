package executor

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// Ending a placed task's agent once the task is finished.
//
// A task placed on another host runs in a window of that host's
// task-daemon-remote-<coordinator> session, and nothing on this machine owns
// that process: the local janitors (cleanupInactiveDoneTasks, the TUI's close
// and archive) only reach the local tmux server. So a remote task that was
// closed or archived kept its agent running for days — on mona, thirteen of
// them holding 3.5 GB, and on a pooled cloud server, a machine its reaper
// counted as busy.
//
// The rule: once a task is done or archived, the daemon ends its task-<id> and
// task-<id>-shell windows on the host its last run was placed on. Blocked tasks
// are not touched — their agent is waiting for a reply, and ending it would
// lose the conversation. Only processes are ended; the remote worktree is left
// exactly as it is.
//
// It runs from the daemon loop rather than at each place a status changes,
// because those places are many and in different processes (`ty close`, the
// TUI, the web API, workflow steps): the database is where they all meet. The
// status change itself never waits for a host. The remote_runs row is the
// to-do list — it is removed only once the host has answered, so a sleeping
// laptop or a deleted server is retried, with backoff, instead of forgotten.

// remoteSessionEndTimeout bounds one host's ssh round trip.
const remoteSessionEndTimeout = 30 * time.Second

// Backoff for a host that could not be reached: from the first to the max,
// doubling. The max is also how long a laptop that wakes up may wait.
const (
	remoteSessionEndMinBackoff = time.Minute
	remoteSessionEndMaxBackoff = 15 * time.Minute
)

// remoteSessionEnder is the daemon's state for ending finished remote sessions.
type remoteSessionEnder struct {
	mu      sync.Mutex
	running bool
	retry   map[string]hostRetry
}

type hostRetry struct {
	at   time.Time
	wait time.Duration
}

func (s *remoteSessionEnder) due(host string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !now.Before(s.retry[host].at)
}

func (s *remoteSessionEnder) failed(host string, now time.Time) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.retry == nil {
		s.retry = make(map[string]hostRetry)
	}
	wait := min(max(s.retry[host].wait*2, remoteSessionEndMinBackoff), remoteSessionEndMaxBackoff)
	s.retry[host] = hostRetry{at: now.Add(wait), wait: wait}
	return wait
}

func (s *remoteSessionEnder) succeeded(host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.retry, host)
}

// startEndingFinishedRemoteSessions runs one sweep in the background, unless
// one is still running: an unreachable host costs a connect timeout, and the
// worker loop must not wait on it.
func (e *Executor) startEndingFinishedRemoteSessions(ctx context.Context) {
	e.sessionEnd.mu.Lock()
	if e.sessionEnd.running {
		e.sessionEnd.mu.Unlock()
		return
	}
	e.sessionEnd.running = true
	e.sessionEnd.mu.Unlock()

	go func() {
		defer func() {
			e.sessionEnd.mu.Lock()
			e.sessionEnd.running = false
			e.sessionEnd.mu.Unlock()
		}()
		e.endFinishedRemoteSessions(ctx, time.Now())
	}()
}

// endFinishedRemoteSessions ends the remote tmux windows of done and archived
// tasks, one ssh call per host.
func (e *Executor) endFinishedRemoteSessions(ctx context.Context, now time.Time) {
	runs, err := e.db.FinishedRemoteRuns()
	if err != nil {
		e.logger.Debug("Failed to list finished remote runs", "error", err)
		return
	}
	if len(runs) == 0 {
		return
	}
	coordinator, err := e.db.CoordinatorID()
	if err != nil {
		e.logger.Debug("Failed to read coordinator ID", "error", err)
		return
	}
	session := remoteDaemonSessionName(coordinator)

	var hosts []string
	for _, run := range runs {
		if len(hosts) == 0 || hosts[len(hosts)-1] != run.Host {
			hosts = append(hosts, run.Host) // runs are ordered by host
		}
	}
	for _, host := range hosts {
		if ctx.Err() != nil {
			return
		}
		if !e.sessionEnd.due(host, now) {
			continue
		}
		// Re-read just before acting: an earlier host may have taken a connect
		// timeout, and a task reopened and placed again since the first read
		// must not lose its new window.
		pending := finishedRunsOn(e.db, host)
		if len(pending) == 0 {
			continue
		}
		ended, err := endRemoteTaskWindows(ctx, host, session, pending)
		if err != nil {
			wait := e.sessionEnd.failed(host, now)
			e.logger.Warn("Could not end finished tasks' sessions on host; will retry",
				"host", host, "tasks", len(pending), "retry_in", wait, "error", err)
			continue
		}
		e.sessionEnd.succeeded(host)
		for _, run := range pending {
			if err := e.db.EndRemoteRun(run.TaskID, run.RunID); err != nil {
				e.logger.Warn("Failed to forget ended remote run", "task", run.TaskID, "error", err)
			}
			if ended[run.TaskID] {
				e.logger.Info("Ended finished task's remote session", "task", run.TaskID, "host", host, "status", run.Status)
				e.logLine(run.TaskID, "system", fmt.Sprintf(
					"Task is %s, so its agent session on %s was ended. The worktree there was left as it is.", run.Status, host))
			}
		}
	}
}

// finishedRunsOn is FinishedRemoteRuns for one host.
func finishedRunsOn(database *db.DB, host string) []db.RemoteRun {
	runs, err := database.FinishedRemoteRuns()
	if err != nil {
		return nil
	}
	var onHost []db.RemoteRun
	for _, run := range runs {
		if run.Host == host {
			onHost = append(onHost, run)
		}
	}
	return onHost
}

// endRemoteTaskWindows kills the task-<id> and task-<id>-shell windows of runs
// in this coordinator's session on host, and reports which tasks had one.
//
// Windows are matched by exact name inside the one session, and killed by ID,
// so another coordinator's task with the same ID — or the host's own ty — is
// never reached. A host with no such session or no tmux server has nothing to
// end, which is success; only failing to reach the host is an error.
func endRemoteTaskWindows(ctx context.Context, host, session string, runs []db.RemoteRun) (map[int64]bool, error) {
	names := make(map[string]int64, 2*len(runs))
	for _, run := range runs {
		names[TmuxWindowName(run.TaskID)] = run.TaskID
		names[TmuxWindowName(run.TaskID)+"-shell"] = run.TaskID
	}
	list := make([]string, 0, len(names))
	for name := range names {
		list = append(list, name)
	}
	script := "tmux list-windows -t " + shellQuote("="+session) + " -F '#{window_id} #{window_name}' 2>/dev/null |" +
		" while read -r id name; do case " + shellQuote(" "+strings.Join(list, " ")+" ") + ` in *" $name "*)` +
		` tmux kill-window -t "$id" && echo "$name";; esac; done; exit 0`

	ctx, cancel := context.WithTimeout(WithRunner(ctx, RemoteRunner{Host: host}), remoteSessionEndTimeout)
	defer cancel()
	out, err := command(ctx, "", "sh", "-c", script).Output()
	if err != nil {
		return nil, err
	}
	ended := make(map[int64]bool)
	for _, name := range strings.Fields(string(out)) {
		if id, ok := names[name]; ok {
			ended[id] = true
		}
	}
	return ended, nil
}
