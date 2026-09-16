// Package reaper finds and kills task side processes (dev servers, watchers,
// build daemons) that outlive the tmux window they were started in.
//
// Why this exists: ty tears sessions down at the tmux *window* level and relies
// on SIGHUP propagating from the pane to whatever was running in it. That works
// for the agent, which stays in the pane's foreground process group, and fails
// for anything that left that group — a dev server that was backgrounded,
// disowned, or setsid'd. Once its parent shell dies such a process is reparented
// to launchd/init and never sees the signal, so it survives every window
// teardown, forever. Four of them (ages 1d8h..4d20h, ~4GB of compressed swap
// ballast) helped exhaust a 24GB machine on 2026-08-19.
//
// The reaper deliberately does NOT reason about process ancestry, process
// groups, or environment variables to decide what belongs to a task. Those have
// each failed this code once already (see cleanupOrphanedSessions' own doc
// comment for the previous incarnation). It matches on the worktree path, which
// is right there on the command line of every process a task started and cannot
// be lost by reparenting.
package reaper

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Defaults for the staleness thresholds. All are overridable via settings.
const (
	// DefaultDoneGrace is how long after a task finishes we still leave its
	// side processes alone. Matches the window sweep's existing 2h grace: a
	// done task is finished work, nobody is coming back to its dev server.
	DefaultDoneGrace = 2 * time.Hour

	// DefaultBlockedIdle is how long a task must show NO activity before its
	// side processes are considered abandoned.
	//
	// In ty, "blocked" overwhelmingly means "waiting for a human to come look
	// at it", not "dead". Reaping on the done-task grace (2h) would routinely
	// kill the dev server behind a PR that is simply awaiting review over
	// lunch. A full day of zero activity — no status change, no log line, no
	// one opening it in the UI — is a much stronger signal that nobody is
	// mid-flight on it, and the cost of being wrong is small and recoverable:
	// only side processes are reaped at this threshold (never the agent), the
	// worktree and the agent's resumable session are untouched, and restarting
	// a dev server is one command.
	DefaultBlockedIdle = 24 * time.Hour

	// DefaultOrphanMinAge is the minimum age for the no-worktree heuristic
	// (PPID 1 + no controlling terminal + a known dev-server binary). Nothing
	// ties these back to a task, so the only evidence we have is that they are
	// detached, headless, and old. Kept deliberately long.
	DefaultOrphanMinAge = 24 * time.Hour

	// DefaultTermGrace is how long a process gets to exit after SIGTERM before
	// it is SIGKILLed.
	DefaultTermGrace = 5 * time.Second

	// Never is the threshold value that disables a staleness rule: no elapsed
	// time can ever reach it. Callers set it when a setting is "0"/"disabled".
	Never = time.Duration(1<<62 - 1)
)

// worktreeRe extracts the task ID from any path pointing into a task worktree
// (<project>/.task-worktrees/<id>-<slug>/...). Matches the layout created by
// executor.setupWorktree.
var worktreeRe = regexp.MustCompile(`\.task-worktrees/((\d+)-[^/\s]*)`)

// devServerNames are the binaries the no-worktree heuristic will consider. This
// list is intentionally narrow: a match here can get a process killed with no
// task to corroborate it, so it only contains long-lived JS dev servers, which
// are the ones observed to leak and the ones that are cheapest to restart.
var devServerNames = []string{
	"webpack-dev-server",
	"vite",
	"next",
	"rollup",
	"nuxt",
	"parcel",
	"esbuild",
	"nodemon",
	"astro",
	"remix",
	"rspack",
}

// agentBinaries are the coding agents ty itself launches. Killing one of these
// ends a resumable conversation, so they are held to the strictest rules: they
// are only ever reaped for tasks that are deleted or long done, never for a
// merely stale one.
var agentBinaries = []string{
	"claude", "codex", "gemini", "opencode", "warp", "amp", "aider", "cursor-agent",
}

// Process is one row of the system process table.
type Process struct {
	PID     int
	PPID    int
	TTY     string        // "?" / "??" when the process has no controlling terminal
	Age     time.Duration // elapsed time since the process started
	Command string        // full command line
}

// HasTTY reports whether the process has a controlling terminal. ps renders the
// absence as "?" on Linux and "??" on macOS.
func (p Process) HasTTY() bool {
	t := strings.TrimSpace(p.TTY)
	return t != "" && t != "?" && t != "??" && t != "-"
}

// TaskState is the slice of a task the reaper needs to judge its processes.
type TaskState struct {
	Exists bool
	Status string
	// LastActivity is the most recent sign of life for the task: status
	// changes, log lines, the UI opening it. Staleness is measured from here
	// and NOT from when the task entered its current status, so a task that is
	// blocked but actively being poked at is never considered abandoned.
	LastActivity time.Time
	// WorktreeDir is the basename of the task's recorded worktree
	// ("5119-creator-referral-v2"), or "" if none is recorded. A process whose
	// worktree directory differs belongs to some other task that happens to
	// share the ID — typically one placed here from another machine.
	WorktreeDir string
}

// Policy carries the thresholds and the invariants a sweep must respect.
type Policy struct {
	Now                  time.Time
	DoneGrace            time.Duration
	BlockedIdle          time.Duration
	OrphanMinAge         time.Duration
	ReapOrphanDevServers bool
	// Protected PIDs are never touched: the sweeping process itself and every
	// one of its ancestors. Without this, `ty sessions cleanup` run by an agent
	// from inside its own task worktree would reap the agent running it.
	Protected map[int]bool
	// OnlyTasks, when non-empty, restricts the sweep to these task IDs. Used by
	// `ty sessions suspend`, which should only clean up after what it suspended.
	OnlyTasks map[int]bool
	// Explicit marks a teardown the user asked for by name rather than one
	// inferred from staleness. Status-based caution (the active-task guard, the
	// idle thresholds, sparing the agent) exists to avoid guessing wrong about
	// abandoned work; when someone runs `ty sessions suspend 42` there is no
	// guessing left to do. Only meaningful together with OnlyTasks.
	Explicit bool
}

// Rule identifies which decision produced a Decision, for logging and tests.
type Rule string

const (
	RuleProtected     Rule = "protected"
	RuleLivePane      Rule = "live-pane"
	RuleTaskDone      Rule = "task-done"
	RuleDoneGrace     Rule = "within-done-grace"
	RuleTaskActive    Rule = "task-active"
	RuleStaleIdle     Rule = "stale-idle"
	RuleIdleGrace     Rule = "within-idle-grace"
	RuleAgentSpared   Rule = "agent-spared"
	RuleOrphanDetach  Rule = "detached-dev-server"
	RuleOrphanYoung   Rule = "detached-but-young"
	RuleOrphanAttach  Rule = "not-detached"
	RuleOrphanDisable Rule = "detached-sweep-disabled"
	RuleExplicit      Rule = "explicit-teardown"
	RuleUnknownTask   Rule = "not-our-task"
)

// Decision is the verdict for one process the sweep considered. Every process
// the reaper looked at produces one, reaped or not, so a dry run explains its
// reasoning for the keeps as well as the kills.
type Decision struct {
	Process Process
	TaskID  int // 0 when the process could not be tied to a task
	IsAgent bool
	Reap    bool
	Rule    Rule
	Reason  string
}

// String renders a decision as a single log line.
func (d Decision) String() string {
	verb := "keep"
	if d.Reap {
		verb = "reap"
	}
	who := "no task"
	if d.TaskID > 0 {
		who = fmt.Sprintf("task %d", d.TaskID)
	}
	return fmt.Sprintf("%s pid %d (%s, age %s) [%s] %s — %s",
		verb, d.Process.PID, who, roundAge(d.Process.Age), d.Rule, shortCommand(d.Process.Command), d.Reason)
}

// TaskIDFor returns the task ID referenced by a command line, or 0.
func TaskIDFor(command string) int {
	m := worktreeRe.FindStringSubmatch(command)
	if m == nil {
		return 0
	}
	var id int
	if _, err := fmt.Sscanf(m[2], "%d", &id); err != nil {
		return 0
	}
	return id
}

// WorktreeDirFor returns the task worktree directory name referenced by a
// command line ("5119-creator-referral-v2"), or "".
func WorktreeDirFor(command string) string {
	if m := worktreeRe.FindStringSubmatch(command); m != nil {
		return m[1]
	}
	return ""
}

// IsAgentProcess reports whether a command line looks like one of the coding
// agents ty launches, rather than a side process it spawned.
func IsAgentProcess(command string) bool {
	for _, field := range strings.Fields(command) {
		// Skip env-var assignments and the interpreter, look at each argv word:
		// agents are invoked both bare ("claude --resume") and by absolute path.
		base := field
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[i+1:]
		}
		for _, name := range agentBinaries {
			if base == name {
				return true
			}
		}
	}
	return strings.Contains(command, "ty mcp-server") || strings.Contains(command, "taskyou mcp-server")
}

// IsDevServer reports whether a command line is one of the known long-lived JS
// dev servers used by the no-worktree heuristic.
func IsDevServer(command string) bool {
	for _, name := range devServerNames {
		if strings.Contains(command, "node_modules/.bin/"+name) {
			return true
		}
	}
	return false
}

// Plan decides what to do with every process it can tie to a task worktree,
// plus detached dev servers that no worktree explains. It is pure: callers
// supply the process table, the live tmux pane PIDs, and the task states, and
// get back the decisions without anything being signalled.
func Plan(procs []Process, tasks map[int]TaskState, livePanePIDs map[int]bool, p Policy) []Decision {
	parents := make(map[int]int, len(procs))
	for _, proc := range procs {
		parents[proc.PID] = proc.PPID
	}

	var decisions []Decision
	for _, proc := range procs {
		taskID := TaskIDFor(proc.Command)
		if taskID > 0 {
			if len(p.OnlyTasks) > 0 && !p.OnlyTasks[taskID] {
				continue
			}
			decisions = append(decisions, planWorktreeProcess(proc, taskID, tasks[taskID], parents, livePanePIDs, p))
			continue
		}
		if len(p.OnlyTasks) > 0 {
			// A scoped sweep has no business judging processes it can't tie to
			// one of its tasks.
			continue
		}
		if d, ok := planDetachedProcess(proc, p); ok {
			decisions = append(decisions, d)
		}
	}
	return decisions
}

func planWorktreeProcess(proc Process, taskID int, state TaskState, parents map[int]int, livePanePIDs map[int]bool, p Policy) Decision {
	d := Decision{Process: proc, TaskID: taskID, IsAgent: IsAgentProcess(proc.Command)}

	if p.Protected[proc.PID] {
		d.Rule, d.Reason = RuleProtected, "process is the running sweep or one of its ancestors"
		return d
	}
	if hasLiveAncestor(proc.PID, parents, livePanePIDs) {
		d.Rule, d.Reason = RuleLivePane, "still running inside a live tmux pane"
		return d
	}

	// Absence from this database is not evidence of deletion. On a host that
	// runs tasks placed from another machine, every one of those tasks is absent
	// here, and judging them "deleted" kills live work — the exact failure the
	// window pass was already fixed for. The same goes for a task ID that exists
	// locally but points at a different worktree: that is a collision with a
	// foreign task, not this one. Neither is ours to judge by task status; the
	// only admissible evidence left is the task-agnostic detached-dev-server test.
	if !state.Exists || (state.WorktreeDir != "" && WorktreeDirFor(proc.Command) != state.WorktreeDir) {
		why := "task is not in this database (it may belong to another machine)"
		if state.Exists {
			why = "worktree does not match this task's recorded worktree " + state.WorktreeDir
		}
		if detached, ok := planDetachedProcess(proc, p); ok && detached.Reap {
			detached.TaskID = taskID
			detached.IsAgent = d.IsAgent
			detached.Reason = why + "; " + detached.Reason
			return detached
		}
		d.Rule, d.Reason = RuleUnknownTask, why
		return d
	}

	if p.Explicit && len(p.OnlyTasks) > 0 {
		d.Reap = true
		d.Rule, d.Reason = RuleExplicit, "task was explicitly torn down"
		return d
	}

	switch state.Status {
	case "processing", "queued":
		d.Rule, d.Reason = RuleTaskActive, "task is "+state.Status
		return d

	case "done", "archived", "trashed":
		idle := p.Now.Sub(state.LastActivity)
		if state.LastActivity.IsZero() || idle >= p.DoneGrace {
			d.Reap = true
			d.Rule = RuleTaskDone
			d.Reason = fmt.Sprintf("task %s and idle %s (>= %s)", state.Status, roundAge(idle), fmtThreshold(p.DoneGrace))
			return d
		}
		d.Rule = RuleDoneGrace
		d.Reason = fmt.Sprintf("task %s but only idle %s (< %s)", state.Status, roundAge(idle), fmtThreshold(p.DoneGrace))
		return d

	default:
		// blocked, backlog, and anything new. "blocked" in ty usually means
		// "waiting for a human", so the bar is high and the agent is off limits
		// at this tier — a leaked dev server is cheap, killing work someone is
		// about to return to is not.
		if d.IsAgent {
			d.Rule = RuleAgentSpared
			d.Reason = fmt.Sprintf("agent process for a %s task is never reaped by staleness", state.Status)
			return d
		}
		idle := p.Now.Sub(state.LastActivity)
		if !state.LastActivity.IsZero() && idle < p.BlockedIdle {
			d.Rule = RuleIdleGrace
			d.Reason = fmt.Sprintf("task %s and active %s ago (< %s)", state.Status, roundAge(idle), fmtThreshold(p.BlockedIdle))
			return d
		}
		if state.LastActivity.IsZero() && proc.Age < p.BlockedIdle {
			// No activity record at all: fall back to the process's own age so
			// a freshly started side process is never reaped.
			d.Rule = RuleIdleGrace
			d.Reason = fmt.Sprintf("task %s with no activity record and process only %s old (< %s)", state.Status, roundAge(proc.Age), fmtThreshold(p.BlockedIdle))
			return d
		}
		d.Reap = true
		d.Rule = RuleStaleIdle
		if state.LastActivity.IsZero() {
			d.Reason = fmt.Sprintf("task %s with no activity record and process %s old (>= %s)", state.Status, roundAge(proc.Age), fmtThreshold(p.BlockedIdle))
		} else {
			d.Reason = fmt.Sprintf("task %s and no activity for %s (>= %s)", state.Status, roundAge(idle), fmtThreshold(p.BlockedIdle))
		}
		return d
	}
}

// planDetachedProcess covers orphans with no worktree on their command line at
// all — the /private/tmp case, where the directory the dev server was started
// in has been deleted out from under it. Nothing links these to a task, so the
// only admissible evidence is that the process is a known dev server, has been
// reparented to init, has no controlling terminal, and is old.
func planDetachedProcess(proc Process, p Policy) (Decision, bool) {
	if !IsDevServer(proc.Command) {
		return Decision{}, false
	}
	d := Decision{Process: proc}
	if p.Protected[proc.PID] {
		d.Rule, d.Reason = RuleProtected, "process is the running sweep or one of its ancestors"
		return d, true
	}
	if !p.ReapOrphanDevServers {
		d.Rule, d.Reason = RuleOrphanDisable, "detached dev-server sweep is disabled"
		return d, true
	}
	if proc.PPID != 1 || proc.HasTTY() {
		d.Rule = RuleOrphanAttach
		d.Reason = fmt.Sprintf("still attached (ppid %d, tty %s)", proc.PPID, proc.TTY)
		return d, true
	}
	if proc.Age < p.OrphanMinAge {
		d.Rule = RuleOrphanYoung
		d.Reason = fmt.Sprintf("detached but only %s old (< %s)", roundAge(proc.Age), fmtThreshold(p.OrphanMinAge))
		return d, true
	}
	d.Reap = true
	d.Rule = RuleOrphanDetach
	d.Reason = fmt.Sprintf("dev server reparented to init, no terminal, %s old (>= %s)", roundAge(proc.Age), fmtThreshold(p.OrphanMinAge))
	return d, true
}

// hasLiveAncestor walks the parent chain looking for a PID that tmux reports as
// a live pane process. A process that is still in a pane is by definition not
// an orphan, whatever the task's status says.
func hasLiveAncestor(pid int, parents map[int]int, livePanePIDs map[int]bool) bool {
	seen := make(map[int]bool, 8)
	for cur := pid; cur > 1 && !seen[cur]; {
		if livePanePIDs[cur] {
			return true
		}
		seen[cur] = true
		next, ok := parents[cur]
		if !ok {
			return false
		}
		cur = next
	}
	return false
}

// fmtThreshold renders a threshold for a reason string, spelling out the
// disabled sentinel instead of printing its absurd duration.
func fmtThreshold(d time.Duration) string {
	if d >= Never {
		return "never"
	}
	return d.String()
}

func roundAge(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	if d >= time.Hour {
		return d.Round(time.Minute)
	}
	return d.Round(time.Second)
}

func shortCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if len(cmd) > 100 {
		return cmd[:97] + "..."
	}
	return cmd
}
